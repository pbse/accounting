package transport

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/ledgers"
)

// The ledger catalog (docs/books-sources.md, Ledger catalog and onboarding): every ledger a client can keep, as
// configuration, with each one's export steps and whether its native connector is switched on for this firm. A client's
// ledger is recorded when it is added and corrected on its page; it says which product the client keeps, not how a
// month's books arrive, and changes no books.

// catalogEntry is a ledger as a firm's member reads it: the catalog's entry, and whether its connector reaches them.
type catalogEntry struct {
	ledgers.Ledger
	// Connected says the ledger's native connector is switched on for the reader's firm; a ledger without one is
	// served by files, with its own export steps.
	Connected bool `json:"connected"`
}

// firmCatalog is the catalog with each connector's switch for firm org: the firm's own pilot, on or off, else the
// environment's.
func firmCatalog(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org string) ([]catalogEntry, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT ON (connector) connector,enabled FROM connector_pilots WHERE organization_id=$1 ORDER BY connector,id DESC`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pilots := map[string]*bool{}
	for rows.Next() {
		var connector string
		var enabled bool
		if err = rows.Scan(&connector, &enabled); err != nil {
			return nil, err
		}
		pilots[connector] = &enabled
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []catalogEntry{}
	for _, l := range ledgers.Catalog() {
		out = append(out, catalogEntry{Ledger: l, Connected: ledgers.Switched(l.Connector, pilots[l.Connector])})
	}
	return out, nil
}

// clientLedger is the ledger recorded for a client, under any of its names: a catalog entry as the firm reads it (with
// whether its connector is switched on for the firm, and its export steps), another ledger by the name given, or not
// known yet; with who recorded it, when and why.
type clientLedger struct {
	ID        string        `json:"id"`
	Ledger    string        `json:"ledger"`
	OtherName string        `json:"other_name,omitempty"`
	Entry     *catalogEntry `json:"entry,omitempty"`
	Reason    string        `json:"reason"`
	Actor     string        `json:"actor"`
	At        time.Time     `json:"at"`
}

// readClientLedger is client name's ledger in firm org, or nil when none is recorded.
func readClientLedger(ctx context.Context, q interface {
	rowReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org, name string) (*clientLedger, error) {
	var l clientLedger
	err := q.QueryRowContext(ctx, `SELECT id::text,ledger,other_name,reason,actor,created_at FROM current_client_ledgers WHERE organization_id=$1 AND client=client_identity($1,$2)`, org, name).
		Scan(&l.ID, &l.Ledger, &l.OtherName, &l.Reason, &l.Actor, &l.At)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, ok := ledgers.Find(l.Ledger); ok {
		catalog, err := firmCatalog(ctx, q, org)
		if err != nil {
			return nil, err
		}
		for i := range catalog {
			if catalog[i].ID == l.Ledger {
				l.Entry = &catalog[i]
			}
		}
	}
	return &l, nil
}

// connectorOff answers 409 and says so when connector is switched off for firm org (or the switch cannot be read):
// a connector switched off connects and reads nothing; disconnecting stays open.
func connectorOff(ctx context.Context, w http.ResponseWriter, db *sql.DB, org, connector, name string) bool {
	var on bool
	if err := db.QueryRowContext(ctx, `SELECT `+ledgers.SwitchedSQL("$1", connector), org).Scan(&on); err != nil {
		problem(w, 503, name+"'s switch is unavailable.")
		return true
	}
	if !on {
		problem(w, 409, name+" is switched off for your firm. Disconnecting still works; ask Krith support to switch it on.")
		return true
	}
	return false
}

// canRecordLedger is a member who may record or correct a client's ledger: one who manages the roster (who adds
// clients), or one who may connect its ledgers.
func canRecordLedger(r *http.Request) bool { return canManageRoster(r) || canConnect(r) }

func registerLedgers(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/ledgers", func(w http.ResponseWriter, r *http.Request) {
		catalog, err := firmCatalog(r.Context(), db, organization(r))
		if err != nil {
			problem(w, 503, "The ledger catalog is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"ledgers": catalog})
	})
	// A client's ledger is recorded or corrected on its page: which product it keeps, not how a month's books arrive.
	// Correcting it changes the export steps and catalog entry shown, and no books.
	mux.HandleFunc("POST /v1/closes/client/ledger", func(w http.ResponseWriter, r *http.Request) {
		if !canRecordLedger(r) {
			problem(w, 403, "Recording a client's ledger requires members:manage with clients:all, or integrations:manage.")
			return
		}
		var in struct {
			Client    string `json:"client"`
			Ledger    string `json:"ledger"`
			OtherName string `json:"other_name"`
			Reason    string `json:"reason"`
			Rev       string `json:"rev"` // the ledger as read, "" for none
		}
		if !decode(w, r, &in) {
			return
		}
		in.Client, in.OtherName, in.Reason = strings.TrimSpace(in.Client), strings.TrimSpace(in.OtherName), strings.TrimSpace(in.Reason)
		if in.Client == "" || utf8.RuneCountInString(in.Client) > 200 {
			problem(w, 422, "Name the client.")
			return
		}
		if !ledgers.Valid(in.Ledger, in.OtherName) {
			problem(w, 422, "Choose the client's ledger from the list, name another one (up to 120 characters), or say it is not known yet.")
			return
		}
		if in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		ctx := r.Context()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			problem(w, 503, "The client's ledger is unavailable.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		name, known, allowed, whole, err := clientNamed(ctx, tx, org, in.Client, firmAccess(r), subject(r))
		if err != nil {
			problem(w, 503, "The client's ledger is unavailable.")
			return
		}
		if !known || !allowed {
			problem(w, 404, "Client not found.")
			return
		}
		if !whole {
			problem(w, 403, "A client's ledger is recorded under its own name; ask for access to "+name+".")
			return
		}
		// One change at a time, against the ledger the person read.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('client_ledgers:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
			problem(w, 503, "The client's ledger is unavailable.")
			return
		}
		var current sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT max(id) FROM current_client_ledgers WHERE organization_id=$1 AND client=client_identity($1,$2)`, org, name).Scan(&current); err != nil {
			problem(w, 503, "The client's ledger is unavailable.")
			return
		}
		read := ""
		if current.Valid {
			read = strconv.FormatInt(current.Int64, 10)
		}
		if in.Rev != read {
			problem(w, 409, "The client's ledger changed since you read it. Review it and try again.")
			return
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO client_ledgers(organization_id,client_entity,ledger,other_name,reason,actor) VALUES($1,$2,$3,$4,$5,$6)`, org, name, in.Ledger, in.OtherName, in.Reason, reviewerName(r)); err != nil {
			problem(w, 503, "The client's ledger could not be saved.")
			return
		}
		saved, err := readClientLedger(ctx, tx, org, name)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The client's ledger could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"ledger": saved})
	})
}

// ledgerCount is how many clients keep one ledger, across firms: a catalog entry, another ledger by the name firms gave
// it, or not known yet; and whether its native connector is switched on in this environment.
type ledgerCount struct {
	Ledger    string `json:"ledger"`
	Name      string `json:"name"`
	Clients   int    `json:"clients"`
	Firms     int    `json:"firms"`
	Connector string `json:"connector"`
	Connected bool   `json:"connected"`
}

// adminLedgers counts clients by the ledger their firms recorded, the names given for other ledgers included, so staff
// see which ledgers firms bring and whether each has a connector: the counts inform which connector to build next,
// beside each ledger's access terms (docs/books-sources.md, More ledgers). Only counts and the names of products are
// shown, never a client.
func adminLedgers(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var counts []ledgerCount
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT l.ledger,CASE WHEN l.ledger='other' THEN lower(trim(l.other_name)) ELSE '' END,count(*),count(DISTINCT l.organization_id)
 FROM current_client_ledgers l JOIN organizations o ON o.id=l.organization_id WHERE o.id<>'local-development'
 GROUP BY 1,2 ORDER BY 3 DESC,1,2`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c ledgerCount
			if err = rows.Scan(&c.Ledger, &c.Name, &c.Clients, &c.Firms); err != nil {
				return err
			}
			switch c.Ledger {
			case ledgers.Other:
			case ledgers.Unknown:
				c.Name = "Not known yet"
			default:
				if entry, ok := ledgers.Find(c.Ledger); ok {
					c.Name, c.Connector, c.Connected = entry.Name, entry.Connector, ledgers.Switched(entry.Connector, nil)
				}
			}
			counts = append(counts, c)
		}
		return rows.Err()
	})
	if err != nil {
		adminFirmProblem(w, err, "ledger counts")
		return
	}
	if counts == nil {
		counts = []ledgerCount{}
	}
	jsonReply(w, 200, map[string]any{"ledgers": counts})
}

// adminConnectorPilot switches a native connector on or off for one firm, with a reason, before it reaches every firm.
func adminConnectorPilot(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm := r.PathValue("id")
	var in struct {
		Connector string `json:"connector"`
		Enabled   bool   `json:"enabled"`
		Reason    string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	known := false
	for _, l := range ledgers.Catalog() {
		known = known || (l.Connector != "" && l.Connector == in.Connector)
	}
	if !known {
		problem(w, 422, "Choose a connector this build has.")
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "connector.pilot", TargetType: "firm", TargetID: firm, Reason: strings.TrimSpace(in.Reason), Request: map[string]any{"connector": in.Connector, "enabled": in.Enabled}},
		func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO connector_pilots(organization_id,connector,enabled,reason,actor) VALUES($1,$2,$3,$4,$5)`, firm, in.Connector, in.Enabled, strings.TrimSpace(in.Reason), subject(r))
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, "connector pilot")
		return
	}
	jsonReply(w, 200, map[string]bool{"changed": true})
}
