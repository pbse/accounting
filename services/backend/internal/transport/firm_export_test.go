package transport

import (
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAFirmDownloadsItsRecordsAtExit(t *testing.T) {
	db := adminDB(t)
	token := strings.Repeat("x", 32)
	h := apiWithStore(db, token)
	entity := "Exit " + rand.Text()
	closeID := adminJSON(t, h, token, "POST", "/v1/closes", `{"entity":"`+entity+`","period":"2026-08"}`, 200)["id"].(string)
	var org, sender string
	if err := db.QueryRow(`SELECT organization_id FROM client_closes WHERE id::text=$1`, closeID).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,webhook_secret,enabled,actor) VALUES($1,$2,'synthetic','api-key-secret','hook-secret',false,'fixture') RETURNING id::text`, org, "exit-"+strings.ToLower(rand.Text())+"@example.test").Scan(&sender); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM close_effort_entries WHERE note=$1`, "Exit setup for "+entity)
		db.Exec(`DELETE FROM procedure_versions WHERE client_entity=$1`, entity)
		db.Exec(`DELETE FROM email_connections WHERE id=$1::bigint`, sender)
		db.Exec(`DELETE FROM client_closes WHERE entity=$1`, entity)
	})
	if _, err := db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor) VALUES($1,$2,'prepaid','Amortize monthly',true,'Exit fixture','fixture')`, org, entity); err != nil {
		t.Fatal(err)
	}
	setup := func() float64 {
		t.Helper()
		return adminJSON(t, h, token, "GET", "/v1/closes/effort", "", 200)["firm_assistance_minutes"].(float64)
	}
	before := setup()
	if _, err := db.Exec(`INSERT INTO close_effort_entries(organization_id,actor,activity,minutes,note) VALUES($1,'staff:fixture','assistance',45,$2)`, org, "Exit setup for "+entity); err != nil {
		t.Fatal(err)
	}
	if after := setup(); after != before+45 {
		t.Fatalf("our team's firm-level setup time is reported to the firm: %v then %v", before, after)
	}

	r := httptest.NewRequest("GET", "/v1/closes/firm-export", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("the firm export downloads: %d %s", w.Code, body[:min(len(body), 300)])
	}
	if strings.Contains(body, "api-key-secret") || strings.Contains(body, "hook-secret") || strings.Contains(body, `"credential"`) || strings.Contains(body, `"webhook_secret"`) {
		t.Fatal("the export never carries the sender's secrets")
	}
	var out struct {
		Records map[string]json.RawMessage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ key, text string }{{"closes", `"package":"/v1/closes/` + closeID + `/export"`}, {"procedure_versions", `"client_entity":"` + entity + `"`}, {"email_senders", `"id":` + sender + `,`}, {"firm", `"support_owner"`}} {
		if !strings.Contains(string(out.Records[want.key]), want.text) {
			t.Fatalf("the export has %s with %s: %.400s", want.key, want.text, out.Records[want.key])
		}
	}
	if policy := mustString(adminJSON(t, h, token, "GET", "/v1/closes/organization/policy", "", 200)); !strings.Contains(policy, `"support_owner":`) || !strings.Contains(policy, `"can_read_roster":true`) || !strings.Contains(policy, `"can_manage_members":false`) {
		t.Fatalf("the firm's settings show its support owner; the local workspace reads the roster but manages no members: %s", policy)
	}
}
