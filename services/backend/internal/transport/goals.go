package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

// A close handed to the agent. The agent keeps the close's preparation scope current, preparation the firm lets agents
// do on their own, and says exactly what it needs from people. It never confirms facts, approves, sends, posts or signs
// off. It stops when a person stops it, when the close is signed off, or when its owner leaves the firm.

type goalItem struct {
	JobID  string   `json:"job_id"`
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Text   string   `json:"text"`
	Drafts []string `json:"drafts"`
}

// goalProgress is where every current document of a close stands. Needs are close-wide things a person must do; notes
// say what a ready result does not cover.
type goalProgress struct {
	Items   []goalItem `json:"items"`
	Needs   []string   `json:"needs"`
	Notes   []string   `json:"notes"`
	Reached bool       `json:"reached"`
}

type goalRow struct{ id, close, org, author, state, scope string }

var goalForms = map[string]string{"deferred_revenue": "Deferred revenue", "depreciation": "Depreciation", "loan": "Loan schedule", "owner": "Owner items", "settlement": "Settlement"}

// goalJobs is each current document's job with its proposal, the drafts it made and its latest client request.
const goalJobs = `SELECT j.id::text,e.name,j.state,j.error,e.extraction_state,COALESCE(i.proposal->>'kind',''),COALESCE(i.proposal->>'summary',''),
 COALESCE(NULLIF(i.proposal->'questions','null'::jsonb),'[]'::jsonb)||jsonb_path_query_array(COALESCE(i.proposal,'{}'::jsonb),'$.facts[*].question'),
 COALESCE(j.workpaper_ids,'[]'::jsonb),
 COALESCE((SELECT jsonb_build_object('id',q.id::text,
   'partial',COALESCE((SELECT NOT s.complete FROM request_submissions s WHERE s.request_id=q.id ORDER BY s.id DESC LIMIT 1),false),
   'undelivered',EXISTS(SELECT 1 FROM (SELECT * FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1) m WHERE ` + undeliveredEmail + `),
   'unsent',q.delivered_at IS NULL AND NOT EXISTS(SELECT 1 FROM email_delivery m WHERE m.request_id=q.id))
  FROM request_jobs rj JOIN work_requests q ON q.id=rj.request_id WHERE rj.job_id=j.id ORDER BY q.id DESC LIMIT 1),'null'::jsonb)
 FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id
 WHERE j.close_id=$1 AND e.superseded_by IS NULL ORDER BY j.id`

// readGoalProgress classifies every current document of the close by what it waits on, from saved state.
func readGoalProgress(ctx context.Context, tx *sql.Tx, closeID string) (goalProgress, error) {
	p := goalProgress{Items: []goalItem{}, Needs: []string{}, Notes: []string{}}
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
	for rows.Next() {
		var state, failure, extraction, kind, summary string
		var questionsRaw, papersRaw, requestRaw []byte
		item := goalItem{Drafts: []string{}}
		if err = rows.Scan(&item.JobID, &item.Name, &state, &failure, &extraction, &kind, &summary, &questionsRaw, &papersRaw, &requestRaw); err != nil {
			return p, err
		}
		var questions, papers []string
		var request *struct {
			ID          string `json:"id"`
			Partial     bool   `json:"partial"`
			Undelivered bool   `json:"undelivered"`
			Unsent      bool   `json:"unsent"`
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
				item.Status, item.Text = "unsupported", strings.TrimSpace("I can't prepare this kind of work. "+why+" Answer in Work, prepare it by hand, or stop it.")
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
			default:
				item.Status, item.Text = "client", "Waiting on the client's reply to request #"+request.ID+"."
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
		p.Items = append(p.Items, item)
	}
	if err = rows.Err(); err != nil {
		return p, err
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
	var ledger *struct {
		Stale bool `json:"stale"`
	}
	if err = json.Unmarshal(coverage, &ledger); err != nil {
		return p, err
	}
	if ledger == nil {
		p.Notes = append(p.Notes, "No ledger capture is linked, so this covers the uploaded documents only.")
	} else if ledger.Stale {
		p.Notes = append(p.Notes, "The ledger capture is out of date. Capture it again before relying on its account suggestions.")
	}
	p.Reached = prepared > 0 && open == 0 && len(p.Needs) == 0
	return p, nil
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

func stopGoal(ctx context.Context, tx *sql.Tx, id, actor, text string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE close_goals SET state='stopped',reached_at=NULL,finished_at=now(),checked_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	return goalEvent(ctx, tx, id, actor, text)
}

// advanceGoal takes the goal's next step in tx, whose caller holds the close row and then the goal row.
func advanceGoal(ctx context.Context, tx *sql.Tx, g goalRow) error {
	// Holding the firm row serializes this step with a member's removal; membership is read in a second statement so it
	// sees a removal the lock waited for. A suspended firm's goals wait.
	var enabled, member bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1 FOR SHARE`, g.org).Scan(&enabled); err != nil || !enabled {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT $1='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)`, g.org, g.author).Scan(&member); err != nil {
		return err
	}
	if !member {
		return stopGoal(ctx, tx, g.id, "agent", "I stopped because "+g.author+" is no longer a member of the firm.")
	}
	if done, err := signedOff(ctx, tx, g.close); err != nil || done {
		if err != nil {
			return err
		}
		return stopGoal(ctx, tx, g.id, "agent", "The close was signed off, so there is nothing left for me to prepare.")
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
	progress, err := readGoalProgress(ctx, tx, g.close)
	if err != nil {
		return err
	}
	switch {
	case progress.Reached && g.state == "active":
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
		if _, err = tx.ExecContext(ctx, `UPDATE close_goals SET state='reached',reached_at=now() WHERE id=$1`, g.id); err == nil {
			err = goalEvent(ctx, tx, g.id, "agent", text)
		}
	case !progress.Reached && g.state == "reached":
		if _, err = tx.ExecContext(ctx, `UPDATE close_goals SET state='active',reached_at=NULL WHERE id=$1`, g.id); err == nil {
			err = goalEvent(ctx, tx, g.id, "agent", "New work arrived after the drafts were ready, so I'm back on it.")
		}
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE close_goals SET checked_at=now() WHERE id=$1`, g.id)
	}
	return err
}

// goalActivity is what happened on the close since its goal started, newest first: the goal's own events (in the order
// they were recorded) and the steps people and the client took, with how many there are in all.
const goalActivity = `SELECT at,actor,text,count(*) OVER () FROM (
 SELECT (e->>'at')::timestamptz AS at,e->>'actor' AS actor,e->>'text' AS text,n FROM close_goals g CROSS JOIN LATERAL jsonb_array_elements(g.events) WITH ORDINALITY x(e,n) WHERE g.id=$2
 UNION ALL SELECT i.confirmed_at,i.confirmed_by,i.confirmed_by||' confirmed the facts read from '||e.name,0 FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 AND i.confirmed_at IS NOT NULL
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
) a WHERE at>=$3 ORDER BY at DESC,n DESC LIMIT $4`

func registerGoals(mux *http.ServeMux, db *sql.DB) {
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
			Events     json.RawMessage `json:"events"`
		}
		var goal *view
		g := &view{}
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,author,COALESCE(due_on::text,''),state,created_at,reached_at,finished_at,events FROM close_goals WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&g.ID, &g.Author, &g.DueOn, &g.State, &g.CreatedAt, &g.ReachedAt, &g.FinishedAt, &g.Events)
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
		if err == nil {
			progress, err = readGoalProgress(r.Context(), tx, closeID)
		}
		if err == nil {
			done, err = signedOff(r.Context(), tx, closeID)
		}
		if err != nil {
			problem(w, 503, "The agent's progress is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"goal": goal, "progress": progress, "activity": history, "activity_total": total, "signed_off": done})
	})
	mux.HandleFunc("POST /v1/closes/{id}/goal", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		actor := subject(r)
		var in struct {
			Confirmed bool   `json:"confirmed"`
			DueOn     string `json:"due_on"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm that the agent may work on this close until its documents are ready for review.")
			return
		}
		text := "Handed over by " + actor
		if in.DueOn != "" {
			// The browser's date may be a day behind the server's.
			today := time.Now().UTC().Truncate(24 * time.Hour)
			due, err := time.Parse("2006-01-02", in.DueOn)
			if err != nil || due.Before(today.AddDate(0, 0, -1)) || due.After(today.AddDate(1, 0, 1)) {
				problem(w, 422, "Choose a due date from today to a year ahead.")
				return
			}
			text += ", to be ready for review by " + due.Format("Mon 2 Jan 2006")
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The agent is unavailable.")
			return
		}
		defer tx.Rollback()
		g := goalRow{author: actor, state: "active"}
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,organization_id FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&g.close, &g.org); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		done, err := signedOff(r.Context(), tx, g.close)
		if err != nil {
			problem(w, 503, "Close readiness unavailable.")
			return
		}
		if done {
			problem(w, 409, "This close is signed off. Reopen it before handing it to the agent.")
			return
		}
		var owner string
		var since time.Time
		switch err = tx.QueryRowContext(r.Context(), `SELECT author,created_at FROM close_goals WHERE close_id=$1 AND state<>'stopped'`, g.close).Scan(&owner, &since); err {
		case nil:
			problem(w, 409, owner+" already handed this close to the agent on "+since.UTC().Format("2 Jan 2006")+".")
			return
		case sql.ErrNoRows:
		default:
			problem(w, 503, "The agent is unavailable.")
			return
		}
		err = tx.QueryRowContext(r.Context(), `INSERT INTO close_goals(close_id,author,due_on,events) VALUES($1,$2,NULLIF($3,'')::date,jsonb_build_array(jsonb_build_object('actor',$2::text,'text',$4::text,'at',now()))) RETURNING id::text`, g.close, actor, in.DueOn, text).Scan(&g.id)
		if err == nil {
			err = advanceGoal(r.Context(), tx, g)
		}
		if err != nil || tx.Commit() != nil {
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

// RunCloseGoals takes each open goal's next step about once a minute, the longest unchecked first.
func RunCloseGoals(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		sweepCloseGoals(ctx, db)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweepCloseGoals steps up to 50 open goals of enabled firms. A goal whose step fails is logged and moved to the back,
// so it cannot hold up the others.
func sweepCloseGoals(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `SELECT g.id::text FROM close_goals g JOIN client_closes c ON c.id=g.close_id JOIN organizations o ON o.id=c.organization_id WHERE g.state<>'stopped' AND o.enabled ORDER BY g.checked_at NULLS FIRST,g.id LIMIT 50`)
	if err != nil {
		if ctx.Err() == nil {
			log.Print("Close goals could not be read: ", err)
		}
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err = stepGoal(ctx, db, id); err != nil && ctx.Err() == nil {
			log.Printf("Close goal #%s could not take its next step: %v", id, err)
			db.ExecContext(ctx, `UPDATE close_goals SET checked_at=now() WHERE id=$1`, id)
		}
	}
}

func stepGoal(ctx context.Context, db *sql.DB, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The close is locked before the goal, as on every other path; a close someone is changing waits for the next sweep.
	var g goalRow
	err = tx.QueryRowContext(ctx, `SELECT c.id::text,c.organization_id FROM client_closes c JOIN close_goals g ON g.close_id=c.id WHERE g.id=$1 AND g.state<>'stopped' FOR UPDATE OF c SKIP LOCKED`, id).Scan(&g.close, &g.org)
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT id::text,author,state,scope_digest FROM close_goals WHERE id=$1 AND state<>'stopped' FOR UPDATE`, id).Scan(&g.id, &g.author, &g.state, &g.scope)
	}
	if err == sql.ErrNoRows {
		return nil
	}
	if err == nil {
		err = advanceGoal(ctx, tx, g)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
