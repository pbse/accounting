package transport

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestServerSessionKeepsWorkOSTokensOnTheServer(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	// As in production: without a signed-in firm or a system mark, the database admits no rows.
	db, err := OpenTenantDB(dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Session" + rand.Text()
	var alpha string
	_, err = raw.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Session fixture')`, org)
	must(err)
	_, err = raw.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = raw.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(raw.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	defer func() {
		for _, q := range []string{`DELETE FROM auth_sessions WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			raw.Exec(q, org)
		}
	}()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	must(err)
	kid, as, permissions := "fixture", org, []string{"workpapers:review"}
	mint := func(session string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a", Issuer: "https://auth.example", IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute))}, Organization: as, Session: session, Permissions: permissions})
		token.Header["kid"] = kid
		signed, e := token.SignedString(key)
		must(e)
		return signed
	}
	// WorkOS: its signing keys, and the token exchange, answered by respond.
	var calls []map[string]string
	respond := func(map[string]string) (int, any) { return 500, nil }
	reply := func(status int, v any) *http.Response {
		body, _ := json.Marshal(v)
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.workos.com/sso/jwks/client_fixture":
			return reply(200, map[string]any{"keys": []any{map[string]string{"kid": "fixture", "kty": "RSA", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})}}}), nil
		case "https://api.workos.com/user_management/authenticate":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			calls = append(calls, body)
			return reply(respond(body)), nil
		}
		t.Fatalf("unexpected provider call %s", r.URL)
		return nil, nil
	})
	tokens := func(session, refresh string) (int, any) {
		return 200, map[string]any{"access_token": mint(session), "refresh_token": refresh, "user": map[string]any{"id": "user_a", "email": "a@firm.test", "email_verified": true}, "organization_id": as}
	}
	t.Setenv("WORKOS_CLIENT_ID", "client_fixture")
	t.Setenv("WORKOS_ISSUER", "https://auth.example")
	t.Setenv("WORKOS_ORGANIZATION_ID", "")
	t.Setenv("WORKOS_API_KEY", "sk_test_fixture")
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("AUTH_SESSION", "server")
	t.Setenv("FRONTEND_ORIGIN", "https://app.example.test")
	t.Setenv("STAFF_EMAIL_DOMAIN", "firm.test")
	handler := apiWithStore(db, "")

	send := func(h http.Handler, method, target string, cookie string, origin string, more ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://app.example.test"+target, strings.NewReader(`{"entity":"Alpha","period":"2026-10"}`))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		}
		for _, c := range more {
			r.AddCookie(c)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	cookieOf := func(w *httptest.ResponseRecorder, name string) *http.Cookie {
		for _, c := range w.Result().Cookies() {
			if c.Name == name {
				return c
			}
		}
		return nil
	}
	// signInTo runs the whole WorkOS round trip for a firm or staff session and returns its cookie.
	signInTo := func(kind sessionKind, back, session, refresh string) (*httptest.ResponseRecorder, string) {
		t.Helper()
		start, provider := "/v1/auth/login?return=", "authkit"
		if kind.staff {
			// Staff go straight to Google, naming no organization.
			start, provider = "/v1/auth/login?staff=1&return=", "GoogleOAuth"
		}
		login := send(handler, "GET", start+url.QueryEscape(back), "", "")
		location, _ := url.Parse(login.Header().Get("Location"))
		state := location.Query().Get("state")
		if login.Code != 302 || location.Host != "api.workos.com" || location.Path != "/user_management/authorize" || location.Query().Get("client_id") != "client_fixture" || location.Query().Get("redirect_uri") != "https://app.example.test/v1/auth/callback" || location.Query().Get("organization_id") != "" || location.Query().Get("provider") != provider || state == "" {
			t.Fatalf("sign-in starts at WorkOS with this client and callback: %d %s", login.Code, location)
		}
		started := cookieOf(login, loginCookie)
		if started == nil || !started.HttpOnly || !started.Secure {
			t.Fatalf("the sign-in state is an HttpOnly, Secure cookie: %+v", started)
		}
		respond = func(map[string]string) (int, any) { return tokens(session, refresh) }
		r := httptest.NewRequest("GET", "https://app.example.test/v1/auth/callback?code=code-1&state="+url.QueryEscape(state), nil)
		r.AddCookie(started)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		issued := cookieOf(w, kind.cookie)
		if w.Code != 302 || issued == nil || len(w.Result().Cookies()) != 2 {
			t.Fatalf("the callback sets only this kind's session cookie (and clears the sign-in state): %d %s %v", w.Code, w.Body.String(), w.Result().Cookies())
		}
		return w, issued.Value
	}
	signIn := func(back, session, refresh string) (*httptest.ResponseRecorder, string) {
		t.Helper()
		return signInTo(firmSession, back, session, refresh)
	}

	// A callback that did not start here, or has the wrong state, is refused before WorkOS is asked, and the person is
	// sent back to start again where their sign-in began.
	callback := func(start, query string) *httptest.ResponseRecorder {
		t.Helper()
		var started *http.Cookie
		if start != "" {
			login := send(handler, "GET", start, "", "")
			at, _ := url.Parse(login.Header().Get("Location"))
			query, started = strings.ReplaceAll(query, "{state}", url.QueryEscape(at.Query().Get("state"))), cookieOf(login, loginCookie)
		}
		r := httptest.NewRequest("GET", "https://app.example.test/v1/auth/callback?"+query, nil)
		if started != nil {
			r.AddCookie(started)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, refused := range []struct{ start, query, to string }{
		{"/v1/auth/login", "code=code-1&state=forged", "/login?error=expired"},
		{"/v1/auth/login", "state={state}", "/login?error=expired"},
		{"", "code=code-1&state=x", "/login?error=expired"},
		{"/v1/auth/login?staff=1", "code=code-1&state=forged", "/admin/"},
		// Each keeps the page the person asked for, so signing in again returns there.
		{"/v1/auth/login?return=%2Fdashboard%2Fcloses%2Fx", "code=code-1&state=forged", "/login?error=expired&return=%2Fdashboard%2Fcloses%2Fx"},
		{"/v1/auth/login?staff=1&return=%2Fadmin%2Ffirms", "code=code-1&state=forged", "/admin/firms"},
	} {
		w := callback(refused.start, refused.query)
		if w.Code != 302 || w.Header().Get("Location") != refused.to || len(calls) != 0 || cookieOf(w, sessionCookie) != nil || cookieOf(w, staffCookie) != nil {
			t.Fatalf("a callback from %q with %q goes to %s without calling WorkOS: %d %q calls=%d", refused.start, refused.query, refused.to, w.Code, w.Header().Get("Location"), len(calls))
		}
	}
	// A code WorkOS will not take (used, or too old) opens nothing, and the login page says to try again.
	login := send(handler, "GET", "/v1/auth/login?return=%2Fdashboard%2Fcloses%2Fx", "", "")
	at, _ := url.Parse(login.Header().Get("Location"))
	respond = func(map[string]string) (int, any) { return 400, map[string]string{"code": "invalid_grant"} }
	r := httptest.NewRequest("GET", "https://app.example.test/v1/auth/callback?code=code-1&state="+url.QueryEscape(at.Query().Get("state")), nil)
	r.AddCookie(cookieOf(login, loginCookie))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 302 || w.Header().Get("Location") != "/login?error=unavailable&return=%2Fdashboard%2Fcloses%2Fx" || len(calls) != 1 || cookieOf(w, sessionCookie) != nil {
		t.Fatalf("a refused code goes back to the login page: %d %q calls=%d", w.Code, w.Header().Get("Location"), len(calls))
	}
	// A staff sign-in starts again from the console, whose button signs in to it, not the firm login page.
	if w := callback("/v1/auth/login?staff=1&return=%2Fadmin%2Ffirms", "code=code-1&state={state}"); w.Code != 302 || w.Header().Get("Location") != "/admin/firms" || len(calls) != 2 || cookieOf(w, staffCookie) != nil {
		t.Fatalf("a refused staff code goes back to the console: %d %q calls=%d", w.Code, w.Header().Get("Location"), len(calls))
	}
	calls = nil

	back := "/dashboard/closes/" + alpha
	done, cookie := signIn(back, "session_1", "refresh-1")
	issued := cookieOf(done, sessionCookie)
	if done.Header().Get("Location") != back || !issued.HttpOnly || !issued.Secure || issued.SameSite != http.SameSiteLaxMode || issued.Path != "/" || issued.MaxAge != 30*24*60*60 {
		t.Fatalf("the session cookie is HttpOnly, Secure, SameSite=Lax and returns to the workspace: %s %+v", done.Header().Get("Location"), issued)
	}
	if first := calls[0]; first["grant_type"] != "authorization_code" || first["code"] != "code-1" || first["client_secret"] != "sk_test_fixture" || first["client_id"] != "client_fixture" {
		t.Fatalf("the code is exchanged with the server's WorkOS key: %v", first)
	}
	var idHash, email string
	var refreshSealed []byte
	must(raw.QueryRow(`SELECT id_hash,email,refresh_token FROM auth_sessions WHERE organization_id=$1`, org).Scan(&idHash, &email, &refreshSealed))
	if idHash == cookie || idHash != hashToken(cookie) || email != "a@firm.test" || bytes.Contains(refreshSealed, []byte("refresh-1")) {
		t.Fatalf("the row keeps a hash of the cookie and sealed tokens, never either in the clear: %s %s", idHash, email)
	}
	for _, elsewhere := range []string{"//evil.example/dashboard", "https://evil.example/dashboard", "/dashboard\\evil.example", "/v1/closes", "/dashboard/../v1/closes"} {
		if done, _ := signIn(elsewhere, "session_x", "refresh-x"); done.Header().Get("Location") != "/dashboard" {
			t.Fatalf("sign-in never returns to another site: %s -> %s", elsewhere, done.Header().Get("Location"))
		}
	}

	// The cookie stands in for the Bearer token on workspace routes.
	if w := send(handler, "GET", "/v1/closes", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"`+alpha+`"`) {
		t.Fatalf("the signed-in member reads their closes: %d %s", w.Code, w.Body.String())
	}
	if w := send(handler, "GET", "/v1/closes", "", ""); w.Code != 401 {
		t.Fatalf("without the cookie the workspace refuses: %d", w.Code)
	}
	if w := send(handler, "POST", "/v1/closes", cookie, "https://evil.example"); w.Code != 403 {
		t.Fatalf("another site cannot make a change with the cookie: %d", w.Code)
	}
	if w := send(handler, "POST", "/v1/closes", cookie, ""); w.Code != 403 {
		t.Fatalf("a change with no Origin is refused: %d", w.Code)
	}
	if w := send(handler, "POST", "/v1/closes", cookie, "https://app.example.test"); w.Code == 401 || w.Code == 403 {
		t.Fatalf("the workspace itself can make a change: %d %s", w.Code, w.Body.String())
	}
	var session struct {
		User struct{ ID, Email string }
	}
	if w := send(handler, "GET", "/v1/auth/session", cookie, ""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &session) != nil || session.User.Email != "a@firm.test" {
		t.Fatalf("the page learns who is signed in: %d %s", w.Code, w.Body.String())
	}

	// An expiring access token is refreshed once, and the rotated refresh token is kept.
	expire := func() {
		t.Helper()
		_, err := raw.Exec(`UPDATE auth_sessions SET access_expires_at=now() WHERE id_hash=$1`, hashToken(cookie))
		must(err)
	}
	expire()
	calls = nil
	respond = func(body map[string]string) (int, any) { return tokens("session_1", "refresh-2") }
	if w := send(handler, "GET", "/v1/closes", cookie, ""); w.Code != 200 || len(calls) != 1 || calls[0]["grant_type"] != "refresh_token" || calls[0]["refresh_token"] != "refresh-1" || calls[0]["organization_id"] != org {
		t.Fatalf("an expiring token is refreshed with the stored refresh token: %d %v", w.Code, calls)
	}
	if w := send(handler, "GET", "/v1/closes", cookie, ""); w.Code != 200 || len(calls) != 1 {
		t.Fatalf("a fresh token is reused, not refreshed again: %d calls=%d", w.Code, len(calls))
	}
	expire()
	respond = func(body map[string]string) (int, any) {
		if body["refresh_token"] != "refresh-2" {
			t.Errorf("the rotated refresh token is used next: %v", body)
		}
		return 503, map[string]string{"error": "unavailable"}
	}
	var revoked bool
	if w := send(handler, "GET", "/v1/closes", cookie, ""); w.Code != 503 {
		t.Fatalf("WorkOS being unavailable is temporary: %d", w.Code)
	}
	must(raw.QueryRow(`SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id_hash=$1`, hashToken(cookie)).Scan(&revoked))
	if revoked {
		t.Fatal("a temporary WorkOS failure does not end the session")
	}
	respond = func(map[string]string) (int, any) { return 429, map[string]string{"error": "rate_limited"} }
	if w := send(handler, "GET", "/v1/closes", cookie, ""); w.Code != 503 {
		t.Fatalf("WorkOS rate limiting is temporary: %d", w.Code)
	}
	// A refreshed token whose signing key cannot be checked yet keeps the session and WorkOS's rotated refresh token.
	kid = "rotated"
	respond = func(map[string]string) (int, any) { return tokens("session_1", "refresh-3") }
	if w := send(handler, "GET", "/v1/closes", cookie, ""); w.Code != 503 {
		t.Fatalf("an unverifiable refreshed token is temporary: %d", w.Code)
	}
	must(raw.QueryRow(`SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id_hash=$1`, hashToken(cookie)).Scan(&revoked))
	if revoked {
		t.Fatal("a key that cannot be checked yet does not end the session")
	}
	kid = "fixture"
	respond = func(body map[string]string) (int, any) {
		if body["refresh_token"] != "refresh-3" {
			t.Errorf("the refresh token WorkOS rotated while its key was unchecked is used next: %v", body)
		}
		return 400, map[string]string{"error": "invalid_grant"}
	}
	if w := send(handler, "GET", "/v1/closes", cookie, ""); w.Code != 401 || cookieOf(w, sessionCookie) == nil || cookieOf(w, sessionCookie).MaxAge >= 0 {
		t.Fatalf("a refused refresh ends the session and clears the cookie: %d", w.Code)
	}
	must(raw.QueryRow(`SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id_hash=$1`, hashToken(cookie)).Scan(&revoked))
	if !revoked {
		t.Fatal("a refused refresh revokes the session")
	}
	if w := send(handler, "GET", "/v1/auth/session", cookie, ""); w.Code != 401 || cookieOf(w, sessionCookie) == nil || cookieOf(w, sessionCookie).MaxAge >= 0 {
		t.Fatalf("the session check clears a dead cookie: %d", w.Code)
	}

	// A refreshed token for another WorkOS session ends this one.
	_, other := signIn(back, "session_2", "refresh-3")
	_, err = raw.Exec(`UPDATE auth_sessions SET access_expires_at=now() WHERE id_hash=$1`, hashToken(other))
	must(err)
	respond = func(map[string]string) (int, any) { return tokens("session_other", "refresh-4") }
	if w := send(handler, "GET", "/v1/closes", other, ""); w.Code != 401 {
		t.Fatalf("a refreshed token must match the session: %d", w.Code)
	}
	// A refreshed token the verifier rejects for its claims (here, no WorkOS session) also ends the session.
	_, invalid := signIn(back, "session_5", "refresh-7")
	_, err = raw.Exec(`UPDATE auth_sessions SET access_expires_at=now() WHERE id_hash=$1`, hashToken(invalid))
	must(err)
	respond = func(map[string]string) (int, any) { return tokens("", "refresh-8") }
	if w := send(handler, "GET", "/v1/closes", invalid, ""); w.Code != 401 {
		t.Fatalf("a refreshed token with invalid claims ends the session: %d", w.Code)
	}
	// The public contact form needs no session: a visitor's ended session does not stop their note.
	contactFirm := "Session contact " + rand.Text()
	defer raw.Exec(`DELETE FROM contact_notes WHERE firm=$1`, contactFirm)
	note := httptest.NewRequest("POST", "https://app.example.test/v1/contact", strings.NewReader(`{"name":"Ana","email":"ana@firm.example","firm":"`+contactFirm+`"}`))
	note.Header.Set("Content-Type", "application/json")
	note.Header.Set("Origin", "https://app.example.test")
	note.AddCookie(&http.Cookie{Name: sessionCookie, Value: invalid})
	if w := httptest.NewRecorder(); func() int { handler.ServeHTTP(w, note); return w.Code }() != 201 {
		t.Fatalf("a contact note with an ended session: %d %s", w.Code, w.Body.String())
	}

	// Sign-out ends the session here and at WorkOS; MCP never accepts the cookie.
	_, last := signIn(back, "session_3", "refresh-5")
	mcpInit := httptest.NewRequest("POST", "https://app.example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	mcpInit.Header.Set("Content-Type", "application/json")
	mcpInit.Header.Set("Accept", "application/json, text/event-stream")
	mcpInit.Header.Set("Origin", "https://app.example.test")
	mcpInit.AddCookie(&http.Cookie{Name: sessionCookie, Value: last})
	if w := httptest.NewRecorder(); func() int { handler.ServeHTTP(w, mcpInit); return w.Code }() != 401 {
		t.Fatalf("MCP ignores the session cookie: %d %s", w.Code, w.Body.String())
	}
	if w := send(handler, "POST", "/v1/auth/logout", last, "https://evil.example"); w.Code != 403 {
		t.Fatalf("another site cannot sign the person out: %d", w.Code)
	}
	var out struct {
		LogoutURL string `json:"logout_url"`
	}
	w = send(handler, "POST", "/v1/auth/logout", last, "https://app.example.test")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.LogoutURL != "https://api.workos.com/user_management/sessions/logout?return_to=https%3A%2F%2Fapp.example.test%2F&session_id=session_3" {
		t.Fatalf("sign-out returns WorkOS's logout for this session: %d %s", w.Code, w.Body.String())
	}
	if w := send(handler, "GET", "/v1/closes", last, ""); w.Code != 401 {
		t.Fatalf("a signed-out cookie no longer works: %d", w.Code)
	}

	// Staff (here, the same person: their address is at the staff domain) hold their own session in their own cookie.
	// It opens only the staff console, and signing in or out as staff leaves the firm session alone.
	_, firm := signIn(back, "session_6", "refresh-9")
	done, staff := signInTo(staffSession, "/admin/firms/"+org, "staff_1", "staff-refresh-1")
	if done.Header().Get("Location") != "/admin/firms/"+org {
		t.Fatalf("staff sign-in returns to the console: %s", done.Header().Get("Location"))
	}
	for elsewhere, lands := range map[string]string{"/dashboard": "/admin/", "/admin/../dashboard": "/admin/", "/administrator": "/admin/", "/admin/firms/": "/admin/firms/"} {
		if done, _ := signInTo(staffSession, elsewhere, "staff_x", "staff-refresh-x"); done.Header().Get("Location") != lands {
			t.Fatalf("staff sign-in returns only to the console: %s -> %s", elsewhere, done.Header().Get("Location"))
		}
	}
	staffCookieOf := func(value string) *http.Cookie { return &http.Cookie{Name: staffCookie, Value: value} }
	var me staffMember
	if w := send(handler, "GET", "/v1/admin/me", "", "", staffCookieOf(staff)); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &me) != nil || me.Subject != "user_a" || me.Mode != "workos" || !slices.Equal(me.Permissions, platformPermissions) {
		t.Fatalf("the staff cookie opens the staff console with every permission: %d %s", w.Code, w.Body.String())
	}
	// Another site's link does not open a staff read, not even a GET; the console's own requests and a typed address do.
	for site, want := range map[string]int{"cross-site": 403, "same-site": 403, "same-origin": 200, "none": 200} {
		r := httptest.NewRequest("GET", "https://app.example.test/v1/admin/me", nil)
		r.AddCookie(staffCookieOf(staff))
		r.Header.Set("Sec-Fetch-Site", site)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("a staff read from %s: %d, want %d", site, w.Code, want)
		}
	}
	if w := send(handler, "GET", "/v1/admin/me", firm, ""); w.Code != 401 {
		t.Fatalf("the firm cookie does not open the staff console: %d", w.Code)
	}
	if w := send(handler, "GET", "/v1/closes", "", "", staffCookieOf(staff)); w.Code != 401 {
		t.Fatalf("the staff cookie does not open the workspace: %d", w.Code)
	}
	// A session value moved to the other kind's cookie is refused (and that cookie cleared), not turned into a token.
	if w := send(handler, "GET", "/v1/closes", staff, ""); w.Code != 401 || cookieOf(w, sessionCookie) == nil || cookieOf(w, sessionCookie).MaxAge >= 0 {
		t.Fatalf("a staff session under the firm cookie is refused: %d", w.Code)
	}
	if w := send(handler, "GET", "/v1/admin/me", "", "", staffCookieOf(firm)); w.Code != 401 || cookieOf(w, staffCookie) == nil || cookieOf(w, staffCookie).MaxAge >= 0 {
		t.Fatalf("a firm session under the staff cookie is refused: %d", w.Code)
	}
	if w := send(handler, "GET", "/v1/auth/session?staff=1", "", "", staffCookieOf(staff)); w.Code != 200 {
		t.Fatalf("the console learns staff are signed in: %d", w.Code)
	}
	if w := send(handler, "GET", "/v1/auth/session?staff=1", "", "", staffCookieOf(firm)); w.Code != 401 || cookieOf(w, staffCookie) == nil || cookieOf(w, staffCookie).MaxAge >= 0 {
		t.Fatalf("a firm session is not a staff session: %d", w.Code)
	}
	if w := send(handler, "GET", "/v1/auth/session", staff, ""); w.Code != 401 {
		t.Fatalf("a staff session is not a firm session: %d", w.Code)
	}
	w = send(handler, "POST", "/v1/auth/logout?staff=1", firm, "https://app.example.test", staffCookieOf(staff))
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.LogoutURL != "https://api.workos.com/user_management/sessions/logout?return_to=https%3A%2F%2Fapp.example.test%2Fadmin%2F&session_id=staff_1" || cookieOf(w, sessionCookie) != nil {
		t.Fatalf("staff sign-out ends only the staff session and returns to the console: %d %s", w.Code, w.Body.String())
	}
	if w := send(handler, "GET", "/v1/admin/me", "", "", staffCookieOf(staff)); w.Code != 401 {
		t.Fatalf("a signed-out staff cookie no longer works: %d", w.Code)
	}
	if w := send(handler, "GET", "/v1/closes", firm, ""); w.Code != 200 {
		t.Fatalf("the firm session outlives the staff session: %d", w.Code)
	}
	// Without a staff domain, staff sign-in is refused rather than admitting anyone, even one already under way.
	login = send(handler, "GET", "/v1/auth/login?staff=1", "", "")
	t.Setenv("STAFF_EMAIL_DOMAIN", "")
	if w := send(handler, "GET", "/v1/auth/login?staff=1", "", ""); w.Code != 503 {
		t.Fatalf("staff sign-in needs a staff domain: %d", w.Code)
	}
	location, _ := url.Parse(login.Header().Get("Location"))
	r = httptest.NewRequest("GET", "https://app.example.test/v1/auth/callback?code=code-1&state="+url.QueryEscape(location.Query().Get("state")), nil)
	r.AddCookie(cookieOf(login, loginCookie))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if cookieOf(w, staffCookie) != nil || w.Header().Get("Location") != "/login?error=staff" {
		t.Fatalf("a staff callback needs a staff domain: %d %s", w.Code, w.Header().Get("Location"))
	}

	// Client mode, the default, has no session routes and ignores the cookie.
	_, live := signIn(back, "session_4", "refresh-6")
	t.Setenv("AUTH_SESSION", "")
	client := apiWithStore(db, "")
	if w := send(client, "GET", "/v1/auth/login", "", ""); w.Code != 404 {
		t.Fatalf("client mode has no session routes: %d", w.Code)
	}
	if w := send(client, "GET", "/v1/closes", live, ""); w.Code != 401 {
		t.Fatalf("client mode ignores the cookie: %d", w.Code)
	}
	for mode, missing := range map[string]string{"server": "WORKOS_API_KEY", "cookie": ""} {
		t.Setenv("AUTH_SESSION", mode)
		if missing != "" {
			t.Setenv(missing, "")
		}
		if _, err := serverSessions(); err == nil {
			t.Fatalf("AUTH_SESSION=%s without %q must stop the API", mode, missing)
		}
	}
	t.Setenv("DATABASE_URL", "")
	if _, _, err := ConfiguredAPI(); err == nil {
		t.Fatal("an unknown mode stops the API even without a database")
	}
}
