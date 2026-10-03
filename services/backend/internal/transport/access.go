package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/auth"
)

// executive is a client's executive: they read their assigned clients' published reports and current cash and budget
// views, and nothing else, whatever other permissions their role carries.
func executive(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && slices.Contains(a.Permissions, "executive:read")
}

func firmAccess(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && !executive(r) && (a.Organization == "" || slices.Contains(a.Permissions, "clients:all"))
}
func subject(r *http.Request) string {
	if a := auth.Actor(r.Context()); a != nil {
		return a.Subject
	}
	return ""
}
func clientAccess(db *sql.DB, r *http.Request, entity string) (bool, error) {
	if firmAccess(r) {
		return true, nil
	}
	var granted bool
	err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($3) AND granted)`, organization(r), subject(r), entity).Scan(&granted)
	return granted, err
}

func registerAccess(mux *http.ServeMux, db *sql.DB) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if !firmAccess(r) || (a.Organization != "" && !slices.Contains(a.Permissions, "members:manage")) {
			problem(w, 403, "Client access administration requires clients:all and members:manage.")
			return
		}
		if r.Method == "GET" {
			var raw []byte
			err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(g) ORDER BY id DESC),'[]') FROM client_access g WHERE organization_id=$1`, organization(r)).Scan(&raw)
			if err != nil {
				problem(w, 503, "Access history unavailable.")
				return
			}
			jsonReply(w, 200, json.RawMessage(raw))
			return
		}
		var in struct {
			Subject    string `json:"subject"`
			Entity     string `json:"entity"`
			Granted    bool   `json:"granted"`
			Reason     string `json:"reason"`
			PreviousID string `json:"previous_id"`
			Confirmed  bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Subject = strings.TrimSpace(in.Subject)
		in.Entity = strings.TrimSpace(in.Entity)
		in.Reason = strings.TrimSpace(in.Reason)
		if !in.Confirmed || !strings.HasPrefix(in.Subject, "user_") || len(in.Subject) > 200 || in.Entity == "" || len(in.Entity) > 200 || len(in.Reason) < 1 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm an exact WorkOS user ID, existing client, and access reason.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Access storage unavailable.")
			return
		}
		defer tx.Rollback()
		var org string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, organization(r)).Scan(&org); err != nil {
			problem(w, 403, "Organization unavailable.")
			return
		}
		var exists bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2))`, org, in.Entity).Scan(&exists); err != nil || !exists {
			problem(w, 422, "Choose an existing client in your organization.")
			return
		}
		var prior string
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($3)),'')`, org, in.Subject, in.Entity).Scan(&prior); err != nil {
			problem(w, 503, "Current access unavailable.")
			return
		}
		if prior != in.PreviousID {
			problem(w, 409, "Access changed. Refresh history before deciding.")
			return
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, org, in.Subject, in.Entity, in.Granted, in.Reason, subject(r)).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Access change could not be confirmed. Reload history before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	}
	mux.HandleFunc("GET /v1/closes/access", handler)
	mux.HandleFunc("POST /v1/closes/access", handler)
}
