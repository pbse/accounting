package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
)

// Notes from the public contact page (docs/marketing-site.md), newest first. The edge forwards each to the team inbox
// when it can; this list is the record, so a note whose email never arrived is still read and answered here. The
// notes are Krith's own correspondence, not a firm's client content.
func registerAdminContact(mux *http.ServeMux, db *sql.DB, token string) {
	mux.HandleFunc("GET /v1/admin/contact-notes", adminGuard(token, db, platformRead, func(w http.ResponseWriter, r *http.Request) { adminContactNotes(w, r, db) }))
}

func adminContactNotes(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			problem(w, 422, "before must be a note number.")
			return
		}
		before = value
	}
	var raw []byte
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(n ORDER BY n.id DESC),'[]'::jsonb) FROM (
 SELECT id,name,email,firm,note,created_at FROM contact_notes WHERE $1=0 OR id<$1 ORDER BY id DESC LIMIT $2) n`, before, adminPage+1).Scan(&raw)
	})
	if err != nil {
		adminFirmProblem(w, err, "contact notes read")
		return
	}
	var list []json.RawMessage
	json.Unmarshal(raw, &list)
	var next any
	if len(list) > adminPage {
		list = list[:adminPage]
		var last struct {
			ID int64 `json:"id"`
		}
		json.Unmarshal(list[len(list)-1], &last)
		next = last.ID
	}
	jsonReply(w, 200, map[string]any{"notes": list, "next_before": next})
}
