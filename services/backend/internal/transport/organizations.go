package transport

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"close.local/backend/internal/auth"
)

func organization(r *http.Request) string {
	if actor := auth.Actor(r.Context()); actor != nil && actor.Organization != "" {
		return actor.Organization
	}
	return "local-development"
}

// All saved-close routes pass this boundary, including downloads and future routes.
// Close ownership is immutable through the API. Child identifiers are additionally
// checked against this close by their existing handlers.
func organizationBoundary(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := organization(r)
		var enabled bool
		err := db.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1`, org).Scan(&enabled)
		if err == sql.ErrNoRows || (err == nil && !enabled) {
			problem(w, 403, "Your organization is not enabled. Contact your firm administrator.")
			return
		}
		if err != nil {
			problem(w, 503, "Organization access unavailable.")
			return
		}
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/closes")
		if path != "" && path != "/work" && path != "/procedures" && path != "/access" && path != "/email" {
			id, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
			id, err = url.PathUnescape(id)
			if err != nil {
				problem(w, 404, "Close not found.")
				return
			}
			var exists bool
			err = db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM client_closes WHERE id::text=$1 AND organization_id=$2 AND ($3 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=client_closes.organization_id AND g.subject=$4 AND lower(g.client_entity)=lower(client_closes.entity) AND g.granted)))`, id, org, firmAccess(r), subject(r)).Scan(&exists)
			if err != nil {
				problem(w, 503, "Close access unavailable.")
				return
			}
			if !exists {
				problem(w, 404, "Close not found.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
