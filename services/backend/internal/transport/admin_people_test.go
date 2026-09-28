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
	// A session the API holds for them (AUTH_SESSION=server).
	must(`INSERT INTO auth_sessions(id_hash,subject,organization_id,workos_session,email,refresh_token,access_token,access_expires_at,expires_at) VALUES($1,$2,$3,'session_1',$4,'\x00','\x00',now()+interval '5 minutes',now()+interval '30 days')`, rand.Text(), person, firm, email)
	must(`INSERT INTO auth_sessions(id_hash,subject,organization_id,workos_session,email,refresh_token,access_token,access_expires_at,expires_at) VALUES($1,$2,$3,'session_expired',$4,'\x00','\x00',now()-interval '2 days',now()-interval '1 day')`, rand.Text(), person, firm, email)
	t.Cleanup(func() { db.Exec(`DELETE FROM auth_sessions WHERE subject=$1`, person) })

	revoked, resent := []string{}, 0
	failRevoke, workosDown, resendLost, copyLost, loseCopyOnResend := false, false, false, false, false
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.workos.com" || r.Header.Get("Authorization") != "Bearer sk_test_people" {
			t.Fatalf("unexpected call %s", r.URL)
		}
		code, body := 200, `{}`
		// Each resend moves WorkOS's expiry an hour on, so its copy tells one resend from the next.
		copyOf := func() string {
			return `{"id":"` + workosID + `","state":"pending","expires_at":"` + time.Now().Add(7*24*time.Hour+time.Duration(resent)*time.Hour).UTC().Format(time.RFC3339) + `"}`
		}
		switch p := r.URL.Path; {
		case workosDown:
			code = 503
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
		case p == "/user_management/users/"+person+"/sessions" && r.URL.Query().Get("after") == "session_2":
			body = `{"data":[{"id":"session_3","status":"active"}],"list_metadata":{}}`
		case p == "/user_management/users/"+person+"/sessions":
			body = `{"data":[{"id":"session_1","status":"active","auth_method":"password","ip_address":"203.0.113.9","user_agent":"Secret Browser"},{"id":"session_2","status":"revoked"}],"list_metadata":{"after":"session_2"}}`
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
			body = copyOf()
			if resendLost {
				code = 504
			}
			copyLost = loseCopyOnResend
		case p == "/user_management/invitations/"+workosID && r.Method == "GET":
			body = copyOf()
			if copyLost {
				code = 503
			}
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
	// Their firms' invitations are reconciled first, as the firm's own list does: a pending one past its expiry expires.
	must(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,$2,'member','Member',$3,'pending','user_owner','Joining the team last month',now()-interval '1 day')`, other, email, "invitation_"+rand.Text())
	adminJSON(t, h, token, "GET", "/v1/admin/people?q="+email, "", 200)
	var stale int
	if db.QueryRow(`SELECT count(*) FROM member_invitations WHERE organization_id=$1 AND state='pending'`, other).Scan(&stale); stale != 0 {
		t.Fatal("a pending invitation past its expiry must be expired before it is listed")
	}
	// By user ID, their invitations are found by the email WorkOS knows them by.
	if byID := mustString(adminJSON(t, h, token, "GET", "/v1/admin/people?q="+person, "", 200)["invitations"]); !strings.Contains(byID, email) {
		t.Fatalf("invitations by the person's WorkOS email: %s", byID)
	}
	// An earlier account that used the same address is not this user: a user-ID search does not widen to it.
	previous := "user_previous" + strings.ToLower(rand.Text())
	must(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'Earlier owner of the address','fixture')`, other, previous)
	must(`INSERT INTO auth_sessions(id_hash,subject,organization_id,workos_session,email,refresh_token,access_token,access_expires_at,expires_at) VALUES($1,$2,$3,'session_old',$4,'\x00','\x00',now(),now()+interval '1 day')`, rand.Text(), previous, other, email)
	t.Cleanup(func() { db.Exec(`DELETE FROM auth_sessions WHERE subject=$1`, previous) })
	must(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,subject,decided_at) VALUES($1,$2,'member','Member',$3,'accepted','user_owner','Joined years ago',$4,now())`, other, email, "invitation_"+rand.Text(), previous)
	// An address the user signed in with once, now someone else's, is not theirs: its open invitation does not show.
	former := strings.ToLower(rand.Text()) + "@former.example"
	must(`INSERT INTO auth_sessions(id_hash,subject,organization_id,workos_session,email,refresh_token,access_token,access_expires_at,expires_at) VALUES($1,$2,$3,'session_former',$4,'\x00','\x00',now(),now()-interval '1 hour')`, rand.Text(), person, firm, former)
	must(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,state,invited_by,reason) VALUES($1,$2,'member','Member','sending','user_owner','Invited the new owner of the address')`, other, former)
	if byID := mustString(adminJSON(t, h, token, "GET", "/v1/admin/people?q="+person, "", 200)["invitations"]); strings.Contains(byID, former) {
		t.Fatalf("a former address's invitation is not this user's: %s", byID)
	}
	if byID := mustString(adminJSON(t, h, token, "GET", "/v1/admin/people?q="+person, "", 200)); strings.Contains(byID, previous) || strings.Contains(byID, `"state":"accepted"`) {
		t.Fatalf("a user-ID search must not include another account's memberships or accepted invitations: %s", byID)
	}
	// When WorkOS does not answer, an email search still finds their memberships through the app's own records.
	workosDown = true
	if down := mustString(adminJSON(t, h, token, "GET", "/v1/admin/people?q="+email, "", 200)); !strings.Contains(down, `"workos":"unavailable"`) || !strings.Contains(down, "Current firm") {
		t.Fatalf("app records during a WorkOS outage: %s", down)
	}
	workosDown = false
	adminJSON(t, h, token, "GET", "/v1/admin/people?q=ab", "", 422)
	adminJSON(t, h, token, "GET", "/v1/admin/people?q=Acme+Payroll", "", 422)
	if nobody := adminJSON(t, h, token, "GET", "/v1/admin/people?q=user_nobody", "", 200); len(nobody["users"].([]any)) != 0 {
		t.Fatalf("an unknown user: %v", nobody)
	}

	// Ending sessions ends the app's own at once, then every active WorkOS session on every page. When WorkOS fails part
	// way, the answer says exactly what ended; ending again finishes.
	adminJSON(t, h, token, "POST", "/v1/admin/people/"+person+"/sessions/revoke", `{"reason":"short"}`, 422)
	failRevoke = true
	if partial := adminJSON(t, h, token, "POST", "/v1/admin/people/"+person+"/sessions/revoke", `{"reason":"Laptop reported stolen"}`, 502)["message"].(string); !strings.Contains(partial, "own sessions for this person ended (1)") || !strings.Contains(partial, "ended 1 active session(s), then failed") {
		t.Fatalf("partial answer: %s", partial)
	}
	var appRevoked bool
	if db.QueryRow(`SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE subject=$1 AND workos_session='session_1'`, person).Scan(&appRevoked); !appRevoked {
		t.Fatal("the app's own session must end even when WorkOS fails")
	}
	failRevoke, revoked = false, nil
	if ended := adminJSON(t, h, token, "POST", "/v1/admin/people/"+person+"/sessions/revoke", `{"reason":"Laptop reported stolen"}`, 200)["ended"]; ended != float64(2) || strings.Join(revoked, ",") != "session_1,session_3" {
		t.Fatalf("ended %v, revoked %v", ended, revoked)
	}
	var sessionAudits string
	if err := db.QueryRow(`SELECT string_agg(action||':'||result||':'||COALESCE(organization_id,'platform'),',' ORDER BY id) FROM platform_audit WHERE action LIKE 'sessions.%' AND target_id=$1 AND reason<>'short'`, person).Scan(&sessionAudits); err != nil ||
		sessionAudits != "sessions.revoke:ok:platform,sessions.revoke_workos:refused:platform,sessions.revoke:ok:platform,sessions.revoke_workos:ok:platform" {
		t.Fatalf("session audits %q %v", sessionAudits, err)
	}

	// Resending a pending invitation keeps WorkOS's new expiry; a closed one is refused.
	var invitation string
	if err := db.QueryRow(`SELECT id::text FROM member_invitations WHERE workos_id=$1`, workosID).Scan(&invitation); err != nil {
		t.Fatal(err)
	}
	// WorkOS sent it but the answer was lost: its copy's later expiry shows it went, so it is kept, not refused.
	resendLost = true
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/invitations/"+invitation+"/resend", `{"reason":"They lost the first email"}`, 200)
	resendLost = false
	var later bool
	if db.QueryRow(`SELECT expires_at>now()+interval '6 days' FROM member_invitations WHERE id::text=$1`, invitation).Scan(&later); !later || resent != 1 {
		t.Fatalf("the new expiry is kept (%v) after one resend (%d)", later, resent)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/invitations/"+invitation+"/resend", `{"reason":"They lost the first email"}`, 200)
	// When neither the answer nor WorkOS's copy can be read, the outcome is reported as unknown, not as nothing sent.
	resendLost, loseCopyOnResend = true, true
	if unknown := adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/invitations/"+invitation+"/resend", `{"reason":"They lost the first email"}`, 502)["message"].(string); !strings.Contains(unknown, "did not confirm whether") {
		t.Fatalf("an unknown outcome: %s", unknown)
	}
	resendLost, loseCopyOnResend, copyLost = false, false, false
	must(`UPDATE member_invitations SET state='revoked' WHERE id::text=$1`, invitation)
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/invitations/"+invitation+"/resend", `{"reason":"They lost the first email"}`, 409)
	if resent != 3 {
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
