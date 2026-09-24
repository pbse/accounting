package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestOwnerDraftsNeedTheClientsOwnEntityType(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Owner client " + rand.Text()
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM procedure_versions WHERE kind='owner' AND organization_id='local-development' AND (client_entity=$1 OR reason='Synthetic owner policy')`, entity)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("o", 32)
	handler := apiWithStore(db, token)
	call := func(path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	input := map[string]any{"entity": entity, "close_period": "2026-09", "entity_type": "s_corp", "transaction_reference": "Purchase 1072", "transaction_date": "2026-09-18", "payee": "Bistro", "amount": "86.40", "ledger_reference": "GL p3",
		"direction": "personal_paid_by_business", "purpose_status": "answered", "stated_purpose": "Family dinner, personal", "client_confirmation_reference": "Client reply", "treatment": "distribution", "business_use_percent": 0,
		"booked_account": "Meals", "owner_account": "Shareholder distributions", "method": "owner_reclass_v1", "treatment_confirmed": true}
	save := map[string]any{"kind": "owner", "input": input}
	path := "/v1/closes/" + closeID + "/workpapers"
	version := func(client, method string) {
		t.Helper()
		if _, e := db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES('local-development',$1,'owner','Use Shareholder distributions for S corporations.',true,'Synthetic owner policy','fixture',$2)`, client, method); e != nil {
			t.Fatal(e)
		}
	}
	// Publishing refuses an entity type on the firm default; inserted directly, it still must not count.
	version("", "s_corp")
	if body := call(path, save, 409); !strings.Contains(string(body), "Record this client's entity type") {
		t.Fatalf("a firm policy must not supply a client's entity type: %s", body)
	}
	version(entity, "sole_proprietor")
	if body := call(path, save, 409); !strings.Contains(string(body), "sole_proprietor") {
		t.Fatalf("a draft must use the client's recorded entity type: %s", body)
	}
	version(entity, "s_corp")
	call(path, save, 200)
	question := map[string]any{"entity": entity, "close_period": "2026-09", "entity_type": "s_corp", "transaction_reference": "Purchase 1073", "transaction_date": "2026-09-19", "payee": "Garage", "amount": "120.00", "ledger_reference": "GL p3", "direction": "personal_paid_by_business", "purpose_status": "open", "business_use_percent": 0, "method": "owner_reclass_v1"}
	var saved struct {
		Paper struct {
			Question string   `json:"question"`
			Blockers []string `json:"blockers"`
		} `json:"paper"`
	}
	json.Unmarshal(call(path, map[string]any{"kind": "owner", "input": question}, 200), &saved)
	if !strings.Contains(saved.Paper.Question, "$120.00 payment to Garage") || len(saved.Paper.Blockers) != 1 {
		t.Fatalf("an unanswered purpose saves as a question: %+v", saved.Paper)
	}
	for _, bad := range []map[string]any{
		{"client_entity": entity, "kind": "owner", "method": "", "instructions": "x", "enabled": true, "reason": "Synthetic", "confirmed": true},
		{"client_entity": "", "kind": "owner", "method": "s_corp", "instructions": "x", "enabled": true, "reason": "Synthetic", "confirmed": true},
		{"client_entity": entity, "kind": "owner", "method": "llc", "instructions": "x", "enabled": true, "reason": "Synthetic", "confirmed": true},
	} {
		call("/v1/closes/procedures", bad, 422)
	}
}
