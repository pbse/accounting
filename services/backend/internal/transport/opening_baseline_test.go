package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/documents"
)

func TestOpeningBaselineGatesTheFirstCloseAndLaterMonths(t *testing.T) {
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
	entity := "Takeover " + rand.Text()
	defer func() {
		for _, q := range []string{`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM quickbooks_connections WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM client_closes WHERE entity=$1`} {
			db.Exec(q, entity)
		}
	}()
	token := strings.Repeat("o", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	create := func(period string) string {
		t.Helper()
		var out struct{ ID string }
		must(json.Unmarshal(call("POST", "/v1/closes", map[string]string{"entity": entity, "period": period}, 200), &out))
		return out.ID
	}
	blockers := func(id string) string {
		t.Helper()
		var out struct{ Blockers []string }
		must(json.Unmarshal(call("GET", "/v1/closes/"+id+"/readiness", nil, 200), &out))
		return strings.Join(out.Blockers, "\n")
	}
	// Opening September first, then August: August, opened before any baseline, becomes the opening month.
	september := create("2026-09")
	august := create("2026-08")
	var flags string
	must(db.QueryRow(`SELECT string_agg(period||'='||takeover::text,',' ORDER BY period) FROM client_closes WHERE entity=$1`, entity).Scan(&flags))
	if flags != "2026-08=true,2026-09=false" {
		t.Fatalf("the earliest month opened before a baseline is the opening month: %s", flags)
	}

	var connection, evidenceID, paperID string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id::text=$1 RETURNING id::text`, august, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, august, rand.Text()).Scan(&evidenceID))
	snapshot := func(savings string) {
		t.Helper()
		row := func(id, name, debit string) map[string]any {
			return map[string]any{"type": "Data", "ColData": []map[string]string{{"id": id, "value": name}, {"value": debit}, {"value": ""}}}
		}
		payload, _ := json.Marshal(map[string]any{"period": "2026-08", "source_id": evidenceID, "coverage": []map[string]string{{"account_id": "5", "name": "Prepaid insurance", "kind": "prepaid", "question": "Which schedule supports it?"}},
			"accounts": []map[string]string{{"Id": "3", "Name": "Operating bank", "AccountType": "Bank", "Classification": "Asset"}, {"Id": "4", "Name": "Savings bank", "AccountType": "Bank", "Classification": "Asset"}, {"Id": "5", "Name": "Prepaid insurance", "AccountType": "Other Current Asset", "Classification": "Asset"}},
			"reports": map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": ""}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}},
				"Rows": map[string]any{"Row": []map[string]any{row("3", "Operating bank", "900.00"), row("4", "Savings bank", savings), row("5", "Prepaid insurance", "300.00")}}}}})
		must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1::bigint,$2,'123',$3,$4,now(),'fixture') RETURNING id::text`, august, connection, rand.Text(), string(payload)).Scan(new(string)))
	}
	snapshot("500.00")
	reconciled := func(account, closing string) {
		t.Helper()
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1::bigint,'reconciliation',$2,$3) RETURNING id::text`, august, rand.Text(), `{"input":{"account":"`+account+`","ledger_closing":"`+closing+`"}}`).Scan(&paperID))
		_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,'approved','Reviewed fixture','fixture' FROM workpapers WHERE id::text=$1`, paperID)
		must(e)
	}
	reconciled("Operating bank", "1000.00")
	reconciled("Savings bank", "500.00")

	if b := blockers(august); !strings.Contains(b, "Review the opening baseline: this is the client's first close") || !strings.Contains(b, "Tie-out: Operating bank") || !strings.Contains(b, "Coverage for Prepaid insurance") {
		t.Fatalf("the opening month waits for its baseline: %s", b)
	}
	if b := blockers(september); !strings.Contains(b, "Review the opening baseline on "+entity+" 2026-08 first") {
		t.Fatalf("a later month waits for the opening baseline: %s", b)
	}
	var view struct {
		Digest string
		Items  []openingItem
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+august+"/opening-baseline", nil, 200), &view))
	if len(view.Items) != 2 || view.Items[0].Key != "row:3" || !strings.Contains(view.Items[0].Label, "a difference of -100.00") || view.Items[1].Key != "row:5" || !strings.Contains(view.Items[1].Label, "no supporting schedule") {
		t.Fatalf("each account that does not tie needs a note: %+v", view.Items)
	}
	notes := map[string]string{"row:3": "Prior accountant left a $100 unreconciled item; to be traced."}
	call("POST", "/v1/closes/"+august+"/opening-baseline", map[string]any{"digest": view.Digest, "notes": notes, "confirmed": true}, 422)
	notes["row:5"] = "No schedule supplied by the prior accountant; client to send the policy."
	call("POST", "/v1/closes/"+august+"/opening-baseline", map[string]any{"digest": "stale", "notes": notes, "confirmed": true}, 409)
	var saved struct{ ID string }
	must(json.Unmarshal(call("POST", "/v1/closes/"+august+"/opening-baseline", map[string]any{"digest": view.Digest, "notes": notes, "confirmed": true}, 200), &saved))
	call("POST", "/v1/closes/"+august+"/opening-baseline", map[string]any{"digest": view.Digest, "notes": notes, "confirmed": true}, 409)

	// Reviewed, the opening month no longer waits for it or for the difference it noted; the later month waits for the
	// inherited cleanup until each item is resolved with a reference.
	if b := blockers(august); strings.Contains(b, "opening baseline") || strings.Contains(b, "Tie-out: Operating bank") || strings.Contains(b, "Coverage for Prepaid insurance") {
		t.Fatalf("a reviewed baseline clears its blocker and the noted difference and coverage: %s", b)
	}
	if b := blockers(september); !strings.Contains(b, "Resolve 2 inherited cleanup item(s) from the opening baseline on "+entity+" 2026-08") {
		t.Fatalf("a later month waits for the inherited cleanup: %s", b)
	}
	resolve := func(item string, want int) {
		call("POST", "/v1/closes/"+august+"/opening-baseline/resolve", map[string]string{"baseline_id": saved.ID, "item": item, "reference": "Journal entry JE-104 posted by the client"}, want)
	}
	resolve("row:3", 200)
	resolve("row:3", 409)
	resolve("row:9", 422)
	if b := blockers(september); !strings.Contains(b, "Resolve 1 inherited cleanup item(s)") {
		t.Fatalf("each item is resolved on its own: %s", b)
	}
	var retention struct {
		Blockers []string `json:"deletion_blockers"`
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+august+"/retention", nil, 200), &retention))
	if !strings.Contains(strings.Join(retention.Blockers, "\n"), "Later months of this client wait for its opening baseline") {
		t.Fatalf("the opening month cannot be deleted while later months wait for it: %v", retention.Blockers)
	}
	// A month opened under the client's confirmed alias is the same client, not a second opening month; a month earlier
	// than a reviewed opening month does not take its place or wait for it.
	alias := "Trading name " + rand.Text()
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES('local-development',$1,$2,'fixture')`, entity, documents.NormalizeEntity(alias))
	must(err)
	defer db.Exec(`DELETE FROM client_closes WHERE entity=$1`, alias)
	defer db.Exec(`DELETE FROM client_aliases WHERE client_entity=$1`, entity)
	october := ""
	{
		var out struct{ ID string }
		must(json.Unmarshal(call("POST", "/v1/closes", map[string]string{"entity": alias, "period": "2026-10"}, 200), &out))
		october = out.ID
	}
	july := create("2026-07")
	must(db.QueryRow(`SELECT string_agg(period||'='||takeover::text,',' ORDER BY period) FROM client_closes WHERE entity IN ($1,$2)`, entity, alias).Scan(&flags))
	if flags != "2026-07=false,2026-08=true,2026-09=false,2026-10=false" {
		t.Fatalf("one opening month per client: %s", flags)
	}
	if b := blockers(october); !strings.Contains(b, "Resolve 1 inherited cleanup item(s)") {
		t.Fatalf("a month under the client's alias waits for its cleanup: %s", b)
	}
	if b := blockers(july); strings.Contains(b, "inherited cleanup") || strings.Contains(b, "opening baseline") {
		t.Fatalf("an earlier month does not wait for a later opening month: %s", b)
	}
	// A client in its own right keeps its identity when a later alias of another client happens to name it.
	zeta := "Zeta " + rand.Text()
	defer db.Exec(`DELETE FROM client_closes WHERE entity=$1`, zeta)
	var own struct{ ID string }
	call("POST", "/v1/closes", map[string]string{"entity": zeta, "period": "2026-05"}, 200)
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES('local-development',$1,$2,'fixture')`, entity, documents.NormalizeEntity(zeta))
	must(err)
	must(json.Unmarshal(call("POST", "/v1/closes", map[string]string{"entity": zeta, "period": "2026-06"}, 200), &own))
	if b := blockers(own.ID); !strings.Contains(b, "opening baseline on "+zeta+" 2026-05") || strings.Contains(b, "inherited cleanup") {
		t.Fatalf("another client named by an alias keeps its own opening month: %s", b)
	}
	// A name two clients' aliases claim belongs to neither.
	other := "Other " + rand.Text()
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES('local-development',$1,$2,'fixture')`, other, documents.NormalizeEntity(alias))
	must(err)
	defer db.Exec(`DELETE FROM client_aliases WHERE client_entity=$1`, other)
	if b := blockers(october); strings.Contains(b, "inherited cleanup") {
		t.Fatalf("an alias two clients claim is no one's: %s", b)
	}
	resolve("row:5", 200)
	if b := blockers(september); strings.Contains(b, "inherited cleanup") || strings.Contains(b, "opening baseline") {
		t.Fatalf("resolved cleanup no longer blocks the later month: %s", b)
	}

	// The same balances captured again keep the review; a changed balance needs it again.
	snapshot("500.00")
	if b := blockers(august); strings.Contains(b, "Review the opening baseline") {
		t.Fatalf("an identical recapture keeps the baseline current: %s", b)
	}
	snapshot("450.00")
	if b := blockers(august); !strings.Contains(b, "Review the opening baseline") {
		t.Fatalf("a changed balance needs the baseline reviewed again: %s", b)
	}
}
