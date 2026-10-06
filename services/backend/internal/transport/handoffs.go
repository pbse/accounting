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
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
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
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	AccountID string    `json:"account_id,omitempty"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	By        string    `json:"by"`
	At        time.Time `json:"at"`
	Mine      bool      `json:"mine"`
	Handed    string    `json:"handed"`
	stall     string
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
	reviewers, err := firmReviewers(ctx, org)
	if err != nil {
		return nil, err
	}
	out := []teammate{}
	for _, t := range members {
		if r := reviewers[t.Subject]; r.review && (r.firmWide || t.viaGrant) {
			t.viaGrant = !r.firmWide
			t.Name = cmp.Or(r.name, t.Subject)
			out = append(out, t)
		}
	}
	return out, nil
}

// reviewer is what a member's WorkOS roles allow: reviewing work, on every client or only on those granted to them.
type reviewer struct {
	review, firmWide bool
	name             string
}

// firmReviewers reads each active member's WorkOS roles in the firm; a member holds the permissions of all their roles,
// as their session does.
func firmReviewers(ctx context.Context, org string) (map[string]reviewer, error) {
	if os.Getenv("WORKOS_API_KEY") == "" {
		return nil, errors.New("WorkOS is not configured")
	}
	var roles struct {
		Data []workosRole `json:"data"`
	}
	if err := workos(ctx, "GET", "/authorization/organizations/"+url.PathEscape(org)+"/roles", nil, &roles); err != nil {
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
	out := map[string]reviewer{}
	for _, m := range memberships {
		p := []string{}
		for _, each := range append(m.Roles, m.Role) {
			p = append(p, held[each.Slug]...)
		}
		r := reviewer{name: cmp.Or(strings.TrimSpace(m.User.FirstName+" "+m.User.LastName), m.User.Email)}
		// Anyone holding executive:read is kept to the executive view (executive) and cannot open a close, whatever else
		// their roles grant, so they are nobody's teammate for close work.
		if m.Status == "active" && slices.Contains(p, "workpapers:review") && !slices.Contains(p, "executive:read") {
			r.review, r.firmWide = true, slices.Contains(p, "clients:all")
		}
		out[m.UserID] = r
	}
	return out, nil
}

// recentRoles is each firm's WorkOS roles for its goals' runs, read at most once a minute per firm: every open goal looks
// at its close about once a minute, and a firm's goals share one read.
var recentRoles = &rolesCache{read: map[string]cachedRoles{}}

type cachedRoles struct {
	members map[string]reviewer
	err     error
	at      time.Time
}

type rolesCache struct {
	sync.Mutex
	read   map[string]cachedRoles
	flight singleflight.Group // goals looking at once share the firm's read in flight
}

// of is sweepRoles.of, remembered for a minute. A read its caller cut short is not remembered.
func (c *rolesCache) of(ctx context.Context, org string) (map[string]reviewer, error) {
	c.Lock()
	read, ok := c.read[org]
	c.Unlock()
	if ok && time.Since(read.at) < time.Minute {
		return read.members, read.err
	}
	shared, _, _ := c.flight.Do(org, func() (any, error) {
		// A read that finished while this caller waited to start one is recent enough.
		c.Lock()
		read, ok := c.read[org]
		c.Unlock()
		if ok && time.Since(read.at) < time.Minute {
			return read, nil
		}
		read = cachedRoles{at: time.Now()}
		read.members, read.err = sweepRoles{}.of(ctx, org)
		if read.err == nil || ctx.Err() == nil {
			c.Lock()
			c.read[org] = read
			c.Unlock()
		}
		return read, nil
	})
	read = shared.(cachedRoles)
	return read.members, read.err
}

// sweepRoles is each firm's WorkOS roles for one sweep of background work, read at most once per firm.
type sweepRoles map[string]struct {
	members map[string]reviewer
	err     error
}

// of is the firm's members' roles: none for the local development firm, which has no WorkOS, and an error for a hosted
// firm whose roles cannot be read, whose background work then waits rather than act on authority it cannot confirm.
func (f sweepRoles) of(ctx context.Context, org string) (map[string]reviewer, error) {
	if org == "local-development" {
		return nil, nil
	}
	read, ok := f[org]
	if !ok {
		read.members, read.err = firmReviewers(ctx, org)
		f[org] = read
	}
	return read.members, read.err
}

// mayAct says whether a firm member may still act for a client. With roles, their current ones decide, as for the
// teammate list: review permission, on every client or through a current grant. Without, as for the local development
// firm, membership does and, for access recorded as a grant, that grant.
func mayAct(roles map[string]reviewer, subject string, member, viaGrant, granted bool) bool {
	if roles == nil {
		return member && (!viaGrant || granted)
	}
	r := roles[subject]
	return member && r.review && (r.firmWide || granted)
}

// The agent follows up on a hand-off that stays open: with the teammate after two days, and with the goal's owner after
// four, or the day before the goal is due when that is sooner (at midday UTC, which falls on that day from UTC-11 to
// UTC+11, where a due date has no time zone), but a day after the hand-off at the earliest. The texts say "at least", since an item keeps its words while it stays open. Each follow-up is
// read from the time, so nothing is sent or stored; a person decides what to do.

// handoffEscalated is SQL for when the owner of hand-off h's goal g is asked to check on it.
const handoffEscalated = `(CASE WHEN g.due_on IS NULL THEN h.created_at+interval '4 days' ELSE LEAST(h.created_at+interval '4 days',GREATEST(h.created_at+interval '1 day',(g.due_on-1)::timestamp AT TIME ZONE 'UTC'+interval '12 hours')) END)`

// handoffStall is SQL for what the owner is asked.
const handoffStall = `h.document||' has been with '||h.assignee_name||' for at least '||(SELECT CASE WHEN d=1 THEN '1 day' ELSE d||' days' END FROM (SELECT floor(extract(epoch FROM ` + handoffEscalated + `-h.created_at)/86400)::int AS d) x)||'. Check with them, or take it back.'`

// readHandoffs is the goal's open hand-offs, marking those handed to me and, once asked, what the owner is asked.
func readHandoffs(ctx context.Context, tx *sql.Tx, goalID, me string) ([]goalHandoff, error) {
	rows, err := tx.QueryContext(ctx, `SELECT h.id::text,COALESCE(h.job_id::text,''),COALESCE(h.account_id,''),h.assignee_name,h.note,h.created_by,h.created_at,h.assignee=$2,h.document,
 CASE WHEN `+handoffEscalated+`<=now() AND h.assignee<>g.author THEN `+handoffStall+` ELSE '' END
 FROM goal_handoffs h JOIN close_goals g ON g.id=h.goal_id WHERE h.goal_id=$1 AND h.closed_at IS NULL ORDER BY h.id`, goalID, me)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []goalHandoff{}
	for rows.Next() {
		var h goalHandoff
		if err = rows.Scan(&h.ID, &h.JobID, &h.AccountID, &h.Name, &h.Note, &h.By, &h.At, &h.Mine, &h.Handed, &h.stall); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// settleHandoffs closes the goal's open hand-offs whose document or account no longer needs a person, or whose teammate
// can no longer act for the client; once the close is signed off, every one. An account's is closed only on coverage
// read from its capture: a different capture ends it, and unread coverage leaves it open. The goal's history says why.
func settleHandoffs(ctx context.Context, tx *sql.Tx, g goalRow, p goalProgress, signed bool, roles map[string]reviewer) error {
	rows, err := tx.QueryContext(ctx, `SELECT h.id::text,COALESCE(h.job_id::text,''),COALESCE(h.account_id,''),COALESCE(h.snapshot_id::text,''),h.document,h.assignee_name,h.assignee,h.via_grant,
 EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=$1 AND m.subject=h.assignee AND m.granted),
 EXISTS(SELECT 1 FROM current_client_access a JOIN client_closes c ON lower(c.entity)=lower(a.client_entity) WHERE c.id=$3 AND a.organization_id=$1 AND a.subject=h.assignee AND a.granted)
 FROM goal_handoffs h WHERE h.goal_id=$2 AND h.closed_at IS NULL ORDER BY h.id`, g.org, g.id, g.close)
	if err != nil {
		return err
	}
	type open struct {
		id, job, account, snapshot, document, name string
		allowed                                    bool
	}
	handoffs := []open{}
	for rows.Next() {
		var h open
		var assignee string
		var viaGrant, member, granted bool
		if err = rows.Scan(&h.id, &h.job, &h.account, &h.snapshot, &h.document, &h.name, &assignee, &viaGrant, &member, &granted); err != nil {
			rows.Close()
			return err
		}
		h.allowed = mayAct(roles, assignee, member, viaGrant, granted)
		handoffs = append(handoffs, h)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, h := range handoffs {
		text := h.document + " no longer needs anyone, so " + h.name + "'s hand-off is closed."
		switch {
		case signed:
			text = "The close was signed off, so " + h.name + "'s hand-off of " + h.document + " is closed."
		case !h.allowed:
			text = h.name + " can no longer act for this client, so I took " + h.document + " back."
		case h.account != "" && p.accounts == nil:
			continue
		case h.account != "" && p.accounts.SnapshotID != h.snapshot:
			text = "The ledger capture changed, so " + h.name + "'s hand-off of " + h.document + " is closed."
		case h.account != "":
			if i := slices.IndexFunc(p.accounts.Items, func(a coverageRequirement) bool { return a.AccountID == h.account }); i >= 0 && p.accounts.Items[i].State != "resolved" && p.accounts.Items[i].State != "excluded" {
				continue
			}
		default:
			if i := slices.IndexFunc(p.Items, func(item goalItem) bool { return item.JobID == h.job }); i >= 0 && (p.Items[i].Handable || p.Items[i].Status == "working") {
				continue
			}
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
	// Teammates are listed to whoever may hand them work: reviewers handing a document, and firm administrators
	// reassigning a close.
	mux.HandleFunc("GET /v1/closes/{id}/people", func(w http.ResponseWriter, r *http.Request) {
		if !canHandOver(r) {
			problem(w, 403, "Your organization role lacks workpapers:review.")
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
			JobID     string `json:"job_id"`
			AccountID string `json:"account_id"`
			Assignee  string `json:"assignee"`
			Note      string `json:"note"`
		}
		if !decode(w, r, &in) {
			return
		}
		if (in.JobID == "") == (in.AccountID == "") {
			problem(w, 422, "Hand over one document or one account.")
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
		var document, snapshot string
		if in.JobID != "" {
			i := slices.IndexFunc(progress.Items, func(item goalItem) bool { return item.JobID == in.JobID })
			if i < 0 || !progress.Items[i].Handable {
				problem(w, 422, "Only a document that needs a person to look into it can be handed to a teammate.")
				return
			}
			document = progress.Items[i].Name
		} else {
			// An account is handed on the capture it was read from, while its coverage still needs a decision; a stale
			// capture's accounts are recaptured first.
			i := -1
			if progress.accounts != nil {
				i = slices.IndexFunc(progress.accounts.Items, func(a coverageRequirement) bool { return a.AccountID == in.AccountID })
			}
			if i < 0 || !slices.Contains([]string{"missing_evidence", "awaiting_work", "needs_review"}, progress.accounts.Items[i].State) {
				problem(w, 422, "Only an account whose coverage still needs a decision can be handed to a teammate; recapture first if the ledger capture is out of date.")
				return
			}
			document, snapshot = "coverage for "+progress.accounts.Items[i].Name, progress.accounts.SnapshotID
		}
		var id string
		switch err = tx.QueryRowContext(r.Context(), `INSERT INTO goal_handoffs(goal_id,job_id,account_id,snapshot_id,document,assignee,assignee_name,via_grant,note,created_by) VALUES($1,NULLIF($2,'')::bigint,NULLIF($3,''),NULLIF($4,'')::bigint,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING RETURNING id::text`, g.id, in.JobID, in.AccountID, snapshot, document, to.Subject, to.Name, to.viaGrant, in.Note, actor).Scan(&id); err {
		case nil:
		case sql.ErrNoRows:
			var holder string
			if tx.QueryRowContext(r.Context(), `SELECT assignee_name FROM goal_handoffs WHERE closed_at IS NULL AND (job_id::text=$1 OR goal_id=$2 AND account_id=$3)`, in.JobID, g.id, in.AccountID).Scan(&holder) == nil {
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
