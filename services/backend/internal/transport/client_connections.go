package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// A client's ledger connections are made from its page (docs/books-sources.md, Client page), at /v1/closes/client/…
// with the client named in the query, as its page's read is: QuickBooks and Xero are connected, their sign-ins finished,
// the company or organisation confirmed, QuickBooks' token refreshed and either disconnected here, and the client's
// QuickBooks and Xero posting grants are given and revoked here. A month's books are still read from the month,
// through the client's connection. A connection, and a grant on it, is kept under the name of the month it was made
// from, which may since have been confirmed as the client's alias: an action on one acts under that name; a new
// connection is made under the client's own name.
func registerClientConnections(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/client/quickbooks", func(w http.ResponseWriter, r *http.Request) {
		a, in, ok := quickbooksRequest(w, r)
		if !ok {
			return
		}
		name, ok := wholeClient(w, r, db)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		held, err := heldUnder(ctx, db, "quickbooks_connections", in.ID, a.Organization, name)
		if err != nil {
			problem(w, 503, "The client's QuickBooks connection is unavailable.")
			return
		}
		quickbooksAct(ctx, w, db, a, held, "", "", in)
	})
	mux.HandleFunc("POST /v1/closes/client/xero", func(w http.ResponseWriter, r *http.Request) {
		a, in, ok := xeroRequest(w, r)
		if !ok {
			return
		}
		name, ok := wholeClient(w, r, db)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		held, err := heldUnder(ctx, db, "xero_connections", in.ID, a.Organization, name)
		if err != nil {
			problem(w, 503, "The client's Xero connection is unavailable.")
			return
		}
		if xeroAct(ctx, w, db, a, held, "", "", in) {
			jsonReply(w, 200, map[string]bool{"saved": true})
		}
	})
	mux.HandleFunc("GET /v1/closes/client/xero-grant", func(w http.ResponseWriter, r *http.Request) {
		a, ok := productionActor(w, r, "")
		if !ok {
			return
		}
		if name, ok := wholeClient(w, r, db); ok {
			xeroGrantRead(w, r, db, a.Organization, name)
		}
	})
	mux.HandleFunc("POST /v1/closes/client/xero-grant", func(w http.ResponseWriter, r *http.Request) {
		a, ok := productionActor(w, r, "posting:approve")
		if !ok {
			return
		}
		if name, ok := wholeClient(w, r, db); ok {
			if held, ok := grantHeld(w, r, db, "xero_connections", "xero_posting_grants", a.Organization, name); ok {
				xeroGrantChange(w, r, db, a, held)
			}
		}
	})
	mux.HandleFunc("GET /v1/closes/client/production-grant", func(w http.ResponseWriter, r *http.Request) {
		a, ok := productionActor(w, r, "")
		if !ok {
			return
		}
		if name, ok := wholeClient(w, r, db); ok {
			productionGrantRead(w, r, db, a, name)
		}
	})
	mux.HandleFunc("POST /v1/closes/client/production-grant", func(w http.ResponseWriter, r *http.Request) {
		a, ok := productionActor(w, r, "posting:approve")
		if !ok {
			return
		}
		if name, ok := wholeClient(w, r, db); ok {
			if held, ok := grantHeld(w, r, db, "quickbooks_connections", "production_posting_grants", a.Organization, name); ok {
				productionGrantChange(w, r, db, a, held)
			}
		}
	})
}

// heldUnder is the name the client's row id in table (a connection or a grant) is held under, for firm org and the
// client's own name: the name of the month it was made from, which may since have been confirmed as the client's alias.
// No id, or an id that is not the client's, is the client's own name, under which the action then finds nothing it
// should not.
func heldUnder(ctx context.Context, db *sql.DB, table, id, org, name string) (string, error) {
	if id == "" {
		return name, nil
	}
	var held string
	err := db.QueryRowContext(ctx, `SELECT client_entity FROM `+table+` WHERE id::text=$1 AND organization_id=$2 AND client_identity(organization_id,client_entity)=client_identity($2,$3)`, id, org, name).Scan(&held)
	if err == sql.ErrNoRows {
		return name, nil
	}
	return held, err
}

// grantHeld is the name a grant request acts under: that of the grant it revokes, or of the connection it grants on. It
// reads the request's body for them and puts it back for the grant's own handler.
func grantHeld(w http.ResponseWriter, r *http.Request, db *sql.DB, connections, grants, org, name string) (string, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		problem(w, 400, "The request could not be read.")
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var ids struct {
		ConnectionID string `json:"connection_id"`
		GrantID      string `json:"grant_id"`
	}
	json.Unmarshal(raw, &ids)
	table, id := connections, ids.ConnectionID
	if ids.GrantID != "" {
		table, id = grants, ids.GrantID
	}
	held, err := heldUnder(r.Context(), db, table, id, org, name)
	if err != nil {
		problem(w, 503, "The client's grant is unavailable.")
		return "", false
	}
	return held, true
}
