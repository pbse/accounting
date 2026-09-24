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

func organizationAccess(db *sql.DB, r *http.Request) (bool, error) {
	var enabled bool
	err := db.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1`, organization(r)).Scan(&enabled)
	if err == sql.ErrNoRows || !enabled && err == nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if organization(r) == "local-development" {
		return true, nil
	}
	var member bool
	err = db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)`, organization(r), subject(r)).Scan(&member)
	return member, err
}

// All saved-close routes pass this boundary, including downloads and future routes.
// Close ownership is immutable through the API. Child identifiers are additionally
// checked against this close by their existing handlers.
func organizationBoundary(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := organization(r)
		allowed, err := organizationAccess(db, r)
		if err != nil {
			problem(w, 503, "Organization access unavailable.")
			return
		}
		if !allowed {
			problem(w, 403, "Your firm or membership is not enabled. Contact your firm administrator.")
			return
		}
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/closes")
		if path != "" && path != "/work" && path != "/dependencies" && path != "/review-queue" && path != "/review-batch" && path != "/effort" && path != "/organization/policy" && path != "/agent-access" && path != "/procedures" && path != "/access" && path != "/members" && path != "/organization/revoke" && path != "/operations" && path != "/email" && path != "/daily" {
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
