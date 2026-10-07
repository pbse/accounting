package transport

import (
	"database/sql"
	"net/http"

	"close.local/backend/internal/reference"
)

// The worked example (a synthetic prepaid workpaper, packages/contracts/reference.ts) is Krith's own: only the firm
// registered for the staff domain (STAFF_EMAIL_DOMAIN) sees it, and local development. Every other firm is told there
// is none, and its address answers 404.
func registerExamples(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/examples", func(w http.ResponseWriter, r *http.Request) {
		own, err := krithFirm(db, r)
		if err != nil {
			problem(w, 503, "Examples are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]bool{"available": own})
	})
	mux.HandleFunc("GET /v1/closes/examples/prepaid", func(w http.ResponseWriter, r *http.Request) {
		own, err := krithFirm(db, r)
		if err != nil {
			problem(w, 503, "Examples are unavailable.")
			return
		}
		if !own {
			problem(w, 404, "Not found.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(reference.Prepaid)
	})
}

// krithFirm says whether the request comes from Krith's own firm, the one registered for the staff domain.
func krithFirm(db *sql.DB, r *http.Request) (bool, error) {
	if organization(r) == "local-development" {
		return true, nil
	}
	if staffDomain() == "" {
		return false, nil
	}
	var own bool
	err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1 AND email_domain=$2)`, organization(r), staffDomain()).Scan(&own)
	return own, err
}
