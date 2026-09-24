package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestIndependentReviewPolicyRefusesPreparersAndUnknownProvenance(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_" + rand.Text()
	if _, err = db.Exec(`INSERT INTO organizations(id,name,require_independent_review) VALUES($1,'Independent firm',true)`, org); err != nil {
		t.Fatal(err)
	}
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES($1,'2026-09',$2) RETURNING id::text`, "Independence "+rand.Text(), org).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organization_policy_changes WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	content := "Invoice " + rand.Text()
	var evidenceID string
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(content)), content).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	sources := fmt.Sprintf(`{"sources":[{"id":"%s","digest":"%s","role":"invoice","name":"invoice.txt","location":"line 1"}],"blockers":[]}`, evidenceID, preparation.Digest([]byte(content)))
	paper := func(digest, createdBy string) string {
		t.Helper()
		var id string
		if e := db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'prepaid',$2,$3,NULLIF($4,'')) RETURNING id::text`, closeID, digest, sources, createdBy).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	prepared := paper("prepared", "")
	if _, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by,workpaper_ids) VALUES($1,'job','[]','completed','user_prep',jsonb_build_array($2::text))`, closeID, prepared); err != nil {
		t.Fatal(err)
	}
	unknown := paper("unknown", "")
	saved := paper("saved", "user_saver")
	decide := func(paperID, digest, approver string) (int, string) {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		_, status, message := decideReview(context.Background(), tx, closeID, paperID, approver, "approved", "Synthetic independence check", digest, "", false)
		if status == 0 && tx.Commit() != nil {
			t.Fatal("commit failed")
		}
		return status, message
	}
	if status, message := decide(prepared, "prepared", "user_prep"); status != 409 || !strings.Contains(message, "another member") {
		t.Fatalf("the preparer must not approve: %d %s", status, message)
	}
	if status, message := decide(unknown, "unknown", "user_other"); status != 409 || !strings.Contains(message, "no preparer is recorded") {
		t.Fatalf("a draft without provenance must fail closed: %d %s", status, message)
	}
	if status, message := decide(saved, "saved", "user_saver"); status != 409 {
		t.Fatalf("the saver of a direct draft must not approve it: %d %s", status, message)
	}
	if status, message := decide(prepared, "prepared", "user_other"); status != 0 {
		t.Fatalf("another member may approve: %d %s", status, message)
	}
	if issue, e := independenceIssue(context.Background(), db, closeID, prepared, "user_prep"); e != nil || issue == "" {
		t.Fatal("posting must refuse a preparer's approval under the policy")
	}

	mux := http.NewServeMux()
	registerIndependence(mux, db)
	policy := func(organization string, permissions []string, want int) {
		t.Helper()
		actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_admin"}, Organization: organization, Permissions: permissions}
		r := httptest.NewRequest("POST", "/v1/closes/organization/policy", bytes.NewReader([]byte(`{"independent_review":false,"reason":"Synthetic policy change"}`))).WithContext(auth.WithActor(context.Background(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("policy %s %v: %d %s", organization, permissions, w.Code, w.Body.String())
		}
	}
	policy(org, []string{"clients:all"}, 403)
	policy("", nil, 409)
	policy(org, []string{"org:manage"}, 200)
	var changes int
	if err = db.QueryRow(`SELECT count(*) FROM organization_policy_changes WHERE organization_id=$1 AND actor='user_admin' AND NOT independent_review`, org).Scan(&changes); err != nil || changes != 1 {
		t.Fatalf("policy change must be recorded: %d %v", changes, err)
	}
	if _, err = db.Exec(`UPDATE organizations SET require_independent_review=true WHERE id='local-development'`); err == nil {
		db.Exec(`UPDATE organizations SET require_independent_review=false WHERE id='local-development'`)
		t.Fatal("the shared local workspace must not enable independent review")
	}
}
