package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

// A client's page (docs/books-sources.md, Client page): one read of everything the firm keeps about a client, apart
// from any month, at /v1/closes/client?name=… (a path segment would collide with a close's own routes). The client is
// named as the Clients page names it, by its own name or any confirmed alias
// (client_identity); its months are every close opened under any of its names. A member sees a client assigned to them
// under any of those names, and only the months they could open; anyone else is told the client is not found, as a
// close they cannot open is.

type clientMonth struct {
	ID        string `json:"id"`
	Entity    string `json:"entity"`
	Period    string `json:"period"`
	SignedOff bool   `json:"signed_off"`
	Rechecked bool   `json:"rechecked"`
	Unchecked bool   `json:"unchecked,omitempty"`
	DueOn     string `json:"due_on,omitempty"`
	Priority  string `json:"priority,omitempty"`
	Assigned  bool   `json:"assigned,omitempty"`
	OwnedByMe bool   `json:"owned_by_me,omitempty"`
	// Books is where the month's books come from now: its newest month-end capture, as the month-end readers take it
	// (periodBooks), or the person's label on uploaded exports; nil when the month has neither.
	Books *monthBooks `json:"books,omitempty"`
}

type monthBooks struct {
	// Source is quickbooks or xero for a capture, exports for labeled exports.
	Source string    `json:"source"`
	At     time.Time `json:"at"`
	// Stale says the capture's evidence was replaced or its connection is no longer active, or a labeled export was
	// replaced since it was labeled.
	Stale bool `json:"stale,omitempty"`
}

type owedClientMonth struct {
	Period string `json:"period"`
	DueOn  string `json:"due_on,omitempty"`
}

func registerClients(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/client", func(w http.ResponseWriter, r *http.Request) {
		asked := strings.TrimSpace(r.URL.Query().Get("name"))
		if asked == "" || len(asked) > 200 {
			problem(w, 404, "Client not found.")
			return
		}
		ctx := r.Context()
		org, firm, who := organization(r), firmAccess(r), subject(r)
		// The firm's roles are read before the snapshot is taken, so a slow provider holds no transaction open; only an
		// administrator, who chooses owners, reads them.
		var reviewers map[string]reviewer
		var peopleErr error
		if canManageRoster(r) && org != "local-development" {
			reviewers, peopleErr = shownReviewers(ctx, org)
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		defer tx.Rollback()
		// The client's own name: its roster entry's, else the name its closes were opened with that is its own (or, when
		// none is, its newest close's). A member's access is to a name, as access to a close is: the client is theirs to
		// open when any of its names is, showing the months they could open. What belongs to the client as a whole (its
		// roster entry, the months it owes, its connections, currency and basis) is shown under its own name only, as the
		// roster and a close's connection read show it.
		var name string
		var known, allowed, whole bool
		err = tx.QueryRowContext(ctx, `WITH client AS (SELECT client_identity($1,$2) AS id),
 names AS (SELECT r.client_entity AS name,0 AS rank,'' AS period FROM current_client_roster r,client WHERE r.organization_id=$1 AND client_identity(r.organization_id,r.client_entity)=client.id
  UNION ALL SELECT c.entity,CASE WHEN lower(c.entity)=client.id THEN 1 ELSE 2 END,c.period FROM client_closes c,client WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client.id),
 own AS (SELECT name FROM names ORDER BY rank,period DESC LIMIT 1),
 granted AS (SELECT lower(g.client_entity) AS name FROM current_client_access g WHERE g.organization_id=$1 AND g.subject=$4 AND g.granted)
 SELECT COALESCE((SELECT name FROM own),''),EXISTS(SELECT 1 FROM names),
  $3 OR EXISTS(SELECT 1 FROM granted WHERE name IN (SELECT lower(name) FROM names)),
  $3 OR EXISTS(SELECT 1 FROM granted WHERE name=(SELECT lower(name) FROM own))`,
			org, asked, firm, who).Scan(&name, &known, &allowed, &whole)
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		if !known || !allowed {
			problem(w, 404, "Client not found.")
			return
		}
		out := map[string]any{"entity": name, "whole": whole, "can_manage": canManageRoster(r), "can_connect": canConnect(r)}

		// Every month opened under any of the client's names that the reader could open, newest first, each with where
		// its books come from now: the newer of its QuickBooks period capture (from the connection its books are read
		// from) and its Xero capture, as periodBooks chooses.
		rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.entity,c.period,COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')='signed_off',
 COALESCE(c.due_on::text,''),c.priority,c.owner<>'',c.owner<>'' AND c.owner=$4,b.source,b.captured_at,b.stale
 FROM client_closes c LEFT JOIN LATERAL (SELECT * FROM (
   SELECT 'quickbooks' AS source,s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active' AS stale FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id
    LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
    WHERE s.close_id=c.id AND s.payload ? 'period' AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+`
   UNION ALL SELECT 'xero',s.captured_at,x.state<>'active' FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id WHERE s.close_id=c.id) v
  ORDER BY captured_at DESC,source='quickbooks' DESC LIMIT 1) b ON true
 WHERE `+sameClient+` AND ($3 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$4 AND g.granted AND lower(g.client_entity)=lower(c.entity)))
 ORDER BY c.period DESC,c.id DESC`, org, name, firm, who)
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		months := []clientMonth{}
		for rows.Next() {
			var m clientMonth
			var source sql.NullString
			var captured sql.NullTime
			var stale sql.NullBool
			if err = rows.Scan(&m.ID, &m.Entity, &m.Period, &m.SignedOff, &m.DueOn, &m.Priority, &m.Assigned, &m.OwnedByMe, &source, &captured, &stale); err != nil {
				break
			}
			if source.Valid {
				m.Books = &monthBooks{Source: source.String, At: captured.Time, Stale: stale.Bool}
			}
			months = append(months, m)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
		// A month without a capture may have its uploaded exports labeled in place of a live ledger.
		if err == nil {
			var bare []string
			for _, m := range months {
				if m.Books == nil {
					bare = append(bare, m.ID)
				}
			}
			var labels map[string]*exportLabel
			if labels, err = latestExportLabels(ctx, tx, bare); err == nil {
				for i := range months {
					if l := labels[months[i].ID]; l != nil {
						months[i].Books = &monthBooks{Source: "exports", At: l.CreatedAt, Stale: !l.Current}
					}
				}
			}
		}
		// A recorded sign-off counts while it still stands, rechecked for the last three months as the roster does.
		since := shiftMonth(time.Now().UTC().Format("2006-01"), -2)
		for i := range months {
			if err != nil || !months[i].SignedOff || months[i].Period < since {
				continue
			}
			var state string
			if state, err = recheckSignOff(ctx, tx, months[i].ID); err == nil {
				months[i].SignedOff, months[i].Rechecked, months[i].Unchecked = state == "signed_off", true, state == "unknown"
			}
		}
		// The months the client owes that nobody opened, through the month now running, as the roster lists them.
		owed := []owedClientMonth{}
		if err == nil && whole {
			var all []owedMonth
			if all, err = owedMonthsThrough(ctx, tx, time.Now().UTC().Format("2006-01"), org, firm, canReview(r), who, name); err == nil {
				for _, o := range all {
					if strings.EqualFold(o.Entity, name) {
						owed = append(owed, owedClientMonth{Period: o.Period, DueOn: o.DueOn})
					}
				}
			}
		}
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["months"], out["owed"] = months, owed
		// A member who can open only some of the client's months, under another of its names, sees those alone.
		if !whole {
			out["roster"], out["template"], out["template_hidden"], out["quickbooks"], out["xero"], out["profile"], out["basis"] = nil, nil, true, []any{}, nil, nil, nil
			jsonReply(w, 200, out)
			return
		}

		// The roster entry, as Settings → Clients shows it; a client with closes but no entry has none.
		var entry *rosterClient
		var c rosterClient
		var due sql.NullInt64
		var owner sql.NullString
		var at time.Time
		switch err = tx.QueryRowContext(ctx, `SELECT r.client_entity,r.active,r.since,r.due_days,r.owner,r.reason,r.actor,r.created_at,r.id::text FROM current_client_roster r WHERE `+rosterMatch("$1", "$2")+` LIMIT 1`, org, name).Scan(&c.Entity, &c.Active, &c.Since, &due, &owner, &c.Reason, &c.Actor, &at, &c.Rev); err {
		case nil:
			if due.Valid {
				days := int(due.Int64)
				c.DueDays = &days
			}
			if owner.Valid {
				c.Owner = &owner.String
			}
			c.At = at.Format(time.RFC3339)
			entry = &c
		case sql.ErrNoRows:
		default:
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["roster"] = entry
		// The firm's monthly template, which the entry's empty overrides follow; a member sees none, as on the roster.
		var template map[string]any
		var templateDue sql.NullInt64
		var templateOwner, templateRev string
		switch err = tx.QueryRowContext(ctx, `SELECT due_days,owner,id::text FROM close_templates WHERE organization_id=$1 AND $2 ORDER BY id DESC LIMIT 1`, org, firm).Scan(&templateDue, &templateOwner, &templateRev); err {
		case nil:
			template = map[string]any{"due_days": nil, "owner": templateOwner, "rev": templateRev}
			if templateDue.Valid {
				template["due_days"] = templateDue.Int64
			}
		case sql.ErrNoRows:
		default:
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["template"], out["template_hidden"] = template, !firm

		// How the books arrive: the client's QuickBooks connections (production first, the one its books are read from)
		// and its Xero connection, matched by the client's own name as connections are kept.
		var quickbooksRaw, xeroRaw []byte
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object('id',x.id::text,'environment',x.environment,'state',x.state,'realm',x.realm,'company_name',x.company_name,'error',x.error,'confirmed_at',x.confirmed_at,'updated_at',x.updated_at) ORDER BY x.environment='production' DESC,x.id DESC)
  FROM quickbooks_connections x WHERE x.organization_id=$1 AND lower(x.client_entity)=lower($2) AND x.state<>'disconnected'),'[]'::jsonb),
 (SELECT jsonb_build_object('id',x.id::text,'state',x.state,'tenant_name',x.tenant_name,'error',x.error,'confirmed_at',x.confirmed_at,'updated_at',x.updated_at)
  FROM xero_connections x WHERE x.organization_id=$1 AND lower(x.client_entity)=lower($2) AND x.state<>'disconnected' ORDER BY x.id DESC LIMIT 1)`, org, name).Scan(&quickbooksRaw, &xeroRaw); err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["quickbooks"] = json.RawMessage(quickbooksRaw)
		if xeroRaw != nil {
			out["xero"] = json.RawMessage(xeroRaw)
		} else {
			out["xero"] = nil
		}

		// The client's currency and fiscal year as its connected ledgers report them, and its approved basis (a client
		// override first, then the firm's), as a close reads both.
		profile, err := preparation.ClientProfileOf(ctx, tx, org, name)
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["profile"] = profile
		var basis, scope string
		switch err = tx.QueryRowContext(ctx, `SELECT p.method,CASE WHEN p.client_entity<>'' THEN 'client' ELSE 'firm' END FROM procedure_versions p WHERE p.organization_id=$1 AND p.kind='basis' AND (p.client_entity='' OR lower(p.client_entity)=lower($2)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1`, org, name).Scan(&basis, &scope); err {
		case nil:
			out["basis"] = map[string]string{"method": basis, "scope": scope}
		case sql.ErrNoRows:
			out["basis"] = nil
		default:
			problem(w, 503, "The client is unavailable.")
			return
		}

		// Who can do the client's work, for an administrator, who assigns it: the reviewers holding every client, and
		// those assigned to this one, as the roster offers owners.
		if canManageRoster(r) {
			if peopleErr != nil {
				out["people_unavailable"] = "Teammates are unavailable right now."
			} else if people, e := clientPeople(ctx, tx, org, who, reviewers, name); e != nil {
				out["people_unavailable"] = "Teammates are unavailable right now."
			} else {
				out["people"] = people
			}
		}
		jsonReply(w, 200, out)
	})
}

// clientPeople is rosterPeople's members who can do entity's work, judged with reviewers read before the snapshot: a
// role reviewing every client, or a reviewer assigned to entity. Each keeps every client they are assigned to, so the
// schedule's owner choice reads as the roster's does.
func clientPeople(ctx context.Context, tx *sql.Tx, org, who string, reviewers map[string]reviewer, entity string) ([]rosterPerson, error) {
	var people []rosterPerson
	var err error
	if org == "local-development" {
		people, err = rosterPeople(ctx, tx, org, who)
	} else {
		var members []rosterPerson
		if members, err = grantedMembers(ctx, tx, org); err == nil {
			people = reviewersAmong(members, reviewers)
		}
	}
	if err != nil {
		return nil, err
	}
	named := strings.ToLower(entity)
	return slices.DeleteFunc(people, func(p rosterPerson) bool { return !p.FirmWide && !slices.Contains(p.Clients, named) }), nil
}

// canConnect is a member of a hosted firm who may connect and disconnect a client's ledgers (integrations:manage), as
// the connection routes require.
func canConnect(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && a.Organization != "" && slices.Contains(a.Permissions, "integrations:manage")
}
