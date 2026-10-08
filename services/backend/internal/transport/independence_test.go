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
	policy := func(organization string, permissions []string, want int, body ...string) {
		t.Helper()
		actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_admin"}, Organization: organization, Permissions: permissions}
		if len(body) == 0 {
			body = []string{`{"independent_review":false,"reason":"Synthetic policy change"}`}
		}
		r := httptest.NewRequest("POST", "/v1/closes/organization/policy", bytes.NewReader([]byte(body[0]))).WithContext(auth.WithActor(context.Background(), actor))
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

	// With the firm-wide rule off, a minimum requires an independent reviewer only for drafts proposing at least that much.
	policy(org, []string{"org:manage"}, 422, `{"independent_review_min_amount":"-5","reason":"Synthetic minimum"}`)
	policy(org, []string{"org:manage"}, 422, `{"independent_review_min_amount":"0","reason":"Synthetic minimum"}`)
	policy("", nil, 409, `{"independent_review_min_amount":"1000","reason":"Synthetic minimum"}`)
	policy(org, []string{"org:manage"}, 200, `{"independent_review_min_amount":"1000","reason":"Synthetic minimum"}`)
	var recorded string
	var review bool
	if err = db.QueryRow(`SELECT independent_review,independent_review_min_amount::text FROM organization_policy_changes WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&review, &recorded); err != nil || review || recorded != "1000.00" {
		t.Fatalf("the change records the minimum with the other settings: %v %s %v", review, recorded, err)
	}
	// Writing approved entries to clients' books is off until a firm administrator turns it on, with a reason; each
	// change records it with the other settings.
	var writes bool
	if err = db.QueryRow(`SELECT allow_ledger_writes FROM organizations WHERE id=$1`, org).Scan(&writes); err != nil || writes {
		t.Fatalf("a firm starts with writing to clients' books off: %v %v", writes, err)
	}
	policy(org, []string{"clients:all"}, 403, `{"ledger_writes":true,"reason":"Post approved entries"}`)
	policy(org, []string{"org:manage"}, 200, `{"ledger_writes":true,"reason":"Post approved entries"}`)
	var recordedWrites bool
	if err = db.QueryRow(`SELECT o.allow_ledger_writes AND c.ledger_writes FROM organizations o JOIN organization_policy_changes c ON c.organization_id=o.id WHERE o.id=$1 ORDER BY c.id DESC LIMIT 1`, org).Scan(&recordedWrites); err != nil || !recordedWrites {
		t.Fatalf("turning writing on is applied and recorded: %v %v", recordedWrites, err)
	}
	policy(org, []string{"org:manage"}, 200, `{"ledger_writes":false,"reason":"Book entries by hand again"}`)
	if err = db.QueryRow(`SELECT allow_ledger_writes FROM organizations WHERE id=$1`, org).Scan(&writes); err != nil || writes {
		t.Fatalf("turning writing off is applied: %v %v", writes, err)
	}
	policy(org, []string{"org:manage"}, 422, `{"reminder_after_days":7,"reason":"Synthetic reminders"}`)
	policy(org, []string{"org:manage"}, 422, `{"reminder_max":0,"reason":"Synthetic reminders"}`)
	policy(org, []string{"org:manage"}, 200, `{"reminder_after_days":2,"reminder_max":1,"reason":"Synthetic reminders"}`)
	var after, most int
	if err = db.QueryRow(`SELECT reminder_after_days,reminder_max,independent_review_min_amount::text FROM organization_policy_changes WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&after, &most, &recorded); err != nil || after != 2 || most != 1 || recorded != "1000.00" {
		t.Fatalf("the change records the reminder rule with the other settings: %d %d %s %v", after, most, recorded, err)
	}
	proposing := func(entries string) string {
		t.Helper()
		body := strings.Replace(sources, `"blockers":[]`, `"blockers":[],`+entries, 1)
		digest := rand.Text()
		var id string
		if e := db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, digest, body).Scan(&id); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by,workpaper_ids) VALUES($1,$2,'[]','completed','user_prep',jsonb_build_array($3::text))`, closeID, rand.Text(), id); e != nil {
			t.Fatal(e)
		}
		return id + "|" + digest
	}
	decideAs := func(paper, approver string) (int, string) {
		t.Helper()
		id, digest, _ := strings.Cut(paper, "|")
		return decide(id, digest, approver)
	}
	small := proposing(`"proposal":{"amount":"999.99"}`)
	large := proposing(`"proposal":{"amount":"1000.00"}`)
	loan := proposing(`"proposals":[{"amount":"600.00"},{"amount":"400.00"}]`)
	if status, message := decideAs(small, "user_prep"); status != 0 {
		t.Fatalf("below the minimum the preparer may approve: %d %s", status, message)
	}
	if status, message := decideAs(large, "user_prep"); status != 409 || !strings.Contains(message, "at least 1000.00") {
		t.Fatalf("at the minimum another member must approve: %d %s", status, message)
	}
	if status, message := decideAs(loan, "user_prep"); status != 409 {
		t.Fatalf("a loan's entries count together: %d %s", status, message)
	}
	policy(org, []string{"org:manage"}, 200, `{"independent_review_min_amount":"","reason":"Synthetic minimum cleared"}`)
	if status, message := decideAs(large, "user_prep"); status != 0 {
		t.Fatalf("clearing the minimum lifts the rule: %d %s", status, message)
	}
	if _, err = db.Exec(`UPDATE organizations SET independent_review_min_amount=1 WHERE id='local-development'`); err == nil {
		db.Exec(`UPDATE organizations SET independent_review_min_amount=NULL WHERE id='local-development'`)
		t.Fatal("the shared local workspace must not set a review minimum")
	}

	// Materiality is recorded with every later change, and the local workspace may set it too.
	policy(org, []string{"org:manage"}, 422, `{"materiality_amount":"0","reason":"Synthetic materiality"}`)
	policy(org, []string{"org:manage"}, 200, `{"materiality_amount":"250","reason":"Synthetic materiality"}`)
	policy(org, []string{"org:manage"}, 200, `{"link_passcode":false,"reason":"Synthetic other change"}`)
	if err = db.QueryRow(`SELECT COALESCE(materiality_amount::text,'') FROM organization_policy_changes WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&recorded); err != nil || recorded != "250.00" {
		t.Fatalf("every change records the resulting materiality: %q %v", recorded, err)
	}
	defer db.Exec(`DELETE FROM organization_policy_changes WHERE organization_id='local-development' AND actor='user_admin' AND reason LIKE 'Synthetic local materiality%'`)
	policy("", nil, 200, `{"materiality_amount":"5","reason":"Synthetic local materiality"}`)
	policy("", nil, 200, `{"materiality_amount":"","reason":"Synthetic local materiality cleared"}`)
}
