package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

func TestEmailInvitationsEnrollOnlyAcceptedInvitees(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Invite" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Invite fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_admin',true,'Synthetic','fixture'),($1,'user_gone',false,'Synthetic revocation','fixture')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM member_invitations WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("WORKOS_API_KEY", "sk_test_synthetic")
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	// emailed is Krith's invitation email queued for this invitation: its recipient, sender's name and opened token.
	emailed := func(workosID string) (to, inviter, token string) {
		t.Helper()
		var sealed []byte
		err := db.QueryRow(`SELECT e.recipient,e.inviter,e.link FROM platform_emails e JOIN member_invitations i ON i.id=e.invitation WHERE i.workos_id=$1 AND e.kind='invitation' AND e.subject IS NULL ORDER BY e.id LIMIT 1`, workosID).Scan(&to, &inviter, &sealed)
		if err == sql.ErrNoRows {
			return "", "", ""
		}
		must(err)
		opened, err := secrets.Open("platform-email:invitation", sealed)
		must(err)
		return to, inviter, string(opened)
	}
	mode, invState, acceptedBy, status, verified := "ok", "pending", "", "active", true
	var sent map[string]string
	revoked, lookups, created := "", 0, 0
	listed := map[string]string{}
	expires := time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.workos.com" || r.Header.Get("Authorization") != "Bearer sk_test_synthetic" {
			t.Fatalf("unexpected call %s", r.URL)
		}
		code, body := 200, "{}"
		switch p := r.URL.Path; {
		case p == "/authorization/organizations/"+org+"/roles":
			body = `{"data":[{"slug":"preparer","name":"Preparer","description":"Prepares and reviews work","permissions":["workpapers:review","widgets:read"]},{"slug":"owner","name":"Owner","permissions":["org:manage","members:manage"]},{"slug":"mystery","name":"Mystery"}]}`
		case p == "/user_management/invitations" && r.Method == "POST":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &sent)
			created++
			switch mode {
			case "race":
				db.Exec(`UPDATE member_invitations SET state='revoked' WHERE organization_id=$1 AND state='sending'`, org)
				code, body = 201, `{"id":"invitation_race","state":"pending","expires_at":"`+expires+`"}`
			case "refuse":
				code = 422
			case "fail":
				code = 500
			default:
				code, body = 201, `{"id":"invitation_`+string(rune('0'+created))+`","state":"pending","expires_at":"`+expires+`","token":"tok_`+string(rune('0'+created))+`"}`
			}
		case p == "/user_management/invitations" && r.Method == "GET":
			body = `{"data":[` + listed[r.URL.Query().Get("email")] + `],"list_metadata":{}}`
		case strings.HasSuffix(p, "/revoke"):
			revoked = p
		case strings.HasPrefix(p, "/user_management/users/"):
			lookups++
			email := map[string]string{"user_new": "New@Example.com", "user_gone": "gone@example.com", "user_admin": "ada@example.com"}[strings.TrimPrefix(p, "/user_management/users/")]
			body = `{"email":"` + email + `","first_name":"Ada","last_name":"Admin","email_verified":` + map[bool]string{true: "true", false: "false"}[verified] + `}`
		case strings.HasPrefix(p, "/user_management/invitations/"):
			var email string
			db.QueryRow(`SELECT email FROM member_invitations WHERE workos_id=$1`, strings.TrimPrefix(p, "/user_management/invitations/")).Scan(&email)
			body = `{"id":"x","state":"` + invState + `","accepted_user_id":"` + acceptedBy + `","organization_id":"` + org + `","email":"` + email + `"}`
		case p == "/user_management/organization_memberships":
			body = `{"data":[{"status":"` + status + `"}]}`
		default:
			t.Fatalf("unexpected WorkOS call %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: []string{"clients:all", "members:manage", "workpapers:review", "widgets:read"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s %v: %d %s", subject, method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var roles struct {
		Roles []struct {
			Slug        string
			Permissions []string
			Allowed     bool
		}
	}
	must(json.Unmarshal(call("user_admin", "GET", "/v1/closes/members/roles", nil, 200), &roles))
	if len(roles.Roles) != 3 || !roles.Roles[0].Allowed || roles.Roles[1].Allowed || roles.Roles[2].Allowed || roles.Roles[0].Permissions[0] != "Review and approve drafts, and start or stop preparation" || roles.Roles[0].Permissions[1] != "widgets:read" {
		t.Fatalf("roles read in plain words, and only those within the administrator's own permissions are offered: %+v", roles)
	}
	path := "/v1/closes/members/invitations"
	send := func(email, role string, want int) {
		t.Helper()
		call("user_admin", "POST", path, map[string]any{"action": "send", "email": email, "role": role, "reason": "Joining the audit team", "confirmed": true}, want)
	}
	state := func(email string) (s, workos string) {
		t.Helper()
		db.QueryRow(`SELECT state,COALESCE(workos_id,'') FROM member_invitations WHERE organization_id=$1 AND email=$2 ORDER BY id DESC LIMIT 1`, org, email).Scan(&s, &workos)
		return
	}
	send("not an address", "preparer", 422)
	send("new@example.com", "owner", 403)
	send("New@Example.com", "preparer", 200)
	if s, id := state("new@example.com"); s != "pending" || id != "invitation_1" || sent["email"] != "new@example.com" || sent["organization_id"] != org || sent["role_slug"] != "preparer" || sent["inviter_user_id"] != "user_admin" {
		t.Fatalf("WorkOS sends the invitation and it is kept pending: %s %s %v", s, id, sent)
	}
	if to, inviter, token := emailed("invitation_1"); to != "new@example.com" || inviter != "Ada Admin" || token != "tok_1" {
		t.Fatalf("Krith's email for the invitation is queued with its sender's name and its sealed token: %q %q %q", to, inviter, token)
	}
	// The list says whether the email has gone, and a newer email for the same invitation replaces one still waiting.
	emailState := func(email string) string {
		t.Helper()
		var listed []struct {
			Email      string  `json:"email"`
			EmailState *string `json:"email_state"`
		}
		must(json.Unmarshal(call("user_admin", "GET", path, nil, 200), &listed))
		for _, i := range listed {
			if i.Email == email && i.EmailState != nil {
				return *i.EmailState
			}
		}
		return ""
	}
	if s := emailState("new@example.com"); s != "queued" {
		t.Fatalf("the list shows the invitation's email waiting: %q", s)
	}
	var invited string
	must(db.QueryRow(`SELECT id::text FROM member_invitations WHERE workos_id='invitation_1'`).Scan(&invited))
	tx, err := db.Begin()
	must(err)
	must(queueInvitationEmail(t.Context(), tx, org, invited, "new@example.com", "tok_1b", "Ada Admin"))
	must(tx.Commit())
	var states string
	must(db.QueryRow(`SELECT string_agg(state,',' ORDER BY id) FROM platform_emails WHERE invitation=$1::bigint`, invited).Scan(&states))
	if states != "cancelled,queued" {
		t.Fatalf("a newer email replaces the one still waiting: %s", states)
	}
	send("new@example.com", "preparer", 409)
	var first string
	must(db.QueryRow(`SELECT id::text FROM member_invitations WHERE organization_id=$1 AND workos_id='invitation_1'`, org).Scan(&first))
	call("user_admin", "POST", path, map[string]any{"action": "revoke", "id": first, "confirmed": true}, 200)
	if s, _ := state("new@example.com"); s != "revoked" || revoked != "/user_management/invitations/invitation_1/revoke" {
		t.Fatalf("a revoked invitation is withdrawn in the app and in WorkOS: %s %s", s, revoked)
	}
	mode = "refuse"
	send("other@example.com", "preparer", 422)
	if s, _ := state("other@example.com"); s != "" {
		t.Fatal("an invitation WorkOS refused leaves nothing behind")
	}
	mode = "fail"
	send("other@example.com", "preparer", 502)
	send("other@example.com", "preparer", 409)
	_, err = db.Exec(`UPDATE member_invitations SET created_at=now()-interval '1 minute' WHERE organization_id=$1 AND state='sending'`, org)
	must(err)
	candidate := func(id, inviter string) string {
		return `{"id":"` + id + `","state":"pending","email":"other@example.com","organization_id":"` + org + `","role_slug":"preparer","inviter_user_id":"` + inviter + `","created_at":"` + time.Now().UTC().Format(time.RFC3339) + `","expires_at":"` + expires + `","token":"tok_` + id + `"}`
	}
	call("user_admin", "GET", path, nil, 200)
	listed["other@example.com"] = candidate("invitation_other", "user_someone_else")
	call("user_admin", "GET", path, nil, 200)
	if s, _ := state("other@example.com"); s != "sending" {
		t.Fatalf("absence, or an invitation another administrator sent, settles nothing: %s", s)
	}
	listed["other@example.com"] = candidate("invitation_one", "user_admin") + "," + candidate("invitation_two", "user_admin")
	call("user_admin", "GET", path, nil, 200)
	if s, _ := state("other@example.com"); s != "sending" {
		t.Fatalf("two matching invitations are ambiguous and settle nothing: %s", s)
	}
	listed["other@example.com"] = candidate("invitation_listed", "user_admin")
	call("user_admin", "GET", path, nil, 200)
	if s, id := state("other@example.com"); s != "pending" || id != "invitation_listed" {
		t.Fatalf("an unconfirmed send is settled from the matching WorkOS invitation: %s %s", s, id)
	}
	if to, inviter, token := emailed("invitation_listed"); to != "other@example.com" || inviter != "Ada Admin" || token != "tok_invitation_listed" {
		t.Fatalf("a settled send has Krith's email queued then: %q %q %q", to, inviter, token)
	}
	send("late@example.com", "preparer", 502)
	var late string
	must(db.QueryRow(`SELECT id::text FROM member_invitations WHERE organization_id=$1 AND email='late@example.com'`, org).Scan(&late))
	if body := call("user_admin", "POST", path, map[string]any{"action": "revoke", "id": late, "confirmed": true}, 200); !strings.Contains(string(body), "still being sent") {
		t.Fatalf("revoking an unconfirmed send says WorkOS is settled later: %s", body)
	}
	_, err = db.Exec(`UPDATE member_invitations SET created_at=now()-interval '1 minute' WHERE id::text=$1`, late)
	must(err)
	listed["late@example.com"] = strings.ReplaceAll(candidate("invitation_late", "user_admin"), "other@example.com", "late@example.com")
	call("user_admin", "GET", path, nil, 200)
	if s, id := state("late@example.com"); s != "revoked" || id != "invitation_late" || revoked != "/user_management/invitations/invitation_late/revoke" {
		t.Fatalf("a send revoked before WorkOS confirmed it is revoked in WorkOS once found: %s %s %s", s, id, revoked)
	}
	mode = "race"
	send("race@example.com", "preparer", 409)
	if s, id := state("race@example.com"); s != "revoked" || id != "invitation_race" || revoked != "/user_management/invitations/invitation_race/revoke" {
		t.Fatalf("an invitation revoked while it was being sent is revoked in WorkOS too: %s %s %s", s, id, revoked)
	}
	if to, _, _ := emailed("invitation_race"); to != "" {
		t.Fatal("an invitation revoked while it was being sent has no email queued")
	}
	mode = "ok"
	send("new@example.com", "preparer", 200)
	_, err = db.Exec(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,'gone@example.com','preparer','Preparer','invitation_gone','pending','user_admin','Synthetic re-invite',now()+interval '7 days')`, org)
	must(err)
	for _, c := range []struct {
		name                               string
		invitation, acceptedBy, membership string
		verified                           bool
	}{
		{"an invitation not yet accepted", "pending", "user_new", "active", true},
		{"one accepted by someone else", "accepted", "user_other", "active", true},
		{"an inactive WorkOS membership", "accepted", "user_new", "inactive", true},
		{"an unverified address", "accepted", "user_new", "active", false},
	} {
		invState, acceptedBy, status, verified = c.invitation, c.acceptedBy, c.membership, c.verified
		call("user_new", "GET", "/v1/closes/work", nil, 403)
		if s, _ := state("new@example.com"); s != "pending" {
			t.Fatalf("%s enrolls nobody", c.name)
		}
	}
	invState, acceptedBy, status, verified = "accepted", "user_new", "active", true
	call("user_new", "GET", "/v1/closes/work", nil, 200)
	var reason, actor string
	must(db.QueryRow(`SELECT reason,actor FROM current_organization_members WHERE organization_id=$1 AND subject='user_new' AND granted`, org).Scan(&reason, &actor))
	if s, _ := state("new@example.com"); s != "accepted" || actor != "user_admin" || !strings.Contains(reason, "Joining the audit team") {
		t.Fatalf("an invitee WorkOS shows accepted joins on their first request, recorded under the inviter: %s %s %s", s, actor, reason)
	}
	before := lookups
	acceptedBy = "user_gone"
	call("user_gone", "GET", "/v1/closes/work", nil, 403)
	if lookups != before {
		t.Fatal("a member the firm revoked is never re-enrolled by an invitation, nor looked up")
	}
	t.Setenv("WORKOS_API_KEY", "")
	if body := call("user_admin", "GET", "/v1/closes/members/roles", nil, 200); !strings.Contains(string(body), `"configured":false`) {
		t.Fatalf("without the key invitations are off: %s", body)
	}
	send("third@example.com", "preparer", 503)
}
