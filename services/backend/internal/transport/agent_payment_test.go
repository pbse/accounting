package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestAgentPaymentDateProposal(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_BillProposal" + rand.Text()
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Payment proposal fixture')`, org); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM model_calls WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	raw := `{"state":"ready","capture_id":"42","payables":[{"kind":"Bill","id":"9","label":"Bill B-9 · Landlord","balance":"250.00","currency":"USD","due":"2026-10-15","action":{"id":"17","status":"open"}}]}`
	for _, readFirst := range []bool{true, false} {
		round := 0
		http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
			var sent struct {
				Tools []any `json:"tools"`
			}
			json.NewDecoder(r.Body).Decode(&sent)
			body := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Done reading."}]}]}`
			if len(sent.Tools) > 0 {
				round++
				name, args := "", "{}"
				switch {
				case readFirst && round == 1:
					name = "open_items"
				case (readFirst && round == 2) || (!readFirst && round == 1):
					name = "propose_payment_date"
					args = `{"item_id":"9","expected_on":"2026-10-14","note":"Use the agreed date","balance":"9999.00","capture_id":"fake"}`
				}
				if name != "" {
					body = `{"status":"completed","output":[{"type":"function_call","id":"fc","call_id":"c","name":` + strconvQuote(name) + `,"arguments":` + strconvQuote(args) + `}]}`
				}
				if !readFirst && round == 1 {
					body = strings.Replace(body, `"output":[`, `"output":[{"type":"function_call","id":"read","call_id":"read","name":"open_items","arguments":"{}"},`, 1)
				}
			} else {
				body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"kind\":\"card\",\"answer\":\"Check the proposed date on the card.\",\"support\":[]}"}]}]}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
		})
		out := runAgent(modelContext(t, t.Context()), db, org, "7", "", "Suggest a payment date for the bill", "Fixture", "Alpha", "2026-09", "2026-10-01", []string{"workpapers:review"}, "closes", nil, func(_ context.Context, method, path string, _ any) (string, bool) {
			if method != "GET" || path != "/v1/closes/7/open-items" {
				t.Fatalf("unexpected read %s %s", method, path)
			}
			return raw, true
		}, func(string) bool { return false }, func(string) bool { return false }, func(string, map[string]any) bool { return true })
		if !readFirst {
			if out.proposal != nil {
				t.Fatal("a payment date was proposed before the model saw its bill read")
			}
			continue
		}
		if out.proposal == nil || out.proposal.Action != "bill_pay" {
			t.Fatalf("a read bill should produce a date card: %+v", out)
		}
		args := out.proposal.Args
		if args["capture_id"] != "42" || args["previous_id"] != "17" || args["balance"] != "250.00" || args["label"] != "Bill B-9 · Landlord" || args["expected_on"] != "2026-10-14" {
			t.Fatalf("card must use the bill read: %+v", args)
		}
	}
}
