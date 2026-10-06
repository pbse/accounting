package transport

import (
	"database/sql"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/auth"
)

// Planning is scheduling metadata for ranking work; it never grants review or posting authority. Who holds a close can
// change hands: a person takes an unheld close or releases their own; whoever holds it (anyone, while nobody does) hands
// it to a teammate who can review the client; a firm administrator (canManageMembers) reassigns it whoever holds it. Each
// change is recorded.
func registerPlanning(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/{id}/planning", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			DueOn      string `json:"due_on"`
			Priority   string `json:"priority"`
			Assignment string `json:"assignment"`
			Assignee   string `json:"assignee"`
			Note       string `json:"note"`
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
		if !slices.Contains([]string{"keep", "take", "release", "assign"}, input.Assignment) {
			problem(w, 422, "Keep, take, release or assign the close.")
			return
		}
		if input.Note = strings.TrimSpace(input.Note); utf8.RuneCountInString(input.Note) > 500 {
			problem(w, 422, "Keep the note to 500 characters.")
			return
		}
		me := subject(r)
		if input.Assignment != "keep" && me == "" {
			problem(w, 403, "Sign in before taking, releasing or assigning a close.")
			return
		}
		if input.Assignment == "assign" && !canHandOver(r) {
			problem(w, 403, "Handing a close over requires workpapers:review, or clients:all and members:manage.")
			return
		}
		// The new holder must be a teammate who can review the client's work. WorkOS is read before any lock is taken; the
		// firm's own records of the teammate are read again under the locks.
		var to teammate
		if input.Assignment == "assign" {
			if input.Assignee == "" || input.Assignee == me {
				problem(w, 422, "Choose a teammate to hand the close to. To hold it yourself, take it.")
				return
			}
			var entity string
			if err := db.QueryRowContext(r.Context(), `SELECT entity FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&entity); err != nil {
				problem(w, 404, "Close not found.")
				return
			}
			people, err := teammates(r.Context(), db, r, entity)
			if err != nil {
				problem(w, 502, "Teammates are unavailable right now.")
				return
			}
			n := slices.IndexFunc(people, func(t teammate) bool { return t.Subject == input.Assignee })
			if n < 0 {
				problem(w, 422, "Choose a teammate who can review "+entity+"'s work.")
				return
			}
			to = people[n]
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Could not save the close plan.")
			return
		}
		defer tx.Rollback()
		var org, entity, owner string
		switch err = tx.QueryRowContext(r.Context(), `SELECT organization_id,entity,owner FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&org, &entity, &owner); err {
		case nil:
		case sql.ErrNoRows:
			problem(w, 404, "Close not found.")
			return
		default:
			problem(w, 503, "Could not save the close plan.")
			return
		}
		next := owner
		switch input.Assignment {
		case "take":
			if owner != "" && owner != me {
				problem(w, 409, "Someone else holds this close. They can release it, or ask a firm administrator.")
				return
			}
			next = me
		case "release":
			if owner != me {
				problem(w, 409, "Someone else holds this close. They can release it, or ask a firm administrator.")
				return
			}
			next = ""
		case "assign":
			if owner != "" && owner != me && !canManageMembers(r) {
				problem(w, 409, "Someone else holds this close. They can hand it over, or ask a firm administrator.")
				return
			}
			// The firm row serializes this with member and client-access decisions, as hand-offs do.
			var allowed bool
			err = tx.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1 FOR SHARE`, org).Scan(&allowed)
			if err == nil && allowed {
				err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)
 AND (NOT $3 OR EXISTS(SELECT 1 FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($4) AND granted))`, org, to.Subject, to.viaGrant, entity).Scan(&allowed)
			}
			if err != nil {
				problem(w, 503, "Could not save the close plan.")
				return
			}
			if !allowed {
				problem(w, 409, to.Name+" can no longer review "+entity+"'s work. Choose another teammate.")
				return
			}
			next = to.Subject
		}
		if _, err = tx.ExecContext(r.Context(), `UPDATE client_closes SET due_on=NULLIF($2,'')::date,priority=$3,owner=$4 WHERE id::text=$1`, r.PathValue("id"), input.DueOn, input.Priority, next); err != nil {
			problem(w, 503, "Could not save the close plan.")
			return
		}
		if next != owner {
			reason := ""
			if input.Assignment == "assign" {
				reason = input.Note
			}
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO close_owner_changes(close_id,from_owner,to_owner,actor,reason) SELECT id,$2,$3,$4,$5 FROM client_closes WHERE id::text=$1`, r.PathValue("id"), owner, next, me, reason); err != nil {
				problem(w, 503, "Could not save the close plan.")
				return
			}
		}
		if tx.Commit() != nil {
			problem(w, 503, "Could not save the close plan.")
			return
		}
		jsonReply(w, 200, map[string]any{"due_on": input.DueOn, "priority": input.Priority, "owned_by_me": next != "" && next == me, "assigned": next != ""})
	})
}

// canHandOver is someone who may hand a close to a teammate and so see who those teammates are: a reviewer, or a firm
// administrator (canManageMembers), who may not review work but reassigns it. Without an organization (local
// development) everyone may.
func canHandOver(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && (a.Organization == "" || slices.Contains(a.Permissions, "workpapers:review") || canManageMembers(r))
}
