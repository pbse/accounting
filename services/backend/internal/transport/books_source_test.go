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

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// A client's months read the newest capture until a reviewer chooses a source, for the client or one month; every
// reader then reads only that source's captures, and a choice that would change a signed-off month's books waits.
func TestTheBooksSourceIsChosenAndHonoured(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Source" + rand.Text()
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "preparer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Source fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b']) s`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM books_sources WHERE organization_id=$1`, `DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM xero_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM xero_connections WHERE organization_id=$1`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	// Acme's August has a QuickBooks capture and a later Xero capture; its July only a QuickBooks one, and is signed off.
	var august, july, qbo, xeroConnection string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2026-08') RETURNING id::text`, org).Scan(&august))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2026-07') RETURNING id::text`, org).Scan(&july))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,'Acme Co','fixture','active',$2,now(),'123') RETURNING id::text`, org, rand.Text()).Scan(&qbo))
	must(db.QueryRow(`INSERT INTO xero_connections(organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_name) VALUES($1,'Acme Co','fixture','active',$2,now(),'Acme') RETURNING id::text`, org, rand.Text()).Scan(&xeroConnection))
	capture := func(closeID, period string) {
		t.Helper()
		var evidence string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
		payload, _ := json.Marshal(map[string]any{"period": period, "source_id": evidence, "accounts": []any{}, "reports": map[string]any{"ProfitAndLoss": map[string]any{}, "GeneralLedger": map[string]any{}}})
		_, err := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1::bigint,$2,'123',$3,$4,now()-interval '2 hours',now()-interval '2 hours','fixture')`, closeID, qbo, rand.Text(), string(payload))
		must(err)
	}
	capture(august, "2026-08")
	capture(july, "2026-07")
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor) VALUES($1,$2,'t-1','2026-08','d','{}','fixture')`, august, xeroConnection)
	must(err)
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, july)
	must(err)

	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/closes/client/books-source", bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", subject, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	partner := []string{"workpapers:review", "clients:all"}
	read := func(closeID string) (string, string) {
		t.Helper()
		m, e := periodBooks(t.Context(), db, closeID, "2026-08")
		must(e)
		tx, e := db.Begin()
		must(e)
		defer tx.Rollback()
		coverage, e := ledgerCoverage(t.Context(), tx, closeID)
		must(e)
		var c struct {
			Source string `json:"source"`
		}
		json.Unmarshal(coverage, &c)
		if m == nil {
			return "", c.Source
		}
		return m.Source, c.Source
	}
	// By default the newest capture: August's Xero one, for the books and coverage alike.
	if books, coverage := read(august); books != "xero" || coverage != "xero" {
		t.Fatalf("the newest capture: %s %s", books, coverage)
	}
	// Only a reviewer chooses, with a reason, against the choice as read.
	call("user_b", []string{"clients:all"}, map[string]any{"client": "Acme Co", "period": "2026-08", "source": "quickbooks", "reason": "Books are kept in QuickBooks", "rev": ""}, 403)
	call("user_a", partner, map[string]any{"client": "Acme Co", "period": "2026-08", "source": "sage", "reason": "No", "rev": ""}, 422)
	saved := call("user_a", partner, map[string]any{"client": "Acme Co", "period": "2026-08", "source": "quickbooks", "reason": "Books are kept in QuickBooks", "rev": ""}, 200)
	call("user_a", partner, map[string]any{"client": "Acme Co", "period": "2026-08", "source": "xero", "reason": "Stale", "rev": ""}, 409)
	if books, coverage := read(august); books != "quickbooks" || coverage != "" {
		t.Fatalf("August reads QuickBooks: %s %s", books, coverage)
	}
	// A default that leaves out QuickBooks would change signed-off July's books: it waits for July to reopen.
	rev := saved["books_sources"].([]any)[0].(map[string]any)["id"]
	if out := call("user_a", partner, map[string]any{"client": "Acme Co", "period": "", "source": "xero", "reason": "Moved to Xero", "rev": rev}, 409); !strings.Contains(out["message"].(string), "2026-07") {
		t.Fatalf("the signed-off month is named: %v", out)
	}
	// A default naming QuickBooks leaves July's books as they were, and stands.
	call("user_a", partner, map[string]any{"client": "Acme Co", "period": "", "source": "quickbooks", "reason": "Kept in QuickBooks", "rev": rev}, 200)
}
