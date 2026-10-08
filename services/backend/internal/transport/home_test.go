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

func TestHomeCountsTheReadersOwnCohort(t *testing.T) {
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
	exec := func(q string, args ...any) {
		t.Helper()
		_, e := db.Exec(q, args...)
		must(e)
	}
	org := "org_Home" + rand.Text()
	exec(`INSERT INTO organizations(id,name) VALUES($1,'Home fixture')`, org)
	exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	defer func() {
		for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	var overdue, undated string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,due_on) VALUES($1,'Alpha','2026-08','2026-09-10') RETURNING id::text`, org).Scan(&overdue))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&undated))
	// Beta is not assigned to user_a, so nothing of it is counted.
	exec(`INSERT INTO client_closes(organization_id,entity,period,due_on) VALUES($1,'Beta','2026-08','2026-09-01')`, org)
	// One job prepared a draft, one finished with none (every row set aside), one failed and one was cancelled.
	exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,workpaper_ids,created_by) VALUES($1,$2,'[]','completed','["1"]','fixture'),($1,$3,'[]','completed','[]','fixture'),($1,$4,'[]','failed','[]','fixture'),($1,$5,'[]','cancelled','[]','fixture')`, overdue, rand.Text(), rand.Text(), rand.Text(), rand.Text())

	store := savedCloses(db, strings.Repeat("a", 32))
	r := httptest.NewRequest("GET", "/v1/closes/home", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review"}}))
	w := httptest.NewRecorder()
	store.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("home: %d %s", w.Code, w.Body.String())
	}
	var home struct {
		Closes    int
		SignedOff struct{ Done, Total int } `json:"signed_off"`
		Decisions struct{ Count, Unread int }
		CanAdd    bool `json:"can_add"`
		Deadlines struct {
			Overdue   int
			DueSoon   int `json:"due_soon"`
			NoDueDate int `json:"no_due_date"`
		}
		Prepared struct {
			Total, Done, Failed, Cancelled int
			NoDraft                        int `json:"no_draft"`
		}
		Books struct{ Captured, Of, Exceptions int }
	}
	must(json.Unmarshal(w.Body.Bytes(), &home))
	if home.SignedOff.Total != 2 || home.SignedOff.Done != 0 {
		t.Fatalf("signed off: %+v", home.SignedOff)
	}
	if home.Deadlines.Overdue != 1 || home.Deadlines.DueSoon != 0 || home.Deadlines.NoDueDate != 1 {
		t.Fatalf("deadlines: %+v", home.Deadlines)
	}
	if home.Closes != 2 || home.Prepared.Total != 3 || home.Prepared.Done != 1 || home.Prepared.Failed != 1 || home.Prepared.NoDraft != 1 || home.Prepared.Cancelled != 1 {
		t.Fatalf("closes %d, prepared: %+v", home.Closes, home.Prepared)
	}
	if home.Books.Of != 2 || home.Books.Captured != 0 || home.Books.Exceptions != 0 {
		t.Fatalf("books: %+v", home.Books)
	}
	if home.Decisions.Count != 0 || home.Decisions.Unread != 0 {
		t.Fatalf("decisions: %+v", home.Decisions)
	}
}
