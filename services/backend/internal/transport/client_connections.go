package transport

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// A client's ledger connections are made from its page (docs/books-sources.md, Client page), at /v1/closes/client/…
// with the client named in the query, as its page's read is: QuickBooks and Xero are connected, their sign-ins finished,
// the company or organisation confirmed, QuickBooks' token refreshed and either disconnected here, and the client's
// QuickBooks and Xero posting grants are given and revoked here. A month's books are still read from the month,
// through the client's connection.
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
		quickbooksAct(ctx, w, db, a, name, "", "", in)
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
		if xeroAct(ctx, w, db, a, name, "", "", in) {
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
			xeroGrantChange(w, r, db, a, name)
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
			productionGrantChange(w, r, db, a, name)
		}
	})
}
