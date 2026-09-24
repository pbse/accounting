package transport

import (
	"database/sql"
	"net/http"
	"time"
)

// Planning is scheduling metadata for ranking work; it never grants review or posting authority.
func registerPlanning(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/{id}/planning", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			DueOn      string `json:"due_on"`
			Priority   string `json:"priority"`
			Assignment string `json:"assignment"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.DueOn != "" {
			if _, err := time.Parse("2006-01-02", input.DueOn); err != nil {
				problem(w, 422, "Enter the due date as YYYY-MM-DD, or leave it empty.")
				return
			}
		}
		if input.Priority != "high" && input.Priority != "normal" && input.Priority != "low" {
			problem(w, 422, "Choose high, normal or low priority.")
			return
		}
		if input.Assignment == "" {
			input.Assignment = "keep"
		}
		if input.Assignment != "keep" && input.Assignment != "take" && input.Assignment != "release" {
			problem(w, 422, "Keep, take or release the assignment.")
			return
		}
		me := subject(r)
		if input.Assignment != "keep" && me == "" {
			problem(w, 403, "Sign in before taking or releasing a close.")
			return
		}
		var owner string
		err := db.QueryRowContext(r.Context(), `UPDATE client_closes SET due_on=NULLIF($2,'')::date,priority=$3,owner=CASE $4 WHEN 'take' THEN $5 WHEN 'release' THEN '' ELSE owner END WHERE id::text=$1 AND ($4='keep' OR ($4='take' AND owner IN ('',$5)) OR ($4='release' AND owner=$5)) RETURNING owner`, r.PathValue("id"), input.DueOn, input.Priority, input.Assignment, me).Scan(&owner)
		if err == sql.ErrNoRows {
			problem(w, 409, "Someone else holds this close. They can release it, or ask a firm administrator.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not save the close plan.")
			return
		}
		jsonReply(w, 200, map[string]any{"due_on": input.DueOn, "priority": input.Priority, "owned_by_me": owner != "" && owner == me, "assigned": owner != ""})
	})
}
