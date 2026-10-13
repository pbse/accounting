package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestListsReadTheWorkersSignOffCheck(t *testing.T) {
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
	org := "org_SignOffChecks" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Sign-off checks fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_partner',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	now := time.Now().UTC()
	var id string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,due_on) VALUES($1,'Signed',$2,$3::date) RETURNING id::text`, org, now.Format("2006-01"), now.AddDate(0, 0, 3).Format("2006-01-02")).Scan(&id))
	defer func() {
		db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, id)
		db.Exec(`DELETE FROM close_scopes WHERE close_id=$1`, id)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	mux := http.NewServeMux()
	registerEffort(mux, db)
	registerReadiness(mux, db)
	h := organizationBoundary(db, mux)
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_partner"}, Organization: org, Permissions: []string{"clients:all", "workpapers:review", "close:signoff"}}
	call := func(method, path string, body any) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var current readiness
	must(json.Unmarshal(call("GET", "/v1/closes/"+id+"/readiness", nil), &current))
	call("POST", "/v1/closes/"+id+"/scope", map[string]any{"digest": current.Digest, "scope": closeScope{Description: "Nothing required", RequiredPapers: []string{}, ExternalSteps: []externalStep{{Name: "Bank tie-out", Complete: true, Reference: "Statement 1"}}}})
	must(json.Unmarshal(call("GET", "/v1/closes/"+id+"/readiness", nil), &current))
	call("POST", "/v1/closes/"+id+"/signoff", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "action": "sign", "note": "Synthetic sign-off", "confirmed": true})
	due := func() string {
		t.Helper()
		var got struct {
			Due []struct {
				CloseID string `json:"close_id"`
				State   string
			}
		}
		must(json.Unmarshal(call("GET", "/v1/closes/morning", nil), &got))
		if len(got.Due) == 0 {
			return "signed_off"
		}
		return got.Due[0].State
	}

	_, err = checkSignOffs(t.Context(), db, org)
	must(err)
	_, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'late.txt',$2,'Late invoice','fixture')`, id, rand.Text())
	must(err)
	if state := due(); state != "signed_off" {
		t.Fatalf("until the worker checks again, the list shows its stored check, not a live recheck: %s", state)
	}
	_, err = checkSignOffs(t.Context(), db, org)
	must(err)
	if state := due(); state != "signed_off" {
		t.Fatalf("a check younger than signOffCheckAge is not made again: %s", state)
	}
	_, err = db.Exec(`UPDATE signoff_checks SET checked_at=checked_at-interval '10 minutes' WHERE close_id=$1`, id)
	must(err)
	if state := due(); state != "signed_off" {
		t.Fatalf("a check older than signOffCheckAge but not stale is still read: %s", state)
	}
	_, err = checkSignOffs(t.Context(), db, org)
	must(err)
	if state := due(); state != "reopen_required" {
		t.Fatalf("an aged check is made again and the change shows: %s", state)
	}
	// A worker that stopped leaves its last check behind; past signOffCheckStale the read rechecks the close itself.
	_, err = db.Exec(`UPDATE signoff_checks SET state='signed_off',checked_at=now()-interval '1 hour' WHERE close_id=$1`, id)
	must(err)
	if state := due(); state != "reopen_required" {
		t.Fatalf("a stale check is not trusted: %s", state)
	}
}
