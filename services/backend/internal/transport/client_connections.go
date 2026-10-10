package transport

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// A client's ledger connections are made from its page (docs/books-sources.md, Client page), at /v1/closes/client/…
// with the client named in the query, as its page's read is: QuickBooks is connected, its authorization finished, its
// company confirmed, its token refreshed and it is disconnected here, and the client's production posting grant is
// given and revoked here. A month's books are still read from the month, through its connection.
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
