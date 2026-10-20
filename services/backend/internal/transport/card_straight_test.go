package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

// A card account the capture's chart names a card is reconciled without a person confirming the agent's reading, as a
// card liability, only when the client's reconciliation procedure approves card accounts; without that approval it waits.
func TestACardIsReconciledStraightThroughOnlyWhenTheProcedureApprovesCards(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
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
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	sign := ""
	var lastQuestions []string
	reconcile := func(covers bool) (string, string, string) {
		t.Helper()
		entity := "Cards " + strings.ToLower(rand.Text())
		var closeID, connection string
		must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
		t.Cleanup(func() {
			for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`,
				`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
				db.Exec(q, closeID)
			}
			db.Exec(`DELETE FROM procedure_versions WHERE organization_id='local-development' AND client_entity=$1`, entity)
		})
		_, err := db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,covers_cards) VALUES('local-development',$1,'reconciliation','Exact matching; card charges increase the liability',true,'Fixture','fixture',$2)`, entity, covers)
		must(err)
		insert := func(name, body string) string {
			t.Helper()
			var id string
			must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture','ready') RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body).Scan(&id))
			return id
		}
		// The capture's chart names the account a card.
		must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'card','USD',1) RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
		captured := insert("QuickBooks transactions 2026-09", "capture")
		_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{}')`, closeID, connection, captured)
		must(err)
		snapshot := insert("QuickBooks 2026-09 accrual period evidence.txt", "period snapshot")
		_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'card',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(),
			`{"period":"2026-09","source_id":"`+snapshot+`","accounts":[{"Id":"70","Name":"Amex Card","AccountType":"Credit Card"},{"Id":"10","Name":"Checking","AccountType":"Bank"}],"reports":{}}`)
		must(err)
		statement := insert("Amex Card rows.csv", "id,date,amount\nv1,2026-09-04,"+sign+"20.00\n")
		ledger := insert("Amex Card ledger.csv", "id,date,amount\nw1,2026-09-04,"+sign+"20.00\n")
		balances := "Amex Card USD: statement opening balance " + sign + "50.00, closing balance " + sign + "70.00; expected pages 1, observed pages 1."
		pdf := insert("Amex Card statement balances.txt", balances)
		books := "Amex Card general ledger: opening balance " + sign + "50.00, closing balance " + sign + "70.00."
		report := insert("Amex Card ledger balances.txt", books)
		tx, e := db.Begin()
		must(e)
		job, e := preparation.StartIntake(context.Background(), tx, closeID, statement, "fixture")
		must(e)
		must(tx.Commit())
		http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
			facts := []any{}
			for field, value := range map[string]string{"account": "Amex Card", "currency": "USD", "statement_opening": sign + "50.00", "statement_closing": sign + "70.00", "ledger_opening": sign + "50.00", "ledger_closing": sign + "70.00", "expected_pages": "1", "pages": "1"} {
				source, quote := pdf, balances
				if strings.HasPrefix(field, "ledger_") {
					source, quote = report, books
				}
				facts = append(facts, map[string]string{"field": field, "value": value, "source_id": source, "quote": quote})
			}
			output, _ := json.Marshal(map[string]any{"kind": "reconciliation", "summary": "Reconcile Amex Card", "questions": []string{}, "sources": []any{map[string]string{"id": statement, "role": "statement"}, map[string]string{"id": ledger, "role": "ledger"}, map[string]string{"id": pdf, "role": "support"}, map[string]string{"id": report, "role": "support"}}, "facts": facts})
			body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
		for {
			worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db)
			must(e)
			if !worked {
				break
			}
		}
		var state, checkpoint string
		var questions []byte
		must(db.QueryRow(`SELECT j.state,COALESCE(i.agent_checkpoint->>'straight_through',''),COALESCE(i.proposal->'questions','[]'::jsonb) FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &checkpoint, &questions))
		must(json.Unmarshal(questions, &lastQuestions))
		if state != "queued" {
			return state, checkpoint, ""
		}
		if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
			t.Fatal("the worker drafts the queued card reconciliation", e)
		}
		var drafted string
		must(db.QueryRow(`SELECT w.paper->'input'->>'account_type'||' '||(w.paper->>'method') FROM preparation_jobs j JOIN workpapers w ON j.workpaper_ids @> jsonb_build_array(w.id::text) WHERE j.id::text=$1`, job).Scan(&drafted))
		return state, checkpoint, drafted
	}
	if state, checkpoint, drafted := reconcile(true); state != "queued" || !strings.HasPrefix(checkpoint, "Prepared without confirmation as a card liability") || drafted != "credit_card card_liability_month_v1" {
		t.Fatalf("a card the procedure approves is reconciled as a card liability: %s %s %s", state, checkpoint, drafted)
	}
	if state, checkpoint, _ := reconcile(false); state != "needs_confirmation" || !strings.Contains(checkpoint, "does not approve card accounts") {
		t.Fatalf("a card the procedure does not approve waits for a person: %s %s", state, checkpoint)
	}
	// Files signed the other way, the balance owed negative, are a person's to confirm.
	sign = "-"
	state, checkpoint, _ := reconcile(true)
	negativeQuestion := false
	for _, question := range lastQuestions {
		q := strings.ToLower(question)
		negativeQuestion = negativeQuestion || (strings.Contains(q, "negative") || strings.Contains(q, "-20.00") || strings.Contains(q, "-50.00") || strings.Contains(q, "-70.00")) && (strings.Contains(q, "sign") || strings.Contains(q, "convention"))
	}
	if state != "needs_confirmation" || !strings.Contains(checkpoint, "read as negative") && !negativeQuestion {
		t.Fatalf("a card whose balances read negative waits for its sign decision: %s %s %v", state, checkpoint, lastQuestions)
	}
}
