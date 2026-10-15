package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

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
			digest, e := preparation.RequestContext(r.Context(), tx, r.PathValue("id"), drafts[0].WorkpaperIDs)
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
}
