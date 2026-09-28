package transport

import (
	"crypto/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestContentGrantsStandInForAReasonOnOneFirmForAWhile(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("g", 32)
	firm, other := "org_Grant"+rand.Text(), "org_Other"+rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Granted firm'),($2,'Other firm')`, firm, other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM content_grants WHERE organization_id IN ($1,$2)`, firm, other) })
	h := apiWithStore(db, token)
	people := func(id string) string { return "/v1/admin/firms/" + id + "/people?reveal=1" }

	// Without a reason or a grant, a reveal is refused; another staff member's grant does not count.
	adminJSON(t, h, token, "GET", people(firm), "", 422)
	if _, err := db.Exec(`INSERT INTO content_grants(staff_user,organization_id,reason,expires_at) VALUES('someone_else',$1,'Their own work',now()+interval '1 hour')`, firm); err != nil {
		t.Fatal(err)
	}
	adminJSON(t, h, token, "GET", people(firm), "", 422)

	// A grant: a known length, a reason, a firm that exists.
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/grant", `{"minutes":45,"reason":"Month-end support for the firm"}`, 422)
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/grant", `{"minutes":60,"reason":"short"}`, 422)
	adminJSON(t, h, token, "POST", "/v1/admin/firms/org_Missing/grant", `{"minutes":60,"reason":"Month-end support for the firm"}`, 404)
	grant := int64(adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/grant", `{"minutes":60,"reason":"Month-end support for the firm"}`, 200)["grant_id"].(float64))

	// Under it, this firm's reveals need no reason and record the grant; another firm still asks.
	if got := adminJSON(t, h, token, "GET", people(firm), "", 200); got["revealed"] != true {
		t.Fatalf("revealed under the grant: %v", got)
	}
	var recorded string
	if err := db.QueryRow(`SELECT reason FROM platform_audit WHERE organization_id=$1 AND action='content.reveal' ORDER BY id DESC LIMIT 1`, firm).Scan(&recorded); err != nil || recorded != "Under content grant #"+strconv.FormatInt(grant, 10)+": Month-end support for the firm" {
		t.Fatalf("the reveal names the grant: %q %v", recorded, err)
	}
	adminJSON(t, h, token, "GET", people(other), "", 422)
	if mine := mustString(adminJSON(t, h, token, "GET", "/v1/admin/grants", "", 200)["grants"]); !strings.Contains(mine, `"firm_name":"Granted firm"`) || strings.Contains(mine, "Their own work") {
		t.Fatalf("my open grants: %s", mine)
	}

	// Opening another for the same firm replaces it; ending it early brings the reason back.
	again := int64(adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/grant", `{"minutes":15,"reason":"Only a quick check now"}`, 200)["grant_id"].(float64))
	var open int
	if db.QueryRow(`SELECT count(*) FROM content_grants WHERE organization_id=$1 AND staff_user='local-development' AND ended_at IS NULL`, firm).Scan(&open); open != 1 {
		t.Fatalf("one open grant per firm: %d", open)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/grants/"+strconv.FormatInt(grant, 10)+"/end", `{}`, 404)
	adminJSON(t, h, token, "POST", "/v1/admin/grants/"+strconv.FormatInt(again, 10)+"/end", `{}`, 200)
	adminJSON(t, h, token, "GET", people(firm), "", 422)

	// Openings that overlap still leave one open grant.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/grant", `{"minutes":15,"reason":"Opening at the same time"}`, 200)
		}()
	}
	wg.Wait()
	if db.QueryRow(`SELECT count(*) FROM content_grants WHERE organization_id=$1 AND staff_user='local-development' AND ended_at IS NULL`, firm).Scan(&open); open != 1 {
		t.Fatalf("overlapping openings leave one open grant: %d", open)
	}
	db.Exec(`UPDATE content_grants SET ended_at=now() WHERE organization_id=$1 AND staff_user='local-development' AND ended_at IS NULL`, firm)

	// An expired grant counts for nothing.
	db.Exec(`INSERT INTO content_grants(staff_user,organization_id,reason,created_at,expires_at) VALUES('local-development',$1,'Long ago',now()-interval '2 hours',now()-interval '1 hour')`, firm)
	adminJSON(t, h, token, "GET", people(firm), "", 422)

	var actions string
	if err := db.QueryRow(`SELECT string_agg(action||':'||result,',' ORDER BY id) FROM platform_audit WHERE organization_id=$1 AND action LIKE 'content.grant%'`, firm).Scan(&actions); err != nil || actions != "content.grant:ok,content.grant:ok,content.grant_end:ok,content.grant:ok,content.grant:ok,content.grant:ok,content.grant:ok" {
		t.Fatalf("grant audit %q %v", actions, err)
	}
}

func TestGrantsNeedContentPermission(t *testing.T) {
	db := adminDB(t)
	sign := signer(t)
	staffOrg := "org_Staff" + rand.Text()
	t.Setenv("PLATFORM_ORGANIZATION_ID", staffOrg)
	h := apiWithStore(db, strings.Repeat("x", 32))
	reader := sign(staffOrg, "platform:read", "platform:operate")
	adminJSON(t, h, reader, "GET", "/v1/admin/grants", "", 403)
	adminJSON(t, h, reader, "POST", "/v1/admin/firms/org_Any/grant", `{"minutes":60,"reason":"Month-end support for the firm"}`, 403)
}
