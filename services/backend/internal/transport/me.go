package transport

import (
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	_ "time/tzdata"
)

// A member's own settings (Your settings): who they are to WorkOS, their time zone in this firm, their attention
// notification kinds and their own Google Drive connection. Firm policy, ledger connections and posting authority are
// the firm's, under Firm settings and the Security center, and are not here.
func registerMe(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/me", func(w http.ResponseWriter, r *http.Request) {
		if subject(r) == "" {
			problem(w, 403, "Sign in to see your own settings.")
			return
		}
		var timeZone string
		err := db.QueryRowContext(r.Context(), `SELECT time_zone FROM member_settings WHERE organization_id=$1 AND subject=$2`, organization(r), subject(r)).Scan(&timeZone)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Your settings are unavailable.")
			return
		}
		kinds, browser, err := attentionPreferences(r.Context(), db, r)
		if err != nil {
			problem(w, 503, "Your settings are unavailable.")
			return
		}
		reply := map[string]any{"subject": subject(r), "time_zone": timeZone, "notifications": map[string]any{"kinds": kinds, "browser": browser, "all_kinds": attentionKinds}, "drive": map[string]any{"configured": false}}
		// Name and email are WorkOS's; local development has neither.
		reply["name_editable"] = false
		if a := driveActor(r); a != nil {
			var user workosUser
			if err := workos(r.Context(), "GET", "/user_management/users/"+url.PathEscape(a.Subject), nil, &user); err != nil {
				problem(w, 502, "WorkOS could not read your account. Try again.")
				return
			}
			reply["email"], reply["first_name"], reply["last_name"], reply["name_editable"] = user.Email, user.FirstName, user.LastName, true
			drive := map[string]any{"configured": true, "connected": false}
			if _, reason, err := driveToken(r.Context(), a); err != nil {
				drive["reason"] = "Google Drive could not be checked. Try again."
			} else if reason != "" {
				drive["reason"] = reason
			} else {
				drive["connected"] = true
			}
			reply["drive"] = drive
		}
		jsonReply(w, 200, reply)
	})
	// Each change names only the settings it changes.
	mux.HandleFunc("POST /v1/closes/me", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			FirstName *string `json:"first_name"`
			LastName  *string `json:"last_name"`
			TimeZone  *string `json:"time_zone"`
		}
		if !decode(w, r, &in) {
			return
		}
		if subject(r) == "" {
			problem(w, 403, "Sign in to keep your own settings.")
			return
		}
		if in.TimeZone != nil {
			zone := strings.TrimSpace(*in.TimeZone)
			if zone != "" {
				if _, err := time.LoadLocation(zone); err != nil || len(zone) > 64 || !strings.Contains(zone, "/") {
					problem(w, 422, "Choose a time zone by its region and city, such as America/New_York.")
					return
				}
			}
			if _, err := db.ExecContext(r.Context(), `INSERT INTO member_settings(organization_id,subject,time_zone) VALUES($1,$2,$3) ON CONFLICT (organization_id,subject) DO UPDATE SET time_zone=EXCLUDED.time_zone,updated_at=now()`, organization(r), subject(r), zone); err != nil {
				problem(w, 503, "Your settings could not be saved.")
				return
			}
			in.TimeZone = &zone
		}
		if in.FirstName != nil || in.LastName != nil {
			a := driveActor(r)
			if a == nil {
				problem(w, 409, "Your name is kept by the sign-in provider, which local development does not have.")
				return
			}
			first, last := "", ""
			if in.FirstName != nil {
				first = strings.TrimSpace(*in.FirstName)
			}
			if in.LastName != nil {
				last = strings.TrimSpace(*in.LastName)
			}
			if first == "" || len(first) > 100 || len(last) > 100 {
				problem(w, 422, "Give a first name, and keep each name under 100 characters.")
				return
			}
			var user workosUser
			if err := workos(r.Context(), "PUT", "/user_management/users/"+url.PathEscape(a.Subject), map[string]string{"first_name": first, "last_name": last}, &user); err != nil {
				problem(w, 502, "WorkOS could not save your name. Try again.")
				return
			}
			in.FirstName, in.LastName = &user.FirstName, &user.LastName
		}
		jsonReply(w, 200, map[string]any{"first_name": in.FirstName, "last_name": in.LastName, "time_zone": in.TimeZone})
	})
	// The same grant as a close's evidence pane asks for, returning to these settings.
	mux.HandleFunc("POST /v1/closes/me/drive/connect", func(w http.ResponseWriter, r *http.Request) {
		a := driveActor(r)
		origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
		if a == nil || err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
			problem(w, 409, "Google Drive is not configured.")
			return
		}
		origin.Path, origin.RawQuery = "/dashboard/settings", "tab=connections"
		var result struct {
			URL string `json:"url"`
		}
		if _, err = driveCall(r.Context(), "POST", "https://api.workos.com/data-integrations/"+driveSlug+"/authorize", os.Getenv("WORKOS_API_KEY"), map[string]string{"user_id": a.Subject, "organization_id": a.Organization, "return_to": origin.String()}, &result, 1<<16); err != nil || !strings.HasPrefix(result.URL, "https://") {
			problem(w, 502, "Google Drive connection is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]string{"url": result.URL})
	})
}
