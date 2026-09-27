package transport

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

// A document of a delegated close handed to a teammate to investigate. The teammate acts through Work with their own
// permissions; the agent closes the hand-off once the document no longer needs a person, or when the teammate can no
// longer act for the client.

type teammate struct {
	Subject  string `json:"subject"`
	Name     string `json:"name"`
	viaGrant bool
}

type goalHandoff struct {
	ID    string    `json:"id"`
	JobID string    `json:"job_id"`
	Name  string    `json:"name"`
	Note  string    `json:"note"`
	By    string    `json:"by"`
	At    time.Time `json:"at"`
	Mine  bool      `json:"mine"`
}

// workosList reads every page of a WorkOS list, at most ten; a longer list is an error, never a partial one.
func workosList[T any](ctx context.Context, path string, query url.Values) ([]T, error) {
	all := []T{}
	for range 10 {
		var page struct {
			Data         []T `json:"data"`
			ListMetadata struct {
				After string `json:"after"`
			} `json:"list_metadata"`
		}
		if err := workos(ctx, "GET", path+"?"+query.Encode(), nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.ListMetadata.After == "" {
			return all, nil
		}
		query.Set("after", page.ListMetadata.After)
	}
	return nil, errors.New("WorkOS list longer than ten pages")
}

// teammates lists the firm's members, other than the requester, who can investigate the client's documents: granted
// in the firm, with an active WorkOS membership whose role holds workpapers:review and either clients:all or, through
// an explicit assignment, the client. Only the local development firm, which has no WorkOS, lists every member.
func teammates(ctx context.Context, db *sql.DB, r *http.Request, entity string) ([]teammate, error) {
	org := organization(r)
	rows, err := db.QueryContext(ctx, `SELECT m.subject,EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=m.organization_id AND g.subject=m.subject AND lower(g.client_entity)=lower($2) AND g.granted)
 FROM current_organization_members m WHERE m.organization_id=$1 AND m.granted AND m.subject<>$3 ORDER BY m.subject`, org, entity, subject(r))
	if err != nil {
		return nil, err
	}
	members := []teammate{}
	for rows.Next() {
		t := teammate{}
		if err = rows.Scan(&t.Subject, &t.viaGrant); err != nil {
			rows.Close()
			return nil, err
		}
		t.Name = t.Subject
		members = append(members, t)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if org == "local-development" {
		for i := range members {
			members[i].viaGrant = false
		}
		return members, nil
	}
	if os.Getenv("WORKOS_API_KEY") == "" {
		return nil, errors.New("WorkOS is not configured")
	}
	var roles struct {
		Data []workosRole `json:"data"`
	}
	if err = workos(ctx, "GET", "/authorization/organizations/"+url.PathEscape(org)+"/roles", nil, &roles); err != nil {
		return nil, err
	}
	held := map[string][]string{}
	for _, role := range roles.Data {
		if role.Permissions != nil {
			held[role.Slug] = *role.Permissions
		}
	}
	type role struct {
		Slug string `json:"slug"`
	}
	memberships, err := workosList[struct {
		UserID string `json:"user_id"`
		Status string `json:"status"`
		Role   role   `json:"role"`
		Roles  []role `json:"roles"`
		User   struct {
			Email     string `json:"email"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"user"`
	}](ctx, "/user_management/organization_memberships", url.Values{"organization_id": {org}, "statuses": {"active"}, "limit": {"100"}})
	if err != nil {
		return nil, err
	}
	reviews, firmWide, names := map[string]bool{}, map[string]bool{}, map[string]string{}
	for _, m := range memberships {
		// A member holds the permissions of all their roles, as their session does.
		p := []string{}
		for _, each := range append(m.Roles, m.Role) {
			p = append(p, held[each.Slug]...)
		}
		if m.Status == "active" && slices.Contains(p, "workpapers:review") {
			reviews[m.UserID], firmWide[m.UserID] = true, slices.Contains(p, "clients:all")
		}
		names[m.UserID] = cmp.Or(strings.TrimSpace(m.User.FirstName+" "+m.User.LastName), m.User.Email)
	}
	out := []teammate{}
	for _, t := range members {
		if reviews[t.Subject] && (firmWide[t.Subject] || t.viaGrant) {
			t.viaGrant = !firmWide[t.Subject]
			t.Name = cmp.Or(names[t.Subject], t.Subject)
			out = append(out, t)
		}
	}
	return out, nil
}

// readHandoffs is the goal's open hand-offs, marking those handed to me.
func readHandoffs(ctx context.Context, tx *sql.Tx, goalID, me string) ([]goalHandoff, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,job_id::text,assignee_name,note,created_by,created_at,assignee=$2 FROM goal_handoffs WHERE goal_id=$1 AND closed_at IS NULL ORDER BY id`, goalID, me)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []goalHandoff{}
	for rows.Next() {
		var h goalHandoff
		if err = rows.Scan(&h.ID, &h.JobID, &h.Name, &h.Note, &h.By, &h.At, &h.Mine); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// settleHandoffs closes the goal's open hand-offs whose document no longer needs a person to investigate it, or whose
// teammate can no longer act for the client; once the close is signed off, every one. The goal's history says why.
func settleHandoffs(ctx context.Context, tx *sql.Tx, g goalRow, items []goalItem, signed bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT h.id::text,h.job_id::text,h.document,h.assignee_name,
 EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=$1 AND m.subject=h.assignee AND m.granted)
 AND (NOT h.via_grant OR EXISTS(SELECT 1 FROM current_client_access a JOIN client_closes c ON lower(c.entity)=lower(a.client_entity) WHERE c.id=$3 AND a.organization_id=$1 AND a.subject=h.assignee AND a.granted))
 FROM goal_handoffs h WHERE h.goal_id=$2 AND h.closed_at IS NULL ORDER BY h.id`, g.org, g.id, g.close)
	if err != nil {
		return err
	}
	type open struct {
		id, job, document, name string
		allowed                 bool
	}
	handoffs := []open{}
	for rows.Next() {
		var h open
		if err = rows.Scan(&h.id, &h.job, &h.document, &h.name, &h.allowed); err != nil {
			rows.Close()
			return err
		}
		handoffs = append(handoffs, h)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, h := range handoffs {
		var text string
		switch i := slices.IndexFunc(items, func(item goalItem) bool { return item.JobID == h.job }); {
		case signed:
			text = "The close was signed off, so " + h.name + "'s hand-off of " + h.document + " is closed."
		case !h.allowed:
			text = h.name + " can no longer act for this client, so I took " + h.document + " back."
		case i >= 0 && (items[i].Handable || items[i].Status == "working"):
			continue
		default:
			text = h.document + " no longer needs anyone, so " + h.name + "'s hand-off is closed."
		}
		if _, err = tx.ExecContext(ctx, `UPDATE goal_handoffs SET closed_at=now(),closed_by='agent' WHERE id=$1`, h.id); err == nil {
			err = goalEvent(ctx, tx, g.id, "agent", text)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func registerHandoffs(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/people", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
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
		jsonReply(w, 200, map[string]any{"people": people})
	})
	mux.HandleFunc("POST /v1/closes/{id}/goal/handoffs", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		actor := subject(r)
		var in struct {
			JobID    string `json:"job_id"`
			Assignee string `json:"assignee"`
			Note     string `json:"note"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Note = strings.TrimSpace(in.Note); len(in.Note) > 500 {
			problem(w, 422, "Keep the note to 500 characters.")
			return
		}
		var entity string
		if err := db.QueryRowContext(r.Context(), `SELECT entity FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&entity); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		// WorkOS is read before any lock is taken; the firm's own records of the teammate are read again under the locks.
		people, err := teammates(r.Context(), db, r, entity)
		if err != nil {
			problem(w, 502, "Teammates are unavailable right now.")
			return
		}
		n := slices.IndexFunc(people, func(t teammate) bool { return t.Subject == in.Assignee })
		if n < 0 {
			problem(w, 422, "Choose a teammate who can review "+entity+"'s work.")
			return
		}
		to := people[n]
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The agent is unavailable.")
			return
		}
		defer tx.Rollback()
		g := goalRow{}
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,organization_id FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&g.close, &g.org); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		switch err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM close_goals WHERE close_id=$1 AND state<>'stopped' FOR UPDATE`, g.close).Scan(&g.id); err {
		case nil:
		case sql.ErrNoRows:
			problem(w, 409, "Hand the close to the agent first.")
			return
		default:
			problem(w, 503, "The agent is unavailable.")
			return
		}
		// The firm row serializes this with member and client-access decisions, as the agent's own steps do.
		var allowed bool
		err = tx.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1 FOR SHARE`, g.org).Scan(&allowed)
		if err == nil && allowed {
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)
 AND (NOT $3 OR EXISTS(SELECT 1 FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($4) AND granted))`, g.org, to.Subject, to.viaGrant, entity).Scan(&allowed)
		}
		if err != nil {
			problem(w, 503, "The agent is unavailable.")
			return
		}
		if !allowed {
			problem(w, 422, "Choose a teammate who can review "+entity+"'s work.")
			return
		}
		signed, err := signedOff(r.Context(), tx, g.close)
		if err != nil {
			problem(w, 503, "Close readiness unavailable.")
			return
		}
		if signed {
			problem(w, 409, "This close is signed off. Reopen it before handing work to a teammate.")
			return
		}
		progress, err := readGoalProgress(r.Context(), tx, g.close, "review")
		if err != nil {
			problem(w, 503, "The agent's progress is unavailable.")
			return
		}
		i := slices.IndexFunc(progress.Items, func(item goalItem) bool { return item.JobID == in.JobID })
		if i < 0 || !progress.Items[i].Handable {
			problem(w, 422, "Only a document that needs a person to look into it can be handed to a teammate.")
			return
		}
		document := progress.Items[i].Name
		var id string
		switch err = tx.QueryRowContext(r.Context(), `INSERT INTO goal_handoffs(goal_id,job_id,document,assignee,assignee_name,via_grant,note,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING RETURNING id::text`, g.id, in.JobID, document, to.Subject, to.Name, to.viaGrant, in.Note, actor).Scan(&id); err {
		case nil:
		case sql.ErrNoRows:
			var holder string
			if tx.QueryRowContext(r.Context(), `SELECT assignee_name FROM goal_handoffs WHERE job_id=$1 AND closed_at IS NULL`, in.JobID).Scan(&holder) == nil {
				problem(w, 409, holder+" already has "+document+". Take it back first.")
				return
			}
			fallthrough
		default:
			problem(w, 503, "The document could not be handed over. Refresh and try again.")
			return
		}
		text := actor + " handed " + document + " to " + to.Name
		if in.Note != "" {
			text += ": " + in.Note
		}
		if err = goalEvent(r.Context(), tx, g.id, actor, text); err != nil || tx.Commit() != nil {
			problem(w, 503, "The document could not be handed over. Refresh and try again.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
	mux.HandleFunc("POST /v1/closes/{id}/goal/handoffs/{handoff}/take-back", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		actor := subject(r)
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
		var assignee, name, document string
		err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM close_goals WHERE close_id=$1 AND state<>'stopped' FOR UPDATE`, closeID).Scan(&goalID)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `UPDATE goal_handoffs SET closed_at=now(),closed_by=$3 WHERE id::text=$1 AND goal_id=$2 AND closed_at IS NULL RETURNING assignee,assignee_name,document`, r.PathValue("handoff"), goalID, actor).Scan(&assignee, &name, &document)
		}
		if err == sql.ErrNoRows {
			problem(w, 409, "This hand-off is already closed. Refresh the close.")
			return
		}
		text := actor + " took " + document + " back from " + name + "."
		if assignee == actor {
			text = name + " handed " + document + " back."
		}
		if err == nil {
			err = goalEvent(r.Context(), tx, goalID, actor, text)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The hand-off could not be taken back. Refresh and try again.")
			return
		}
		jsonReply(w, 200, map[string]bool{"closed": true})
	})
}
