package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/auth"
)

// The firm's client roster and monthly close template (B01/B02, migration 127). Every active client on the roster owes
// a close for each ended month from the first month it owes; a month no close was opened for is listed until it is opened
// (owedMonths), so an obligation nobody started still shows. A month's close takes the template's due date and owner,
// unless the client's own entry overrides them (applyCloseTemplate). A client joins the roster when it is taken over or
// a firm administrator adds it, and leaves when an administrator marks it inactive or its close is deleted because the
// relationship ended.

// rosterMatch is SQL for roster row r being the entry of the client name names in firm org. Clients are matched by
// client_identity, so a name that is a confirmed alias of a client is that client.
func rosterMatch(org, name string) string {
	return `r.organization_id=` + org + ` AND client_identity(r.organization_id,r.client_entity)=client_identity(` + org + `,` + name + `)`
}

// rosterTemplate is SQL, over a current_client_roster row r, for the firm's current template t.
const rosterTemplate = `LEFT JOIN LATERAL (SELECT due_days,owner FROM close_templates WHERE organization_id=r.organization_id ORDER BY id DESC LIMIT 1) t ON true`

// applyCloseTemplate gives a newly opened close of an active roster client its month's due date (due_days after the
// month ends) and owner, from the client's overrides or else the firm's template, recording the owner as a change of
// hands. The owner must still be able to do the work, judged as mayAct judges it from the firm's WorkOS roles (nil for
// the local development firm, which has none): a hosted firm whose roles could not be read gives the close no owner.
// A close already dated or held keeps what it has, and a close of a client not on the roster is left alone.
func applyCloseTemplate(ctx context.Context, tx *sql.Tx, closeID string, roles map[string]reviewer) error {
	var org, period, owner string
	var dueDays sql.NullInt64
	var member, assigned bool
	err := tx.QueryRowContext(ctx, `SELECT c.organization_id,c.period,COALESCE(r.due_days,t.due_days),COALESCE(r.owner,t.owner,''),
 EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=c.organization_id AND m.subject=COALESCE(r.owner,t.owner,'') AND m.granted),
 EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=COALESCE(r.owner,t.owner,'') AND client_identity(g.organization_id,g.client_entity)=client_identity(c.organization_id,c.entity) AND g.granted)
 FROM client_closes c JOIN current_client_roster r ON `+rosterMatch("c.organization_id", "c.entity")+` AND r.active `+rosterTemplate+`
 WHERE c.id::text=$1`, closeID).Scan(&org, &period, &dueDays, &owner, &member, &assigned)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if dueDays.Valid && dueDays.Int64 > 0 {
		due := monthEnd(period).AddDate(0, 0, int(dueDays.Int64)).Format("2006-01-02")
		if _, err = tx.ExecContext(ctx, `UPDATE client_closes SET due_on=$2::date WHERE id::text=$1 AND due_on IS NULL`, closeID, due); err != nil {
			return err
		}
	}
	// The local development firm has no WorkOS and need not record its members.
	if org == "local-development" {
		member = true
	}
	if owner == "" || (roles == nil && org != "local-development") || !mayAct(roles, owner, member, false, assigned) {
		return nil
	}
	held, err := tx.ExecContext(ctx, `UPDATE client_closes SET owner=$2 WHERE id::text=$1 AND owner=''`, closeID, owner)
	if err != nil {
		return err
	}
	if n, _ := held.RowsAffected(); n == 1 {
		_, err = tx.ExecContext(ctx, `INSERT INTO close_owner_changes(close_id,from_owner,to_owner,actor,reason) VALUES($1::bigint,'',$2,'monthly close template','Assigned by the firm''s monthly close template')`, closeID, owner)
	}
	return err
}

// owedMonth is a month an active roster client owes a close for and none was opened: due when the template would date
// it, mine when it would go to the reader, and hand_over when the client's monthly hand-over is set to open it (it still
// shows until a close exists, however late the hand-over is).
type owedMonth struct {
	Entity   string `json:"entity"`
	Period   string `json:"period"`
	DueOn    string `json:"due_on"`
	Mine     bool   `json:"mine"`
	HandOver bool   `json:"hand_over"`
}

// owedMonths lists, newest first, the months the active roster clients the reader can work on (firm: every client; else
// those assigned to who) owe and have no close for: each ended month from the client's first owed month, matching
// client (a name fragment or YYYY-MM, or "") as Work's filter does. A month the client's monthly hand-over is set to
// open, and not stuck waiting, is marked as the hand-over's.
func owedMonths(ctx context.Context, tx *sql.Tx, now time.Time, org string, firm, review bool, who, client string) ([]owedMonth, error) {
	// Months are matched by client identity, so a month opened under a confirmed alias of the client counts.
	rows, err := tx.QueryContext(ctx, `WITH opened AS (SELECT DISTINCT client_identity(c.organization_id,c.entity) AS client,c.period FROM client_closes c WHERE c.organization_id=$1)
 SELECT r.client_entity,m.period,COALESCE(r.due_days,t.due_days),COALESCE(r.owner,t.owner,''),
  EXISTS(SELECT 1 FROM close_routines h WHERE h.organization_id=r.organization_id AND client_identity(h.organization_id,h.client_entity)=client_identity(r.organization_id,r.client_entity) AND h.stopped_at IS NULL AND h.waiting='' AND h.last_period<m.period)
 FROM current_client_roster r `+rosterTemplate+`
 CROSS JOIN LATERAL (SELECT to_char(g,'YYYY-MM') AS period FROM generate_series(to_date(r.since,'YYYY-MM'),to_date($4,'YYYY-MM'),interval '1 month') g) m
 WHERE r.organization_id=$1 AND r.active
  AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=r.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(r.client_entity) AND g.granted))
  AND NOT EXISTS(SELECT 1 FROM opened o WHERE o.client=client_identity(r.organization_id,r.client_entity) AND o.period=m.period)
  AND ($5='' OR r.client_entity ILIKE '%'||$5||'%' OR m.period=$5)
 ORDER BY m.period DESC,lower(r.client_entity)`, org, firm, who, endedMonth(now), client)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []owedMonth{}
	for rows.Next() {
		var o owedMonth
		var dueDays sql.NullInt64
		var owner string
		if err = rows.Scan(&o.Entity, &o.Period, &dueDays, &owner, &o.HandOver); err != nil {
			return nil, err
		}
		if dueDays.Valid && dueDays.Int64 > 0 {
			o.DueOn = monthEnd(o.Period).AddDate(0, 0, int(dueDays.Int64)).Format("2006-01-02")
		}
		// Yours when the template would give it to you and you can do the work now: the months listed are of clients you
		// can work on, so it is your own review permission that is left to check.
		o.Mine = owner != "" && owner == who && review
		out = append(out, o)
	}
	return out, rows.Err()
}

// rosterOwner says whether subject can hold a client's closes, and their name: a granted member who reviews entity's
// work, through a role that reviews every client or an assignment to entity. The firm template applies to every client
// (entity ""), so only a role reviewing every client will do. Only the local development firm, which has no WorkOS,
// takes any member.
func rosterOwner(ctx context.Context, db *sql.DB, r *http.Request, entity, subject string) (string, bool, error) {
	org := organization(r)
	if org == "local-development" {
		return subject, true, nil
	}
	var member, assigned bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted),
 EXISTS(SELECT 1 FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($3) AND granted)`, org, subject, entity).Scan(&member, &assigned)
	if err != nil || !member {
		return "", false, err
	}
	reviewers, err := firmReviewers(ctx, org)
	if err != nil {
		return "", false, err
	}
	rv := reviewers[subject]
	if !rv.review || !(rv.firmWide || entity != "" && assigned) {
		return "", false, nil
	}
	return cmp.Or(rv.name, subject), true, nil
}

type rosterClient struct {
	Entity     string   `json:"entity"`
	Active     bool     `json:"active"`
	Since      string   `json:"since"`
	DueDays    *int     `json:"due_days"`
	Owner      *string  `json:"owner"`
	Reason     string   `json:"reason"`
	Actor      string   `json:"actor"`
	At         string   `json:"at"`
	LastPeriod string   `json:"last_period"`
	Owed       []string `json:"owed"`
}

func registerRoster(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/roster", func(w http.ResponseWriter, r *http.Request) {
		if !firmAccess(r) {
			problem(w, 403, "The client roster requires clients:all.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "The client roster is unavailable.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		var template map[string]any
		var dueDays sql.NullInt64
		var owner, reason, actor string
		var at time.Time
		switch err = tx.QueryRowContext(r.Context(), `SELECT due_days,owner,reason,actor,created_at FROM close_templates WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&dueDays, &owner, &reason, &actor, &at); err {
		case nil:
			template = map[string]any{"due_days": nil, "owner": owner, "reason": reason, "actor": actor, "at": at}
			if dueDays.Valid {
				template["due_days"] = dueDays.Int64
			}
		case sql.ErrNoRows:
		default:
			problem(w, 503, "The client roster is unavailable.")
			return
		}
		rows, err := tx.QueryContext(r.Context(), `SELECT r.client_entity,r.active,r.since,r.due_days,r.owner,r.reason,r.actor,r.created_at,
 COALESCE((SELECT max(c.period) FROM client_closes c WHERE c.organization_id=r.organization_id AND client_identity(c.organization_id,c.entity)=client_identity(r.organization_id,r.client_entity)),'')
 FROM current_client_roster r WHERE r.organization_id=$1 ORDER BY NOT r.active,lower(r.client_entity)`, org)
		if err != nil {
			problem(w, 503, "The client roster is unavailable.")
			return
		}
		clients, index := []rosterClient{}, map[string]int{}
		for rows.Next() {
			var c rosterClient
			var due sql.NullInt64
			var override sql.NullString
			var when time.Time
			if err = rows.Scan(&c.Entity, &c.Active, &c.Since, &due, &override, &c.Reason, &c.Actor, &when, &c.LastPeriod); err != nil {
				break
			}
			if due.Valid {
				days := int(due.Int64)
				c.DueDays = &days
			}
			if override.Valid {
				c.Owner = &override.String
			}
			c.At, c.Owed = when.Format(time.RFC3339), []string{}
			index[strings.ToLower(c.Entity)] = len(clients)
			clients = append(clients, c)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
		var owed []owedMonth
		if err == nil {
			owed, err = owedMonths(r.Context(), tx, time.Now(), org, true, canReview(r), subject(r), "")
		}
		if err != nil {
			problem(w, 503, "The client roster is unavailable.")
			return
		}
		for _, o := range owed {
			if i, ok := index[strings.ToLower(o.Entity)]; ok {
				clients[i].Owed = append(clients[i].Owed, o.Period)
			}
		}
		out := map[string]any{"template": template, "clients": clients, "can_manage": canManageRoster(r)}
		// An administrator chooses owners from the members who can do the work: a role reviewing every client (any
		// client, and the template), or a reviewer, for the clients assigned to them.
		if canManageRoster(r) {
			people, err := rosterPeople(r.Context(), tx, org, subject(r))
			if err != nil {
				out["people_unavailable"] = "Teammates are unavailable right now."
			} else {
				out["people"] = people
			}
		}
		jsonReply(w, 200, out)
	})
	// A firm administrator adds a client, changes its first owed month or its overrides, or marks it inactive.
	mux.HandleFunc("POST /v1/closes/roster", func(w http.ResponseWriter, r *http.Request) {
		if !canManageRoster(r) {
			problem(w, 403, "Changing the client roster requires clients:all and members:manage.")
			return
		}
		var in struct {
			Entity  string  `json:"entity"`
			Active  bool    `json:"active"`
			Since   string  `json:"since"`
			DueDays *int    `json:"due_days"`
			Owner   *string `json:"owner"`
			Reason  string  `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Entity, in.Reason = strings.TrimSpace(in.Entity), strings.TrimSpace(in.Reason)
		if in.Entity == "" || utf8.RuneCountInString(in.Entity) > 200 {
			problem(w, 422, "Enter the client's name (1–200 characters).")
			return
		}
		// Every owed month from the first is listed on each read, so the first is within the last five years.
		since, err := time.Parse("2006-01", in.Since)
		if now := time.Now().UTC(); err != nil || since.Before(time.Date(now.Year()-5, now.Month(), 1, 0, 0, 0, 0, time.UTC)) {
			problem(w, 422, "Enter the first month the client owes a close as YYYY-MM, within the last five years.")
			return
		}
		if in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Give a reason (1–500 characters).")
			return
		}
		if in.DueDays != nil && (*in.DueDays < 0 || *in.DueDays > 60) {
			problem(w, 422, "A close is due 1–60 days after its month ends, or 0 for no due date.")
			return
		}
		// The roster names each client once, by its own name: a confirmed alias of a client is that client.
		var client string
		if err := db.QueryRowContext(r.Context(), `SELECT client_identity($1,$2)`, organization(r), in.Entity).Scan(&client); err != nil {
			problem(w, 503, "The client roster could not be saved.")
			return
		}
		if client != strings.ToLower(in.Entity) {
			problem(w, 422, in.Entity+" is a confirmed alias of "+client+". Use the client's own name.")
			return
		}
		if in.Owner != nil && *in.Owner != "" {
			_, ok, err := rosterOwner(r.Context(), db, r, in.Entity, *in.Owner)
			if err != nil {
				problem(w, 502, "Teammates are unavailable right now.")
				return
			}
			if !ok {
				problem(w, 422, "Choose a member who can review "+in.Entity+"'s work as its owner.")
				return
			}
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The client roster could not be saved.")
			return
		}
		defer tx.Rollback()
		// The firm lock, which opening a close takes, orders this with any month being opened for the client.
		_, err = tx.ExecContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organization(r))
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO client_roster(organization_id,client_entity,active,since,due_days,owner,reason,actor) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				organization(r), in.Entity, in.Active, in.Since, in.DueDays, in.Owner, in.Reason, subject(r))
		}
		// A client that owes nothing more has no monthly hand-over to open its months.
		if err == nil && !in.Active {
			_, err = tx.ExecContext(r.Context(), `UPDATE close_routines SET stopped_at=now(),stopped_by=$3,stop_reason=$4 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND stopped_at IS NULL`,
				organization(r), in.Entity, subject(r), "Stopped when "+in.Entity+" left the firm's roster: "+in.Reason)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The client roster could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"entity": in.Entity, "active": in.Active, "since": in.Since})
	})
	// A firm administrator sets the monthly close template: when each month's close is due, and who holds it.
	mux.HandleFunc("POST /v1/closes/close-template", func(w http.ResponseWriter, r *http.Request) {
		if !canManageRoster(r) {
			problem(w, 403, "Changing the monthly close template requires clients:all and members:manage.")
			return
		}
		var in struct {
			DueDays *int   `json:"due_days"`
			Owner   string `json:"owner"`
			Reason  string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Reason = strings.TrimSpace(in.Reason); in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Give a reason (1–500 characters).")
			return
		}
		if in.DueDays != nil && (*in.DueDays < 1 || *in.DueDays > 60) {
			problem(w, 422, "A close is due 1–60 days after its month ends.")
			return
		}
		if in.Owner != "" {
			_, ok, err := rosterOwner(r.Context(), db, r, "", in.Owner)
			if err != nil {
				problem(w, 502, "Teammates are unavailable right now.")
				return
			}
			if !ok {
				problem(w, 422, "Choose a member who reviews every client's work as the default owner, or none.")
				return
			}
		}
		if _, err := db.ExecContext(r.Context(), `INSERT INTO close_templates(organization_id,due_days,owner,reason,actor) VALUES($1,$2,$3,$4,$5)`, organization(r), in.DueDays, in.Owner, in.Reason, subject(r)); err != nil {
			problem(w, 503, "The monthly close template could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"due_days": in.DueDays, "owner": in.Owner})
	})
}

type rosterPerson struct {
	Subject  string   `json:"subject"`
	Name     string   `json:"name"`
	FirmWide bool     `json:"firm_wide"`
	Clients  []string `json:"clients"`
}

// rosterPeople is the granted members who can hold closes, as rosterOwner judges them, with the clients assigned to
// each; in the local development firm, which need not record members, the reader is one of them.
func rosterPeople(ctx context.Context, tx *sql.Tx, org, who string) ([]rosterPerson, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.subject,COALESCE((SELECT jsonb_agg(g.client_entity ORDER BY lower(g.client_entity)) FROM current_client_access g WHERE g.organization_id=m.organization_id AND g.subject=m.subject AND g.granted),'[]'::jsonb)
 FROM current_organization_members m WHERE m.organization_id=$1 AND m.granted ORDER BY m.subject`, org)
	if err != nil {
		return nil, err
	}
	members := []rosterPerson{}
	for rows.Next() {
		var p rosterPerson
		var clients []byte
		if err = rows.Scan(&p.Subject, &clients); err != nil {
			rows.Close()
			return nil, err
		}
		json.Unmarshal(clients, &p.Clients)
		p.Name = p.Subject
		members = append(members, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if org == "local-development" {
		if who != "" && !slices.ContainsFunc(members, func(p rosterPerson) bool { return p.Subject == who }) {
			members = append(members, rosterPerson{Subject: who, Name: who, Clients: []string{}})
		}
		for i := range members {
			members[i].FirmWide = true
		}
		return members, nil
	}
	reviewers, err := firmReviewers(ctx, org)
	if err != nil {
		return nil, err
	}
	out := []rosterPerson{}
	for _, p := range members {
		if rv := reviewers[p.Subject]; rv.review {
			p.Name, p.FirmWide = cmp.Or(rv.name, p.Subject), rv.firmWide
			out = append(out, p)
		}
	}
	return out, nil
}

// canReview is a reader who reviews work: with workpapers:review, or anyone without an organization (local development).
func canReview(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && (a.Organization == "" || slices.Contains(a.Permissions, "workpapers:review"))
}

// canManageRoster is a firm administrator (canManageMembers), or anyone without an organization (local development),
// who changes the roster and the monthly close template.
func canManageRoster(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return canManageMembers(r) || a != nil && a.Organization == ""
}
