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
	"time"
)

func TestOpenItemsKeepDecisionsAndSettleFromTheLedger(t *testing.T) {
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
	entity := "Open items " + rand.Text()
	var closeID, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM chat_sessions WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM open_item_actions WHERE client_entity=$1`, entity)
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123') RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	capture := func(finished time.Time, completeness string, invoices ...map[string]any) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"finished_at": finished.UTC().Format(time.RFC3339), "completeness": completeness, "open_items": map[string]any{
			"Invoice": invoices,
			"Bill":    []map[string]any{{"Id": "9", "DocNumber": "B-9", "TxnDate": "2026-09-01", "DueDate": "2026-10-15", "Balance": json.Number("250.00"), "VendorRef": map[string]string{"value": "7", "name": "Landlord"}, "CurrencyRef": map[string]string{"value": "USD"}}},
		}})
		_, e := db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',now(),$3)`, closeID, connection, string(payload))
		must(e)
	}
	acme := map[string]any{"Id": "1", "DocNumber": "INV-1", "TxnDate": "2026-08-01", "DueDate": "2026-08-31", "Balance": json.Number("100.00"), "CustomerRef": map[string]string{"value": "5", "name": "Acme"}, "CurrencyRef": map[string]string{"value": "USD"}}
	full := "All supported types were paged."
	capture(time.Now().Add(-time.Hour), full, acme)

	token := strings.Repeat("o", 32)
	handler := apiWithStore(db, token)
	call := func(method string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/closes/"+closeID+"/open-items", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type item struct {
		Kind, ID, Label, Balance, Reason string
		Settled                          bool
		Action                           *openItemAction
	}
	var got struct {
		Source, Company                string
		CaptureID                      string `json:"capture_id"`
		Receivables, Payables, Settled []item
	}
	read := func() {
		t.Helper()
		got.Receivables, got.Payables, got.Settled = nil, nil, nil
		must(json.Unmarshal(call("GET", nil, 200), &got))
	}
	read()
	if got.Source != "quickbooks" || got.Company != "quickbooks:sandbox:123" || got.CaptureID == "" || len(got.Receivables) != 1 || got.Receivables[0].Label != "Invoice INV-1 · Acme" || got.Receivables[0].Action != nil || len(got.Payables) != 1 || got.Payables[0].Balance != "250.00" {
		t.Fatalf("the latest capture's open invoices and bills, with no decisions yet: %+v", got)
	}

	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Bill", "item_id": "9", "status": "promised", "expected_on": "2026-10-20"}, 422)
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "1", "status": "disputed"}, 422)
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "1", "status": "promised"}, 422)
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "1", "status": "written_off"}, 422)
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "404", "status": "following_up"}, 409)
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "1", "status": "promised", "expected_on": "2026-10-10", "note": "Their AP lead promised on the phone", "assign": "take"}, 200)
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "1", "status": "following_up"}, 409)
	// A proposed date does nothing until the person confirms an external handoff; the ordinary route owns freshness.
	args := map[string]any{"item_id": "9", "expected_on": "2026-10-14", "note": "Scheduled in the client's bill-pay tool"}
	if why := billHandoffProposal(string(call("GET", nil, 200)), args); why != "" {
		t.Fatal(why)
	}
	var session string
	must(db.QueryRow(`INSERT INTO chat_sessions(organization_id,author,close_id,title) VALUES('local-development','local-development',$1,'Bill handoff') RETURNING id::text`, closeID).Scan(&session))
	card := func() string {
		t.Helper()
		var id string
		raw, _ := json.Marshal(agentProposal{Action: "bill_pay", Args: args})
		must(db.QueryRow(`INSERT INTO chat_messages(organization_id,author,close_id,role,body,action,proposal,session_id) VALUES('local-development','local-development',$1,'assistant','Proposed date','bill_pay',$2,$3) RETURNING id::text`, closeID, string(raw), session).Scan(&id))
		return id
	}
	confirm := func(id string, handed bool, want int) []byte {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/chat/"+id+"/confirm", strings.NewReader(fmtAny(map[string]bool{"handed_off": handed})))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("confirm %s: %d %s", id, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	id := card()
	confirm(id, false, 422)
	read()
	if got.Payables[0].Action != nil {
		t.Fatal("a proposal changed the bill without handoff confirmation")
	}
	confirm(id, true, 200)
	confirm(id, true, 409)
	if raw := confirm(card(), true, 409); !strings.Contains(string(raw), "new payment-date card") {
		t.Fatalf("a stale card asks for a new proposal, not a futile refresh: %s", raw)
	}

	read()
	promise, handoff := got.Receivables[0].Action, got.Payables[0].Action
	if promise == nil || promise.Status != "promised" || promise.ExpectedOn != "2026-10-10" || promise.Owner == "" || handoff == nil || handoff.Status != "handed_to_bill_pay" || handoff.ExpectedOn != "2026-10-14" || handoff.Owner != "" {
		t.Fatalf("each document keeps its newest decision, owner and date: %+v %+v", promise, handoff)
	}
	// Releasing keeps the decision; a decision is made against the one the person saw.
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "1", "status": "following_up", "assign": "release", "previous_id": promise.ID}, 200)
	read()
	if a := got.Receivables[0].Action; a.Status != "following_up" || a.Owner != "" || a.ExpectedOn != "" {
		t.Fatalf("a later decision replaces the promise and releases the owner: %+v", a)
	}

	// The ledger, not a person, says the invoice is paid, and only a later capture that read every open item can.
	stale := got.CaptureID
	capture(time.Now().Add(time.Minute), full+" Open items were not fully read, so their overdue questions are missing: Open Invoice count: refused.")
	read()
	if len(got.Receivables) != 0 || len(got.Settled) != 1 || got.Settled[0].Settled || !strings.Contains(got.Settled[0].Reason, "did not read every open item") {
		t.Fatalf("an incomplete capture cannot say the invoice is paid: %+v", got)
	}
	call("POST", map[string]any{"capture_id": stale, "kind": "Bill", "item_id": "9", "status": "on_hold", "previous_id": handoff.ID}, 409)
	capture(time.Now().Add(2*time.Minute), full)
	read()
	if len(got.Settled) != 1 || !got.Settled[0].Settled || got.Settled[0].Label != "Invoice INV-1 · Acme" || got.Settled[0].Action.Status != "following_up" {
		t.Fatalf("an invoice gone from a later, complete capture is settled, with its last decision: %+v", got)
	}
	call("POST", map[string]any{"capture_id": got.CaptureID, "kind": "Invoice", "item_id": "1", "status": "following_up", "previous_id": got.Settled[0].Action.ID}, 409)
}
