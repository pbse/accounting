package transport

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

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

// firmCatalog is the catalog with each connector's switch for firm org: the environment's, or the firm's own pilot.
func firmCatalog(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org string) ([]catalogEntry, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT ON (connector) connector,enabled FROM connector_pilots WHERE organization_id=$1 ORDER BY connector,id DESC`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pilots := map[string]bool{}
	for rows.Next() {
		var connector string
		var enabled bool
		if err = rows.Scan(&connector, &enabled); err != nil {
			return nil, err
		}
		pilots[connector] = enabled
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

func registerLedgers(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/ledgers", func(w http.ResponseWriter, r *http.Request) {
		catalog, err := firmCatalog(r.Context(), db, organization(r))
		if err != nil {
			problem(w, 503, "The ledger catalog is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"ledgers": catalog})
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
					c.Name, c.Connector, c.Connected = entry.Name, entry.Connector, ledgers.Switched(entry.Connector, false)
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
