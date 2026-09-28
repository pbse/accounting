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

// dashboardPath admits only a workspace path on this site as the place to return to after sign-in.
func dashboardPath(p string) bool {
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n\t") {
		return false
	}
	clean := path.Clean(u.Path)
	return clean == u.Path && (clean == "/dashboard" || strings.HasPrefix(clean, "/dashboard/"))
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

type workosTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

// authenticate exchanges a code or refresh token with WorkOS. refused is true only when WorkOS answered and declined
// (a 4xx other than rate limiting); a network error, 429 or 5xx is temporary, so a session never ends because WorkOS
// was briefly unreachable or busy.
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

func (c *sessionConfig) register(mux *http.ServeMux, db *sql.DB, verifier *auth.Verifier) {
	mux.HandleFunc("GET /v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		back := r.URL.Query().Get("return")
		if !dashboardPath(back) {
			back = "/dashboard"
		}
		state := randomToken()
		setCookie(w, loginCookie, state+"."+base64.RawURLEncoding.EncodeToString([]byte(back)), 600)
		query := url.Values{"client_id": {c.clientID}, "redirect_uri": {c.app + "/v1/auth/callback"}, "response_type": {"code"}, "provider": {"authkit"}, "state": {state}}
		if org := os.Getenv("WORKOS_ORGANIZATION_ID"); org != "" {
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
		if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 || r.URL.Query().Get("code") == "" || !dashboardPath(back) {
			problem(w, 400, "This sign-in expired or did not start here. Start again from the sign-in page.")
			return
		}
		tokens, _, err := c.authenticate(r.Context(), map[string]string{"grant_type": "authorization_code", "code": r.URL.Query().Get("code")})
		if err != nil {
			problem(w, 502, "WorkOS could not complete the sign-in. Try again.")
			return
		}
		claims, err := verifier.Verify(r.Context(), tokens.AccessToken)
		if err != nil || claims.Subject != tokens.User.ID || claims.ExpiresAt == nil {
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
		setCookie(w, sessionCookie, id, sessionDays*24*60*60)
		http.Redirect(w, r, back, http.StatusFound)
	})
	mux.HandleFunc("GET /v1/auth/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var subject, email, org string
		cookie, err := r.Cookie(sessionCookie)
		if err == nil {
			err = db.QueryRowContext(systemContext(r.Context()), `SELECT subject,email,organization_id FROM auth_sessions WHERE id_hash=$1 AND revoked_at IS NULL AND expires_at>now()`, hashToken(cookie.Value)).Scan(&subject, &email, &org)
		}
		if err == sql.ErrNoRows || errors.Is(err, http.ErrNoCookie) {
			if err == sql.ErrNoRows {
				setCookie(w, sessionCookie, "", -1)
			}
			problem(w, 401, "Sign in to open your firm's workspace.")
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
		logout := c.app + "/"
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			var workos string
			err = db.QueryRowContext(systemContext(r.Context()), `UPDATE auth_sessions SET revoked_at=now() WHERE id_hash=$1 AND revoked_at IS NULL RETURNING workos_session`, hashToken(cookie.Value)).Scan(&workos)
			if err != nil && err != sql.ErrNoRows {
				problem(w, 503, "Sessions are unavailable.")
				return
			}
			if workos != "" {
				logout = workosAPI + "/user_management/sessions/logout?" + url.Values{"session_id": {workos}, "return_to": {c.app + "/"}}.Encode()
			}
		}
		setCookie(w, sessionCookie, "", -1)
		jsonReply(w, 200, map[string]string{"logout_url": logout})
	})
}

// bearer turns a session cookie into the request's Bearer token for /v1 routes. A request that already names a token
// keeps it, /v1/auth/* manages the cookie itself, and routes outside /v1 (MCP among them) never accept it. A cookie
// cannot make a cross-site change: every request other than GET or HEAD must come from this site.
func (c *sessionConfig) bearer(db *sql.DB, verifier *auth.Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || r.Header.Get("Authorization") != "" || !strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != c.app {
			problem(w, 403, "This change must come from the workspace itself.")
			return
		}
		token, status := c.accessToken(r.Context(), db, verifier, cookie.Value)
		switch status {
		case 0:
			r.Header.Set("Authorization", "Bearer "+token)
			next.ServeHTTP(w, r)
		case 401:
			setCookie(w, sessionCookie, "", -1)
			problem(w, 401, "Your session ended. Sign in again.")
		default:
			problem(w, 503, "Sign-in is temporarily unavailable. Try again shortly.")
		}
	})
}

// accessToken returns the session's WorkOS access token, refreshing it when it has under a minute left. The refresh
// runs with the row locked and checks again after the lock, so two tabs refresh once; the rotated refresh token is
// saved. WorkOS declining the refresh, or a refreshed token for another person, session or firm, ends the session.
func (c *sessionConfig) accessToken(ctx context.Context, db *sql.DB, verifier *auth.Verifier, raw string) (string, int) {
	ctx = systemContext(ctx)
	var sealed []byte
	var expires time.Time
	err := db.QueryRowContext(ctx, `SELECT access_token,access_expires_at FROM auth_sessions WHERE id_hash=$1 AND revoked_at IS NULL AND expires_at>now()`, hashToken(raw)).Scan(&sealed, &expires)
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
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", 503
	}
	defer tx.Rollback()
	var subject, org, workos string
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
