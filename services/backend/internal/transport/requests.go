package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

type requestDraft struct {
	ID            string    `json:"id"`
	Body          string    `json:"body"`
	WorkpaperIDs  []string  `json:"workpaper_ids"`
	ContextDigest string    `json:"context_digest"`
	CreatedBy     string    `json:"created_by"`
	Agent         string    `json:"agent,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Stale         bool      `json:"stale"`
}

func registerRequests(mux *http.ServeMux, db *sql.DB) {
	// The close's approved requests, newest first, each with where it stands: when it reached the client (or why it
	// did not), whether it was answered in part or in full, a reply held for review, its private link and reminders,
	// and the work it is handled in (none for a request about accounts alone, handled in coverage).
	mux.HandleFunc("GET /v1/closes/{id}/requests/approved", func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',q.id::text,'body',d.body,'recipient',q.recipient,'approved_at',q.approved_at,'approved_by',q.approved_by,
 'sent_at',COALESCE(q.delivered_at,(SELECT min(COALESCE(m.finished_at,m.created_at)) FROM email_delivery m WHERE m.request_id=q.id AND m.state='accepted' AND m.delivery NOT IN ('bounced','complained','failed','suppressed'))),
 'email',COALESCE((SELECT CASE WHEN m.state IN ('failed','unknown') OR m.delivery IN ('bounced','complained','failed','suppressed') THEN 'failed' WHEN m.state IN ('queued','sending') THEN 'sending' END
  FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),''),
 'answer',COALESCE((SELECT CASE WHEN s.complete THEN 'answered' ELSE 'answered_in_part' END FROM request_submissions s WHERE s.request_id=q.id ORDER BY s.id DESC LIMIT 1),''),
 'held',EXISTS(SELECT 1 FROM inbound_replies i WHERE i.request_id=q.id AND i.state='held'),
 'link',(SELECT jsonb_build_object('used_at',used_at,'revoked_at',revoked_at,'expires_at',expires_at) FROM client_request_links WHERE request_id=q.id ORDER BY issued_at DESC LIMIT 1),
 'reminder',`+reminderStatus+`,
 'job',CASE WHEN `+aboutAccounts+` THEN '' ELSE COALESCE((SELECT j.id::text FROM `+openJobs+` ORDER BY j.state='waiting_client' DESC,j.id LIMIT 1),(SELECT min(job_id)::text FROM request_jobs WHERE request_id=q.id),'') END)
 ORDER BY q.id DESC),'[]'::jsonb) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id::text=$1`, r.PathValue("id")).Scan(&body)
		if err != nil {
			problem(w, 503, "The close's requests are unavailable.")
			return
		}
		jsonReply(w, 200, json.RawMessage(body))
	})
	path := "/v1/closes/{id}/requests"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Request drafts unavailable.")
			return
		}
		defer tx.Rollback()
		rows, err := tx.QueryContext(r.Context(), `SELECT id::text,body,workpaper_ids,context_digest,created_by,COALESCE(agent,''),created_at FROM request_drafts WHERE close_id::text=$1 ORDER BY request_drafts.id DESC LIMIT 20`, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Could not read drafts.")
			return
		}
		drafts := []requestDraft{}
		for rows.Next() {
			var d requestDraft
			var ids []byte
			if err = rows.Scan(&d.ID, &d.Body, &ids, &d.ContextDigest, &d.CreatedBy, &d.Agent, &d.CreatedAt); err != nil {
				break
			}
			if err = json.Unmarshal(ids, &d.WorkpaperIDs); err != nil {
				break
			}
			drafts = append(drafts, d)
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			problem(w, 503, "Could not read request history.")
			return
		}
		if len(drafts) > 0 {
			digest, _, e := preparation.RequestContext(r.Context(), tx, r.PathValue("id"), drafts[0].WorkpaperIDs)
			if e != nil {
				problem(w, 503, "Could not verify draft context.")
				return
			}
			drafts[0].Stale = digest != drafts[0].ContextDigest
		}
		if tx.Commit() != nil {
			problem(w, 503, "Could not finish draft snapshot.")
			return
		}
		jsonReply(w, 200, drafts)
	})
	// candidates is what the assistant's request card shows: the drafts a request would ask about and the exact wording
	// it would save, with one digest over both and the latest draft it would revise.
	mux.HandleFunc("GET "+path+"/candidates", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Request drafts unavailable.")
			return
		}
		defer tx.Rollback()
		candidates, more, err := preparation.RequestCandidates(r.Context(), tx, r.PathValue("id"))
		var latest string
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM request_drafts WHERE close_id::text=$1 ORDER BY request_drafts.id DESC LIMIT 1),'')`, r.PathValue("id")).Scan(&latest)
		}
		ids := []string{}
		for _, c := range candidates {
			ids = append(ids, c.ID)
		}
		// The save route sorts the ids the same way before it words the request.
		sort.Strings(ids)
		var contextDigest, body string
		if err == nil {
			contextDigest, body, err = preparation.RequestContext(r.Context(), tx, r.PathValue("id"), ids)
		}
		if err != nil {
			problem(w, 503, "Could not read the drafts a request would cover.")
			return
		}
		digest := preparation.Digest([]byte(latest + "\x00" + strings.Join(ids, ",") + "\x00" + contextDigest))
		jsonReply(w, 200, map[string]any{"workpapers": candidates, "more": more, "latest_id": latest, "context_digest": contextDigest, "body": body, "digest": digest})
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ExpectedID    string   `json:"expected_id"`
			WorkpaperIDs  []string `json:"workpaper_ids"`
			Body          *string  `json:"body"`
			ContextDigest string   `json:"context_digest"`
		}
		if !decode(w, r, &input) {
			return
		}
		if len(input.WorkpaperIDs) > 20 {
			problem(w, 422, "Select at most 20 workpapers for one request.")
			return
		}
		sort.Strings(input.WorkpaperIDs)
		for i, id := range input.WorkpaperIDs {
			if id == "" || (i > 0 && id == input.WorkpaperIDs[i-1]) {
				problem(w, 422, "Select each workpaper once.")
				return
			}
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
		var latest requestDraft
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,body,context_digest FROM request_drafts WHERE close_id=$1 ORDER BY request_drafts.id DESC LIMIT 1`, closeID).Scan(&latest.ID, &latest.Body, &latest.ContextDigest)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Could not read current request.")
			return
		}
		digest, suggestion, err := preparation.RequestContext(r.Context(), tx, closeID, input.WorkpaperIDs)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		body := suggestion
		if input.Body != nil {
			body = strings.TrimSpace(*input.Body)
			if input.ContextDigest != digest {
				problem(w, 409, "Evidence or review changed. Refresh the draft context before saving edited text.")
				return
			}
		}
		if len(body) == 0 || len(body) > 20000 {
			problem(w, 422, "Keep the consolidated request between 1 and 20,000 bytes; narrow the selected work if needed.")
			return
		}
		// A retry with identical body and context has already succeeded.
		if latest.Body == body && latest.ContextDigest == digest {
			jsonReply(w, 200, map[string]string{"id": latest.ID})
			return
		}
		if input.ExpectedID != latest.ID {
			problem(w, 409, "Another request revision was saved. Reload history before editing.")
			return
		}
		actor := auth.Actor(r.Context())
		subject := "local-workspace (development)"
		if actor != nil {
			subject = actor.Subject
		}
		ids, _ := json.Marshal(input.WorkpaperIDs)
		if input.WorkpaperIDs == nil {
			ids = []byte("[]")
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by,agent) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')) RETURNING id::text`, closeID, body, string(ids), digest, subject, auth.Agent(r.Context())).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Save could not be confirmed. Retry the same draft safely.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
