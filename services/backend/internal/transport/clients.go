package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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

// aboutAccounts is SQL for a request q about accounts alone, handled in its month's coverage: any work it covers came
// from the client's own documents after it was approved.
const aboutAccounts = `(q.coverage_scope IS NOT NULL AND NOT EXISTS(SELECT 1 FROM request_jobs rj WHERE rj.request_id=q.id AND NOT ` + preparation.RequestReleased + `))`

// unfinishedRequest is SQL for a request q of draft d that the client has not finished answering. Before any reply it is
// waiting as attention counts it: work still waiting on it rather than on a newer request, or, about accounts alone,
// coverage exactly as approved. After a reply short of complete it is open as its link counts it (requestOpen): no work
// it was approved for is finished or taken over by a newer request, and its coverage has moved only by its own progress.
var unfinishedRequest = `CASE WHEN ` + requestReplied("q.id") + ` THEN NOT EXISTS(SELECT 1 FROM request_submissions WHERE request_id=q.id AND complete)
  AND NOT EXISTS(SELECT 1 FROM request_jobs rj JOIN preparation_jobs j ON j.id=rj.job_id WHERE rj.request_id=q.id AND ((j.state IN ('completed','cancelled') AND NOT ` + preparation.RequestReleased + `)
   OR EXISTS(SELECT 1 FROM request_jobs newer WHERE newer.job_id=rj.job_id AND newer.request_id>rj.request_id)))
  AND ` + preparation.RequestCoverageFollowsReply + `
 ELSE EXISTS(SELECT 1 FROM request_jobs rj JOIN preparation_jobs j ON j.id=rj.job_id WHERE rj.request_id=q.id AND j.state='waiting_client'
  AND NOT EXISTS(SELECT 1 FROM request_jobs newer WHERE newer.job_id=j.id AND newer.request_id>q.id)) OR ` + waitingCoverage + ` END`

// openJobs is SQL for the work request q covers that is not finished and not taken over by a newer request.
const openJobs = `request_jobs rj JOIN preparation_jobs j ON j.id=rj.job_id WHERE rj.request_id=q.id AND j.state NOT IN ('completed','cancelled')
  AND NOT EXISTS(SELECT 1 FROM request_jobs newer WHERE newer.job_id=j.id AND newer.request_id>q.id)`

// openRequest is a request to the client that it has not finished answering: who it is for, whether it reached them
// (sent, with when; sending; failed; or not sent yet), whether they answered it in part or in full, and how many items
// wait on it.
type openRequest struct {
	ID        string     `json:"id"`
	CloseID   string     `json:"close_id"`
	Period    string     `json:"period"`
	Recipient string     `json:"recipient"`
	State     string     `json:"state"`
	Sent      *time.Time `json:"sent_at,omitempty"`
	// Answer is answered or answered_in_part when the client has replied, in full or in part, else "".
	Answer string `json:"answer,omitempty"`
	// Job is the request's unfinished work, the first still waiting on the client first, where its delivery, reply and
	// follow-up are handled; "" for a request about accounts alone, handled in its month's coverage.
	Job   string `json:"job,omitempty"`
	Items int    `json:"items"`
}

type owedClientMonth struct {
	Period string `json:"period"`
	DueOn  string `json:"due_on,omitempty"`
}

func registerClients(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/client", func(w http.ResponseWriter, r *http.Request) {
		asked := strings.TrimSpace(r.URL.Query().Get("name"))
		if asked == "" || utf8.RuneCountInString(asked) > 200 {
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
		// The client's own name: its roster entry's, else the name its closes were opened with that is its own, else the
		// own name its confirmed aliases record (or, with none of these, its newest close's). A member's access is to a name, as access to a close is: the client is theirs to
		// open when any of its names is, showing the months they could open. What belongs to the client as a whole (its
		// roster entry, the months it owes, its connections, currency and basis) is shown under its own name only, as the
		// roster and a close's connection read show it.
		var name string
		var known, allowed, whole bool
		err = tx.QueryRowContext(ctx, `WITH client AS (SELECT client_identity($1,$2) AS id),
 names AS (SELECT r.client_entity AS name,0 AS rank,'' AS period FROM current_client_roster r,client WHERE r.organization_id=$1 AND client_identity(r.organization_id,r.client_entity)=client.id
  UNION ALL SELECT c.entity,CASE WHEN lower(c.entity)=client.id THEN 1 ELSE 3 END,c.period FROM client_closes c,client WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client.id
  UNION ALL SELECT a.client_entity,2,'' FROM client_aliases a,client WHERE a.organization_id=$1 AND a.retired_at IS NULL AND lower(a.client_entity)=client.id),
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
		// What the client was asked and has not finished answering, across the months the reader can open, as Work and
		// the attention list find it: the newest request of work still waiting on the client, and a request about
		// accounts alone still current that has no complete reply (waitingCoverage, a partial reply kept). Each says
		// whether it reached the client: its delivery recorded by a person, or an email the provider accepted that did not
		// then bounce, fail or get suppressed; else whether its latest email is on its way or failed, or it is not sent
		// yet. It says whether the client has answered it in part or in full, and how many items wait on it: the work
		// still waiting on it rather than on a newer request, or the accounts it asks about.
		ids := make([]string, len(months))
		for i, m := range months {
			ids[i] = m.ID
		}
		requests := []openRequest{}
		var waiting *sql.Rows
		if waiting, err = tx.QueryContext(ctx, `SELECT q.id::text,d.close_id::text,c.period,q.recipient,
 COALESCE(q.delivered_at,(SELECT min(COALESCE(m.finished_at,m.created_at)) FROM email_delivery m WHERE m.request_id=q.id AND m.state='accepted' AND m.delivery NOT IN ('bounced','complained','failed','suppressed'))),
 COALESCE((SELECT CASE WHEN m.state IN ('failed','unknown') OR m.delivery IN ('bounced','complained','failed','suppressed') THEN 'failed' WHEN m.state IN ('queued','sending') THEN 'sending' END
  FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),''),
 COALESCE((SELECT CASE WHEN s.complete THEN 'answered' ELSE 'answered_in_part' END FROM request_submissions s WHERE s.request_id=q.id ORDER BY s.id DESC LIMIT 1),''),
 CASE WHEN `+aboutAccounts+` THEN '' ELSE COALESCE((SELECT j.id::text FROM `+openJobs+` ORDER BY j.state='waiting_client' DESC,j.id LIMIT 1),'') END,
 CASE WHEN `+aboutAccounts+` THEN jsonb_array_length(COALESCE(q.coverage_scope->'bindings','[]'::jsonb)) ELSE (SELECT count(*) FROM `+openJobs+`) END
 FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id
 WHERE d.close_id::text=ANY($1) AND `+unfinishedRequest+`
 ORDER BY q.approved_at,q.id`, ids); err == nil {
			for waiting.Next() {
				var q openRequest
				var sent sql.NullTime
				var email string
				if err = waiting.Scan(&q.ID, &q.CloseID, &q.Period, &q.Recipient, &sent, &email, &q.Answer, &q.Job, &q.Items); err != nil {
					break
				}
				// A request that reached the client stays sent, whatever became of a later reminder; one whose every email
				// bounced or failed has not reached it.
				switch {
				case sent.Valid:
					q.State, q.Sent = "sent", &sent.Time
				case email == "failed":
					q.State = "failed"
				case email == "sending":
					q.State = "sending"
				default:
					q.State = "not_sent"
				}
				requests = append(requests, q)
			}
			waiting.Close()
			if err == nil {
				err = waiting.Err()
			}
		}
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["requests"] = requests
		// A member who can open only some of the client's months, under another of its names, sees those alone.
		if !whole {
			out["roster"], out["template"], out["template_hidden"], out["quickbooks"], out["xero"], out["profile"], out["basis"], out["ledger"] = nil, nil, true, []any{}, []any{}, nil, nil, nil
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

		// How the books arrive: the client's QuickBooks connections (under its own name first, then production first, as
		// its books are read) and its Xero connections, under any of its names. A connection is kept under the name of the
		// close it was made from, which may since have been confirmed as an alias, and serves the months opened under
		// that name; each says which name it is held under.
		var quickbooksRaw, xeroRaw []byte
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object('id',x.id::text,'client_entity',x.client_entity,'environment',x.environment,'state',x.state,'realm',x.realm,'company_name',x.company_name,'error',x.error,'confirmed_at',x.confirmed_at,'updated_at',x.updated_at)
  ORDER BY lower(x.client_entity)=lower($2) DESC,x.environment='production' DESC,x.id DESC)
  FROM quickbooks_connections x WHERE x.organization_id=$1 AND client_identity(x.organization_id,x.client_entity)=client_identity($1,$2) AND x.state<>'disconnected'),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',x.id::text,'client_entity',x.client_entity,'state',x.state,'tenant_name',x.tenant_name,'error',x.error,'confirmed_at',x.confirmed_at,'updated_at',x.updated_at)
  ORDER BY lower(x.client_entity)=lower($2) DESC,x.id DESC)
  FROM xero_connections x WHERE x.organization_id=$1 AND client_identity(x.organization_id,x.client_entity)=client_identity($1,$2) AND x.state<>'disconnected'),'[]'::jsonb)`, org, name).Scan(&quickbooksRaw, &xeroRaw); err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["quickbooks"], out["xero"] = json.RawMessage(quickbooksRaw), json.RawMessage(xeroRaw)

		// The client's currency and fiscal year as its connected ledgers report them, under any of its names, and its
		// approved basis (a client override first, then the firm's), as a close reads it.
		profile, err := preparation.ClientProfileOf(ctx, tx, org, name)
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["profile"] = profile
		// Whether QuickBooks and Xero are switched on for the firm: a connection to one switched off reads nothing.
		catalog, err := firmCatalog(ctx, tx, org)
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		switched := map[string]bool{}
		for _, l := range catalog {
			if l.Connector != "" {
				switched[l.Connector] = l.Connected
			}
		}
		out["connectors"] = switched
		// The ledger the client keeps, as recorded, and its catalog entry.
		if out["ledger"], err = readClientLedger(ctx, tx, org, name); err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		// The approved basis, as a close reads it for the name it was opened under: the client's own override, else the
		// firm's. An override approved under a name since confirmed as the client's alias is that name's months' basis,
		// so each is listed with the name it was approved under rather than folded into one client-wide value.
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
		aliasBases := []map[string]string{}
		if err == nil || err == sql.ErrNoRows {
			var rows *sql.Rows
			if rows, err = tx.QueryContext(ctx, `SELECT DISTINCT ON (lower(p.client_entity)) p.client_entity,p.method FROM procedure_versions p
 WHERE p.organization_id=$1 AND p.kind='basis' AND p.client_entity<>'' AND lower(p.client_entity)<>lower($2) AND client_identity(p.organization_id,p.client_entity)=client_identity($1,$2)
 ORDER BY lower(p.client_entity),p.id DESC`, org, name); err == nil {
				for rows.Next() {
					var held, method string
					if err = rows.Scan(&held, &method); err != nil {
						break
					}
					aliasBases = append(aliasBases, map[string]string{"held_under": held, "method": method})
				}
				rows.Close()
				if err == nil {
					err = rows.Err()
				}
			}
		}
		if err != nil {
			problem(w, 503, "The client is unavailable.")
			return
		}
		out["alias_bases"] = aliasBases

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
// role reviewing every client, or a reviewer assigned to the client under any of its names (one assigned under a
// confirmed alias opens that name's months). Each keeps every client they are assigned to, so the schedule's owner
// choice, which needs an assignment under the client's own name, reads as the roster's does.
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
	names := map[string]bool{strings.ToLower(entity): true}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT lower(client_entity) FROM current_client_access WHERE organization_id=$1 AND granted AND client_identity(organization_id,client_entity)=client_identity($1,$2)`, org, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(people, func(p rosterPerson) bool {
		return !p.FirmWide && !slices.ContainsFunc(p.Clients, func(c string) bool { return names[c] })
	}), nil
}

// canConnect is a member of a hosted firm who may connect and disconnect a client's ledgers (integrations:manage), as
// the connection routes require.
func canConnect(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && a.Organization != "" && slices.Contains(a.Permissions, "integrations:manage")
}
