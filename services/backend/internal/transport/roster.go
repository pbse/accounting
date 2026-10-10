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

// The firm's client roster and monthly close template (B01/B02, migration 127). Roster entries are matched to closes by
// client identity (rosterMatch), so a confirmed alias is the client; client assignments are matched by name, as access to
// a client's closes is (clientAccess, closeAccess), so the roster never shows or gives anyone work they cannot open. Every active client on the roster owes
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

// rosterLeft is SQL, true when the client named name has left the firm's roster: its own entry, matched by client
// identity, is inactive; or, with no entry of its own, an inactive entry has its name once case and punctuation are set
// aside, as a departed client's forgotten alias is kept (client_aliases holds only that form of the name).
func rosterLeft(org, name string) string {
	plain := func(s string) string { return `trim(regexp_replace(lower(` + s + `),'[^[:alnum:]]+',' ','g'))` }
	return `COALESCE((SELECT NOT r.active FROM current_client_roster r WHERE ` + rosterMatch(org, name) + ` LIMIT 1),
 EXISTS(SELECT 1 FROM current_client_roster r WHERE r.organization_id=` + org + ` AND NOT r.active AND ` + plain("r.client_entity") + `=` + plain(name) + `))`
}

// templateRoles reads a firm's WorkOS roles for applyCloseTemplate at most once, when first wanted: the local
// development firm has none, and roles that cannot be read are none, so no owner is given. A request warms it before
// taking the firm lock when the firm has named an owner anywhere (warmTemplateRoles); a month opened in the moment an
// owner is first named reads them under the lock instead, and a firm naming no owner never reads them.
func templateRoles(org string) func(context.Context) map[string]reviewer {
	var roles map[string]reviewer
	read := org == "local-development"
	return func(ctx context.Context) map[string]reviewer {
		if !read {
			roles, _ = firmReviewers(ctx, org)
			read = true
		}
		return roles
	}
}

// warmTemplateRoles reads the firm's roles before any lock is taken when its template or a roster entry names an owner.
func warmTemplateRoles(ctx context.Context, db *sql.DB, org string, roles func(context.Context) map[string]reviewer) {
	var named bool
	if org == "local-development" || db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM close_templates WHERE organization_id=$1 AND owner<>'' AND id=(SELECT max(id) FROM close_templates WHERE organization_id=$1))
 OR EXISTS(SELECT 1 FROM client_roster WHERE organization_id=$1 AND owner<>'')`, org).Scan(&named) != nil || !named {
		return
	}
	roles(ctx)
}

// rosterTemplate is SQL, over a current_client_roster row r, for the firm's current template t.
const rosterTemplate = `LEFT JOIN LATERAL (SELECT due_days,owner FROM close_templates WHERE organization_id=r.organization_id ORDER BY id DESC LIMIT 1) t ON true`

// applyCloseTemplate gives a newly opened close of an active roster client its month's due date (due_days after the
// month ends) and owner, from the client's overrides or else the firm's template, recording the owner as a change of
// hands. The owner must still be able to do the work, judged as mayAct judges it from the firm's WorkOS roles, read
// only when there is an owner to judge (nil for the local development firm, which has none): a hosted firm whose roles
// could not be read gives the close no owner.
// A close already dated or held keeps what it has, and a close of a client not on the roster is left alone.
func applyCloseTemplate(ctx context.Context, tx *sql.Tx, closeID string, firmRoles func(context.Context) map[string]reviewer) error {
	var org, period, owner string
	var dueDays sql.NullInt64
	var member, assigned bool
	err := tx.QueryRowContext(ctx, `SELECT c.organization_id,c.period,COALESCE(r.due_days,t.due_days),COALESCE(r.owner,t.owner,''),
 EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=c.organization_id AND m.subject=COALESCE(r.owner,t.owner,'') AND m.granted),
 EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=COALESCE(r.owner,t.owner,'') AND lower(g.client_entity)=lower(c.entity) AND g.granted)
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
	if owner == "" {
		return nil
	}
	if roles := firmRoles(ctx); (roles == nil && org != "local-development") || !mayAct(roles, owner, member, false, assigned) {
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
// those assigned to who) owe and have no close for: each ended month from the client's first owed month (a month whose
// close was deleted under any of the client's names, as its deletion receipt records, was done), matching
// client (a name fragment or YYYY-MM, or "") as Work's filter does. A month the client's monthly hand-over is set to
// open, and not stuck waiting, is marked as the hand-over's.
func owedMonths(ctx context.Context, tx *sql.Tx, now time.Time, org string, firm, review bool, who, client string) ([]owedMonth, error) {
	return owedMonthsThrough(ctx, tx, endedMonth(now), org, firm, review, who, client)
}

// owedMonthsThrough is owedMonths up to and including the month through (YYYY-MM): the roster read uses the current
// month, so a client's month that is still running, its first among them, opens with one tap where it is listed.
func owedMonthsThrough(ctx context.Context, tx *sql.Tx, through, org string, firm, review bool, who, client string) ([]owedMonth, error) {
	// Months are matched by client identity, so a month opened under a confirmed alias of the client counts. A deleted
	// month is matched through its receipt, which holds digests, never names: of the client the close belonged to
	// (client_digest), which may since have been confirmed as an alias of this one, and the same with case and
	// punctuation set aside (plain_digest), matched only through a confirmed alias, which is kept in that form, so two
	// clients whose names differ only in punctuation stay apart; or, on a receipt from before the
	// roster, of the close's own name. Both are compared with every name the firm knows the client by (its closes', its
	// confirmed aliases' and its roster entries').
	rows, err := tx.QueryContext(ctx, `WITH opened AS (SELECT DISTINCT client_identity(c.organization_id,c.entity) AS client,c.period FROM client_closes c WHERE c.organization_id=$1),
 names AS (SELECT client_identity($1,n.name) AS client,n.name,n.alias FROM (SELECT lower(c.entity) AS name,false AS alias FROM client_closes c WHERE c.organization_id=$1
  UNION SELECT a.alias,true FROM client_aliases a WHERE a.organization_id=$1 AND a.retired_at IS NULL
  UNION SELECT lower(r.client_entity),false FROM client_roster r WHERE r.organization_id=$1) n),
 done AS (SELECT n.client,d.period FROM close_deletion_receipts d JOIN names n ON d.client_digest IN (encode(sha256(convert_to($1,'UTF8')||'\x00'::bytea||convert_to(n.client,'UTF8')||'\x00'::bytea||convert_to(d.period,'UTF8')),'hex'),encode(sha256(convert_to($1,'UTF8')||'\x00'::bytea||convert_to(n.name,'UTF8')||'\x00'::bytea||convert_to(d.period,'UTF8')),'hex'))
   OR (n.alias AND d.plain_digest=encode(sha256(convert_to($1,'UTF8')||'\x00'::bytea||convert_to(n.name,'UTF8')||'\x00'::bytea||convert_to(d.period,'UTF8')),'hex'))
   OR (d.client_digest IS NULL AND d.entity_digest=encode(sha256(convert_to($1,'UTF8')||'\x00'::bytea||convert_to(n.name,'UTF8')||'\x00'::bytea||convert_to(d.period,'UTF8')),'hex'))
  WHERE d.organization_id=$1 AND d.reason_code<>'demonstration_reset' AND d.period>=(SELECT min(since) FROM current_client_roster WHERE organization_id=$1 AND active))
 SELECT r.client_entity,m.period,COALESCE(r.due_days,t.due_days),COALESCE(r.owner,t.owner,''),
  EXISTS(SELECT 1 FROM close_routines h WHERE h.organization_id=r.organization_id AND client_identity(h.organization_id,h.client_entity)=client_identity(r.organization_id,r.client_entity) AND h.stopped_at IS NULL AND h.waiting='' AND h.last_period<m.period)
 FROM current_client_roster r `+rosterTemplate+`
 CROSS JOIN LATERAL (SELECT to_char(g,'YYYY-MM') AS period FROM generate_series(to_date(r.since,'YYYY-MM'),to_date($4,'YYYY-MM'),interval '1 month') g) m
 WHERE r.organization_id=$1 AND r.active
  AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=r.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(r.client_entity) AND g.granted))
  AND NOT EXISTS(SELECT 1 FROM opened o WHERE o.client=client_identity(r.organization_id,r.client_entity) AND o.period=m.period)
  AND NOT EXISTS(SELECT 1 FROM done x WHERE x.client=client_identity(r.organization_id,r.client_entity) AND x.period=m.period)
  AND ($5='' OR r.client_entity ILIKE '%'||$5||'%' OR m.period=$5)
 ORDER BY m.period DESC,lower(r.client_entity)`, org, firm, who, through, client)
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

// rosterReviewers is the firm's WorkOS roles for rosterOwner, read before any lock is taken; the local development
// firm has none.
func rosterReviewers(ctx context.Context, org string) (map[string]reviewer, error) {
	if org == "local-development" {
		return nil, nil
	}
	return firmReviewers(ctx, org)
}

// rosterOwner says whether subject can hold a client's closes, and their name: a granted member who reviews entity's
// work, through a role that reviews every client or an assignment to entity. The firm template applies to every client
// (entity ""), so only a role reviewing every client will do. Only the local development firm, which has no WorkOS,
// takes any member. q is the transaction holding the firm lock, which membership and assignment changes take too.
func rosterOwner(ctx context.Context, q querier, org string, reviewers map[string]reviewer, entity, subject string) (string, bool, error) {
	if org == "local-development" {
		return subject, true, nil
	}
	var member, assigned bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted),
 EXISTS(SELECT 1 FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($3) AND granted)`, org, subject, entity).Scan(&member, &assigned)
	if err != nil || !member {
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
	Rev        string   `json:"rev"` // the entry's row, new with every change
	LastPeriod string   `json:"last_period"`
	Owed       []string `json:"owed"`
	// Closes is every month opened under any of the client's names, newest first, with whether its sign-off stands.
	Closes []rosterClose `json:"closes"`
}

type rosterClose struct {
	ID        string `json:"id"`
	Entity    string `json:"entity"`
	Period    string `json:"period"`
	SignedOff bool   `json:"signed_off"`
	// Rechecked says SignedOff was confirmed against the close as it is now; an older close's sign-off is shown as
	// recorded, not as standing, since rechecking every one on each read reads the whole close.
	Rechecked bool `json:"rechecked"`
	// Unchecked says the recheck itself failed: the sign-off is neither standing nor known to have changed.
	Unchecked bool `json:"unchecked,omitempty"`
	// The close's planning, as the closes list gives it: due date, priority, whether it is held and by the reader.
	DueOn     string `json:"due_on,omitempty"`
	Priority  string `json:"priority,omitempty"`
	Assigned  bool   `json:"assigned,omitempty"`
	OwnedByMe bool   `json:"owned_by_me,omitempty"`
	// Owner is who holds the close now (a subject), "" for nobody; a firm administrator sees whom it names.
	Owner string `json:"owner,omitempty"`
}

func registerRoster(mux *http.ServeMux, db *sql.DB) {
	// The whole roster is the firm's to read; a member assigned to clients reads those clients' entries alone (by the
	// same exact assignment the work endpoints require), with no template and nothing to manage.
	mux.HandleFunc("GET /v1/closes/roster", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "The client roster is unavailable.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		var template map[string]any
		var dueDays sql.NullInt64
		var owner, reason, actor, rev string
		var at time.Time
		switch err = tx.QueryRowContext(r.Context(), `SELECT due_days,owner,reason,actor,created_at,id::text FROM close_templates WHERE organization_id=$1 AND $2 ORDER BY id DESC LIMIT 1`, org, firmAccess(r)).Scan(&dueDays, &owner, &reason, &actor, &at, &rev); err {
		case nil:
			template = map[string]any{"due_days": nil, "owner": owner, "reason": reason, "actor": actor, "at": at, "rev": rev}
			if dueDays.Valid {
				template["due_days"] = dueDays.Int64
			}
		case sql.ErrNoRows:
		default:
			problem(w, 503, "The client roster is unavailable.")
			return
		}
		rows, err := tx.QueryContext(r.Context(), `SELECT r.client_entity,r.active,r.since,r.due_days,r.owner,r.reason,r.actor,r.created_at,r.id::text,
 COALESCE((SELECT max(c.period) FROM client_closes c WHERE c.organization_id=r.organization_id AND client_identity(c.organization_id,c.entity)=client_identity(r.organization_id,r.client_entity)
  AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND g.granted AND lower(g.client_entity)=lower(c.entity)))),'')
 FROM current_client_roster r WHERE r.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=r.organization_id AND g.subject=$3 AND g.granted AND lower(g.client_entity)=lower(r.client_entity)))
 ORDER BY NOT r.active,lower(r.client_entity)`, org, firmAccess(r), subject(r))
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
			if err = rows.Scan(&c.Entity, &c.Active, &c.Since, &due, &override, &c.Reason, &c.Actor, &when, &c.Rev, &c.LastPeriod); err != nil {
				break
			}
			if due.Valid {
				days := int(due.Int64)
				c.DueDays = &days
			}
			if override.Valid {
				c.Owner = &override.String
			}
			c.At, c.Owed, c.Closes = when.Format(time.RFC3339), []string{}, []rosterClose{}
			index[strings.ToLower(c.Entity)] = len(clients)
			clients = append(clients, c)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
		// Every close of each listed client, under any of its names, so the page needs no capped list for its history;
		// a member sees only the closes they can open (the close endpoints check the close's own name).
		if err == nil {
			var closes *sql.Rows
			closes, err = tx.QueryContext(r.Context(), `SELECT r.client_entity,c.id::text,c.entity,c.period,COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')='signed_off',
 COALESCE(c.due_on::text,''),c.priority,c.owner<>'',c.owner<>'' AND c.owner=$3,c.owner
 FROM current_client_roster r JOIN client_closes c ON c.organization_id=r.organization_id AND client_identity(c.organization_id,c.entity)=client_identity(r.organization_id,r.client_entity)
 WHERE r.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=r.organization_id AND g.subject=$3 AND g.granted AND lower(g.client_entity)=lower(r.client_entity)))
 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND g.granted AND lower(g.client_entity)=lower(c.entity)))
 ORDER BY c.period DESC,c.id DESC`, org, firmAccess(r), subject(r))
			if err == nil {
				for closes.Next() {
					var entity string
					var cl rosterClose
					if err = closes.Scan(&entity, &cl.ID, &cl.Entity, &cl.Period, &cl.SignedOff, &cl.DueOn, &cl.Priority, &cl.Assigned, &cl.OwnedByMe, &cl.Owner); err != nil {
						break
					}
					if i, ok := index[strings.ToLower(entity)]; ok {
						clients[i].Closes = append(clients[i].Closes, cl)
					}
				}
				if err == nil {
					err = closes.Err()
				}
				closes.Close()
			}
		}
		// A recorded sign-off counts only while it still stands. The recheck reads the whole close, so on this list it is
		// made for the closes of the last three months (at most three a client), the ones still in play; an older one is
		// shown as recorded, and Home, which rechecks every owed close, still counts it once it changes.
		since := shiftMonth(time.Now().UTC().Format("2006-01"), -2)
		for i := range clients {
			for j := range clients[i].Closes {
				if err != nil || !clients[i].Closes[j].SignedOff || clients[i].Closes[j].Period < since {
					continue
				}
				var state string
				if state, err = recheckSignOff(r.Context(), tx, clients[i].Closes[j].ID); err == nil {
					clients[i].Closes[j].SignedOff, clients[i].Closes[j].Rechecked, clients[i].Closes[j].Unchecked = state == "signed_off", true, state == "unknown"
				}
			}
		}
		// How each client's books arrive, by client identity (its own name in lower case, as the Clients page groups
		// it): the state of its QuickBooks connection (production first) and of its Xero connection, under any of its
		// names, for every client the reader can see whole (the firm's, or one assigned to them under its own name, as
		// the client's page shows them), on the roster or not; and, as "exports", whether the newest label of its
		// uploaded exports, in place of a live ledger, is still on the current exports (labeled) or one has been
		// replaced since (stale).
		connections := map[string]map[string]string{}
		if err == nil {
			var rows *sql.Rows
			if rows, err = tx.QueryContext(r.Context(), `SELECT DISTINCT ON (provider,client_identity($1,client_entity)) provider,client_identity($1,client_entity),state FROM (
 SELECT 'quickbooks' AS provider,client_entity,state,environment='production' AS first,id FROM quickbooks_connections WHERE organization_id=$1 AND state<>'disconnected'
 UNION ALL SELECT 'xero',client_entity,state,true,id FROM xero_connections WHERE organization_id=$1 AND state<>'disconnected'
 UNION ALL SELECT 'exports',c.entity,CASE WHEN EXISTS(SELECT 1 FROM unnest(l.evidence_ids) e(id) WHERE NOT EXISTS(SELECT 1 FROM evidence v WHERE v.id=e.id AND v.close_id=l.close_id AND v.superseded_by IS NULL)) THEN 'stale' ELSE 'labeled' END,true,l.id
  FROM close_export_labels l JOIN client_closes c ON c.id=l.close_id WHERE c.organization_id=$1 AND l.id=(SELECT max(n.id) FROM close_export_labels n WHERE n.close_id=l.close_id)) x
 WHERE $2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=$1 AND g.subject=$3 AND g.granted AND lower(g.client_entity)=client_identity($1,x.client_entity))
 ORDER BY provider,client_identity($1,client_entity),first DESC,id DESC`, org, firmAccess(r), subject(r)); err == nil {
				for rows.Next() {
					var provider, client, state string
					if err = rows.Scan(&provider, &client, &state); err != nil {
						break
					}
					if connections[client] == nil {
						connections[client] = map[string]string{}
					}
					connections[client][provider] = state
				}
				rows.Close()
				if err == nil {
					err = rows.Err()
				}
			}
		}
		var owed []owedMonth
		if err == nil {
			owed, err = owedMonthsThrough(r.Context(), tx, time.Now().UTC().Format("2006-01"), org, firmAccess(r), canReview(r), subject(r), "")
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
		// A member sees no template, which is not the same as the firm having none: template_hidden says so.
		out := map[string]any{"template": template, "template_hidden": !firmAccess(r), "clients": clients, "connections": connections, "can_manage": canManageRoster(r)}
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
			Rev     string  `json:"rev"` // the client's entry as read, "" for none
		}
		if !decode(w, r, &in) {
			return
		}
		in.Entity, in.Reason = strings.TrimSpace(in.Entity), strings.TrimSpace(in.Reason)
		if in.Entity == "" || utf8.RuneCountInString(in.Entity) > 200 {
			problem(w, 422, "Enter the client's name (1–200 characters).")
			return
		}
		since, err := time.Parse("2006-01", in.Since)
		if err != nil {
			problem(w, 422, "Enter the first month the client owes a close as YYYY-MM, within the last five years.")
			return
		}
		// Adding a client needs no reason beyond the adding; a change to its entry does.
		if in.Rev == "" && in.Reason == "" {
			in.Reason = "Added as a client"
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
		// Every owed month from the first is listed on each read, so a new or changed first month is within the last five
		// years; an entry keeping the first month it has can still be changed, or the client marked inactive.
		var kept string
		if err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT since FROM current_client_roster r WHERE `+rosterMatch("$1", "$2")+`),'')`, organization(r), in.Entity).Scan(&kept); err != nil {
			problem(w, 503, "The client roster could not be saved.")
			return
		}
		if now := time.Now().UTC(); in.Since != kept && since.Before(time.Date(now.Year()-5, now.Month(), 1, 0, 0, 0, 0, time.UTC)) {
			problem(w, 422, "Enter the first month the client owes a close as YYYY-MM, within the last five years.")
			return
		}
		var reviewers map[string]reviewer
		if in.Owner != nil && *in.Owner != "" {
			if reviewers, err = rosterReviewers(r.Context(), organization(r)); err != nil {
				problem(w, 502, "Teammates are unavailable right now.")
				return
			}
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The client roster could not be saved.")
			return
		}
		defer tx.Rollback()
		// The firm lock, which opening a close and changing members or assignments take, orders this with them. Under it
		// the entry must still be the one the administrator read, and the owner still able to do the work.
		var current string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organization(r)).Scan(new(string)); err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT r.id::text FROM current_client_roster r WHERE `+rosterMatch("$1", "$2")+`),'')`, organization(r), in.Entity).Scan(&current)
		}
		if err != nil {
			problem(w, 503, "The client roster could not be saved.")
			return
		}
		if current != in.Rev {
			if in.Rev == "" {
				problem(w, 409, in.Entity+" is already on the roster. Change its entry instead.")
			} else {
				problem(w, 409, in.Entity+"'s entry changed since you looked. Review it again.")
			}
			return
		}
		if in.Owner != nil && *in.Owner != "" {
			_, ok, err := rosterOwner(r.Context(), tx, organization(r), reviewers, in.Entity, *in.Owner)
			if err != nil {
				problem(w, 503, "The client roster could not be saved.")
				return
			}
			if !ok {
				problem(w, 422, "Choose a member who can review "+in.Entity+"'s work as its owner.")
				return
			}
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO client_roster(organization_id,client_entity,active,since,due_days,owner,reason,actor) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			organization(r), in.Entity, in.Active, in.Since, in.DueDays, in.Owner, in.Reason, subject(r))
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
			Rev     string `json:"rev"` // the template as read, "" for none
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
		var reviewers map[string]reviewer
		var err error
		if in.Owner != "" {
			if reviewers, err = rosterReviewers(r.Context(), organization(r)); err != nil {
				problem(w, 502, "Teammates are unavailable right now.")
				return
			}
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The monthly close template could not be saved.")
			return
		}
		defer tx.Rollback()
		// Under the firm lock the template must still be the one the administrator read, and the owner still able to
		// review every client's work.
		var current string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organization(r)).Scan(new(string)); err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM close_templates WHERE organization_id=$1 ORDER BY id DESC LIMIT 1),'')`, organization(r)).Scan(&current)
		}
		if err != nil {
			problem(w, 503, "The monthly close template could not be saved.")
			return
		}
		if current != in.Rev {
			problem(w, 409, "The monthly template changed since you looked. Review it again.")
			return
		}
		if in.Owner != "" {
			_, ok, err := rosterOwner(r.Context(), tx, organization(r), reviewers, "", in.Owner)
			if err != nil {
				problem(w, 503, "The monthly close template could not be saved.")
				return
			}
			if !ok {
				problem(w, 422, "Choose a member who reviews every client's work as the default owner, or none.")
				return
			}
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO close_templates(organization_id,due_days,owner,reason,actor) VALUES($1,$2,$3,$4,$5)`, organization(r), in.DueDays, in.Owner, in.Reason, subject(r)); err != nil || tx.Commit() != nil {
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
	members, err := grantedMembers(ctx, tx, org)
	if err != nil {
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
	reviewers, err := shownReviewers(ctx, org)
	if err != nil {
		return nil, err
	}
	return reviewersAmong(members, reviewers), nil
}

// grantedMembers is the firm's granted members, each with the clients assigned to them.
func grantedMembers(ctx context.Context, tx *sql.Tx, org string) ([]rosterPerson, error) {
	// Each assigned client is named as its assignment names it (lowercase), as access to a client's closes is granted.
	rows, err := tx.QueryContext(ctx, `SELECT m.subject,COALESCE((SELECT jsonb_agg(DISTINCT lower(g.client_entity)) FROM current_client_access g WHERE g.organization_id=m.organization_id AND g.subject=m.subject AND g.granted),'[]'::jsonb)
 FROM current_organization_members m WHERE m.organization_id=$1 AND m.granted ORDER BY m.subject`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []rosterPerson{}
	for rows.Next() {
		var p rosterPerson
		var clients []byte
		if err = rows.Scan(&p.Subject, &clients); err != nil {
			return nil, err
		}
		json.Unmarshal(clients, &p.Clients)
		p.Name = p.Subject
		members = append(members, p)
	}
	return members, rows.Err()
}

// reviewersAmong is the members whose WorkOS role reviews work, named as WorkOS names them.
func reviewersAmong(members []rosterPerson, reviewers map[string]reviewer) []rosterPerson {
	out := []rosterPerson{}
	for _, p := range members {
		if rv := reviewers[p.Subject]; rv.review {
			p.Name, p.FirmWide = cmp.Or(rv.name, p.Subject), rv.firmWide
			out = append(out, p)
		}
	}
	return out
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
