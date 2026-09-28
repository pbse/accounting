package transport

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStaffFindAPersonAcrossFirmsAndEndTheirSessions(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	t.Setenv("WORKOS_API_KEY", "sk_test_people")
	token := strings.Repeat("p", 32)
	firm, other := "org_People"+rand.Text(), "org_Former"+rand.Text()
	person := "user_person" + strings.ToLower(rand.Text())
	email := strings.ToLower(rand.Text()) + "@person.example"
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name) VALUES($1,'Current firm'),($2,'Former firm')`, firm, other)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM member_invitations WHERE organization_id IN ($1,$2)`, `DELETE FROM client_access WHERE organization_id IN ($1,$2)`, `DELETE FROM organization_member_access WHERE organization_id IN ($1,$2)`} {
			db.Exec(q, firm, other)
		}
	})
	must(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$3,true,'Hired for the Acme Secret Client','fixture'),($2,$3,false,'Left for another firm','fixture')`, firm, other, person)
	must(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,'Acme Secret Client',true,'Assigned to the payroll close','fixture')`, firm, person)
	workosID := "invitation_" + rand.Text()
	expires := time.Now().Add(24 * time.Hour)
	must(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,$2,'member','Member',$3,'pending','user_owner','Joining the team next week',$4)`, firm, email, workosID, expires)

	revoked, resent := []string{}, 0
	failRevoke := false
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.workos.com" || r.Header.Get("Authorization") != "Bearer sk_test_people" {
			t.Fatalf("unexpected call %s", r.URL)
		}
		code, body := 200, `{}`
		switch p := r.URL.Path; {
		case p == "/user_management/users" && r.URL.Query().Get("email") == email:
			body = `{"data":[{"id":"` + person + `","email":"` + email + `","first_name":"Pat","last_name":"Person"}],"list_metadata":{}}`
		case p == "/user_management/users":
			body = `{"data":[],"list_metadata":{}}`
		case p == "/user_management/users/"+person:
			body = `{"id":"` + person + `","email":"` + email + `","first_name":"Pat","last_name":"Person"}`
		case strings.HasPrefix(p, "/user_management/users/") && !strings.HasSuffix(p, "/sessions"):
			code = 404
		case p == "/user_management/organization_memberships":
			body = `{"data":[{"organization_id":"` + firm + `","status":"active","role":{"slug":"member"}}],"list_metadata":{}}`
		case p == "/user_management/users/"+person+"/sessions":
			body = `{"data":[{"id":"session_1","status":"active","auth_method":"password","ip_address":"203.0.113.9","user_agent":"Secret Browser"},{"id":"session_2","status":"revoked"},{"id":"session_3","status":"active"}]}`
		case p == "/user_management/sessions/revoke" && r.Method == "POST":
			var in map[string]string
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &in)
			if failRevoke && in["session_id"] == "session_3" {
				code = 500
			} else {
				revoked = append(revoked, in["session_id"])
			}
		case p == "/user_management/invitations/"+workosID+"/resend" && r.Method == "POST":
			resent++
			body = `{"id":"` + workosID + `","state":"pending","expires_at":"` + time.Now().Add(7*24*time.Hour).UTC().Format(time.RFC3339) + `"}`
		default:
			t.Fatalf("unexpected call %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	h := apiWithStore(db, token)

	// By email and by user ID: WorkOS's account, memberships and sessions, and the app's records in every firm.
	for _, q := range []string{email, strings.ToUpper(email), person} {
		found := mustString(adminJSON(t, h, token, "GET", "/v1/admin/people?q="+q, "", 200))
		for _, want := range []string{`"workos":"ok"`, `"first_name":"Pat"`, `"firm_name":"Current firm"`, `"firm_name":"Former firm"`, `"granted":false`, `"assignments":1`, `"status":"active"`} {
			if !strings.Contains(found, want) {
				t.Fatalf("%s: %s lacks %s", q, found, want)
			}
		}
		if strings.Contains(found, "Acme") || strings.Contains(found, "another firm") || strings.Contains(found, "203.0.113.9") || strings.Contains(found, "Secret Browser") {
			t.Fatalf("reasons, client names and where people signed in from never show: %s", found)
		}
	}
	if byEmail := mustString(adminJSON(t, h, token, "GET", "/v1/admin/people?q="+email, "", 200)["invitations"]); !strings.Contains(byEmail, `"state":"pending"`) {
		t.Fatalf("the person's invitations are listed: %s", byEmail)
	}
	adminJSON(t, h, token, "GET", "/v1/admin/people?q=ab", "", 422)
	adminJSON(t, h, token, "GET", "/v1/admin/people?q=Acme+Payroll", "", 422)
	if nobody := adminJSON(t, h, token, "GET", "/v1/admin/people?q=user_nobody", "", 200); len(nobody["users"].([]any)) != 0 {
		t.Fatalf("an unknown user: %v", nobody)
	}

	// Ending sessions ends the active ones only, and says how many; a partial failure is refused and audited.
	adminJSON(t, h, token, "POST", "/v1/admin/people/"+person+"/sessions/revoke", `{"reason":"short"}`, 422)
	failRevoke = true
	adminJSON(t, h, token, "POST", "/v1/admin/people/"+person+"/sessions/revoke", `{"reason":"Laptop reported stolen"}`, 502)
	failRevoke, revoked = false, nil
	if ended := adminJSON(t, h, token, "POST", "/v1/admin/people/"+person+"/sessions/revoke", `{"reason":"Laptop reported stolen"}`, 200)["ended"]; ended != float64(2) || strings.Join(revoked, ",") != "session_1,session_3" {
		t.Fatalf("ended %v, revoked %v", ended, revoked)
	}
	var sessionAudits string
	if err := db.QueryRow(`SELECT string_agg(result||':'||COALESCE(organization_id,'platform'),',' ORDER BY id) FROM platform_audit WHERE action='sessions.revoke' AND target_id=$1`, person).Scan(&sessionAudits); err != nil || sessionAudits != "refused:platform,ok:platform" {
		t.Fatalf("session audits %q %v", sessionAudits, err)
	}

	// Resending a pending invitation keeps WorkOS's new expiry; a closed one is refused.
	var invitation string
	if err := db.QueryRow(`SELECT id::text FROM member_invitations WHERE workos_id=$1`, workosID).Scan(&invitation); err != nil {
		t.Fatal(err)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/invitations/"+invitation+"/resend", `{"reason":"They lost the first email"}`, 200)
	var later bool
	if db.QueryRow(`SELECT expires_at>now()+interval '6 days' FROM member_invitations WHERE id::text=$1`, invitation).Scan(&later); !later || resent != 1 {
		t.Fatalf("the new expiry is kept (%v) after one resend (%d)", later, resent)
	}
	must(`UPDATE member_invitations SET state='revoked' WHERE id::text=$1`, invitation)
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/invitations/"+invitation+"/resend", `{"reason":"They lost the first email"}`, 409)
	if resent != 1 {
		t.Fatal("a revoked invitation must not reach WorkOS")
	}

	// Without WorkOS the app's records still show; WorkOS actions say what they need.
	t.Setenv("WORKOS_API_KEY", "")
	if found := mustString(adminJSON(t, h, token, "GET", "/v1/admin/people?q="+person, "", 200)); !strings.Contains(found, `"workos":"not_configured"`) || !strings.Contains(found, "Current firm") {
		t.Fatalf("app records without WorkOS: %s", found)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/people/"+person+"/sessions/revoke", `{"reason":"Laptop reported stolen"}`, 503)
}

func TestReadOnlyStaffCannotEndSessionsOrResend(t *testing.T) {
	db := adminDB(t)
	sign := signer(t)
	staffOrg := "org_Staff" + rand.Text()
	t.Setenv("PLATFORM_ORGANIZATION_ID", staffOrg)
	t.Setenv("WORKOS_API_KEY", "sk_test_people")
	h := apiWithStore(db, strings.Repeat("x", 32))
	reader := sign(staffOrg, "platform:read")
	adminJSON(t, h, reader, "POST", "/v1/admin/people/user_a/sessions/revoke", `{"reason":"Trying without permission"}`, 403)
	adminJSON(t, h, reader, "POST", "/v1/admin/firms/org_Any/invitations/1/resend", `{"reason":"Trying without permission"}`, 403)
}
