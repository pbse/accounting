package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/wf"
	"close.local/backend/workflows/close_goal"
	"close.local/backend/workflows/close_goal_upkeep"
)

// A close handed to the agent. The agent keeps the close's preparation scope current, preparation the firm lets agents
// do on their own, and says exactly what it needs from people. It never confirms facts, approves, sends, posts or signs
// off. It stops when a person stops it, when the close is signed off, or when its owner leaves the firm.
//
// Each goal has a close_goal workflow run that takes its next step about once a minute (lookAtGoal); the hourly
// close_goal_upkeep run hands newly ended months over and starts a run for any open goal without one.

func init() {
	close_goal.Next = func(ctx context.Context, goalID string) (bool, error) {
		return lookAtGoal(ctx, wf.DB(ctx), goalID, recentRoles.of)
	}
	close_goal_upkeep.HandOverMonths = func(ctx context.Context) (int, error) { return handOverMonths(ctx, wf.DB(ctx), time.Now()) }
	close_goal_upkeep.WatchGoals = func(ctx context.Context) (int, error) { return watchGoals(ctx, wf.DB(ctx)) }
}

type goalItem struct {
	JobID    string   `json:"job_id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Text     string   `json:"text"`
	Drafts   []string `json:"drafts"`
	Handable bool     `json:"handable,omitempty"`
}

// goalProgress is where every current document of a close stands. Needs are close-wide things a person must do. Ready
// means every current document has a draft ready for review; a goal that finishes at sign-off also carries what sign-off
// needs.
type goalProgress struct {
	Items    []goalItem    `json:"items"`
	Needs    []string      `json:"needs"`
	Ready    bool          `json:"ready"`
	Signoff  *goalSignoff  `json:"signoff,omitempty"`
	Coverage *goalCoverage `json:"coverage,omitempty"`
	Plan     []planStep    `json:"plan,omitempty"`
	// accounts is the capture's coverage when it was read, for handing accounts to teammates.
	accounts *coverageView
	// approve counts documents whose drafts a sign-off goal needs approved; limited, a close past coverage's limit.
	approve int
	limited bool
}

const coverageLimitNeed = "Account coverage and sign-off read at most 100 intake jobs, and this close has more, so it can't be signed off here."

// goalCoverage counts the accounts of the close's ledger capture, and those not yet resolved or excluded.
type goalCoverage struct {
	Total int `json:"total"`
	Open  int `json:"open"`
}

// goalSignoff is what signing the close off needs, from its readiness: its needs that are not about a listed document,
// each approved draft's proposed entry and where it stands, who signed off, and next month's close once one exists.
type goalSignoff struct {
	State     string      `json:"state"`
	Signer    string      `json:"signer"`
	Needs     []string    `json:"needs"`
	Entries   []goalEntry `json:"entries"`
	NextClose string      `json:"next_close"`
}

type goalEntry struct {
	Draft string `json:"draft"`
	Text  string `json:"text"`
}

type goalRow struct {
	id, close, org, author, state, scope, finish string
	ready                                        bool
}

var goalFinishes = map[string]string{"review": "ready for review", "signoff": "signed off"}

var goalForms = map[string]string{"deferred_revenue": "Deferred revenue", "depreciation": "Depreciation", "loan": "Loan schedule", "owner": "Owner items", "settlement": "Settlement"}

// goalJobs is each current document's job with its proposal, the drafts it made and its latest client request.
const goalJobs = `SELECT j.id::text,e.name,j.state,j.error,e.extraction_state,COALESCE(i.proposal->>'kind',''),COALESCE(i.proposal->>'summary',''),
 COALESCE(NULLIF(i.proposal->'questions','null'::jsonb),'[]'::jsonb)||jsonb_path_query_array(COALESCE(i.proposal,'{}'::jsonb),'$.facts[*].question'),
 COALESCE(j.workpaper_ids,'[]'::jsonb),
 COALESCE((SELECT jsonb_build_object('id',q.id::text,
   'partial',COALESCE((SELECT NOT s.complete FROM request_submissions s WHERE s.request_id=q.id ORDER BY s.id DESC LIMIT 1),false),
   'undelivered',EXISTS(SELECT 1 FROM (SELECT * FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1) m WHERE ` + undeliveredEmail + `),
   'unsent',q.delivered_at IS NULL AND NOT EXISTS(SELECT 1 FROM email_delivery m WHERE m.request_id=q.id),
   'follow',` + reminderFollow + `)
  FROM request_jobs rj JOIN work_requests q ON q.id=rj.request_id WHERE rj.job_id=j.id ORDER BY q.id DESC LIMIT 1),'null'::jsonb)
 FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id
 WHERE j.close_id=$1 AND e.superseded_by IS NULL ORDER BY j.id`

// readGoalProgress classifies every current document of the close by what it waits on, from saved state; with finish
// "signoff" it adds what sign-off needs.
func readGoalProgress(ctx context.Context, tx *sql.Tx, closeID, finish string) (goalProgress, error) {
	p := goalProgress{Items: []goalItem{}, Needs: []string{}}
	type draft struct {
		state    string
		blockers []string
	}
	drafts, newer := map[string]draft{}, map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT w.id::text,COALESCE(w.paper->>'revises',''),w.review_state,CASE WHEN jsonb_typeof(w.paper->'blockers')='array' THEN w.paper->'blockers' ELSE '[]'::jsonb END FROM workpaper_review_state w WHERE w.close_id=$1 ORDER BY w.id`, closeID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var id, revises, state string
		var raw []byte
		var d draft
		if err = rows.Scan(&id, &revises, &state, &raw); err == nil {
			err = json.Unmarshal(raw, &d.blockers)
		}
		if err != nil {
			rows.Close()
			return p, err
		}
		d.state = state
		drafts[id] = d
		if revises != "" {
			newer[revises] = id // ascending ids, so the newest revision wins
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return p, err
	}
	// current follows a draft through its revisions; the newest one is the draft under review.
	current := func(id string) string {
		for n := 0; newer[id] != "" && n < len(drafts); n++ {
			id = newer[id]
		}
		return id
	}
	rows, err = tx.QueryContext(ctx, goalJobs, closeID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	prepared, open := 0, 0
	// waiting is the documents waiting on a client request, by their place in p.Items.
	type wait struct {
		at      int
		request string
	}
	waiting := []wait{}
	for rows.Next() {
		var state, failure, extraction, kind, summary string
		var questionsRaw, papersRaw, requestRaw []byte
		item := goalItem{Drafts: []string{}}
		if err = rows.Scan(&item.JobID, &item.Name, &state, &failure, &extraction, &kind, &summary, &questionsRaw, &papersRaw, &requestRaw); err != nil {
			return p, err
		}
		var questions, papers, unapproved []string
		var request *struct {
			ID          string `json:"id"`
			Partial     bool   `json:"partial"`
			Undelivered bool   `json:"undelivered"`
			Unsent      bool   `json:"unsent"`
			Follow      string `json:"follow"`
		}
		if err = errors.Join(json.Unmarshal(questionsRaw, &questions), json.Unmarshal(papersRaw, &papers), json.Unmarshal(requestRaw, &request)); err != nil {
			return p, err
		}
		// A fact without a question carries an empty one.
		questions = slices.DeleteFunc(questions, func(q string) bool { return strings.TrimSpace(q) == "" })
		item.Status = "needs_you"
		switch state {
		case "cancelled":
			item.Status, item.Text = "stopped", strings.TrimSpace(failure+" Stopped documents are left out.")
		case "failed":
			item.Text = strings.TrimSpace("Preparation failed: " + failure + " Fix the document or retry it in Work.")
		case "waiting_evidence", "interpreting", "queued":
			item.Status, item.Text = "working", map[string]string{"waiting_evidence": "Waiting to be read.", "interpreting": "Reading it now.", "queued": "Preparing its draft."}[state]
			if extraction == "failed" {
				item.Status, item.Text = "needs_you", "Its text could not be extracted. Repair it on the Evidence tab, then recheck it in Work."
			}
		case "needs_confirmation":
			switch {
			case kind == "unsupported":
				why := summary
				if len(questions) > 0 {
					why = questions[0]
				}
				item.Status, item.Text = "unsupported", strings.TrimSpace("I can't prepare this kind of work. "+why+" Answer in Work, hand it to a teammate, prepare it by hand, or stop it.")
			case len(questions) > 0:
				item.Text = fmt.Sprintf("Answer %s in Work: %s", plural(len(questions), "question", "questions"), questions[0])
			case goalForms[kind] != "":
				item.Text = "Open its " + goalForms[kind] + " form from Work, check the facts I filled in and save it."
			default:
				item.Text = "Confirm the facts I read from it in Work."
			}
		case "waiting_client":
			switch {
			case request == nil:
				item.Status, item.Text = "client", "Waiting on the client."
			case request.Partial:
				item.Text = "The client answered part of request #" + request.ID + ". Resume now from Work, or wait for the rest."
			case request.Undelivered:
				item.Text = "The email for request #" + request.ID + " was not delivered. Check it on the Requests tab."
			case request.Unsent:
				item.Text = "Request #" + request.ID + " is approved but not sent. Send it, or record how you delivered it."
			// Following up is the firm's decision: a reminder is due under its rule, or the rule allows no more.
			case request.Follow == "due":
				item.Text = "A reminder for request #" + request.ID + " is due under the firm's rule. Approve it on the Requests tab."
			case request.Follow == "exhausted":
				item.Text = "Request #" + request.ID + " is unanswered after all the reminders the firm's rule allows. Contact the client another way."
			case request.Follow == "expiring":
				item.Text = "The link for request #" + request.ID + " has expired, or expires before the next reminder is due. Contact the client another way."
			default:
				item.Status, item.Text = "client", "Waiting on the client's reply to request #"+request.ID+"."
			}
			if request != nil && !request.Partial && !request.Undelivered && !request.Unsent {
				waiting = append(waiting, wait{len(p.Items), request.ID})
			}
		case "completed":
			ready, approved := []string{}, []string{}
			for _, id := range papers {
				id = current(id)
				d, ok := drafts[id]
				item.Drafts = append(item.Drafts, id)
				switch {
				case !ok:
					item.Text = "Draft #" + id + " is no longer in this close. Check it in Work."
				case d.state == "changes_requested":
					item.Text = "Changes were requested on draft #" + id + "."
				case d.state == "source_changed":
					item.Text = "A source of draft #" + id + " changed. Revise the draft."
				case len(d.blockers) > 0:
					item.Text = "Draft #" + id + " is blocked: " + d.blockers[0]
				case d.state == "approved":
					approved = append(approved, "#"+id)
				default:
					ready = append(ready, "#"+id)
				}
				if item.Text != "" {
					break
				}
			}
			switch {
			case len(papers) == 0:
				item.Text = "It finished without a draft. Check it in Work."
			case item.Text != "":
			case len(ready) == 0:
				item.Status, item.Text = "prepared", "Approved: draft "+strings.Join(approved, ", ")+"."
			default:
				item.Status, item.Text = "prepared", "Ready for review: draft "+strings.Join(ready, ", ")+"."
				if len(approved) > 0 {
					item.Text += " Already approved: draft " + strings.Join(approved, ", ") + "."
				}
				unapproved = ready
			}
		default:
			item.Text = "In an unexpected state: " + strings.ReplaceAll(state, "_", " ") + ". Check it in Work."
		}
		switch item.Status {
		case "prepared":
			prepared++
		case "stopped":
		default:
			open++
		}
		// A teammate can look into what the document itself needs with review permission; a client request's sending
		// needs its own permission, and approving a draft is left to the firm's reviewers.
		item.Handable = (item.Status == "needs_you" || item.Status == "unsupported") && state != "waiting_client"
		// Before sign-off a person approves each draft, so an unapproved one is theirs to act on.
		if finish == "signoff" && len(unapproved) > 0 {
			item.Status, item.Text = "needs_you", "Review and approve draft "+strings.Join(unapproved, ", ")+"."
			p.approve++
		}
		p.Items = append(p.Items, item)
	}
	if err = rows.Err(); err != nil {
		return p, err
	}
	// A request whose work changed takes no reply, so waiting on it, or following it up, is a person's to redo.
	for _, w := range waiting {
		if stale := preparation.FreshClientRequest(ctx, tx, closeID, w.request); stale != nil {
			p.Items[w.at].Status, p.Items[w.at].Text = "needs_you", "Request #"+w.request+" no longer takes replies. "+stale.Error()
		}
	}
	switch {
	case len(p.Items) == 0:
		p.Needs = append(p.Needs, "Add the client's documents: upload, forward or request them, and I'll take it from there.")
	case prepared+open == 0:
		p.Needs = append(p.Needs, "Every document here is stopped. Add documents, or restart one from Work.")
	case len(p.Items) > 100:
		p.Needs = append(p.Needs, "This close has more than 100 current documents, more than one preparation scope holds. Prepare it from Work.")
	}
	var clean, unscanned, held int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE state='clean'),count(*) FILTER (WHERE state NOT IN ('clean','released','rejected')),(SELECT count(*) FROM inbound_replies WHERE close_id=$1 AND state='held') FROM client_attachments WHERE close_id=$1`, closeID).Scan(&clean, &unscanned, &held); err != nil {
		return p, err
	}
	if clean > 0 {
		p.Needs = append(p.Needs, "Release or reject "+plural(clean, "scanned client file", "scanned client files")+" on the Requests tab.")
	}
	if unscanned > 0 {
		p.Needs = append(p.Needs, plural(unscanned, "client file has", "client files have")+" no clean scan yet. Reject them, or ask the client to send them again from their link.")
	}
	if held > 0 {
		p.Needs = append(p.Needs, "Review "+plural(held, "held email reply", "held email replies")+" on the Requests tab.")
	}
	coverage, err := ledgerCoverage(ctx, tx, closeID)
	if err != nil {
		return p, err
	}
	p.Ready = prepared > 0 && open == 0 && len(p.Needs) == 0
	// Sign-off comes after the documents; its readiness is read only once they are ready, or while a sign-off stands.
	// Account coverage is read with it, or on its own while sign-off is not read.
	signed, err := signedOff(ctx, tx, closeID)
	if err != nil {
		return p, err
	}
	readSignoff := finish == "signoff" && (p.Ready || signed)
	var ready readiness
	var accounts coverageView
	switch {
	case readSignoff:
		ready, err = closeReadiness(ctx, tx, closeID)
		accounts = ready.coverage
	case string(coverage) != "null":
		accounts, err = currentCoverage(ctx, tx, closeID)
	}
	if errors.Is(err, errCoverageLimit) {
		p.limited = true
		if finish == "signoff" {
			p.Needs = append(p.Needs, coverageLimitNeed)
		}
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if accounts.SnapshotID != "" {
		p.accounts = &accounts
		p.Coverage = &goalCoverage{Total: len(accounts.Items)}
		for _, item := range accounts.Items {
			if item.State != "resolved" && item.State != "excluded" {
				p.Coverage.Open++
			}
		}
	}
	if !readSignoff {
		return p, nil
	}
	s := &goalSignoff{State: ready.State, Needs: []string{}, Entries: []goalEntry{}}
	listed := map[string]bool{}
	for _, item := range p.Items {
		listed["Work #"+item.JobID] = true
		for _, d := range item.Drafts {
			listed["Draft #"+d] = true
		}
	}
	for _, b := range ready.Blockers {
		// Documents on the list, their drafts, client files and accounts (counted in coverage) are already shown.
		job, _, _ := strings.Cut(b, " is ")
		draft, _, _ := strings.Cut(b, " requires ")
		if listed[job] || listed[draft] || strings.Contains(b, "client attachments") || strings.HasPrefix(b, "Coverage for ") {
			continue
		}
		s.Needs = append(s.Needs, b)
	}
	// Each posting record is shown on its own: one per provider, whose ids and order are not comparable.
	entries, err := tx.QueryContext(ctx, `SELECT w.id::text,COALESCE(p.state,''),COALESCE(p.approval_expires_at<now(),false),COALESCE(p.external_id,''),COALESCE(p.target,'')
 FROM workpaper_review_state w LEFT JOIN (
  SELECT workpaper_id,state,approval_expires_at,external_id,'QuickBooks' AS target FROM production_postings WHERE close_id=$1
  UNION ALL SELECT workpaper_id,state,approval_expires_at,external_id,'Xero' FROM xero_postings WHERE close_id=$1
  UNION ALL SELECT workpaper_id,state,approval_expires_at,external_id,'the QuickBooks sandbox' FROM sandbox_postings WHERE close_id=$1) p ON p.workpaper_id=w.id
 WHERE w.close_id=$1 AND w.review_state='approved' AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)
 AND (COALESCE(w.paper->'proposal','null'::jsonb)<>'null'::jsonb OR jsonb_array_length(COALESCE(w.paper->'proposals','[]'::jsonb))>0) ORDER BY w.id,p.target`, closeID)
	if err != nil {
		return p, err
	}
	defer entries.Close()
	for entries.Next() {
		var e goalEntry
		var state, external, target string
		var expired bool
		if err = entries.Scan(&e.Draft, &state, &expired, &external, &target); err != nil {
			return p, err
		}
		switch {
		case state == "verified":
			e.Text = "Posted to " + target + " and confirmed by its readback" + map[bool]string{true: " (" + external + ")"}[external != ""] + "."
		case state == "executing" || state == "unknown":
			e.Text = "Its posting to " + target + " has an unknown outcome. Check it on the draft before anything else."
		case state == "approved" && expired:
			e.Text = "Its approval to post expired. Approve and post it again from the draft, or book it in the ledger yourself."
		case state == "approved":
			e.Text = "Approved to post to " + target + ". Post it from the draft."
		default:
			e.Text = "Not posted in the app. Post it from the draft, or book it in the ledger yourself."
		}
		s.Entries = append(s.Entries, e)
	}
	if err = entries.Err(); err != nil {
		return p, err
	}
	if ready.State == "signed_off" {
		if err = tx.QueryRowContext(ctx, `SELECT actor FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&s.Signer); err != nil {
			return p, err
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT n.id::text FROM client_closes c JOIN client_closes n ON n.organization_id=c.organization_id AND lower(n.entity)=lower(c.entity) AND n.period=to_char(to_date(c.period,'YYYY-MM')+interval '1 month','YYYY-MM') WHERE c.id=$1),'')`, closeID).Scan(&s.NextClose)
	p.Signoff = s
	return p, err
}

// signedOff reports whether the close's latest sign-off decision is a sign-off, as Work and Attention read it; a close
// must be reopened before an agent works on it again.
func signedOff(ctx context.Context, tx *sql.Tx, closeID string) (bool, error) {
	var signed bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT decision FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1),'')='signed_off'`, closeID).Scan(&signed)
	return signed, err
}

func goalEvent(ctx context.Context, tx *sql.Tx, id, actor, text string) error {
	_, err := tx.ExecContext(ctx, `UPDATE close_goals SET events=events||jsonb_build_array(jsonb_build_object('actor',$2::text,'text',$3::text,'at',now())) WHERE id=$1`, id, actor, text)
	return err
}

// handOver hands the close g.close, whose row the caller holds, to the agent as g.author, with text as the first line of
// its history, unless the close is signed off or already handed over.
func handOver(ctx context.Context, tx *sql.Tx, g *goalRow, due, text string) *closeActionError {
	done, err := signedOff(ctx, tx, g.close)
	if err != nil {
		return closeRefusal(503, "Close readiness unavailable.")
	}
	if done {
		return closeRefusal(409, "This close is signed off. Reopen it before handing it to the agent.")
	}
	var owner string
	var since time.Time
	switch err = tx.QueryRowContext(ctx, `SELECT author,created_at FROM close_goals WHERE close_id=$1 AND state<>'stopped'`, g.close).Scan(&owner, &since); err {
	case nil:
		return closeRefusal(409, owner+" already handed this close to the agent on "+since.UTC().Format("2 Jan 2006")+".")
	case sql.ErrNoRows:
	default:
		return closeRefusal(503, "The agent is unavailable.")
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO close_goals(close_id,author,due_on,finish,events) VALUES($1,$2,NULLIF($3,'')::date,$5,jsonb_build_array(jsonb_build_object('actor',$2::text,'text',$4::text,'at',now()))) RETURNING id::text`, g.close, g.author, due, text, g.finish).Scan(&g.id)
	if err == nil {
		err = advanceGoal(ctx, tx, *g, nil)
	}
	if err == nil {
		err = startGoalRun(ctx, tx, *g, "user:"+g.author)
	}
	if err != nil {
		return closeRefusal(503, "The close could not be handed to the agent. Refresh and try again.")
	}
	return nil
}

// startGoalRun starts the close_goal run that takes goal g's next steps, in the transaction that created the goal.
func startGoalRun(ctx context.Context, tx *sql.Tx, g goalRow, by string) error {
	_, _, err := wf.Start(ctx, tx, wf.StartRequest{Workflow: "close_goal", Organization: g.org, Input: close_goal.Input{GoalID: g.id}, IdempotencyKey: "goal:" + g.id, StartedBy: by})
	return err
}

// stopGoal stops goal id. Its close_goal run sees that on its next look and ends.
func stopGoal(ctx context.Context, tx *sql.Tx, id, actor, text string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE close_goals SET state='stopped',reached_at=NULL,finished_at=now(),checked_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE goal_handoffs SET closed_at=now(),closed_by=$2 WHERE goal_id=$1 AND closed_at IS NULL`, id, actor); err != nil {
		return err
	}
	return goalEvent(ctx, tx, id, actor, text)
}

// advanceGoal takes the goal's next step in tx, whose caller holds the close row and then the goal row. roles, when
// known, are the firm's current WorkOS roles, which the author and teammates must still hold.
func advanceGoal(ctx context.Context, tx *sql.Tx, g goalRow, roles map[string]reviewer) error {
	// Holding the firm row serializes this step with a member's removal; membership is read in a second statement so it
	// sees a removal the lock waited for. A suspended firm's goals wait.
	var enabled, member, granted bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1 FOR SHARE`, g.org).Scan(&enabled); err != nil || !enabled {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT $1='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted),
 EXISTS(SELECT 1 FROM current_client_access a JOIN client_closes c ON lower(c.entity)=lower(a.client_entity) WHERE c.id=$3 AND a.organization_id=$1 AND a.subject=$2 AND a.granted)`, g.org, g.author, g.close).Scan(&member, &granted); err != nil {
		return err
	}
	if !member {
		return stopGoal(ctx, tx, g.id, "agent", "I stopped because "+g.author+" is no longer a member of the firm.")
	}
	if !mayAct(roles, g.author, member, false, granted) {
		return stopGoal(ctx, tx, g.id, "agent", "I stopped because "+g.author+" can no longer review this client's work.")
	}
	signed, err := signedOff(ctx, tx, g.close)
	switch {
	case err != nil:
		return err
	case signed && g.finish == "review":
		return stopGoal(ctx, tx, g.id, "agent", "The close was signed off, so there is nothing left for me to prepare.")
	case signed:
		// A sign-off goal is reached while the sign-off stands. A close that changed after its sign-off waits for a person
		// to reopen it, and the agent prepares nothing meanwhile.
		current, err := closeReadiness(ctx, tx, g.close)
		if errors.Is(err, errCoverageLimit) {
			// Past coverage's limit the sign-off cannot be checked: the goal is not reached, and Work says what the card says.
			if g.state == "reached" {
				if _, err = tx.ExecContext(ctx, `UPDATE close_goals SET state='active',reached_at=NULL WHERE id=$1`, g.id); err == nil {
					err = goalEvent(ctx, tx, g.id, "agent", "The close has more intake jobs than sign-off can check, so I can't confirm its sign-off.")
				}
				if err != nil {
					return err
				}
			}
			progress, err := readGoalProgress(ctx, tx, g.close, g.finish)
			if err == nil {
				err = settleHandoffs(ctx, tx, g, goalProgress{}, true, roles)
			}
			if err == nil {
				err = saveSummary(ctx, tx, g, progress)
			}
			return err
		}
		if err != nil {
			return err
		}
		var signer string
		if err = tx.QueryRowContext(ctx, `SELECT actor FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, g.close).Scan(&signer); err != nil {
			return err
		}
		switch {
		case current.State == "signed_off" && g.state != "reached":
			if _, err = tx.ExecContext(ctx, `UPDATE close_goals SET state='reached',reached_at=now() WHERE id=$1`, g.id); err == nil {
				err = goalEvent(ctx, tx, g.id, "agent", "Signed off by "+signer+".")
			}
		case current.State != "signed_off" && g.state == "reached":
			if _, err = tx.ExecContext(ctx, `UPDATE close_goals SET state='active',reached_at=NULL WHERE id=$1`, g.id); err == nil {
				err = goalEvent(ctx, tx, g.id, "agent", "Something changed after the sign-off, so the close needs reopening before I continue.")
			}
		}
		if err == nil {
			err = settleHandoffs(ctx, tx, g, goalProgress{}, true, roles)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE close_goals SET checked_at=now(),summary=jsonb_build_object('needs',0,'ready',true,'signoff',$2::text,'as_of',now()) WHERE id=$1`, g.id, current.State)
		}
		return err
	case g.finish == "signoff" && g.state == "reached":
		if _, err = tx.ExecContext(ctx, `UPDATE close_goals SET state='active',reached_at=NULL WHERE id=$1`, g.id); err == nil {
			err = goalEvent(ctx, tx, g.id, "agent", "The close was reopened, so I'm back on it.")
		}
		if err != nil {
			return err
		}
		g.state = "active"
	}
	inventory, err := preparationInventory(ctx, tx, g.close)
	if err != nil {
		return err
	}
	coverage, err := ledgerCoverage(ctx, tx, g.close)
	if err != nil {
		return err
	}
	var sources []json.RawMessage
	if err = json.Unmarshal(scopeSources(inventory), &sources); err != nil {
		return err
	}
	// The goal compares with the scope it last brought in, not the latest run, so a scope that returns to an earlier
	// run is recorded once.
	if digest := scopeDigest(inventory, coverage); digest != g.scope && len(sources) > 0 && len(sources) <= 100 {
		run, created, refused := prepareClose(ctx, tx, g.close, digest, "close goal #"+g.id+" for "+g.author, "")
		if refused != nil {
			return errors.New(refused.message)
		}
		text := fmt.Sprintf("I brought %s into preparation as run #%s.", plural(len(sources), "document", "documents"), run)
		if !created {
			text = fmt.Sprintf("The documents match preparation run #%s again.", run)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE close_goals SET scope_digest=$2 WHERE id=$1`, g.id, digest); err == nil {
			err = goalEvent(ctx, tx, g.id, "agent", text)
		}
		if err != nil {
			return err
		}
	}
	// Ready for review is the finish of a review goal and a milestone, kept in ready_at, of a sign-off goal.
	progress, err := readGoalProgress(ctx, tx, g.close, "review")
	if err == nil {
		err = settleHandoffs(ctx, tx, g, progress, false, roles)
	}
	if err != nil {
		return err
	}
	was, mark := g.ready, `UPDATE close_goals SET ready_at=CASE WHEN $2 THEN now() END WHERE id=$1`
	if g.finish == "review" {
		was, mark = g.state == "reached", `UPDATE close_goals SET state=CASE WHEN $2 THEN 'reached' ELSE 'active' END,reached_at=CASE WHEN $2 THEN now() END WHERE id=$1`
	}
	switch {
	case progress.Ready && !was:
		drafts, documents := 0, 0
		for _, item := range progress.Items {
			if item.Status == "prepared" {
				drafts += len(item.Drafts)
				documents++
			}
		}
		text := fmt.Sprintf("Every uploaded document has a draft ready for review: %s from %s.", plural(drafts, "draft", "drafts"), plural(documents, "document", "documents"))
		if stopped := len(progress.Items) - documents; stopped > 0 {
			text += " " + plural(stopped, "stopped document is", "stopped documents are") + " left out."
		}
		if g.finish == "signoff" {
			text += " Next: approvals and sign-off."
		}
		if _, err = tx.ExecContext(ctx, mark, g.id, true); err == nil {
			err = goalEvent(ctx, tx, g.id, "agent", text)
		}
	case !progress.Ready && was:
		if _, err = tx.ExecContext(ctx, mark, g.id, false); err == nil {
			err = goalEvent(ctx, tx, g.id, "agent", "New work arrived after the drafts were ready, so I'm back on it.")
		}
	}
	if err != nil {
		return err
	}
	// Work lists every delegated close from this summary; what people owe is counted as the goal card counts it.
	if g.finish == "signoff" {
		if progress, err = readGoalProgress(ctx, tx, g.close, g.finish); err != nil {
			return err
		}
	}
	return saveSummary(ctx, tx, g, progress)
}

// saveSummary records what Work lists for the goal, counting what people owe as the goal card counts it.
func saveSummary(ctx context.Context, tx *sql.Tx, g goalRow, p goalProgress) error {
	needs, signoff := len(p.Needs), ""
	for _, item := range p.Items {
		if item.Status == "needs_you" || item.Status == "unsupported" {
			needs++
		}
	}
	if p.Signoff != nil {
		needs, signoff = needs+len(p.Signoff.Needs), p.Signoff.State
	}
	if g.finish == "signoff" && p.Coverage != nil && p.Coverage.Open > 0 {
		needs++
	}
	_, err := tx.ExecContext(ctx, `UPDATE close_goals SET checked_at=now(),summary=jsonb_build_object('needs',$2::int,'ready',$3::boolean,'signoff',$4::text,'as_of',now()) WHERE id=$1`, g.id, needs, p.Ready, signoff)
	return err
}

// goalActivity is what happened on the close since its goal started, newest first: the goal's own events (in the order
// they were recorded) and the steps people and the client took, with how many there are in all.
const goalActivity = `WITH postings AS (
 SELECT workpaper_id,attempted_at,attempted_by,verified_at,external_id,'QuickBooks' AS target FROM production_postings WHERE close_id=$1
 UNION ALL SELECT workpaper_id,attempted_at,attempted_by,verified_at,external_id,'Xero' FROM xero_postings WHERE close_id=$1
 UNION ALL SELECT workpaper_id,attempted_at,attempted_by,verified_at,external_id,'the QuickBooks sandbox' FROM sandbox_postings WHERE close_id=$1)
SELECT at,actor,text,count(*) OVER () FROM (
 SELECT (e->>'at')::timestamptz AS at,e->>'actor' AS actor,e->>'text' AS text,n FROM close_goals g CROSS JOIN LATERAL jsonb_array_elements(g.events) WITH ORDINALITY x(e,n) WHERE g.id=$2
 UNION ALL SELECT i.confirmed_at,i.confirmed_by,CASE WHEN i.confirmed_by='agent' THEN 'The agent prepared '||e.name||' from verified facts, without confirmation' ELSE i.confirmed_by||' confirmed the facts read from '||e.name END,0 FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 AND i.confirmed_at IS NOT NULL
 UNION ALL SELECT a.created_at,a.created_by,a.created_by||' answered a question on '||e.name,0 FROM preparation_answers a JOIN preparation_intakes i ON i.job_id=a.job_id JOIN preparation_jobs j ON j.id=a.job_id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1
 UNION ALL SELECT q.approved_at,q.approved_by,q.approved_by||' approved request #'||q.id||' to the client',0 FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1
 UNION ALL SELECT q.delivered_at,q.delivered_by,q.delivered_by||' recorded delivery of request #'||q.id,0 FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1 AND q.delivered_at IS NOT NULL
 UNION ALL SELECT m.finished_at,'email',CASE WHEN m.reminder_of IS NULL THEN 'The email for request #' ELSE 'A reminder for request #' END||m.request_id||' was sent',0 FROM email_outbox m JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1 AND m.state='accepted'
 UNION ALL SELECT s.created_at,'client',CASE WHEN s.complete THEN 'The client replied to request #' ELSE 'The client answered part of request #' END||s.request_id,0 FROM request_submissions s JOIN work_requests q ON q.id=s.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1
 UNION ALL SELECT r.recorded_at,r.recorded_by,r.recorded_by||' recorded the client''s reply to request #'||r.request_id,0 FROM work_replies r JOIN work_requests q ON q.id=r.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1
 UNION ALL SELECT i.received_at,'email','An email reply to request #'||i.request_id||' arrived and was held for review',0 FROM inbound_replies i WHERE i.close_id=$1
 UNION ALL SELECT i.decided_at,i.decided_by,i.decided_by||' '||i.state||' an email reply to request #'||i.request_id,0 FROM inbound_replies i WHERE i.close_id=$1 AND i.decided_at IS NOT NULL
 UNION ALL SELECT r.created_at,r.actor,r.actor||CASE r.decision WHEN 'approved' THEN ' approved draft #' ELSE ' asked for changes to draft #' END||r.workpaper_id,0 FROM workpaper_reviews r JOIN workpapers w ON w.id=r.workpaper_id WHERE w.close_id=$1
 UNION ALL SELECT x.created_at,x.actor,x.actor||' rechecked '||e.name,0 FROM recovery_actions x JOIN preparation_intakes i ON i.job_id=x.job_id JOIN evidence e ON e.id=i.source_id WHERE x.close_id=$1
 UNION ALL SELECT h.created_at+interval '2 days','agent','I followed up with '||h.assignee_name||' on '||h.document,0 FROM goal_handoffs h WHERE h.goal_id=$2 AND h.created_at+interval '2 days'<=LEAST(h.closed_at,now())
 UNION ALL SELECT ` + handoffEscalated + `,'agent','I asked '||g.author||' to check with '||h.assignee_name||' on '||h.document,0 FROM goal_handoffs h JOIN close_goals g ON g.id=h.goal_id WHERE h.goal_id=$2 AND h.assignee<>g.author AND ` + handoffEscalated + `<=LEAST(h.closed_at,now())
 UNION ALL SELECT s.created_at,s.actor,s.actor||' set what sign-off covers: '||(s.scope->>'description'),0 FROM close_scopes s WHERE s.close_id=$1
 UNION ALL SELECT s.created_at,s.actor,s.actor||CASE s.decision WHEN 'signed_off' THEN ' signed off the close: ' ELSE ' reopened the close: ' END||s.note,0 FROM close_signoffs s WHERE s.close_id=$1
 UNION ALL SELECT p.attempted_at,p.attempted_by,p.attempted_by||' posted the entry for draft #'||p.workpaper_id||' to '||p.target,0 FROM postings p WHERE p.attempted_at IS NOT NULL
 UNION ALL SELECT p.verified_at,'ledger',p.target||' confirmed the entry for draft #'||p.workpaper_id||COALESCE(' ('||NULLIF(p.external_id,'')||')',''),0 FROM postings p WHERE p.verified_at IS NOT NULL
) a WHERE at>=$3 ORDER BY at DESC,n DESC LIMIT $4`

func registerGoals(mux *http.ServeMux, db *sql.DB) {
	// Every close handed to the agent that the member can access, with what the agent last found: those needing a person
	// first, then the rest still in progress, then those finished in the past 14 days.
	mux.HandleFunc("GET /v1/closes/goals", func(w http.ResponseWriter, r *http.Request) {
		search := strings.TrimSpace(r.URL.Query().Get("client"))
		if len(search) > 200 {
			problem(w, 422, "Client search is too long.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT g.id::text,c.id::text,c.entity,c.period,g.finish,COALESCE(g.due_on::text,''),g.state,g.author,g.summary,
 (SELECT count(*) FROM goal_handoffs h WHERE h.goal_id=g.id AND h.closed_at IS NULL)
 FROM close_goals g JOIN client_closes c ON c.id=g.close_id
 WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access a WHERE a.organization_id=c.organization_id AND a.subject=$3 AND lower(a.client_entity)=lower(c.entity) AND a.granted))
 AND ($4='' OR c.entity ILIKE '%'||$4||'%' OR c.period=$4) AND (g.state='active' OR g.state='reached' AND g.reached_at>now()-interval '14 days')
 ORDER BY CASE WHEN (g.summary->>'needs')::int>0 OR g.summary->>'signoff'='reopen_required' OR g.summary->>'signoff'='open' AND (g.summary->>'ready')::boolean THEN 0 WHEN g.state='active' THEN 1 ELSE 2 END,g.due_on NULLS LAST,lower(c.entity),c.period LIMIT 101`, organization(r), firmAccess(r), subject(r), search)
		if err != nil {
			problem(w, 503, "Delegated closes are unavailable.")
			return
		}
		defer rows.Close()
		type delegated struct {
			ID      string          `json:"id"`
			CloseID string          `json:"close_id"`
			Entity  string          `json:"entity"`
			Period  string          `json:"period"`
			Finish  string          `json:"finish"`
			DueOn   string          `json:"due_on"`
			State   string          `json:"state"`
			Author  string          `json:"author"`
			Summary json.RawMessage `json:"summary"`
			Handed  int             `json:"handed"`
		}
		goals := []delegated{}
		for rows.Next() {
			var d delegated
			if err = rows.Scan(&d.ID, &d.CloseID, &d.Entity, &d.Period, &d.Finish, &d.DueOn, &d.State, &d.Author, &d.Summary, &d.Handed); err != nil {
				problem(w, 503, "Delegated closes are unavailable.")
				return
			}
			goals = append(goals, d)
		}
		if rows.Err() != nil {
			problem(w, 503, "Delegated closes are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"goals": goals[:min(len(goals), 100)], "more": len(goals) > 100})
	})
	mux.HandleFunc("GET /v1/closes/{id}/goal", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "The agent's progress is unavailable.")
			return
		}
		defer tx.Rollback()
		closeID := r.PathValue("id")
		type view struct {
			ID         string          `json:"id"`
			Author     string          `json:"author"`
			DueOn      string          `json:"due_on"`
			State      string          `json:"state"`
			CreatedAt  time.Time       `json:"created_at"`
			ReachedAt  *time.Time      `json:"reached_at"`
			FinishedAt *time.Time      `json:"finished_at"`
			Finish     string          `json:"finish"`
			ReadyAt    *time.Time      `json:"ready_at"`
			Events     json.RawMessage `json:"events"`
		}
		var goal *view
		g := &view{}
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,author,COALESCE(due_on::text,''),state,created_at,reached_at,finished_at,finish,ready_at,events FROM close_goals WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&g.ID, &g.Author, &g.DueOn, &g.State, &g.CreatedAt, &g.ReachedAt, &g.FinishedAt, &g.Finish, &g.ReadyAt, &g.Events)
		switch err {
		case nil:
			goal = g
		case sql.ErrNoRows:
			err = nil
		}
		type activity struct {
			At    time.Time `json:"at"`
			Actor string    `json:"actor"`
			Text  string    `json:"text"`
		}
		history, total := []activity{}, 0
		if err == nil && goal != nil {
			// The card shows the newest 50; ?activity=all reads the whole history.
			var limit any = 50
			if r.URL.Query().Get("activity") == "all" {
				limit = nil
			}
			var rows *sql.Rows
			if rows, err = tx.QueryContext(r.Context(), goalActivity, closeID, goal.ID, goal.CreatedAt, limit); err == nil {
				for rows.Next() {
					var a activity
					if err = rows.Scan(&a.At, &a.Actor, &a.Text, &total); err != nil {
						break
					}
					history = append(history, a)
				}
				rows.Close()
				if err == nil {
					err = rows.Err()
				}
			}
		}
		var progress goalProgress
		var done bool
		var routine *closeRoutine
		var closeInfo struct {
			Entity string `json:"entity"`
			Period string `json:"period"`
		}
		if err == nil {
			routine, err = readRoutine(r.Context(), tx, closeID)
		}
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT entity,period FROM client_closes WHERE id=$1`, closeID).Scan(&closeInfo.Entity, &closeInfo.Period)
		}
		if err == nil {
			finish := "review"
			if goal != nil && goal.State != "stopped" {
				finish = goal.Finish
			}
			progress, err = readGoalProgress(r.Context(), tx, closeID, finish)
			var books booksRead
			if err == nil {
				books, err = readBooks(r.Context(), tx, closeID)
			}
			progress.Plan = goalPlan(progress, books, closeID, closeInfo.Entity, closeInfo.Period, finish)
		}
		if err == nil {
			done, err = signedOff(r.Context(), tx, closeID)
		}
		handoffs := []goalHandoff{}
		if err == nil && goal != nil {
			handoffs, err = readHandoffs(r.Context(), tx, goal.ID, subject(r))
		}
		if err != nil {
			problem(w, 503, "The agent's progress is unavailable.")
			return
		}
		// Only the goal's owner is asked to check on a stalled hand-off; it is theirs to decide, not a need of the close.
		if i := slices.IndexFunc(progress.Plan, func(s planStep) bool { return s.Key == "documents" }); i >= 0 && goal != nil && goal.Author == subject(r) {
			for _, h := range handoffs {
				if h.stall != "" {
					progress.Plan[i].State, progress.Plan[i].Lines = "you", append(progress.Plan[i].Lines, h.stall)
				}
			}
		}
		// The checks behind the close's brief are heavier than the rest, so the card asks for them about once a minute.
		if r.URL.Query().Get("verify") == "1" {
			verified(mux, r, closeID, progress.Plan)
		}
		jsonReply(w, 200, map[string]any{"goal": goal, "progress": progress, "activity": history, "activity_total": total, "signed_off": done, "routine": routine, "close": closeInfo, "handoffs": handoffs})
	})
	mux.HandleFunc("POST /v1/closes/{id}/goal", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		actor := subject(r)
		var in struct {
			Confirmed  bool   `json:"confirmed"`
			DueOn      string `json:"due_on"`
			Finish     string `json:"finish"`
			EveryMonth bool   `json:"every_month"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Finish == "" {
			in.Finish = "review"
		}
		if !in.Confirmed || (in.Finish != "review" && in.Finish != "signoff") {
			problem(w, 422, "Confirm that the agent may work on this close until it is ready for review or signed off.")
			return
		}
		text, finish := "Handed over by "+actor, goalFinishes[in.Finish]
		var due time.Time
		if in.DueOn == "" {
			text += ", to get it " + finish
		} else {
			// The browser's date may be a day behind the server's.
			today := time.Now().UTC().Truncate(24 * time.Hour)
			var err error
			if due, err = time.Parse("2006-01-02", in.DueOn); err != nil || due.Before(today.AddDate(0, 0, -1)) || due.After(today.AddDate(1, 0, 1)) {
				problem(w, 422, "Choose a due date from today to a year ahead.")
				return
			}
			text += ", to get it " + finish + " by " + due.Format("Mon 2 Jan 2006")
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The agent is unavailable.")
			return
		}
		defer tx.Rollback()
		g := goalRow{author: actor, state: "active", finish: in.Finish}
		var entity, period string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,organization_id,entity,period FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&g.close, &g.org, &entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		// A monthly hand-over keeps the due date's distance after month end; one on or before month end has none.
		var dueDays *int
		if in.EveryMonth {
			if due.After(monthEnd(period)) {
				days := int(due.Sub(monthEnd(period)).Hours() / 24)
				if days > 60 {
					problem(w, 422, "For a monthly hand-over, choose a due date at most 60 days after the month ends.")
					return
				}
				dueDays = &days
			}
			var owner string
			switch err = tx.QueryRowContext(r.Context(), `SELECT author FROM close_routines WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND stopped_at IS NULL`, g.org, entity).Scan(&owner); err {
			case nil:
				problem(w, 409, owner+" already hands every new month of "+entity+" to the agent.")
				return
			case sql.ErrNoRows:
				text += ". Every new month of " + entity + " will be handed over too"
			default:
				problem(w, 503, "The agent is unavailable.")
				return
			}
		}
		if refused := handOver(r.Context(), tx, &g, in.DueOn, text); refused != nil {
			problem(w, refused.status, refused.message)
			return
		}
		if in.EveryMonth {
			// Months that ended before the hand-over was set up are not new; it starts after this close or with the current month.
			_, err = tx.ExecContext(r.Context(), `INSERT INTO close_routines(organization_id,client_entity,author,via_grant,finish,due_days,last_period) VALUES($1,$2,$3,$4,$5,$6,$7)`, g.org, entity, actor, !firmAccess(r), in.Finish, dueDays, max(period, endedMonth(time.Now())))
			if err != nil {
				problem(w, 503, "The close could not be handed to the agent. Refresh and try again.")
				return
			}
		}
		if tx.Commit() != nil {
			problem(w, 503, "The close could not be handed to the agent. Refresh and try again.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": g.id})
	})
	mux.HandleFunc("POST /v1/closes/{id}/goal/stop", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		actor := subject(r)
		var in struct {
			Reason string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Reason = strings.TrimSpace(in.Reason); len(in.Reason) < 1 || len(in.Reason) > 500 {
			problem(w, 422, "Give a reason of 1–500 characters for stopping the agent.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The agent is unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, goalID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		switch err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM close_goals WHERE close_id=$1 AND state<>'stopped' FOR UPDATE`, closeID).Scan(&goalID); err {
		case nil:
			err = stopGoal(r.Context(), tx, goalID, actor, "Stopped by "+actor+": "+in.Reason)
		case sql.ErrNoRows:
			problem(w, 409, "The agent is not working on this close.")
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The agent could not be stopped. Refresh and try again.")
			return
		}
		jsonReply(w, 200, map[string]bool{"stopped": true})
	})
}

// lookAtGoal is one look of a goal's close_goal run, with roles read before any row is locked: done once the goal is
// stopped (or gone), and otherwise the goal's next step. An error, including a firm whose roles cannot be read, is left
// for the next look; the run records it.
func lookAtGoal(ctx context.Context, db *sql.DB, goalID string, roles func(context.Context, string) (map[string]reviewer, error)) (bool, error) {
	id, err := strconv.ParseInt(goalID, 10, 64)
	if err != nil {
		return true, nil // no such goal: nothing to watch
	}
	var org string
	err = db.QueryRowContext(ctx, `SELECT c.organization_id FROM close_goals g JOIN client_closes c ON c.id=g.close_id WHERE g.id=$1 AND g.state<>'stopped'`, id).Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	members, err := roles(ctx, org)
	if err == nil {
		err = stepGoal(ctx, db, goalID, members)
	}
	if err != nil {
		return false, err
	}
	var open bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM close_goals WHERE id=$1 AND state<>'stopped')`, id).Scan(&open)
	return !open, err
}

// watchGoals starts a close_goal run for each open goal of an enabled firm that no active run watches, a thousand at a
// time until none is left, and returns how many it started.
func watchGoals(ctx context.Context, db *sql.DB) (int, error) {
	started := 0
	for range 1000 {
		n, more, err := watchSomeGoals(ctx, db, 1000)
		started += n
		if err != nil || !more {
			return started, err
		}
	}
	return started, nil
}

// watchSomeGoals starts runs for up to limit unwatched goals; more says it found limit of them.
func watchSomeGoals(ctx context.Context, db *sql.DB, limit int) (started int, more bool, err error) {
	rows, err := db.QueryContext(ctx, `SELECT g.id::text,c.organization_id FROM close_goals g JOIN client_closes c ON c.id=g.close_id JOIN organizations o ON o.id=c.organization_id
 WHERE g.state<>'stopped' AND o.enabled AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE COALESCE(r.organization_id,'')=c.organization_id AND r.concurrency_key='close_goal:'||g.id AND r.state IN ('queued','running','waiting'))
 ORDER BY g.id LIMIT $1`, limit)
	if err != nil {
		return 0, false, err
	}
	goals := []goalRow{}
	for rows.Next() {
		var g goalRow
		if err = rows.Scan(&g.id, &g.org); err != nil {
			rows.Close()
			return 0, false, err
		}
		goals = append(goals, g)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, false, err
	}
	for _, g := range goals {
		// A key of its own for this pass: the goal's earlier runs keep theirs. The concurrency key keeps it to one run.
		_, existing, err := wf.Start(ctx, db, wf.StartRequest{Workflow: "close_goal", Organization: g.org, Input: close_goal.Input{GoalID: g.id}, IdempotencyKey: "goal:" + g.id + "/" + wf.CurrentStepID(ctx), StartedBy: "workflow:close_goal_upkeep"})
		switch {
		case errors.Is(err, wf.ErrBusy):
		case err != nil:
			return started, false, err
		case !existing:
			started++
		}
	}
	return started, len(goals) == limit, nil
}

func stepGoal(ctx context.Context, db *sql.DB, id string, roles map[string]reviewer) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The close is locked before the goal, as on every other path; a close someone is changing waits for the next sweep.
	var g goalRow
	err = tx.QueryRowContext(ctx, `SELECT c.id::text,c.organization_id FROM client_closes c JOIN close_goals g ON g.close_id=c.id WHERE g.id=$1 AND g.state<>'stopped' FOR UPDATE OF c SKIP LOCKED`, id).Scan(&g.close, &g.org)
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT id::text,author,state,scope_digest,finish,ready_at IS NOT NULL FROM close_goals WHERE id=$1 AND state<>'stopped' FOR UPDATE`, id).Scan(&g.id, &g.author, &g.state, &g.scope, &g.finish, &g.ready)
	}
	if err == sql.ErrNoRows {
		return nil
	}
	if err == nil {
		err = advanceGoal(ctx, tx, g, roles)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
