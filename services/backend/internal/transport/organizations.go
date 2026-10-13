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

// organizationAccess reads the firm's switch, the person's membership and, for closeID ("" for none), whether they can
// open that close (closeAccess), in one round trip.
func organizationAccess(db *sql.DB, r *http.Request, closeID string) (allowed, canOpen bool, err error) {
	var enabled, member bool
	err = db.QueryRowContext(r.Context(), `SELECT enabled,$2='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$2 AND subject=$4 AND granted),$1<>'' AND (`+closeAccess+`) FROM organizations WHERE id=$2`, closeID, organization(r), firmAccess(r), subject(r)).Scan(&enabled, &member, &canOpen)
	if err == sql.ErrNoRows || !enabled && err == nil {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !member {
		member = invitationEnrollment(r.Context(), db, organization(r), subject(r))
		// An invitation accepted just now may give the person clients, so the close is checked again.
		if member && closeID != "" {
			err = db.QueryRowContext(r.Context(), closeAccess, closeID, organization(r), firmAccess(r), subject(r)).Scan(&canOpen)
		}
	}
	return member, canOpen, err
}

// All saved-close routes pass this boundary, including downloads and future routes. A client's routes (/client and
// /client/…) check access to the client themselves (clients.go), since a client has no close of its own.
// Close ownership is immutable through the API. Child identifiers are additionally
// checked against this close by their existing handlers.
// executiveRoutes are all an executive may call: their overview, a close's published packs, their exports and
// acknowledgment, the read-only cash plan and budget comparison, the plan's what-if scenarios, and the agent's
// conversation (firm-wide only: an executive opens no close).
var executiveRoutes = regexp.MustCompile(`^(GET /executive|GET /chat|POST /chat|GET /chat/sessions|POST /chat/sessions/[^/]+|GET /[^/]+/management-pack/publications(/[^/]+(/export)?)?|POST /[^/]+/management-pack/publications|GET /[^/]+/(cash-plan|cash-plan/reviews|budget|cash-scenarios)|POST /[^/]+/cash-scenarios)$`)

func organizationBoundary(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := organization(r)
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/closes")
		// A path that names a close is checked with the firm, in the same read.
		closePath := path != "" && !strings.HasPrefix(path, "/chat/confirm/") && !strings.HasPrefix(path, "/chat/sessions") && path != "/executive" && path != "/examples" && path != "/examples/prepaid" && path != "/work" && path != "/chat" && path != "/ledgers" && path != "/search" && path != "/dependencies" && path != "/review-queue" && path != "/review-batch" && path != "/effort" && path != "/track-record" && path != "/organization/policy" && path != "/agent-access" && path != "/procedures" && path != "/access" && path != "/members" && path != "/organization/revoke" && path != "/operations" && path != "/email" && path != "/daily" && path != "/morning" && path != "/home" && path != "/attention" && path != "/attention/stream" && path != "/attention/preferences" && path != "/me" && path != "/me/profile" && path != "/me/drive" && path != "/me/drive/connect" && path != "/security" && path != "/security/clients" && path != "/roster" && path != "/client" && !strings.HasPrefix(path, "/client/") && path != "/close-template" && path != "/security/operations" && path != "/index-rates" && path != "/portfolio" && path != "/portfolio/run" && path != "/goals" && path != "/plans/catalog" && path != "/plans/proposals" && path != "/setup" && path != "/members/roles" && path != "/members/invitations" && path != "/members/people" && path != "/members/role" && path != "/firm-export"
		var id string
		var idErr error
		if closePath {
			id, _, _ = strings.Cut(strings.TrimPrefix(path, "/"), "/")
			if id, idErr = url.PathUnescape(id); idErr != nil {
				id = ""
			}
		}
		allowed, canOpen, err := organizationAccess(db, r, id)
		if err != nil {
			problem(w, 503, "Organization access unavailable.")
			return
		}
		if !allowed {
			problem(w, 403, "Your firm or membership is not enabled. Contact your firm administrator.")
			return
		}
		if executive(r) && !executiveRoutes.MatchString(r.Method+" "+path) {
			problem(w, 403, "Executive access covers published reports, the cash plan and the budget comparison only.")
			return
		}
		if closePath && (idErr != nil || !canOpen) {
			problem(w, 404, "Close not found.")
			return
		}
		next.ServeHTTP(w, r.WithContext(llm.WithIdentity(r.Context(), org, subject(r))))
	})
}
