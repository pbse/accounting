package transport

import (
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

// Only Krith's own firm (registered for the staff domain) sees the worked example; another firm is told there is none
// and cannot fetch it.
func TestOnlyKrithsOwnFirmSeesTheExample(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	domain := "krith" + strings.ToLower(rand.Text()[:8]) + ".test"
	krith, other := "org_Krith"+rand.Text(), "org_Other"+rand.Text()
	for _, firm := range []struct{ id, domain string }{{krith, domain}, {other, "other-" + domain}} {
		if _, err := db.Exec(`INSERT INTO organizations(id,name,email_domain) VALUES($1,'Example fixture',$2)`, firm.id, firm.domain); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_x',true,'Synthetic','fixture')`, firm.id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range []string{krith, other} {
			db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, id)
			db.Exec(`DELETE FROM organizations WHERE id=$1`, id)
		}
	})
	t.Setenv("STAFF_EMAIL_DOMAIN", domain)
	store := savedCloses(db, strings.Repeat("e", 32))
	call := func(org, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_x"}, Organization: org}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		return w
	}

	var answer struct{ Available bool }
	if w := call(krith, "/v1/closes/examples"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &answer) != nil || !answer.Available {
		t.Fatalf("Krith's firm has the example: %d %s", w.Code, w.Body.String())
	}
	var paper struct {
		Mode     string
		Schedule []struct{ Remaining string }
	}
	if w := call(krith, "/v1/closes/examples/prepaid"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &paper) != nil || paper.Mode != "synthetic" || len(paper.Schedule) != 12 || paper.Schedule[11].Remaining != "0.00" {
		t.Fatalf("Krith's firm reads the example: %d %s", w.Code, w.Body.String())
	}
	if w := call(other, "/v1/closes/examples"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &answer) != nil || answer.Available {
		t.Fatalf("another firm has no example: %d %s", w.Code, w.Body.String())
	}
	if w := call(other, "/v1/closes/examples/prepaid"); w.Code != 404 {
		t.Fatalf("another firm cannot read the example: %d", w.Code)
	}
	// Without a staff domain, no hosted firm is Krith's.
	t.Setenv("STAFF_EMAIL_DOMAIN", "")
	if w := call(krith, "/v1/closes/examples/prepaid"); w.Code != 404 {
		t.Fatalf("without a staff domain there is no example: %d", w.Code)
	}
}
