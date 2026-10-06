package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/secrets"
)

// Backend-held sign-in, chosen with AUTH_SESSION=server. WorkOS AuthKit still signs the person in, but the API keeps
// WorkOS's tokens in a sealed auth_sessions row and gives the browser only an HttpOnly cookie, which it turns into the
// usual Bearer token so every existing check runs unchanged. AUTH_SESSION unset or "client" keeps WorkOS's browser
// session, which needs a custom WorkOS authentication domain in production; deleting this file, its registration in
// apiWithStore, the frontend's server branch and the table returns to that.

const (
	sessionCookie = "__Host-close_session"
	staffCookie   = "__Host-close_staff"
	loginCookie   = "__Host-close_login"
	sessionDays   = 30
)

type sessionConfig struct{ clientID, secret, app string }

// serverSessions reads AUTH_SESSION: nil in client mode, or an error when server mode lacks a setting or the value is
// unknown, so a half-configured server mode stops the API instead of leaving sign-in broken.
func serverSessions() (*sessionConfig, error) {
	switch os.Getenv("AUTH_SESSION") {
	case "", "client":
		return nil, nil
	case "server":
	default:
		return nil, errors.New("AUTH_SESSION must be client or server")
	}
	app, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	_, sealErr := secrets.Seal("auth-session-check", nil)
	c := &sessionConfig{clientID: os.Getenv("WORKOS_CLIENT_ID"), secret: os.Getenv("WORKOS_API_KEY")}
	if err != nil || app.Scheme != "https" || app.Host == "" || app.User != nil || (app.Path != "" && app.Path != "/") || app.RawQuery != "" || app.Fragment != "" || c.clientID == "" || c.secret == "" || sealErr != nil {
		return nil, errors.New("AUTH_SESSION=server needs WORKOS_CLIENT_ID, WORKOS_API_KEY, SECRETS_KEY and an https PUBLIC_APP_URL")
	}
	c.app = app.Scheme + "://" + app.Host
	return c, nil
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// sessionKind is the firm workspace's session or the staff console's; neither opens the other's routes.
type sessionKind struct {
	staff              bool
	cookie, root, home string
}

var (
	firmSession  = sessionKind{false, sessionCookie, "/dashboard", "/dashboard"}
	staffSession = sessionKind{true, staffCookie, "/admin", "/admin/"}
)

func kindOf(r *http.Request) sessionKind {
	if r.URL.Query().Get("staff") == "1" {
		return staffSession
	}
	return firmSession
}

// returnPath admits only a path of this site under the session's root as the place to return to after sign-in.
func (k sessionKind) returnPath(p string) bool {
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n\t") {
		return false
	}
	clean := path.Clean(u.Path)
	return (u.Path == clean || u.Path == clean+"/") && (clean == k.root || strings.HasPrefix(clean, k.root+"/"))
}

// owns says whether a session row's organization belongs to this kind: staff hold the platform organization, firms never do.
func (k sessionKind) owns(org string) bool {
	platform := os.Getenv("PLATFORM_ORGANIZATION_ID")
	if k.staff {
		return platform != "" && org == platform
	}
	return platform == "" || org != platform
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

type signedInUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

type workosTokens struct {
	AccessToken    string       `json:"access_token"`
	RefreshToken   string       `json:"refresh_token"`
	OrganizationID string       `json:"organization_id"`
	User           signedInUser `json:"user"`
}

// organizationChoice is WorkOS asking which of several organizations a person signs in to.
type organizationChoice struct {
	Token         string `json:"pending_authentication_token"`
	Organizations []struct {
		ID string `json:"id"`
	} `json:"organizations"`
	User signedInUser `json:"user"`
}

func (*organizationChoice) Error() string { return "WorkOS asks which organization to sign in to" }

// providers are the sign-in buttons on the login page, each WorkOS's own name for it; anything else uses AuthKit's page.
var providers = map[string]string{"google": "GoogleOAuth", "microsoft": "MicrosoftOAuth"}

// authenticate exchanges a code or refresh token with WorkOS. refused is true only when WorkOS answered and declined
// (a 4xx other than rate limiting); a network error, 429 or 5xx is temporary, so a session never ends because WorkOS
// was briefly unreachable or busy. WorkOS asking which organization to sign in to comes back as *organizationChoice.
func (c *sessionConfig) authenticate(ctx context.Context, fields map[string]string) (tokens workosTokens, refused bool, err error) {
	fields["client_id"], fields["client_secret"] = c.clientID, c.secret
	body, _ := json.Marshal(fields)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "POST", workosAPI+"/user_management/authenticate", bytes.NewReader(body))
	if err != nil {
		return tokens, false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return tokens, false, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
		var answer struct {
			Code string `json:"code"`
			organizationChoice
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&answer) == nil && answer.Code == "organization_selection_required" && answer.Token != "" {
			return tokens, true, &answer.organizationChoice
		}
		return tokens, true, errors.New("WorkOS declined the sign-in")
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens) != nil || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return tokens, false, errors.New("WorkOS did not complete the sign-in")
	}
	return tokens, false, nil
}

// systemContext reads and writes auth_sessions before any firm is known; the request itself stays firm-scoped.
func systemContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemKey{}, true)
}

// firmTokens exchanges the sign-in code and settles which organization the session is for. Only a company address may
// sign in. A person in several organizations goes to the firm of their address's domain; a person in no firm finds or
// creates it (domainFirm), and the session is then renewed into it. A refusal names why for the login page.
func (c *sessionConfig) firmTokens(ctx context.Context, db *sql.DB, kind sessionKind, code string) (workosTokens, string, error) {
	tokens, _, err := c.authenticate(ctx, map[string]string{"grant_type": "authorization_code", "code": code})
	var choice *organizationChoice
	if errors.As(err, &choice) {
		if _, company := companyDomain(choice.User.Email); !company {
			return tokens, refusePersonal, nil
		}
		org := c.chosenOrganization(ctx, db, kind, choice)
		if org == "" {
			return tokens, refuseUnavailable, nil
		}
		tokens, _, err = c.authenticate(ctx, map[string]string{"grant_type": "urn:workos:oauth:grant-type:organization-selection", "pending_authentication_token": choice.Token, "organization_id": org})
	}
	if err != nil {
		return tokens, "", err
	}
	if _, company := companyDomain(tokens.User.Email); !company {
		return tokens, refusePersonal, nil
	}
	// Every session, not only a first one, needs an address WorkOS has verified.
	if !tokens.User.EmailVerified {
		return tokens, refuseUnverified, nil
	}
	if kind.staff {
		return tokens, "", nil
	}
	org, refusal := firmFor(ctx, db, tokens)
	if refusal != "" {
		return tokens, refusal, nil
	}
	// The session starts from a token issued now, for the settled firm, so it carries the role the person holds after
	// joining (someone set to Member keeps nothing WorkOS gave them before).
	tokens, _, err = c.authenticate(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": tokens.RefreshToken, "organization_id": org})
	return tokens, "", err
}

// firmFor decides which firm a firm session is for. A registered firm WorkOS signed them in to keeps them when they are
// a member there or it has an open invitation for them (an accountant a client's firm invited stays there). Otherwise their
// address decides: the firm registered for their domain (joined if need be); else a registered firm WorkOS holds them
// in; else their domain's claim, which may create the firm or take over an organization WorkOS placed them in.
func firmFor(ctx context.Context, db *sql.DB, tokens workosTokens) (string, string) {
	ctx = systemContext(ctx)
	domain, _ := companyDomain(tokens.User.Email)
	held := tokens.OrganizationID
	var registered, belongs bool
	var own string
	if held != "" && held != os.Getenv("PLATFORM_ORGANIZATION_ID") {
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1),
			EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted) OR EXISTS(SELECT 1 FROM member_invitations WHERE organization_id=$1 AND email=$3 AND state IN ('sending','pending'))`,
			held, tokens.User.ID, strings.ToLower(strings.TrimSpace(tokens.User.Email))).Scan(&registered, &belongs); err != nil {
			return "", refuseUnavailable
		}
	}
	if registered && belongs {
		return held, domainMember(ctx, db, held, tokens.User)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE email_domain=$1`, domain).Scan(&own); err != nil && err != sql.ErrNoRows {
		return "", refuseUnavailable
	}
	switch {
	case own != "" && own == held:
		return held, domainMember(ctx, db, held, tokens.User)
	case own != "":
		return domainFirm(ctx, db, tokens.User)
	case registered:
		return held, domainMember(ctx, db, held, tokens.User)
	}
	return domainFirm(ctx, db, tokens.User)
}

// chosenOrganization answers WorkOS asking which organization a person in several signs in to. Staff sign in to the
// platform's. For a firm session: a firm with an open invitation for them, then the firm they last signed in to where
// they are still a member, then their domain's registered firm; failing all, any of theirs completes the sign-in, and
// firmFor settles the firm.
func (c *sessionConfig) chosenOrganization(ctx context.Context, db *sql.DB, kind sessionKind, choice *organizationChoice) string {
	platform := os.Getenv("PLATFORM_ORGANIZATION_ID")
	listed := []string{}
	for _, o := range choice.Organizations {
		if kind.staff && o.ID == platform {
			return o.ID
		}
		if organizationID.MatchString(o.ID) {
			listed = append(listed, o.ID)
		}
	}
	if kind.staff || len(listed) == 0 {
		return ""
	}
	domain, _ := companyDomain(choice.User.Email)
	var org string
	if db.QueryRowContext(systemContext(ctx), `SELECT organization_id FROM member_invitations WHERE email=$1 AND organization_id=ANY($2) AND state IN ('sending','pending') ORDER BY created_at DESC LIMIT 1`, strings.ToLower(strings.TrimSpace(choice.User.Email)), listed).Scan(&org) == nil {
		return org
	}
	if db.QueryRowContext(systemContext(ctx), `SELECT s.organization_id FROM auth_sessions s JOIN current_organization_members m ON m.organization_id=s.organization_id AND m.subject=s.subject AND m.granted
		WHERE s.subject=$1 AND s.organization_id=ANY($2) ORDER BY s.created_at DESC LIMIT 1`, choice.User.ID, listed).Scan(&org) == nil {
		return org
	}
	if db.QueryRowContext(systemContext(ctx), `SELECT id FROM organizations WHERE email_domain=$1 AND id=ANY($2)`, domain, listed).Scan(&org) == nil {
		return org
	}
	return listed[0]
}

func (c *sessionConfig) register(mux *http.ServeMux, db *sql.DB, verifier *auth.Verifier) {
	staff := staffVerifier()
	mux.HandleFunc("GET /v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		kind := kindOf(r)
		org := os.Getenv("WORKOS_ORGANIZATION_ID")
		if kind.staff {
			if org = os.Getenv("PLATFORM_ORGANIZATION_ID"); org == "" {
				problem(w, 503, "Staff sign-in is not configured: set PLATFORM_ORGANIZATION_ID.")
				return
			}
		}
		back := r.URL.Query().Get("return")
		if !kind.returnPath(back) {
			back = kind.home
		}
		state := randomToken()
		setCookie(w, loginCookie, state+"."+base64.RawURLEncoding.EncodeToString([]byte(back)), 600)
		provider := providers[r.URL.Query().Get("provider")]
		if provider == "" {
			provider = "authkit"
		}
		query := url.Values{"client_id": {c.clientID}, "redirect_uri": {c.app + "/v1/auth/callback"}, "response_type": {"code"}, "provider": {provider}, "state": {state}}
		if org != "" {
			query.Set("organization_id", org)
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, workosAPI+"/user_management/authorize?"+query.Encode(), http.StatusFound)
	})
	mux.HandleFunc("GET /v1/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		setCookie(w, loginCookie, "", -1)
		cookie, err := r.Cookie(loginCookie)
		var state, encoded string
		if err == nil {
			state, encoded, _ = strings.Cut(cookie.Value, ".")
		}
		raw, _ := base64.RawURLEncoding.DecodeString(encoded)
		back := string(raw)
		// Sign-in only ever returns a staff session to the console, so the return path says which kind this is.
		kind, check := firmSession, verifier
		if staffSession.returnPath(back) {
			kind, check = staffSession, staff
		}
		if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 || r.URL.Query().Get("code") == "" || !kind.returnPath(back) {
			problem(w, 400, "This sign-in expired or did not start here. Start again from the sign-in page.")
			return
		}
		tokens, refusal, err := c.firmTokens(r.Context(), db, kind, r.URL.Query().Get("code"))
		if refusal != "" {
			// The login page says why, in words; nothing about the person goes in the address.
			http.Redirect(w, r, "/login?error="+refusal, http.StatusFound)
			return
		}
		if err != nil {
			problem(w, 502, "WorkOS could not complete the sign-in. Try again.")
			return
		}
		claims, err := check.Verify(r.Context(), tokens.AccessToken)
		if err != nil || claims.Subject != tokens.User.ID || claims.ExpiresAt == nil || !kind.owns(claims.Organization) {
			problem(w, 401, "The sign-in could not be verified.")
			return
		}
		refresh, err := secrets.Seal("auth-session:refresh", []byte(tokens.RefreshToken))
		access, accessErr := secrets.Seal("auth-session:access", []byte(tokens.AccessToken))
		if err != nil || accessErr != nil {
			problem(w, 503, "Sessions are unavailable.")
			return
		}
		id, ctx := randomToken(), systemContext(r.Context())
		_, err = db.ExecContext(ctx, `INSERT INTO auth_sessions(id_hash,subject,organization_id,workos_session,email,refresh_token,access_token,access_expires_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()+make_interval(days=>$9))`,
			hashToken(id), claims.Subject, claims.Organization, claims.Session, tokens.User.Email, refresh, access, claims.ExpiresAt.Time, sessionDays)
		if err != nil {
			problem(w, 503, "Sessions are unavailable.")
			return
		}
		db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at<now() OR revoked_at<now()-interval '1 day'`)
		setCookie(w, kind.cookie, id, sessionDays*24*60*60)
		http.Redirect(w, r, back, http.StatusFound)
	})
	mux.HandleFunc("GET /v1/auth/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		kind := kindOf(r)
		var subject, email, org string
		cookie, err := r.Cookie(kind.cookie)
		if err == nil {
			err = db.QueryRowContext(systemContext(r.Context()), `SELECT subject,email,organization_id FROM auth_sessions WHERE id_hash=$1 AND revoked_at IS NULL AND expires_at>now()`, hashToken(cookie.Value)).Scan(&subject, &email, &org)
			if err == nil && !kind.owns(org) {
				err = sql.ErrNoRows
			}
		}
		if err == sql.ErrNoRows || errors.Is(err, http.ErrNoCookie) {
			if err == sql.ErrNoRows {
				setCookie(w, kind.cookie, "", -1)
			}
			problem(w, 401, "Sign in again.")
			return
		}
		if err != nil {
			problem(w, 503, "Sessions are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"user": map[string]string{"id": subject, "email": email}, "organization_id": org})
	})
	mux.HandleFunc("POST /v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != c.app {
			problem(w, 403, "Sign out from this site.")
			return
		}
		kind := kindOf(r)
		home := c.app + "/"
		if kind.staff {
			home = c.app + kind.home
		}
		logout := home
		if cookie, err := r.Cookie(kind.cookie); err == nil {
			var workos string
			err = db.QueryRowContext(systemContext(r.Context()), `UPDATE auth_sessions SET revoked_at=now() WHERE id_hash=$1 AND revoked_at IS NULL RETURNING workos_session`, hashToken(cookie.Value)).Scan(&workos)
			if err != nil && err != sql.ErrNoRows {
				problem(w, 503, "Sessions are unavailable.")
				return
			}
			if workos != "" {
				logout = workosAPI + "/user_management/sessions/logout?" + url.Values{"session_id": {workos}, "return_to": {home}}.Encode()
			}
		}
		setCookie(w, kind.cookie, "", -1)
		jsonReply(w, 200, map[string]string{"logout_url": logout})
	})
}

// bearer turns the staff cookie (for /v1/admin/*) or the firm cookie (other /v1 routes) into the request's Bearer token.
// A request that already names a token keeps it, /v1/auth/* manages the cookies itself, routes outside /v1 (MCP among
// them) never accept one, and every request other than GET or HEAD must come from this site (a staff one, any request).
func (c *sessionConfig) bearer(db *sql.DB, verifier *auth.Verifier, next http.Handler) http.Handler {
	staff := staffVerifier()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, check := firmSession, verifier
		if strings.HasPrefix(r.URL.Path, "/v1/admin/") {
			kind, check = staffSession, staff
		}
		cookie, err := r.Cookie(kind.cookie)
		// The public contact form never uses a session, so an ended one cannot stop a visitor's note.
		if err != nil || r.Header.Get("Authorization") != "" || !strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1/auth/") || r.URL.Path == "/v1/contact" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != c.app {
			problem(w, 403, "This change must come from the workspace itself.")
			return
		}
		// Nor does another site's link open a staff read: under a content grant, or for an export, a GET alone reveals
		// and records. Browsers say where a request comes from; the console's own requests are same-origin.
		if site := r.Header.Get("Sec-Fetch-Site"); kind.staff && site != "" && site != "same-origin" && site != "none" {
			problem(w, 403, "Open the staff console directly.")
			return
		}
		token, status := c.accessToken(r.Context(), db, kind, check, cookie.Value)
		switch status {
		case 0:
			r.Header.Set("Authorization", "Bearer "+token)
			next.ServeHTTP(w, r)
		case 401:
			setCookie(w, kind.cookie, "", -1)
			problem(w, 401, "Your session ended. Sign in again.")
		default:
			problem(w, 503, "Sign-in is temporarily unavailable. Try again shortly.")
		}
	})
}

// accessToken returns the session's WorkOS access token, refreshing it when it has under a minute left. The refresh
// runs with the row locked and checks again after the lock, so two tabs refresh once; the rotated refresh token is
// saved. WorkOS declining the refresh, or a refreshed token for another person, session or firm, ends the session.
func (c *sessionConfig) accessToken(ctx context.Context, db *sql.DB, kind sessionKind, verifier *auth.Verifier, raw string) (string, int) {
	ctx = systemContext(ctx)
	var sealed []byte
	var expires time.Time
	var org string
	err := db.QueryRowContext(ctx, `SELECT organization_id,access_token,access_expires_at FROM auth_sessions WHERE id_hash=$1 AND revoked_at IS NULL AND expires_at>now()`, hashToken(raw)).Scan(&org, &sealed, &expires)
	if err == sql.ErrNoRows || (err == nil && !kind.owns(org)) {
		return "", 401
	}
	if err != nil {
		return "", 503
	}
	if time.Until(expires) > time.Minute {
		if access, err := secrets.Open("auth-session:access", sealed); err == nil {
			return string(access), 0
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", 503
	}
	defer tx.Rollback()
	var subject, workos string
	var refresh []byte
	err = tx.QueryRowContext(ctx, `SELECT subject,organization_id,workos_session,refresh_token,access_token,access_expires_at FROM auth_sessions WHERE id_hash=$1 AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, hashToken(raw)).Scan(&subject, &org, &workos, &refresh, &sealed, &expires)
	if err == sql.ErrNoRows {
		return "", 401
	}
	if err != nil {
		return "", 503
	}
	if time.Until(expires) > time.Minute {
		if access, err := secrets.Open("auth-session:access", sealed); err == nil {
			return string(access), 0
		}
	}
	end := func() (string, int) {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE id_hash=$1`, hashToken(raw)); err != nil || tx.Commit() != nil {
			return "", 503
		}
		return "", 401
	}
	plain, err := secrets.Open("auth-session:refresh", refresh)
	if err != nil {
		return end()
	}
	tokens, refused, err := c.authenticate(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": string(plain), "organization_id": org})
	if refused {
		return end()
	}
	if err != nil {
		return "", 503
	}
	newRefresh, err := secrets.Seal("auth-session:refresh", []byte(tokens.RefreshToken))
	if err != nil {
		return "", 503
	}
	claims, err := verifier.Verify(ctx, tokens.AccessToken)
	if errors.Is(err, auth.ErrSigningKeys) {
		// WorkOS's keys could not be checked: keep its rotated refresh token and try again on the next request.
		if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET refresh_token=$2,access_expires_at=now() WHERE id_hash=$1`, hashToken(raw), newRefresh); err != nil || tx.Commit() != nil {
			return "", 503
		}
		return "", 503
	}
	if err != nil || claims.Subject != subject || claims.Organization != org || claims.Session != workos || claims.ExpiresAt == nil {
		return end()
	}
	newAccess, err := secrets.Seal("auth-session:access", []byte(tokens.AccessToken))
	if err != nil {
		return "", 503
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET refresh_token=$2,access_token=$3,access_expires_at=$4 WHERE id_hash=$1`, hashToken(raw), newRefresh, newAccess, claims.ExpiresAt.Time); err != nil || tx.Commit() != nil {
		return "", 503
	}
	return tokens.AccessToken, 0
}
