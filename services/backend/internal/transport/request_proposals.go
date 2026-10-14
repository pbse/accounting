package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

// A close's goal drafts one consolidated client request for the open questions its documents raise that the client can
// answer; a person approves it (and, separately, its email) from the goal card, chat or the Requests tab. The draft is
// a request_drafts row naming the jobs it covers and the recipient it proposes; approving it opens a consolidated request
// with no ledger bindings, so it implies nothing about the month's ledger coverage.

// proposedQuestion is one question of a drafted request: the document or ledger account it is about, what the client
// is asked, and what an answer lets the agent continue (the firm's to see; the client is asked the question only).
type proposedQuestion struct {
	Document string `json:"document"`
	Question string `json:"question"`
	Unblocks string `json:"unblocks,omitempty"`
}

// proposedAccount is a ledger account a drafted request asks the client's records for, at the coverage binding it was
// read at, as a coverage request binds it.
type proposedAccount struct {
	AccountID string `json:"account_id"`
	ID        string `json:"id"`
}

// clientAsk is what a goal's drafted request covers, routed from everything that waits on information: its documents'
// questions, the accounts whose records are missing and the pages its drafts lack, all the client's to answer; and,
// kept with the firm and never sent, what needs its own judgment. Withdrawn marks a draft overtaken before approval.
type clientAsk struct {
	Jobs      []proposedJob      `json:"jobs"`
	Questions []proposedQuestion `json:"questions"`
	Snapshot  string             `json:"snapshot_id,omitempty"`
	Accounts  []proposedAccount  `json:"accounts,omitempty"`
	Papers    []string           `json:"papers,omitempty"`
	Kept      []string           `json:"kept,omitempty"`
	Withdrawn bool               `json:"withdrawn,omitempty"`
}

// asks is whether two drafts ask the client the same: the same documents at the same readings, questions, accounts at
// the same capture and drafts. What stays with the firm does not change what the client is asked.
func (a clientAsk) asks(b clientAsk) bool {
	return slices.Equal(a.Jobs, b.Jobs) && slices.Equal(a.Questions, b.Questions) && a.Snapshot == b.Snapshot && slices.Equal(a.Accounts, b.Accounts) && slices.Equal(a.Papers, b.Papers)
}

type proposedJob struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

// clientQuestion says whether an agent's question is one the client can answer, rather than the firm's own check of a
// cited value, a fact the firm confirms from the document, or the general question an unsupported reading carries.
func clientQuestion(q string) bool {
	q = strings.TrimSpace(q)
	return q != "" && !strings.HasPrefix(q, "Confirm ") && !(strings.HasPrefix(q, "Check the ") && strings.Contains(q, " in source #")) &&
		q != "Which supported work should this evidence support, and what additional evidence establishes its entity, period and method?"
}

// openQuestions are the close's documents waiting for a person whose supported reading asks the client something, and
// that no live request accepts at their current reading, with those questions: at most 20 documents and the 50
// questions a client's page numbers one by one. An unsupported reading's questions are about the work itself, the
// firm's to settle.
func openQuestions(ctx context.Context, q reader, closeID string) ([]proposedJob, []proposedQuestion, error) {
	rows, err := q.QueryContext(ctx, `SELECT j.id::text,i.revision,e.name,COALESCE(NULLIF(i.proposal->'questions','null'::jsonb),'[]'::jsonb)
 FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id
 WHERE j.close_id::text=$1 AND j.state='needs_confirmation' AND e.superseded_by IS NULL AND COALESCE(i.proposal->>'kind','')<>'unsupported'
  AND NOT EXISTS(SELECT 1 FROM work_requests q JOIN client_request_links l ON l.request_id=q.id LEFT JOIN work_request_targets t ON t.request_id=q.id AND t.job_id=j.id
   WHERE (q.job_id=j.id AND q.revision=i.revision OR t.revision=i.revision) AND l.revoked_at IS NULL AND l.expires_at>now())
 ORDER BY j.id`, closeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	jobs, questions := []proposedJob{}, []proposedQuestion{}
	for rows.Next() && len(jobs) < 20 {
		var job proposedJob
		var name string
		var raw []byte
		if err = rows.Scan(&job.ID, &job.Revision, &name, &raw); err != nil {
			return nil, nil, err
		}
		var asked []string
		json.Unmarshal(raw, &asked)
		own := []proposedQuestion{}
		for _, text := range asked {
			if clientQuestion(text) && !slices.ContainsFunc(own, func(p proposedQuestion) bool { return strings.EqualFold(p.Question, strings.TrimSpace(text)) }) {
				own = append(own, proposedQuestion{name, strings.TrimSpace(text), "preparing " + name})
			}
		}
		if len(own) == 0 || len(questions)+len(own) > 50 {
			continue
		}
		questions, jobs = append(questions, own...), append(jobs, job)
	}
	return jobs, questions, rows.Err()
}

// requestBody is the numbered request text the client's page splits into questions.
func requestBody(entity, period string, questions []proposedQuestion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Request for information — %s · %s\n\nPlease help us complete this month's books:\n", entity, period)
	for n, q := range questions {
		fmt.Fprintf(&b, "\n%d. %s: %s\n", n+1, q.Document, q.Question)
	}
	b.WriteString("\nReply through the private link; you can answer some questions now and the rest later.")
	return b.String()
}

// month is a close period in words, as a client reads it.
func month(period string) string {
	if t, err := time.Parse("2006-01", period); err == nil {
		return t.Format("January 2006")
	}
	return period
}

// clientAsks routes what waits on information in a close: a document reading's question for the client (openQuestions);
// a reconciliation draft's missing statement page; and, from the ledger capture's coverage, an account with no
// evidence, whose records the client holds: a bank or card statement, the invoices and agreements behind a prepaid
// balance, or the bills for services not yet billed or booked. Each is asked in the client's words, never as a
// blocker's text, with what the answer unblocks. An account the firm must judge (its activity and balance to review),
// and a draft's other blockers, stay with the firm. Accounts and pages another live request already asks for are left
// to it, so its partial answers stand. At most 50 questions and 20 accounts.
func clientAsks(ctx context.Context, tx *sql.Tx, closeID string) (clientAsk, error) {
	var ask clientAsk
	var err error
	if ask.Jobs, ask.Questions, err = openQuestions(ctx, tx, closeID); err != nil {
		return ask, err
	}
	var period string
	if err = tx.QueryRowContext(ctx, `SELECT period FROM client_closes WHERE id::text=$1`, closeID).Scan(&period); err != nil {
		return ask, err
	}
	when := month(period)
	room := func() bool { return len(ask.Questions) < 50 }
	// Pages a reconciliation draft lacks, and what else holds the close's current drafts; a draft whose sources changed
	// is prepared again, not asked about. The accounts a current reconciliation draft already reads a statement for are
	// not asked for one again.
	reconciled := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT w.id::text,w.kind,COALESCE(w.paper->'input'->>'account',''),COALESCE((SELECT s->>'name' FROM jsonb_array_elements(COALESCE(w.paper->'sources','[]'::jsonb)) s WHERE s->>'role'='statement' LIMIT 1),''),COALESCE(b,''),
  EXISTS(SELECT 1 FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_request_links l ON l.request_id=q.id WHERE d.close_id=w.close_id AND d.workpaper_ids ? w.id::text AND l.revoked_at IS NULL AND l.expires_at>now())
 FROM workpaper_review_state w LEFT JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(w.paper->'blockers')='array' THEN w.paper->'blockers' ELSE '[]'::jsonb END) b ON true
 WHERE w.close_id::text=$1 AND w.review_state<>'source_changed' AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text) ORDER BY w.id`, closeID)
	if err != nil {
		return ask, err
	}
	for rows.Next() {
		var id, kind, account, statement, blocker string
		var asked bool
		if err = rows.Scan(&id, &kind, &account, &statement, &blocker, &asked); err != nil {
			rows.Close()
			return ask, err
		}
		if kind == "reconciliation" && statement != "" {
			reconciled[strings.ToLower(account)] = true
		}
		if blocker == "" {
			continue
		}
		page, missing := strings.CutPrefix(blocker, "Statement page ")
		if page, missing = strings.CutSuffix(page, " is missing."); missing && page != "" && strings.Trim(page, "0123456789") == "" {
			if !asked && room() {
				if statement == "" {
					statement = account + " statement"
				}
				ask.Questions = append(ask.Questions, proposedQuestion{statement, fmt.Sprintf("Please send page %s of this statement; it is missing from what we have.", page), "the " + account + " reconciliation"})
				if !slices.Contains(ask.Papers, id) {
					ask.Papers = append(ask.Papers, id)
				}
			}
			continue
		}
		if len(ask.Kept) < 10 {
			ask.Kept = append(ask.Kept, fmt.Sprintf("Draft #%s: %s", id, blocker))
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ask, err
	}
	view, err := currentCoverage(ctx, tx, closeID)
	if errors.Is(err, errCoverageLimit) || err == nil && (view.Stale || view.SnapshotID == "") {
		return ask, nil
	}
	if err != nil {
		return ask, err
	}
	// Accounts a live request on this capture already asks about, answered in part or not at all.
	covered := map[string]bool{}
	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT b->>'account_id' FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id CROSS JOIN jsonb_array_elements(COALESCE(q.coverage_scope->'bindings','[]'::jsonb)) b
 WHERE d.close_id::text=$1 AND q.coverage_scope->>'snapshot_id'=$2 AND EXISTS(SELECT 1 FROM client_request_links l WHERE l.request_id=q.id AND l.revoked_at IS NULL AND l.expires_at>now())`, closeID, view.SnapshotID)
	if err != nil {
		return ask, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return ask, err
		}
		covered[id] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ask, err
	}
	// A draft names its account by name: it vouches for an account only when no other in the capture shares the name.
	named := map[string]int{}
	for _, item := range view.Items {
		named[strings.ToLower(item.Name)]++
	}
	for _, item := range view.Items {
		if item.State != "missing_evidence" || len(item.JobIDs) > 0 || len(item.SuggestedJobIDs) > 0 || covered[item.AccountID] || item.Kind == "reconciliation" && reconciled[strings.ToLower(item.Name)] && named[strings.ToLower(item.Name)] == 1 {
			continue
		}
		var q proposedQuestion
		switch item.Kind {
		case "reconciliation":
			q = proposedQuestion{item.Name, fmt.Sprintf("Please upload the complete %s statement for %s, every page.", when, item.Name), "the " + item.Name + " reconciliation"}
		case "prepaid":
			q = proposedQuestion{item.Name, fmt.Sprintf("Please send the invoices or agreements behind %s (amounts paid in advance, such as annual software or insurance), showing the period each covers.", item.Name), "the " + item.Name + " schedule"}
		case "accrual":
			q = proposedQuestion{item.Name, fmt.Sprintf("Please send any bills for services received by the end of %s that were not yet billed or paid, for %s.", when, item.Name), "the " + item.Name + " balance at month end"}
		default:
			if len(ask.Kept) < 10 {
				ask.Kept = append(ask.Kept, item.Name+": review its activity and balance, or exclude it from the close")
			}
			continue
		}
		if !room() || len(ask.Accounts) >= 20 {
			break
		}
		ask.Questions = append(ask.Questions, q)
		ask.Accounts = append(ask.Accounts, proposedAccount{item.AccountID, item.BindingID})
	}
	if len(ask.Accounts) > 0 {
		ask.Snapshot = view.SnapshotID
	}
	return ask, nil
}

// draftGoalRequest drafts goal g's client request when its close has open client questions no live request covers and
// the newest draft the goal made does not already cover the same documents at the same readings. It proposes the
// client's most recently approved recipient. It reports whether it drafted one.
func draftGoalRequest(ctx context.Context, tx *sql.Tx, g goalRow) (bool, error) {
	ask, err := clientAsks(ctx, tx, g.close)
	if err != nil {
		return false, err
	}
	agent := "close goal #" + g.id
	// The goal's newest draft not yet approved: unchanged, it stands; with nothing left to ask, it is withdrawn.
	var newestID string
	var newest []byte
	err = tx.QueryRowContext(ctx, `SELECT d.id::text,COALESCE(d.proposal,'{}') FROM request_drafts d WHERE d.close_id::text=$1 AND d.agent=$2 AND NOT EXISTS(SELECT 1 FROM work_requests q WHERE q.draft_id=d.id) ORDER BY d.id DESC LIMIT 1`, g.close, agent).Scan(&newestID, &newest)
	if err == sql.ErrNoRows {
		err = nil
	} else if err != nil {
		return false, err
	}
	var before clientAsk
	json.Unmarshal(newest, &before)
	if len(ask.Questions) == 0 {
		if newestID != "" && !before.Withdrawn {
			_, err = tx.ExecContext(ctx, `UPDATE request_drafts SET proposal=COALESCE(proposal,'{}')||'{"withdrawn":true}' WHERE id::text=$1`, newestID)
		}
		return false, err
	}
	// Unchanged for the client, the draft stands; what the firm keeps is brought up to date on it.
	if newestID != "" && !before.Withdrawn && before.asks(ask) {
		if !slices.Equal(before.Kept, ask.Kept) {
			kept, _ := json.Marshal(ask.Kept)
			_, err = tx.ExecContext(ctx, `UPDATE request_drafts SET proposal=jsonb_set(proposal,'{kept}',$2::jsonb) WHERE id::text=$1`, newestID, string(kept))
		}
		return false, err
	}
	covered, _ := json.Marshal(ask.Jobs)
	papers, _ := json.Marshal(ask.Papers)
	if ask.Papers == nil {
		papers = []byte("[]")
	}
	routed, _ := json.Marshal(ask)
	var entity, period, recipient string
	if err = tx.QueryRowContext(ctx, `SELECT c.entity,c.period,COALESCE((SELECT q.recipient FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes o ON o.id=d.close_id
  WHERE o.organization_id=c.organization_id AND client_identity(o.organization_id,o.entity)=client_identity(c.organization_id,c.entity) ORDER BY q.id DESC LIMIT 1),'') FROM client_closes c WHERE c.id::text=$1`, g.close).Scan(&entity, &period, &recipient); err != nil {
		return false, err
	}
	digest, _, err := preparation.RequestContext(ctx, tx, g.close, nil)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by,agent,job_ids,recipient,proposal) VALUES($1::bigint,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)`,
		g.close, requestBody(entity, period, ask.Questions), string(papers), digest, agent+" for "+g.author, agent, string(covered), recipient, string(routed))
	if err == nil {
		about := []string{}
		if len(ask.Jobs) > 0 {
			about = append(about, plural(len(ask.Jobs), "document", "documents"))
		}
		if len(ask.Accounts) > 0 {
			about = append(about, plural(len(ask.Accounts), "account's records", "accounts' records"))
		}
		if len(ask.Papers) > 0 {
			about = append(about, plural(len(ask.Papers), "statement's missing pages", "statements' missing pages"))
		}
		text := fmt.Sprintf("I drafted a client request with %s about %s. It goes nowhere until a person approves it.", plural(len(ask.Questions), "question", "questions"), strings.Join(about, ", "))
		if len(ask.Kept) > 0 {
			text += " " + plural(len(ask.Kept), "item needs", "items need") + " the firm's own judgment, so I did not ask the client."
		}
		err = goalEvent(ctx, tx, g.id, "agent", text)
	}
	if err != nil {
	}
	return err == nil, err
}

// requestProposal is a close's newest drafted client request that is still current: not approved, and each document it
// covers still waits for a person at the reading it was drafted from, with no live request of its own.
type requestProposal struct {
	ID        string             `json:"id"`
	Entity    string             `json:"entity"`
	Period    string             `json:"period"`
	Recipient string             `json:"recipient"`
	Body      string             `json:"body"`
	Questions []proposedQuestion `json:"questions"`
	Kept      []string           `json:"kept"`
	ask       clientAsk
}

// currentProposal is the close's newest drafted request, unless approved, withdrawn, or its documents answered, read
// again or covered by another request since (the goal then drafts again). Exact, it also asks the close again what the
// client must be asked (as approval does, inside its transaction) and holds only a draft that still asks exactly that;
// otherwise the goal's next look, within a minute, brings an account or page answered since up to date.
func currentProposal(ctx context.Context, q reader, closeID string, exact *sql.Tx) (*requestProposal, error) {
	var p requestProposal
	var raw, routed []byte
	err := q.QueryRowContext(ctx, `SELECT d.id::text,c.entity,c.period,COALESCE(d.recipient,''),d.body,d.job_ids,d.proposal FROM request_drafts d JOIN client_closes c ON c.id=d.close_id
 WHERE d.close_id::text=$1 AND d.job_ids IS NOT NULL AND NOT EXISTS(SELECT 1 FROM work_requests q WHERE q.draft_id=d.id) ORDER BY d.id DESC LIMIT 1`, closeID).Scan(&p.ID, &p.Entity, &p.Period, &p.Recipient, &p.Body, &raw, &routed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil || json.Unmarshal(raw, &p.ask.Jobs) != nil {
		return nil, err
	}
	jobs, questions, err := openQuestions(ctx, q, closeID)
	if err != nil {
		return nil, err
	}
	// A draft from before routing asks its documents' questions only.
	if routed == nil {
		p.ask.Questions = questions
	} else if json.Unmarshal(routed, &p.ask) != nil || p.ask.Withdrawn {
		return nil, nil
	}
	if !slices.Equal(jobs, p.ask.Jobs) {
		return nil, nil
	}
	if exact != nil && routed != nil {
		now, err := clientAsks(ctx, exact, closeID)
		if err != nil {
			return nil, err
		}
		if !now.asks(p.ask) {
			return nil, nil
		}
	}
	p.Questions, p.Kept = p.ask.Questions, p.ask.Kept
	if p.Kept == nil {
		p.Kept = []string{}
	}
	return &p, nil
}

// openConsolidated opens an approved consolidated request from draft draftID: the request (scope its coverage scope),
// each of its jobs waiting on the client from a new revision, bound to the request, and a private link.
func openConsolidated(ctx context.Context, tx *sql.Tx, draftID, recipient, actor string, scope []byte, jobs []string) (rid, token, passcode string, err error) {
	// The drafts it asks about are recorded as approved, so a request whose drafts change takes no reply.
	if err = tx.QueryRowContext(ctx, `INSERT INTO work_requests(draft_id,revision,recipient,approved_by,coverage_scope,approved_papers) VALUES($1,0,$2,$3,$4,(SELECT `+preparation.RequestPapers+` FROM request_drafts d WHERE d.id=$1)) RETURNING id::text`, draftID, recipient, actor, string(scope)).Scan(&rid); err != nil {
		return
	}
	for _, id := range jobs {
		_, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='waiting_client',error='' WHERE id::text=$1`, id)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL WHERE job_id::text=$1`, id)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO work_request_targets(request_id,job_id,revision) SELECT $1,job_id,revision FROM preparation_intakes WHERE job_id::text=$2`, rid, id)
		}
		if err != nil {
			return
		}
	}
	token = rand.Text()
	passcode, err = issueLink(ctx, tx, rid, actor, token)
	return
}

func registerRequestProposals(mux *http.ServeMux, db *sql.DB) {
	// The close's drafted client request, if one is current, for the goal card, chat and the Requests tab alike.
	mux.HandleFunc("GET /v1/closes/{id}/requests/proposed", func(w http.ResponseWriter, r *http.Request) {
		p, err := currentProposal(r.Context(), db, r.PathValue("id"), nil)
		if err != nil {
			problem(w, 503, "The drafted request is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"proposal": p})
	})
	// Approving the drafted request opens it as a consolidated request with a private link; sending its email is a
	// separate approval of the sender and recipient.
	mux.HandleFunc("POST /v1/closes/{id}/requests/{draft}/approve", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		// Questions, when sent, are the drafted questions as the person worded them: the same documents in the same order,
		// each on one line.
		var in struct {
			Recipient string             `json:"recipient"`
			Confirmed bool               `json:"confirmed"`
			Questions []proposedQuestion `json:"questions"`
		}
		if !decode(w, r, &in) {
			return
		}
		recipient, e := mail.ParseAddress(strings.TrimSpace(in.Recipient))
		if !in.Confirmed || e != nil || recipient.Address != strings.TrimSpace(in.Recipient) || len(in.Recipient) > 254 {
			problem(w, 422, "Confirm the request and its exact recipient email address.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		p, err := currentProposal(r.Context(), tx, closeID, tx)
		if err != nil {
			problem(w, 503, "The drafted request is unavailable.")
			return
		}
		if p == nil || p.ID != r.PathValue("draft") {
			problem(w, 409, "This drafted request was approved, or its documents changed since it was drafted. Refresh to see the current one.")
			return
		}
		if in.Questions != nil {
			if len(in.Questions) != len(p.Questions) {
				problem(w, 422, "Keep every drafted question; change only its wording.")
				return
			}
			for i, q := range in.Questions {
				q.Question = strings.Join(strings.Fields(q.Question), " ")
				if q.Document != p.Questions[i].Document || q.Question == "" || utf8.RuneCountInString(q.Question) > 1000 {
					problem(w, 422, "Each question needs its document and 1–1,000 characters of wording.")
					return
				}
				q.Unblocks = p.Questions[i].Unblocks
				in.Questions[i] = q
			}
			if !slices.Equal(in.Questions, p.Questions) {
				// A request's text is stored up to 20,000 bytes.
				body := requestBody(p.Entity, p.Period, in.Questions)
				if len(body) > 20000 {
					problem(w, 422, "The request is too long to send; shorten its questions.")
					return
				}
				if _, err = tx.ExecContext(r.Context(), `UPDATE request_drafts SET body=$2 WHERE id::text=$1`, p.ID, body); err != nil {
					problem(w, 503, "Request storage unavailable.")
					return
				}
			}
		}
		ids := []string{}
		for _, j := range p.ask.Jobs {
			ids = append(ids, j.ID)
		}
		// Accounts are bound as a coverage request binds them, on the capture they were read from; with none, the request
		// binds no capture.
		bindings := []proposedAccount{}
		bindings = append(bindings, p.ask.Accounts...)
		scope, _ := json.Marshal(map[string]any{"snapshot_id": p.ask.Snapshot, "bindings": bindings, "jobs": ids})
		rid, token, passcode, err := openConsolidated(r.Context(), tx, p.ID, recipient.Address, actor, scope, ids)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			problem(w, 409, "The request could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": rid, "link_token": token, "passcode": passcode})
	})
}
