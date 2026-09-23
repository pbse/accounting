package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"github.com/golang-jwt/jwt/v5"
)

type sandboxTransport func(*http.Request) (*http.Response, error)

func (f sandboxTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSandboxAttemptIsDurableAndNotRepeated(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for name, value := range map[string]string{"WORKOS_ORGANIZATION_ID": "org_fixture", "QBO_SANDBOX_REALM_ID": "123", "QBO_SANDBOX_ACCESS_TOKEN": "synthetic-only", "QBO_SANDBOX_ENTITY": "Posting fixture", "QBO_SANDBOX_PREPAID_ACCOUNT_ID": "10", "QBO_SANDBOX_EXPENSE_ACCOUNT_ID": "20"} {
		t.Setenv(name, value)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "unknown"}[uncertain], func(t *testing.T) {
			var closeID, paperID string
			err := db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Posting fixture "+time.Now().Format("150405.000000000")).Scan(&closeID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				db.Exec(`DELETE FROM sandbox_postings WHERE close_id=$1`, closeID)
				db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
				db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
			}()
			paper, err := prepaids.Prepare(prepaids.Input{Entity: "Posting fixture", SourceReference: "Synthetic invoice", Amount: "12000", ServiceStart: "2026-09-01", ServiceEnd: "2027-08-31", ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: "Software", PrepaidAccount: "Prepaid", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(paper)
			err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','test',$2) RETURNING id::text`, closeID, string(raw)).Scan(&paperID)
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			var journal map[string]any
			http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "sandbox-quickbooks.api.intuit.com" {
					t.Fatalf("unexpected provider: %s", r.URL.Host)
				}
				var result any
				switch {
				case strings.Contains(r.URL.Path, "/companyinfo/"):
					result = map[string]any{"CompanyInfo": map[string]string{"CompanyName": "Posting fixture"}}
				case strings.HasSuffix(r.URL.Path, "/account/10"):
					result = map[string]any{"Account": map[string]any{"Id": "10", "Name": "Prepaid", "Active": true, "Classification": "Asset"}}
				case strings.HasSuffix(r.URL.Path, "/account/20"):
					result = map[string]any{"Account": map[string]any{"Id": "20", "Name": "Software", "Active": true, "Classification": "Expense"}}
				case r.Method == "POST":
					writes++
					var state string
					if err := db.QueryRow(`SELECT state FROM sandbox_postings WHERE workpaper_id=$1`, paperID).Scan(&state); err != nil || state != "executing" {
						t.Fatalf("provider call preceded durable attempt: %s %v", state, err)
					}
					if uncertain {
						return nil, errors.New("synthetic lost response")
					}
					json.NewDecoder(r.Body).Decode(&journal)
					journal["Id"] = "456"
					result = map[string]any{"JournalEntry": journal}
				default:
					result = map[string]any{"JournalEntry": journal}
				}
				body, _ := json.Marshal(result)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
			})
			mux := http.NewServeMux()
			registerPosting(mux, db)
			path := "/v1/closes/" + closeID + "/workpapers/" + paperID + "/posting"
			actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_reviewer"}, Organization: "org_fixture", Permissions: []string{"posting:approve", "posting:execute"}}
			call := func(action, digest, confirmation string, want int) posting {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"action": action, "digest": digest, "confirmation": confirmation})
				r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
				r.Header.Set("Content-Type", "application/json")
				r = r.WithContext(auth.WithActor(context.Background(), actor))
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
				}
				var record posting
				json.Unmarshal(w.Body.Bytes(), &record)
				return record
			}
			preview := call("preview", "", "", 200)
			call("execute", preview.Digest, "POST TO SANDBOX", 409)
			call("approve", "wrong-digest", "APPROVE SANDBOX DRAFT", 409)
			call("approve", preview.Digest, "APPROVE SANDBOX DRAFT", 409)
			if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'test','approved','Reviewed synthetic schedule','fixture')`, paperID); err != nil {
				t.Fatal(err)
			}
			call("approve", preview.Digest, "APPROVE SANDBOX DRAFT", 200)
			if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'test','changes_requested','Correct treatment first','fixture')`, paperID); err != nil {
				t.Fatal(err)
			}
			call("execute", preview.Digest, "POST TO SANDBOX", 409)
			if writes != 0 {
				t.Fatal("Posting bypassed requested changes")
			}
			if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'test','approved','Reviewed corrected treatment','fixture')`, paperID); err != nil {
				t.Fatal(err)
			}
			status := 200
			if uncertain {
				status = 502
			}
			call("execute", preview.Digest, "POST TO SANDBOX", status)
			call("execute", preview.Digest, "POST TO SANDBOX", 409)
			record, err := readPosting(context.Background(), db, closeID, paperID)
			expected := "verified"
			if uncertain {
				expected = "unknown"
			}
			if err != nil || record.State != expected || writes != 1 {
				t.Fatalf("invalid durable outcome: %+v %v writes=%d", record, err, writes)
			}
			if !uncertain {
				journal["TxnDate"] = "2026-10-01"
				call("verify", preview.Digest, "", 422)
				record, err = readPosting(context.Background(), db, closeID, paperID)
				if err != nil || record.State != "unknown" {
					t.Fatal("changed provider entry retained verified status")
				}
			}
		})
	}
}
