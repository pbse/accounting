package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

// A close's goal drafts one consolidated client request for the open questions its documents raise that the client can
// answer; a person approves it (and, separately, its email) from the goal card, chat or the Requests tab. The draft is
// a request_drafts row naming the jobs it covers and the recipient it proposes; approving it opens a consolidated request
// with no ledger bindings, so it implies nothing about the month's ledger coverage.

// proposedQuestion is one question of a drafted request: the document it is about and what the client is asked.
type proposedQuestion struct {
	Document string `json:"document"`
	Question string `json:"question"`
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
				own = append(own, proposedQuestion{name, strings.TrimSpace(text)})
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

// draftGoalRequest drafts goal g's client request when its close has open client questions no live request covers and
// the newest draft the goal made does not already cover the same documents at the same readings. It proposes the
// client's most recently approved recipient. It reports whether it drafted one.
func draftGoalRequest(ctx context.Context, tx *sql.Tx, g goalRow) (bool, error) {
	jobs, questions, err := openQuestions(ctx, tx, g.close)
	if err != nil || len(jobs) == 0 {
		return false, err
	}
	covered, _ := json.Marshal(jobs)
	agent := "close goal #" + g.id
	var same bool
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT job_ids=$2::jsonb FROM request_drafts WHERE close_id::text=$1 AND agent=$3 ORDER BY id DESC LIMIT 1),false)`, g.close, string(covered), agent).Scan(&same); err != nil || same {
		return false, err
	}
	var entity, period, recipient string
	if err = tx.QueryRowContext(ctx, `SELECT c.entity,c.period,COALESCE((SELECT q.recipient FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes o ON o.id=d.close_id
  WHERE o.organization_id=c.organization_id AND client_identity(o.organization_id,o.entity)=client_identity(c.organization_id,c.entity) ORDER BY q.id DESC LIMIT 1),'') FROM client_closes c WHERE c.id::text=$1`, g.close).Scan(&entity, &period, &recipient); err != nil {
		return false, err
	}
	digest, _, err := preparation.RequestContext(ctx, tx, g.close, nil)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by,agent,job_ids,recipient) VALUES($1::bigint,$2,'[]',$3,$4,$5,$6,NULLIF($7,''))`,
		g.close, requestBody(entity, period, questions), digest, agent+" for "+g.author, agent, string(covered), recipient)
	if err == nil {
		err = goalEvent(ctx, tx, g.id, "agent", fmt.Sprintf("I drafted a client request with %s about %s. It goes nowhere until a person approves it.", plural(len(questions), "question", "questions"), plural(len(jobs), "document", "documents")))
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
	jobs      []proposedJob
}

func currentProposal(ctx context.Context, q reader, closeID string) (*requestProposal, error) {
	var p requestProposal
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT d.id::text,c.entity,c.period,COALESCE(d.recipient,''),d.body,d.job_ids FROM request_drafts d JOIN client_closes c ON c.id=d.close_id
 WHERE d.close_id::text=$1 AND d.job_ids IS NOT NULL AND NOT EXISTS(SELECT 1 FROM work_requests q WHERE q.draft_id=d.id) ORDER BY d.id DESC LIMIT 1`, closeID).Scan(&p.ID, &p.Entity, &p.Period, &p.Recipient, &p.Body, &raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil || json.Unmarshal(raw, &p.jobs) != nil {
		return nil, err
	}
	jobs, questions, err := openQuestions(ctx, q, closeID)
	if err != nil {
		return nil, err
	}
	// A document answered, read again or covered by another request since makes the draft stale; the goal drafts again.
	if !slices.Equal(jobs, p.jobs) {
		return nil, nil
	}
	p.Questions = questions
	return &p, nil
}

// openConsolidated opens an approved consolidated request from draft draftID: the request (scope its coverage scope),
// each of its jobs waiting on the client from a new revision, bound to the request, and a private link.
func openConsolidated(ctx context.Context, tx *sql.Tx, draftID, recipient, actor string, scope []byte, jobs []string) (rid, token, passcode string, err error) {
	if err = tx.QueryRowContext(ctx, `INSERT INTO work_requests(draft_id,revision,recipient,approved_by,coverage_scope) VALUES($1,0,$2,$3,$4) RETURNING id::text`, draftID, recipient, actor, string(scope)).Scan(&rid); err != nil {
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
		p, err := currentProposal(r.Context(), db, r.PathValue("id"))
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
		p, err := currentProposal(r.Context(), tx, closeID)
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
		for _, j := range p.jobs {
			ids = append(ids, j.ID)
		}
		scope, _ := json.Marshal(map[string]any{"snapshot_id": "", "bindings": []any{}, "jobs": ids})
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
