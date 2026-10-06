package transport

import (
	"database/sql"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/llm"
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
	if err == nil && !member {
		member = invitationEnrollment(r.Context(), db, organization(r), subject(r))
	}
	return member, err
}

// All saved-close routes pass this boundary, including downloads and future routes.
// Close ownership is immutable through the API. Child identifiers are additionally
// checked against this close by their existing handlers.
// executiveRoutes are all an executive may call: their overview, a close's published packs, their exports and
// acknowledgment, the read-only cash plan and budget comparison, and the plan's what-if scenarios.
var executiveRoutes = regexp.MustCompile(`^(GET /executive|GET /[^/]+/management-pack/publications(/[^/]+(/export)?)?|POST /[^/]+/management-pack/publications|GET /[^/]+/(cash-plan|cash-plan/reviews|budget|cash-scenarios)|POST /[^/]+/cash-scenarios)$`)

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
		if executive(r) && !executiveRoutes.MatchString(r.Method+" "+path) {
			problem(w, 403, "Executive access covers published reports, the cash plan and the budget comparison only.")
			return
		}
		if path != "" && path != "/executive" && path != "/work" && path != "/work/interpret" && path != "/chat" && path != "/search" && path != "/dependencies" && path != "/review-queue" && path != "/review-batch" && path != "/effort" && path != "/track-record" && path != "/organization/policy" && path != "/agent-access" && path != "/procedures" && path != "/access" && path != "/members" && path != "/organization/revoke" && path != "/operations" && path != "/email" && path != "/daily" && path != "/morning" && path != "/attention" && path != "/attention/preferences" && path != "/security" && path != "/security/operations" && path != "/index-rates" && path != "/portfolio" && path != "/portfolio/run" && path != "/goals" && path != "/plans/catalog" && path != "/plans/proposals" && path != "/setup" && path != "/members/roles" && path != "/members/invitations" && path != "/members/people" && path != "/members/role" && path != "/firm-export" {
			id, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
			id, err = url.PathUnescape(id)
			if err != nil {
				problem(w, 404, "Close not found.")
				return
			}
			var exists bool
			err = db.QueryRowContext(r.Context(), closeAccess, id, org, firmAccess(r), subject(r)).Scan(&exists)
			if err != nil {
				problem(w, 503, "Close access unavailable.")
				return
			}
			if !exists {
				problem(w, 404, "Close not found.")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(llm.WithIdentity(r.Context(), org, subject(r))))
	})
}
