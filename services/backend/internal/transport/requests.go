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
	CreatedAt     time.Time `json:"created_at"`
	Stale         bool      `json:"stale"`
}

func registerRequests(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/requests"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Request drafts unavailable.")
			return
		}
		defer tx.Rollback()
		rows, err := tx.QueryContext(r.Context(), `SELECT id::text,body,workpaper_ids,context_digest,created_by,created_at FROM request_drafts WHERE close_id::text=$1 ORDER BY request_drafts.id DESC LIMIT 20`, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Could not read drafts.")
			return
		}
		drafts := []requestDraft{}
		for rows.Next() {
			var d requestDraft
			var ids []byte
			if err = rows.Scan(&d.ID, &d.Body, &ids, &d.ContextDigest, &d.CreatedBy, &d.CreatedAt); err != nil {
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
		err = tx.QueryRowContext(r.Context(), `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, closeID, body, string(ids), digest, subject).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Save could not be confirmed. Retry the same draft safely.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
