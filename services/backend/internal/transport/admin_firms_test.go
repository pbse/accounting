package transport

import (
	"crypto/rand"
	"strings"
	"testing"
)

func TestStaffManageAFirmAndClientTextStaysMasked(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	t.Setenv("WORKOS_API_KEY", "fixture")
	token := strings.Repeat("f", 32)
	firm := "org_Firm" + rand.Text()
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name) VALUES($1,'Managed firm')`, firm)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM member_invitations WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`,
			`DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`} {
			db.Exec(q, firm)
		}
	})
	must(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_owner',true,'Founding partner of the firm','fixture'),($1,'user_leaver',true,'Hired for the Acme Secret Client','fixture')`, firm)
	must(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_leaver','Acme Secret Client',true,'Assigned to the payroll close','fixture')`, firm)
	must(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,state,invited_by,reason) VALUES($1,'new@firm.example','member','Member','sending','user_owner','Joining the team next week')`, firm)
	must(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,'late@firm.example','member','Member',$2,'pending','user_owner','Joining the team last month',now()-interval '1 day')`, firm, "invitation_"+rand.Text())
	must(`INSERT INTO workflow_runs(organization_id,workflow,idempotency_key,started_by,input,state,finished_at) VALUES($1,'test_admin_flow',$2,'schedule','{}','completed',now())`, firm, rand.Text())
	h := apiWithStore(db, token)
	base := "/v1/admin/firms/" + firm

	// People: identities, decisions and assignment counts show; reasons and client names only when revealed.
	people := mustString(adminJSON(t, h, token, "GET", base+"/people", "", 200))
	if strings.Contains(people, "Acme Secret") || strings.Contains(people, "Founding partner") || !strings.Contains(people, `"subject":"user_leaver"`) || !strings.Contains(people, "new@firm.example") {
		t.Fatalf("people must show identities and mask client text: %s", people)
	}
	if !strings.Contains(people, `"state":"expired"`) {
		t.Fatalf("an invitation past its expiry is listed as expired: %s", people)
	}
	adminJSON(t, h, token, "GET", base+"/people?reveal=1", "", 422)
	if revealed := mustString(adminJSON(t, h, token, "GET", base+"/people?reveal=1", "", 200, "X-Access-Reason", "the firm asked who has Acme")); !strings.Contains(revealed, "Acme Secret Client") {
		t.Fatalf("a reveal shows client text: %s", revealed)
	}

	// Activity: kinds and workflow runs show; the firm's records are masked until revealed; kinds filter.
	activity := mustString(adminJSON(t, h, token, "GET", base+"/activity", "", 200))
	if strings.Contains(activity, "Acme Secret") || !strings.Contains(activity, `"kind":"member.enrolled"`) || !strings.Contains(activity, `"kind":"workflow.completed"`) {
		t.Fatalf("activity must show kinds and mask client text: %s", activity)
	}
	if only := mustString(adminJSON(t, h, token, "GET", base+"/activity?kind=client", "", 200)); strings.Contains(only, "member.") || !strings.Contains(only, "client.assigned") {
		t.Fatalf("kind filter: %s", only)
	}
	if only := mustString(adminJSON(t, h, token, "GET", base+"/activity?kind=%25", "", 200)); strings.Contains(only, "kind") {
		t.Fatalf("a kind filter is a plain prefix, not a pattern: %s", only)
	}
	if revealed := mustString(adminJSON(t, h, token, "GET", base+"/activity?reveal=1", "", 200, "X-Access-Reason", "tracing a support ticket")); !strings.Contains(revealed, "Acme Secret Client") {
		t.Fatalf("a reveal shows the activity text: %s", revealed)
	}

	// Suspend and resume need the typed firm ID and a reason; doing it twice is refused.
	adminJSON(t, h, token, "POST", base+"/suspend", `{"confirm":"org_other","reason":"Chargeback on the account"}`, 422)
	adminJSON(t, h, token, "POST", base+"/suspend", `{"confirm":"`+firm+`","reason":"short"}`, 422)
	adminJSON(t, h, token, "POST", base+"/suspend", `{"confirm":"`+firm+`","reason":"Chargeback on the account"}`, 200)
	adminJSON(t, h, token, "POST", base+"/suspend", `{"confirm":"`+firm+`","reason":"Chargeback on the account"}`, 409)
	var enabled bool
	if db.QueryRow(`SELECT enabled FROM organizations WHERE id=$1`, firm).Scan(&enabled); enabled {
		t.Fatal("the firm must be suspended")
	}
	if detail := mustString(adminJSON(t, h, token, "GET", base, "", 200)); strings.Contains(detail, "Chargeback") || !strings.Contains(detail, `"last_suspension":{`) {
		t.Fatalf("the firm shows when it was suspended, not why: %s", detail)
	}
	adminJSON(t, h, token, "POST", base+"/resume", `{"confirm":"`+firm+`","reason":"Chargeback settled with the bank"}`, 200)
	if db.QueryRow(`SELECT enabled FROM organizations WHERE id=$1`, firm).Scan(&enabled); !enabled {
		t.Fatal("the firm must be active again")
	}
	adminJSON(t, h, token, "POST", "/v1/admin/firms/org_Missing/suspend", `{"confirm":"org_Missing","reason":"Chargeback on the account"}`, 404)

	// Revoking a member needs the decision staff saw; a stale one is refused.
	var current string
	if err := db.QueryRow(`SELECT id::text FROM current_organization_members WHERE organization_id=$1 AND subject='user_leaver'`, firm).Scan(&current); err != nil {
		t.Fatal(err)
	}
	adminJSON(t, h, token, "POST", base+"/members/user_leaver/revoke", `{"previous_id":"1","reason":"Left the firm last Friday"}`, 409)
	adminJSON(t, h, token, "POST", base+"/members/user_stranger/revoke", `{"previous_id":"","reason":"Never belonged to the firm"}`, 409)
	adminJSON(t, h, token, "POST", base+"/members/user_leaver/revoke", `{"previous_id":"`+current+`","reason":"Left the firm last Friday"}`, 200)
	var granted bool
	if db.QueryRow(`SELECT granted FROM current_organization_members WHERE organization_id=$1 AND subject='user_leaver'`, firm).Scan(&granted); granted {
		t.Fatal("the member must be revoked")
	}
	adminJSON(t, h, token, "POST", base+"/members/user_leaver/revoke", `{"previous_id":"`+current+`","reason":"Left the firm last Friday"}`, 409)
	var strangers int
	if db.QueryRow(`SELECT count(*) FROM organization_member_access WHERE organization_id=$1 AND subject='user_stranger'`, firm).Scan(&strangers); strangers != 0 {
		t.Fatal("revoking a stranger must not record anything")
	}

	// An open invitation is revoked once.
	var invitation string
	if err := db.QueryRow(`SELECT id::text FROM member_invitations WHERE organization_id=$1 AND state='sending'`, firm).Scan(&invitation); err != nil {
		t.Fatal(err)
	}
	adminJSON(t, h, token, "POST", base+"/invitations/"+invitation+"/revoke", `{"reason":"Sent to the wrong address"}`, 200)
	adminJSON(t, h, token, "POST", base+"/invitations/"+invitation+"/revoke", `{"reason":"Sent to the wrong address"}`, 409)

	// Without a WorkOS key, the app still revokes; the answer says to finish in WorkOS.
	t.Setenv("WORKOS_API_KEY", "")
	var pending string
	if err := db.QueryRow(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,'open@firm.example','member','Member',$2,'pending','user_owner','Joining the team next month',now()+interval '1 day') RETURNING id::text`, firm, "invitation_"+rand.Text()).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if answer := mustString(adminJSON(t, h, token, "POST", base+"/invitations/"+pending+"/revoke", `{"reason":"Sent to the wrong address"}`, 200)); !strings.Contains(answer, "no WorkOS key") {
		t.Fatalf("the answer must say WorkOS still needs revoking: %s", answer)
	}

	// Every action and reveal is in the firm's audit, refusals included; it pages by ID.
	if page := adminJSON(t, h, token, "GET", base+"/audit?before=2", "", 200); len(page["entries"].([]any)) != 0 {
		t.Fatalf("before pages by ID: %v", page)
	}
	audit := mustString(adminJSON(t, h, token, "GET", base+"/audit", "", 200))
	if strings.Contains(audit, "Chargeback") || !strings.Contains(audit, `"previous_id":"••••"`) || !strings.Contains(audit, `"error":"••••"`) {
		t.Fatalf("the audit masks request details and errors until revealed: %s", audit)
	}
	if revealed := mustString(adminJSON(t, h, token, "GET", base+"/audit?reveal=1", "", 200, "X-Access-Reason", "reviewing staff actions")); !strings.Contains(revealed, `"previous_id":"`+current+`"`) || !strings.Contains(revealed, "Chargeback") {
		t.Fatalf("a reveal shows the request details: %s", revealed)
	}
	for _, want := range []string{`"action":"firm.suspend"`, `"action":"firm.resume"`, `"action":"member.revoke"`, `"result":"refused"`, `"action":"invitation.revoke"`, `"action":"content.reveal"`} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit lacks %s: %s", want, audit)
		}
	}
}

func TestReadOnlyStaffCannotChangeAFirm(t *testing.T) {
	db := adminDB(t)
	sign := signer(t)
	staffOrg := "org_Staff" + rand.Text()
	t.Setenv("PLATFORM_ORGANIZATION_ID", staffOrg)
	firm := "org_Read" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Read-only view')`, firm); err != nil {
		t.Fatal(err)
	}
	h := apiWithStore(db, strings.Repeat("x", 32))
	reader := sign(staffOrg, "platform:read")
	for _, path := range []string{"/suspend", "/resume", "/members/user_a/revoke", "/invitations/1/revoke"} {
		adminJSON(t, h, reader, "POST", "/v1/admin/firms/"+firm+path, `{"confirm":"`+firm+`","reason":"Trying without permission"}`, 403)
	}
	adminJSON(t, h, reader, "GET", "/v1/admin/firms/"+firm+"/people?reveal=1", "", 403, "X-Access-Reason", "curious")
	adminJSON(t, h, reader, "GET", "/v1/admin/firms/"+firm+"/activity?reveal=1", "", 403, "X-Access-Reason", "curious")
}
