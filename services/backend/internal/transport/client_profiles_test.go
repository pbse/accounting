package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// A client's confirmed profile is its currency and fiscal year from the month it takes effect, whatever its connections
// say; a connection that reports otherwise is a question that holds drafts, never an override; earlier months keep what
// they had. A member who reviews confirms it, under the client's own name, against the profile they read.
func TestAConfirmedProfileStandsAndAsks(t *testing.T) {
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
	org := "org_Profile" + rand.Text()
	asFirm(t, map[string]string{"user_a": "partner", "user_c": "reviewer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Profile fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_c']) s`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Maple Trading Ltd',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM client_profiles WHERE organization_id=$1`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	// Maple keeps its books in Canadian dollars, its year from April, by its QuickBooks connection; a month was opened
	// under an alias Cy is assigned to.
	var august, before, alias string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Maple Co','2026-08') RETURNING id::text`, org).Scan(&august))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Maple Co','2025-12') RETURNING id::text`, org).Scan(&before))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,created_at) VALUES($1,'Maple Trading Ltd','2026-07',now()-interval '1 day') RETURNING id::text`, org).Scan(&alias))
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Maple Co','maple trading ltd','fixture',now()-interval '2 days')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,$2,'Maple Co','fixture','active',$3,now(),'77','CAD',4)`, august, org, rand.Text())
	must(err)

	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s: %d %s", subject, method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	partner := []string{"workpapers:review", "clients:all"}
	profileOf := func(closeID string) preparation.Profile {
		t.Helper()
		p, e := preparation.ClientProfile(t.Context(), db, closeID)
		must(e)
		return p
	}
	if p := profileOf(august); p.State != "detected" || p.Currency != "CAD" || p.FiscalStart != 4 {
		t.Fatalf("without a confirmed profile, the connection's: %+v", p)
	}

	// Confirming needs a reviewer, the client's own name for Cy (assigned to its alias only), valid values and the
	// profile as read.
	confirm := map[string]any{"client": "maple trading ltd", "currency": "usd", "fiscal_year_start": 1, "effective_from": "2026-01", "reason": "Engagement letter: US dollars, calendar year", "rev": ""}
	call("user_e", []string{"clients:all"}, "POST", "/v1/closes/client/profile", confirm, 403)
	call("user_c", []string{"workpapers:review"}, "POST", "/v1/closes/client/profile", confirm, 403)
	for field, bad := range map[string]any{"currency": "US", "fiscal_year_start": 13, "effective_from": "2026-13", "reason": " "} {
		wrong := map[string]any{}
		for k, v := range confirm {
			wrong[k] = v
		}
		wrong[field] = bad
		call("user_a", partner, "POST", "/v1/closes/client/profile", wrong, 422)
	}
	saved := call("user_a", partner, "POST", "/v1/closes/client/profile", confirm, 200)
	if p := saved["profile"].(map[string]any); p["state"] != "differs" || p["currency"] != "USD" || p["ledger"].(map[string]any)["currency"] != "CAD" {
		t.Fatalf("the confirmed profile stands, and the connection asks: %v", p)
	}
	call("user_a", partner, "POST", "/v1/closes/client/profile", confirm, 409)

	// The question holds drafts until it is answered; a month before the profile took effect keeps the connection's.
	if p := profileOf(august); p.State != "differs" || p.Currency != "USD" || !p.Unsettled() || !strings.Contains(p.Question(), "reports CAD and a fiscal year from month 4") {
		t.Fatalf("August: %+v %q", p, p.Question())
	}
	if e := preparation.CurrencyIssue(t.Context(), db, august, []byte(`{"input":{"currency":"USD"}}`)); e == nil || !strings.Contains(e.Error(), "correct the profile or the connection") {
		t.Fatalf("a draft waits on the question: %v", e)
	}
	if p := profileOf(before); p.State != "detected" || p.Currency != "CAD" {
		t.Fatalf("December 2025 keeps the connection's: %+v", p)
	}

	// August is signed off under US dollars: a change reaching back to it is refused until it is reopened.
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, august)
	must(err)
	t.Cleanup(func() { db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, august) })
	backdated := map[string]any{"client": "Maple Co", "currency": "CAD", "fiscal_year_start": 4, "effective_from": "2026-08", "reason": "Backdated", "rev": saved["profiles"].([]any)[0].(map[string]any)["id"]}
	if out := call("user_a", partner, "POST", "/v1/closes/client/profile", backdated, 409); !strings.Contains(fmt.Sprint(out), "2026-08") {
		t.Fatalf("the signed-off month is named: %v", out)
	}
	// The firm finds the connection is right: the profile is corrected to match it, from September, and the question
	// closes from then; July and August keep US dollars as confirmed.
	history := saved["profiles"].([]any)
	correct := map[string]any{"client": "Maple Co", "currency": "CAD", "fiscal_year_start": 4, "effective_from": "2026-09", "reason": "The engagement letter was wrong", "rev": history[0].(map[string]any)["id"]}
	call("user_a", partner, "POST", "/v1/closes/client/profile", correct, 200)
	var september string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Maple Co','2026-09') RETURNING id::text`, org).Scan(&september))
	if p := profileOf(september); p.State != "confirmed" || p.Currency != "CAD" || p.EffectiveFrom != "2026-09" || p.Unsettled() {
		t.Fatalf("September: %+v", p)
	}
	if p := profileOf(alias); p.State != "differs" || p.Currency != "USD" {
		t.Fatalf("July, opened under the alias, reads the client's connection and keeps its confirmed profile, asking: %+v", p)
	}

	// The client page shows the profile in force and its history, newest first, under the client's own name.
	read := call("user_a", partner, "GET", "/v1/closes/client?name=Maple%20Co", nil, 200)
	if profiles := read["profiles"].([]any); len(profiles) != 2 || profiles[0].(map[string]any)["effective_from"] != "2026-09" || profiles[1].(map[string]any)["reason"] != "Engagement letter: US dollars, calendar year" {
		t.Fatalf("history: %v", read["profiles"])
	}
	if partial := call("user_c", []string{"workpapers:review"}, "GET", "/v1/closes/client?name=Maple%20Co", nil, 200); len(partial["profiles"].([]any)) != 0 {
		t.Fatalf("Cy sees only the months they can open: %v", partial["profiles"])
	}

	// A client with no ledger connected takes its confirmed profile as is, with no question.
	var files string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Fern Studio','2026-09') RETURNING id::text`, org).Scan(&files))
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Fern Studio", "currency": "GBP", "fiscal_year_start": 4, "effective_from": "2026-04", "reason": "Kept on files in pounds", "rev": ""}, 200)
	if p := profileOf(files); p.State != "confirmed" || p.Currency != "GBP" || p.FiscalStart != 4 || p.Ledger != nil {
		t.Fatalf("a client on files: %+v", p)
	}
	if e := preparation.CurrencyIssue(t.Context(), db, files, []byte(`{"input":{"currency":"USD"}}`)); e == nil || !strings.Contains(e.Error(), "kept in GBP") {
		t.Fatalf("a draft in another currency than the confirmed one: %v", e)
	}
	// A connection whose currency is not read yet still asks when its fiscal year differs.
	_, err = db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,fiscal_year_start) VALUES($1,$2,'Fern Studio','fixture','active',$3,now(),'78',1)`, files, org, rand.Text())
	must(err)
	if p := profileOf(files); p.State != "differs" || p.Ledger == nil || p.Ledger.FiscalStart != 1 || p.Ledger.Currency != "" || !strings.Contains(p.Question(), "reports a fiscal year from month 1;") {
		t.Fatalf("a differing fiscal year with the currency unread: %+v", p)
	}
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Nobody Ltd", "currency": "GBP", "fiscal_year_start": 4, "effective_from": "2026-04", "reason": "x", "rev": ""}, 404)
	// September is signed off under CAD: a change from February that the September profile still overrides there is
	// not held by it.
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, september)
	must(err)
	t.Cleanup(func() { db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, september) })
	_, err = db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, august)
	must(err)
	newest := call("user_a", partner, "GET", "/v1/closes/client?name=Maple%20Co", nil, 200)["profiles"].([]any)[0].(map[string]any)["id"]
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Maple Co", "currency": "GBP", "fiscal_year_start": 1, "effective_from": "2026-02", "reason": "Earlier months were in pounds", "rev": newest}, 200)
	// A client on files has approved work in pounds before any profile: confirming dollars would contradict it, so it
	// waits; confirming pounds goes through.
	var oak, paper string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Oak Ltd','2026-08') RETURNING id::text`, org).Scan(&oak))
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','oak-paper','{"input":{"currency":"GBP"}}') RETURNING id::text`, oak).Scan(&paper))
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest) VALUES($1,'approved','Synthetic approval','fixture','oak-paper')`, paper)
	must(err)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id=$1`, paper)
		db.Exec(`DELETE FROM workpapers WHERE id=$1`, paper)
	})
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Oak Ltd", "currency": "USD", "fiscal_year_start": 1, "effective_from": "2026-01", "reason": "Dollars", "rev": ""}, 409)
	oakProfile := call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Oak Ltd", "currency": "GBP", "fiscal_year_start": 1, "effective_from": "2026-01", "reason": "Pounds, as its work is", "rev": ""}, 200)["profiles"].([]any)[0].(map[string]any)["id"]
	// Its approved August work was prepared under a calendar year: moving the year's start under it waits.
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Oak Ltd", "currency": "GBP", "fiscal_year_start": 4, "effective_from": "2026-01", "reason": "April year", "rev": oakProfile}, 409)
	// A month signed off under Canadian dollars, as kept with its sign-off, holds a later change to dollars even once
	// nothing it reads says CAD any more.
	var pine string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Pine Ltd','2026-05') RETURNING id::text`, org).Scan(&pine))
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor,profile) VALUES($1,'signed_off','fixture','{}','Done','fixture','{"currency":"CAD","fiscal_year_start":1,"state":"detected"}')`, pine)
	must(err)
	t.Cleanup(func() { db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, pine) })
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Pine Ltd", "currency": "USD", "fiscal_year_start": 1, "effective_from": "2026-01", "reason": "Dollars", "rev": ""}, 409)
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Pine Ltd", "currency": "CAD", "fiscal_year_start": 1, "effective_from": "2026-01", "reason": "Canadian dollars, as signed off", "rev": ""}, 200)

	// A client whose books come as files is taken over with its confirmed profile, and no connection.
	var birch string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,takeover) VALUES($1,'Birch Ltd','2026-08',true) RETURNING id::text`, org).Scan(&birch))
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES($1,'Birch Ltd','basis','Synthetic',true,'Synthetic basis','fixture','accrual')`, org)
	must(err)
	t.Cleanup(func() { db.Exec(`DELETE FROM procedure_versions WHERE organization_id=$1`, org) })
	takeover := "/v1/closes/" + birch + "/takeover-setup"
	if p := call("user_a", partner, "GET", takeover, nil, 200)["problems"].([]any); len(p) != 1 || !strings.Contains(p[0].(string), "confirm its currency and fiscal year") {
		t.Fatalf("a client with neither a connection nor a profile: %v", p)
	}
	unbound := call("user_a", partner, "GET", "/v1/closes/"+birch+"/readiness", nil, 200)
	call("user_a", partner, "POST", "/v1/closes/client/profile", map[string]any{"client": "Birch Ltd", "currency": "GBP", "fiscal_year_start": 4, "effective_from": "2026-08", "reason": "Books as files, in pounds", "rev": ""}, 200)
	// A sign-off made against readiness read before the profile was confirmed is refused: the digest binds the profile.
	if after := call("user_a", partner, "GET", "/v1/closes/"+birch+"/readiness", nil, 200); after["digest"] == unbound["digest"] {
		t.Fatal("the readiness digest binds the client's profile")
	}
	call("user_a", append([]string{"close:signoff"}, partner...), "POST", "/v1/closes/"+birch+"/signoff", map[string]any{"digest": unbound["digest"], "expected_decision_id": unbound["decision_id"], "action": "sign", "note": "Read before the profile", "confirmed": true}, 409)
	setup := call("user_a", partner, "GET", takeover, nil, 200)
	content := setup["content"].(map[string]any)
	if len(setup["problems"].([]any)) != 0 || content["books"] != "files" || content["profile"].(map[string]any)["currency"] != "GBP" {
		t.Fatalf("a file-only client is taken over with its profile: %v", setup)
	}
	// A ledger connected later that reports dollars is a question the takeover waits on.
	_, err = db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,$2,'Birch Ltd','fixture','active',$3,now(),'88','USD',4)`, birch, org, rand.Text())
	must(err)
	if p := call("user_a", partner, "GET", takeover, nil, 200)["problems"].([]any); len(p) != 1 || !strings.Contains(p[0].(string), "its connected ledger reports USD") {
		t.Fatalf("a takeover waits on a ledger that differs: %v", p)
	}
}
