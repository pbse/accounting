package transport

import (
	"bytes"
	"context"
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
	"sync"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestOnlyCompanyAddressesCount(t *testing.T) {
	for email, want := range map[string]bool{
		"ana@acme-cpa.com": true, "Ana@Acme-CPA.com ": true, "ben@smith.co.uk": true, "erin@firm.test": true, "live@live.example-firm.com": true,
		"ana@gmail.com": false, "ana@googlemail.com": false, "ana@outlook.com": false, "ana@hotmail.co.uk": false, "ana@yahoo.co.jp": false,
		"ana@icloud.com": false, "ana@yahoo.com.au": false, "ana@hotmail.com.br": false, "ana@outlook.consulting": true, "ana@yahoo.example-firm.com": true, "ana@proton.me": false, "ana@gmx.de": false, "ana@live.com": false, "ana": false, "@acme.com": false, "ana@acme": false, "ana@-acme.com": false,
	} {
		if _, got := companyDomain(email); got != want {
			t.Errorf("%q: company=%v, want %v", email, got, want)
		}
	}
	if domain, _ := companyDomain(" Ana@Acme-CPA.com. "); domain != "acme-cpa.com" {
		t.Fatalf("the domain is lower case without a trailing dot: %q", domain)
	}
}

// fakeWorkOS answers the WorkOS calls sign-in and the members page make, from memory.
type fakeWorkOS struct {
	mu          sync.Mutex
	key         *rsa.PrivateKey
	users       map[string]signedInUser
	memberships []firmMembership
	orgs        map[string]string // id → domain
	created     []string
	invitations map[string]map[string]string // WorkOS invitation id → its state and who accepted it
	roleChanges []string
	put         string // how the next role change goes: "" applies it, "refuse" declines, "lost" applies it but the answer is lost
}

func (f *fakeWorkOS) token(t *testing.T, user, org string, permissions ...string) string {
	t.Helper()
	claims := auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: user, Issuer: "https://auth.example", IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute))}, Organization: org, Session: "session_" + user, Permissions: permissions}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "fixture"
	signed, err := token.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func (f *fakeWorkOS) membership(user, org string) *firmMembership {
	for i := range f.memberships {
		if f.memberships[i].UserID == user && f.memberships[i].OrganizationID == org {
			return &f.memberships[i]
		}
	}
	return nil
}

var adminPermissions = []string{"clients:all", "members:manage", "org:manage", "workpapers:review"}

func (f *fakeWorkOS) serve(t *testing.T, r *http.Request) (int, any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	str := func(k string) string { s, _ := body[k].(string); return s }
	q := r.URL.Query()
	switch {
	case r.URL.Path == "/sso/jwks/client_fixture":
		return 200, map[string]any{"keys": []any{map[string]string{"kid": "fixture", "kty": "RSA", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})}}}
	case r.URL.Path == "/user_management/authenticate":
		// The code names the person; their organization is the one membership they hold, if exactly one.
		user, org := str("code"), str("organization_id")
		if str("grant_type") == "refresh_token" {
			user = strings.TrimPrefix(str("refresh_token"), "refresh-")
		}
		if org == "" {
			var held []map[string]string
			for _, m := range f.memberships {
				if m.UserID == user && m.Status == "active" {
					held = append(held, map[string]string{"id": m.OrganizationID})
				}
			}
			if len(held) > 1 {
				return 403, map[string]any{"code": "organization_selection_required", "pending_authentication_token": "pending-" + user, "organizations": held, "user": f.users[user]}
			}
			if len(held) == 1 {
				org = held[0]["id"]
			}
		}
		if str("grant_type") == "urn:workos:oauth:grant-type:organization-selection" {
			user = strings.TrimPrefix(str("pending_authentication_token"), "pending-")
		}
		perms := []string{}
		if m := f.membership(user, org); m != nil && m.Role.Slug == "firm-admin" {
			perms = adminPermissions
		}
		return 200, map[string]any{"access_token": f.token(t, user, org, perms...), "refresh_token": "refresh-" + user, "organization_id": org, "user": f.users[user]}
	case r.Method == "GET" && r.URL.Path == "/organizations":
		found := []map[string]string{}
		for id, domain := range f.orgs {
			if domain == q.Get("domains") {
				found = append(found, map[string]string{"id": id, "name": domain})
			}
		}
		return 200, map[string]any{"data": found, "list_metadata": map[string]string{}}
	case r.Method == "POST" && r.URL.Path == "/organizations":
		id := "org_Domain" + rand.Text()
		domains, _ := body["domain_data"].([]any)
		first, _ := domains[0].(map[string]any)
		if first["state"] != "verified" {
			t.Errorf("the firm's domain is created verified: %v", body)
		}
		f.orgs[id] = first["domain"].(string)
		f.created = append(f.created, id)
		return 201, map[string]string{"id": id}
	case r.Method == "GET" && r.URL.Path == "/user_management/organization_memberships":
		found := []firmMembership{}
		for _, m := range f.memberships {
			if (q.Get("user_id") == "" || m.UserID == q.Get("user_id")) && m.OrganizationID == q.Get("organization_id") && (q.Get("statuses") == "" || m.Status == q.Get("statuses")) {
				found = append(found, m)
			}
		}
		return 200, map[string]any{"data": found, "list_metadata": map[string]string{}}
	case r.Method == "POST" && r.URL.Path == "/user_management/organization_memberships":
		m := firmMembership{ID: "om_" + str("user_id"), UserID: str("user_id"), OrganizationID: str("organization_id"), Status: "active"}
		m.Role.Slug = str("role_slug")
		f.memberships = append(f.memberships, m)
		return 201, m
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/organizations/"):
		domain := f.orgs[strings.TrimPrefix(r.URL.Path, "/organizations/")]
		return 200, map[string]any{"domains": []map[string]string{{"domain": domain, "state": "verified"}}}
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/user_management/organization_memberships/"):
		outcome := f.put
		f.put = ""
		if outcome == "refuse" {
			return 400, map[string]string{"code": "invalid_role"}
		}
		for i := range f.memberships {
			if "/user_management/organization_memberships/"+f.memberships[i].ID == r.URL.Path {
				f.memberships[i].Role.Slug = str("role_slug")
				f.roleChanges = append(f.roleChanges, f.memberships[i].UserID+"→"+str("role_slug"))
				if outcome == "lost" {
					return 500, map[string]string{}
				}
				return 200, f.memberships[i]
			}
		}
		return 404, map[string]string{}
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/user_management/invitations/"):
		if inv, ok := f.invitations[strings.TrimPrefix(r.URL.Path, "/user_management/invitations/")]; ok {
			return 200, inv
		}
		return 404, map[string]string{}
	case strings.HasPrefix(r.URL.Path, "/user_management/users/"):
		u := f.users[strings.TrimPrefix(r.URL.Path, "/user_management/users/")]
		return 200, map[string]any{"id": u.ID, "email": u.Email, "email_verified": u.EmailVerified, "first_name": strings.Split(u.Email, "@")[0], "last_name": ""}
	case strings.HasPrefix(r.URL.Path, "/authorization/organizations/") && strings.HasSuffix(r.URL.Path, "/roles"):
		return 200, map[string]any{"data": []map[string]any{
			{"slug": "member", "name": "Member", "permissions": []string{}},
			{"slug": "firm-admin", "name": "Firm admin", "permissions": adminPermissions},
			{"slug": "staff-only", "name": "Staff only", "permissions": []string{"platform:operate"}},
		}}
	}
	t.Errorf("unexpected WorkOS call %s %s", r.Method, r.URL)
	return 500, nil
}

func TestSignInMakesOrJoinsTheFirmOfTheAddressDomain(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	// Closed by Cleanup, registered first so it runs after the data cleanup below (a defer would run before it).
	db, err := OpenTenantDB(dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	domain := "acme" + strings.ToLower(rand.Text()[:8]) + ".test"
	f := &fakeWorkOS{key: key, orgs: map[string]string{}, users: map[string]signedInUser{}, invitations: map[string]map[string]string{}}
	person := func(name string, verified bool, at string) string {
		id := "user_" + name + strings.ToLower(rand.Text()[:6])
		f.users[id] = signedInUser{ID: id, Email: name + "@" + at, EmailVerified: verified}
		return id
	}
	ana, ben, carol, dave, erin := person("ana", true, domain), person("ben", true, domain), person("carol", true, "gmail.com"), person("dave", false, domain), person("erin", true, domain)
	// Only the test's own company domains are run on Google Workspace or Microsoft 365.
	hosted := map[string]bool{domain: true}
	realHosting := hostedMail
	hostedMail = func(_ context.Context, d string) (bool, error) { return hosted[d], nil }
	t.Cleanup(func() { hostedMail = realHosting })
	t.Cleanup(func() {
		for id := range f.orgs {
			for _, q := range []string{`DELETE FROM member_invitations WHERE organization_id=$1`, `DELETE FROM auth_sessions WHERE organization_id=$1`, `DELETE FROM member_role_changes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
				if _, err := raw.Exec(q, id); err != nil {
					t.Errorf("cleanup %s: %v", id, err)
				}
			}
		}
	})
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		status, v := f.serve(t, r)
		out, _ := json.Marshal(v)
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(out)), Header: make(http.Header)}, nil
	})
	t.Setenv("WORKOS_CLIENT_ID", "client_fixture")
	t.Setenv("WORKOS_ISSUER", "https://auth.example")
	t.Setenv("WORKOS_ORGANIZATION_ID", "")
	t.Setenv("WORKOS_API_KEY", "sk_test_fixture")
	t.Setenv("SECRETS_KEY", strings.Repeat("cd", 32))
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("AUTH_SESSION", "server")
	t.Setenv("FRONTEND_ORIGIN", "https://app.example.test")
	t.Setenv("PLATFORM_ORGANIZATION_ID", "org_PlatformFixture")
	handler := apiWithStore(db, "")

	// signIn runs the round trip with a provider button and returns where it lands and the session cookie, if any.
	signIn := func(user, provider string) (string, string) {
		t.Helper()
		start := httptest.NewRecorder()
		handler.ServeHTTP(start, httptest.NewRequest("GET", "https://app.example.test/v1/auth/login?provider="+provider, nil))
		at, _ := url.Parse(start.Header().Get("Location"))
		if want := map[string]string{"google": "GoogleOAuth", "microsoft": "MicrosoftOAuth"}[provider]; at.Query().Get("provider") != want {
			t.Fatalf("the %s button goes straight to %s: %s", provider, want, at)
		}
		r := httptest.NewRequest("GET", "https://app.example.test/v1/auth/callback?code="+user+"&state="+url.QueryEscape(at.Query().Get("state")), nil)
		for _, c := range start.Result().Cookies() {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 302 {
			t.Fatalf("sign-in for %s: %d %s", user, w.Code, w.Body.String())
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie && c.MaxAge > 0 {
				return w.Header().Get("Location"), c.Value
			}
		}
		return w.Header().Get("Location"), ""
	}
	call := func(cookie, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://app.example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example.test")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// The first person from the domain creates its firm, with the domain verified, and becomes its administrator.
	where, anaCookie := signIn(ana, "google")
	if where != "/dashboard" || anaCookie == "" || len(f.created) != 1 {
		t.Fatalf("Ana lands in a new firm: %s cookie=%v created=%v", where, anaCookie != "", f.created)
	}
	firm := f.created[0]
	var stored, actor string
	if err := raw.QueryRow(`SELECT o.email_domain,m.actor FROM organizations o JOIN organization_member_access m ON m.organization_id=o.id WHERE o.id=$1 AND m.subject=$2 AND m.granted`, firm, ana).Scan(&stored, &actor); err != nil || stored != domain || actor != ana {
		t.Fatalf("the firm records its domain and its first member: %q %q %v", stored, actor, err)
	}
	if m := f.membership(ana, firm); m == nil || m.Role.Slug != "firm-admin" {
		t.Fatalf("Ana is the firm's administrator in WorkOS: %+v", m)
	}
	if w := call(anaCookie, "GET", "/v1/closes", ""); w.Code != 200 {
		t.Fatalf("the administrator opens the firm's work: %d %s", w.Code, w.Body.String())
	}

	// A colleague joins the same firm as a member, with no access until the administrator gives a role.
	where, benCookie := signIn(ben, "microsoft")
	if where != "/dashboard" || benCookie == "" || len(f.created) != 1 {
		t.Fatalf("Ben joins rather than creating a firm: %s created=%v", where, f.created)
	}
	if m := f.membership(ben, firm); m == nil || m.Role.Slug != "member" {
		t.Fatalf("Ben is a member in WorkOS: %+v", m)
	}
	if err := raw.QueryRow(`SELECT actor FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted`, firm, ben).Scan(&actor); err != nil || actor != "company-domain" {
		t.Fatalf("the firm records that Ben joined by his address: %q %v", actor, err)
	}
	if w := call(benCookie, "GET", "/v1/closes/members/people", ""); w.Code != 403 {
		t.Fatalf("a member without a role cannot manage people: %d", w.Code)
	}

	// Personal addresses, unverified ones and a person the firm removed are turned away, with no session.
	if where, cookie := signIn(carol, "google"); where != "/login?error=company" || cookie != "" {
		t.Fatalf("a Gmail address is refused: %s", where)
	}
	if where, cookie := signIn(dave, "google"); where != "/login?error=unverified" || cookie != "" {
		t.Fatalf("an unverified address cannot join: %s", where)
	}
	frank := person("frank", true, "shared-mail"+strings.ToLower(rand.Text()[:6])+".test")
	if where, cookie := signIn(frank, "google"); where != "/login?error=hosting" || cookie != "" || len(f.created) != 1 {
		t.Fatalf("a domain not run on Google Workspace or Microsoft 365 creates no firm: %s %v", where, f.created)
	}
	if _, err := raw.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,false,'Left the firm; access removed.',$3)`, firm, ben, ana); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(ben, "google"); where != "/login?error=removed" || cookie != "" {
		t.Fatalf("a removed member is not let back in by their address: %s", where)
	}

	// Someone WorkOS itself placed in the firm by its verified domain is recorded as joining by their address.
	f.mu.Lock()
	m := firmMembership{ID: "om_" + erin, UserID: erin, OrganizationID: firm, Status: "active"}
	m.Role.Slug = "member"
	f.memberships = append(f.memberships, m)
	f.mu.Unlock()
	if where, cookie := signIn(erin, "google"); where != "/dashboard" || cookie == "" {
		t.Fatalf("Erin, provisioned by WorkOS, signs in: %s", where)
	}
	if err := raw.QueryRow(`SELECT actor FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted`, firm, erin).Scan(&actor); err != nil || actor != "company-domain" {
		t.Fatalf("Erin is recorded as joining by her address: %q %v", actor, err)
	}

	// Someone WorkOS already holds at a higher role, joining by their address, is set to the member role: their role is
	// the administrator's decision.
	ivy := person("ivy", true, domain)
	f.mu.Lock()
	im := firmMembership{ID: "om_" + ivy, UserID: ivy, OrganizationID: firm, Status: "active"}
	im.Role.Slug = "firm-admin"
	f.memberships = append(f.memberships, im)
	f.mu.Unlock()
	where, ivyCookie := signIn(ivy, "google")
	if where != "/dashboard" || ivyCookie == "" || f.membership(ivy, firm).Role.Slug != "member" {
		t.Fatalf("Ivy joins as a member, whatever WorkOS held: %s %s", where, f.membership(ivy, firm).Role.Slug)
	}
	if w := call(ivyCookie, "GET", "/v1/closes/members/people", ""); w.Code != 403 {
		t.Fatalf("Ivy's session carries the member role, not what WorkOS held before: %d", w.Code)
	}

	// Someone the firm invited joins through the invitation, with its role: before accepting it they are asked to.
	jack := person("jack", true, domain)
	if _, err := raw.Exec(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,state,invited_by,reason) VALUES($1,$2,'firm-admin','Firm admin','sending',$3,'Jack will run reviews.')`, firm, f.users[jack].Email, ana); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(jack, "google"); where != "/login?error=invited" || cookie != "" || f.membership(jack, firm) != nil {
		t.Fatalf("an invited person is asked to accept the invitation first: %s", where)
	}

	// WorkOS may give an invitee a membership by their domain before they accept the invitation; they are still asked.
	oscar := person("oscar", true, domain)
	invitation := "invitation_" + rand.Text()
	f.mu.Lock()
	om := firmMembership{ID: "om_" + oscar, UserID: oscar, OrganizationID: firm, Status: "active"}
	om.Role.Slug = "member"
	f.memberships = append(f.memberships, om)
	f.invitations[invitation] = map[string]string{"id": invitation, "state": "pending", "organization_id": firm, "email": f.users[oscar].Email}
	f.mu.Unlock()
	if _, err := raw.Exec(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,$2,'member','Member',$3,'pending',$4,'Oscar joins the team.',now()+interval '7 days')`, firm, f.users[oscar].Email, invitation, ana); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(oscar, "google"); where != "/login?error=invited" || cookie != "" {
		t.Fatalf("an invitee WorkOS placed by domain is asked to accept first: %s", where)
	}
	f.mu.Lock()
	f.invitations[invitation]["state"], f.invitations[invitation]["accepted_user_id"] = "accepted", oscar
	f.mu.Unlock()
	if where, cookie := signIn(oscar, "google"); where != "/dashboard" || cookie == "" {
		t.Fatalf("once accepted, the invitee signs in: %s", where)
	}

	// An expired invitation does not stand in the way of joining by address.
	quinn := person("quinn", true, domain)
	if _, err := raw.Exec(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,$2,'member','Member',$3,'pending',$4,'Quinn was invited last month.',now()-interval '1 day')`, firm, f.users[quinn].Email, "invitation_"+rand.Text(), ana); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(quinn, "google"); where != "/dashboard" || cookie == "" || f.membership(quinn, firm) == nil {
		t.Fatalf("Quinn, whose invitation expired, joins by her address: %s", where)
	}

	// Someone WorkOS placed in its organization for their domain, which the app has not registered, makes that the firm.
	placed := "placed" + strings.ToLower(rand.Text()[:8]) + ".test"
	hosted[placed] = true
	grace := person("grace", true, placed)
	f.mu.Lock()
	placedOrg := "org_Placed" + rand.Text()
	f.orgs[placedOrg] = placed
	gm := firmMembership{ID: "om_" + grace, UserID: grace, OrganizationID: placedOrg, Status: "active"}
	gm.Role.Slug = "member"
	f.memberships = append(f.memberships, gm)
	f.mu.Unlock()
	if where, cookie := signIn(grace, "google"); where != "/dashboard" || cookie == "" {
		t.Fatalf("Grace, placed by WorkOS, signs in: %s", where)
	}
	if err := raw.QueryRow(`SELECT email_domain FROM organizations WHERE id=$1`, placedOrg).Scan(&stored); err != nil || stored != placed || f.membership(grace, placedOrg).Role.Slug != "firm-admin" {
		t.Fatalf("WorkOS's organization becomes the firm, with Grace its administrator: %q %v", stored, err)
	}

	// A firm set up by hand, whose WorkOS organization has the domain verified, takes the domain when a colleague signs in.
	handmade := "handmade" + strings.ToLower(rand.Text()[:8]) + ".test"
	handOrg := "org_Hand" + rand.Text()
	hank := person("hank", true, handmade)
	f.mu.Lock()
	f.orgs[handOrg] = handmade
	hm := firmMembership{ID: "om_" + hank, UserID: hank, OrganizationID: handOrg, Status: "active"}
	hm.Role.Slug = "member"
	f.memberships = append(f.memberships, hm)
	f.mu.Unlock()
	if _, err := raw.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Hand-made firm')`, handOrg); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(hank, "google"); where != "/dashboard" || cookie == "" {
		t.Fatalf("Hank signs in to the hand-made firm: %s", where)
	}
	if err := raw.QueryRow(`SELECT o.email_domain,m.actor FROM organizations o JOIN current_organization_members m ON m.organization_id=o.id AND m.subject=$2 WHERE o.id=$1`, handOrg, hank).Scan(&stored, &actor); err != nil || stored != handmade || actor != hank || f.membership(hank, handOrg).Role.Slug != "firm-admin" {
		t.Fatalf("the hand-made firm, with no member yet, takes its verified domain and Hank as its administrator: %q %q %v", stored, actor, err)
	}

	// The administrator sees the firm's people and gives Erin a role; the change is recorded and reaches WorkOS.
	w := call(anaCookie, "GET", "/v1/closes/members/people", "")
	var page struct {
		People []firmPerson `json:"people"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.People) != len(slices.DeleteFunc(slices.Clone(f.memberships), func(m firmMembership) bool { return m.OrganizationID != firm })) {
		t.Fatalf("the administrator lists the firm's people: %d %s", w.Code, w.Body.String())
	}
	if w := call(anaCookie, "POST", "/v1/closes/members/role", `{"subject":"`+erin+`","role":"staff-only","reason":"Give Erin a staff role.","confirmed":true}`); w.Code != 403 {
		t.Fatalf("a role with permissions the administrator lacks cannot be given: %d %s", w.Code, w.Body.String())
	}
	if w := call(anaCookie, "POST", "/v1/closes/members/role", `{"subject":"`+ben+`","role":"firm-admin","reason":"Bring Ben back as admin.","confirmed":true}`); w.Code != 409 {
		t.Fatalf("a removed person cannot be given a role: %d %s", w.Code, w.Body.String())
	}
	if w := call(anaCookie, "POST", "/v1/closes/members/role", `{"subject":"`+ana+`","role":"member","reason":"Step down from administrator.","confirmed":true}`); w.Code != 422 {
		t.Fatalf("an administrator cannot change their own role: %d", w.Code)
	}
	role := `{"subject":"` + erin + `","role":"firm-admin","reason":"Erin runs the September closes.","confirmed":true}`
	stateOf := func() string {
		t.Helper()
		var state string
		if err := raw.QueryRow(`SELECT state FROM member_role_changes WHERE organization_id=$1 AND subject=$2 ORDER BY id DESC LIMIT 1`, firm, erin).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	f.put = "refuse"
	if w := call(anaCookie, "POST", "/v1/closes/members/role", role); w.Code != 502 || stateOf() != "failed" {
		t.Fatalf("a change WorkOS refuses is recorded as failed: %d %s", w.Code, stateOf())
	}
	// WorkOS makes the change but its answer is lost: the record stays pending, and the list settles it from WorkOS.
	f.put = "lost"
	if w := call(anaCookie, "POST", "/v1/closes/members/role", role); w.Code != 502 || stateOf() != "pending" {
		t.Fatalf("an unconfirmed change stays pending: %d %s", w.Code, stateOf())
	}
	if w := call(anaCookie, "GET", "/v1/closes/members/people", ""); w.Code != 200 || stateOf() != "applied" {
		t.Fatalf("the list settles the change from WorkOS's role: %d %s", w.Code, stateOf())
	}
	var from, to string
	if err := raw.QueryRow(`SELECT from_role,to_role,actor FROM member_role_changes WHERE organization_id=$1 AND subject=$2 AND state='applied'`, firm, erin).Scan(&from, &to, &actor); err != nil || from != "member" || to != "firm-admin" || actor != ana || !slices.Equal(slices.DeleteFunc(slices.Clone(f.roleChanges), func(c string) bool { return !strings.HasPrefix(c, erin) }), []string{erin + "→firm-admin"}) {
		t.Fatalf("the role change is recorded and made in WorkOS: %q %q %q %v %v", from, to, actor, f.roleChanges, err)
	}
	if w := call(anaCookie, "POST", "/v1/closes/members/role", `{"subject":"`+erin+`","role":"firm-admin","reason":"Erin runs the September closes.","confirmed":true}`); w.Code != 409 {
		t.Fatalf("giving the role they already have changes nothing: %d", w.Code)
	}

	sessionOrg := func(user string) string {
		t.Helper()
		var org string
		if err := raw.QueryRow(`SELECT organization_id FROM auth_sessions WHERE subject=$1 ORDER BY created_at DESC LIMIT 1`, user).Scan(&org); err != nil {
			t.Fatal(err)
		}
		return org
	}
	// Someone WorkOS holds only in another registered firm (one that invited them) joins their own domain's firm.
	kate := person("kate", true, domain)
	elsewhere := "org_Elsewhere" + rand.Text()
	f.mu.Lock()
	f.orgs[elsewhere] = "elsewhere-" + domain
	km := firmMembership{ID: "om_" + kate, UserID: kate, OrganizationID: elsewhere, Status: "active"}
	km.Role.Slug = "member"
	f.memberships = append(f.memberships, km)
	f.mu.Unlock()
	if _, err := raw.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Another firm')`, elsewhere); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(kate, "google"); where != "/dashboard" || cookie == "" || sessionOrg(kate) != firm || f.membership(kate, firm) == nil {
		t.Fatalf("Kate lands in her own domain's firm: %s %s", where, sessionOrg(kate))
	}

	// A first-time signer WorkOS asks to choose between organizations claims the one for their domain.
	second := "second" + strings.ToLower(rand.Text()[:8]) + ".test"
	hosted[second] = true
	leo := person("leo", true, second)
	secondOrg, unrelated := "org_Second"+rand.Text(), "org_Unrelated"+rand.Text()
	f.mu.Lock()
	f.orgs[secondOrg], f.orgs[unrelated] = second, "unrelated-"+second
	for _, o := range []string{unrelated, secondOrg} {
		m := firmMembership{ID: "om_" + leo + o, UserID: leo, OrganizationID: o, Status: "active"}
		m.Role.Slug = "member"
		f.memberships = append(f.memberships, m)
	}
	f.mu.Unlock()
	if where, cookie := signIn(leo, "microsoft"); where != "/dashboard" || cookie == "" || sessionOrg(leo) != secondOrg {
		t.Fatalf("Leo claims his domain's organization: %s %s", where, sessionOrg(leo))
	}
	if err := raw.QueryRow(`SELECT email_domain FROM organizations WHERE id=$1`, secondOrg).Scan(&stored); err != nil || stored != second || f.membership(leo, secondOrg).Role.Slug != "firm-admin" {
		t.Fatalf("the domain's organization becomes Leo's firm: %q %v", stored, err)
	}

	// Someone a firm in another domain invited stays in that firm, though their own domain has a firm too.
	mia := person("mia", true, domain)
	inviting := "org_Inviting" + rand.Text()
	f.mu.Lock()
	f.orgs[inviting] = "inviting-" + domain
	mm := firmMembership{ID: "om_" + mia, UserID: mia, OrganizationID: inviting, Status: "active"}
	mm.Role.Slug = "member"
	f.memberships = append(f.memberships, mm)
	f.mu.Unlock()
	if _, err := raw.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Inviting firm')`, inviting); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,state,invited_by,reason) VALUES($1,$2,'member','Member','sending',$3,'Mia keeps our books.')`, inviting, f.users[mia].Email, ana); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(mia, "google"); where != "/dashboard" || cookie == "" || sessionOrg(mia) != inviting || f.membership(mia, firm) != nil {
		t.Fatalf("Mia stays in the firm that invited her: %s %s", where, sessionOrg(mia))
	}

	// Someone a client's firm removed, signed in by WorkOS to that firm, still reaches their own domain's firm.
	nora := person("nora", true, domain)
	f.mu.Lock()
	nm := firmMembership{ID: "om_" + nora, UserID: nora, OrganizationID: elsewhere, Status: "active"}
	nm.Role.Slug = "member"
	f.memberships = append(f.memberships, nm)
	f.mu.Unlock()
	if _, err := raw.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'Invited from another firm.',$3),($1,$2,false,'The engagement ended.',$3)`, elsewhere, nora, nora); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(nora, "google"); where != "/dashboard" || cookie == "" || sessionOrg(nora) != firm {
		t.Fatalf("Nora, removed from another firm, lands in her own domain's firm: %s %s", where, sessionOrg(nora))
	}

	// A pending invitation from another firm never keeps a member out of the firm they work in; once accepted, it wins.
	pending := "invitation_" + rand.Text()
	f.mu.Lock()
	pm := firmMembership{ID: "om_" + erin + inviting, UserID: erin, OrganizationID: inviting, Status: "active"}
	pm.Role.Slug = "member"
	f.memberships = append(f.memberships, pm)
	f.invitations[pending] = map[string]string{"id": pending, "state": "pending", "organization_id": inviting}
	f.mu.Unlock()
	if _, err := raw.Exec(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,$2,'member','Member',$3,'pending',$4,'Erin helps with our books.',now()+interval '7 days')`, inviting, f.users[erin].Email, pending, ana); err != nil {
		t.Fatal(err)
	}
	if where, cookie := signIn(erin, "google"); where != "/dashboard" || cookie == "" || sessionOrg(erin) != firm {
		t.Fatalf("Erin, invited elsewhere but not yet accepted, signs in to her firm: %s %s", where, sessionOrg(erin))
	}
	f.mu.Lock()
	f.invitations[pending]["state"], f.invitations[pending]["accepted_user_id"] = "accepted", erin
	f.mu.Unlock()
	if where, cookie := signIn(erin, "google"); where != "/dashboard" || cookie == "" || sessionOrg(erin) != inviting {
		t.Fatalf("Erin, having accepted, signs in to the inviting firm: %s %s", where, sessionOrg(erin))
	}

	// Every sign-in needs a verified address: a member whose address WorkOS no longer counts as verified is turned away.
	f.mu.Lock()
	u := f.users[erin]
	u.EmailVerified = false
	f.users[erin] = u
	f.mu.Unlock()
	if where, cookie := signIn(erin, "google"); where != "/login?error=unverified" || cookie != "" {
		t.Fatalf("an existing member with an unverified address is refused: %s", where)
	}
	// A removed member from another domain (one the firm invited) is told so, not given a session that cannot work.
	if _, err := raw.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'Invited from another firm.',$3),($1,$2,false,'The engagement ended.',$3)`, elsewhere, kate, kate); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	ku := f.users[kate]
	ku.Email = "kate@outside" + strings.ToLower(rand.Text()[:6]) + ".test"
	f.users[kate] = ku
	f.memberships = slices.DeleteFunc(f.memberships, func(m firmMembership) bool { return m.UserID == kate && m.OrganizationID == firm })
	f.mu.Unlock()
	if where, cookie := signIn(kate, "google"); where != "/login?error=removed" || cookie != "" {
		t.Fatalf("a removed member from another domain is refused: %s", where)
	}

	// A person in two firms signs in to the firm of their address's domain.
	f.mu.Lock()
	other := firmMembership{ID: "om_other_" + ana, UserID: ana, OrganizationID: "org_ElsewhereFixture", Status: "active"}
	other.Role.Slug = "member"
	f.memberships = append(f.memberships, other)
	f.mu.Unlock()
	if where, cookie := signIn(ana, "google"); where != "/dashboard" || cookie == "" {
		t.Fatalf("Ana, in two firms, signs in: %s", where)
	}
	var org string
	if err := raw.QueryRow(`SELECT organization_id FROM auth_sessions WHERE id_hash=(SELECT id_hash FROM auth_sessions WHERE subject=$1 ORDER BY created_at DESC LIMIT 1)`, ana).Scan(&org); err != nil || org != firm {
		t.Fatalf("the session is for her domain's firm: %q %v", org, err)
	}
}
