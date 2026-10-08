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

func TestDailyBooksShowsExceptionsNewSinceTheLastReview(t *testing.T) {
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
	org, other := "org_Daily"+rand.Text(), "org_Daily"+rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Daily fixture'),($2,'Other firm')`, org, other)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	month := time.Now().UTC().Format("2006-01")
	closes := []string{}
	open := func(firm, entity string) string {
		var id string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, firm, entity, month).Scan(&id))
		closes = append(closes, id)
		return id
	}
	defer func() {
		for _, id := range closes {
			db.Exec(`DELETE FROM daily_reviews WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, org, other)
	}()
	connect := func(closeID, entity, firm, state string) string {
		var id string
		must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,environment) VALUES($1,$2,$3,'fixture',$4,$5,now(),'123','sandbox') RETURNING id::text`, closeID, firm, entity, state, rand.Text()).Scan(&id))
		return id
	}
	capture := func(closeID, connection string, exceptions ...[3]string) string {
		items := []map[string]any{}
		for _, e := range exceptions {
			items = append(items, map[string]any{"type": e[0], "kind": e[1], "provider_ids": []string{e[2]}, "finding": e[0] + " " + e[2], "question": "About " + e[2] + "?"})
		}
		raw, _ := json.Marshal(items)
		var id string
		must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,exceptions) VALUES($1,$2,$3,'scheduled-quickbooks-refresh','completed',now(),$4) RETURNING id::text`, closeID, connection, month, string(raw)).Scan(&id))
		return id
	}
	dental, bakery := open(org, "Dental"), open(org, "Bakery")
	signed, foreign := open(org, "Signed"), open(other, "Foreign")
	dentalConnection := connect(dental, "Dental", org, "active")
	first := capture(dental, dentalConnection, [3]string{"possible_duplicate", "Purchase", "1"})
	latest := capture(dental, dentalConnection, [3]string{"possible_duplicate", "Purchase", "1"}, [3]string{"no_payee", "Purchase", "3"})
	capture(bakery, connect(bakery, "Bakery", org, "active"), [3]string{"no_payee", "Purchase", "9"})
	capture(signed, connect(signed, "Signed", org, "active"), [3]string{"no_payee", "Purchase", "5"})
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed','fixture')`, signed)
	must(err)
	capture(foreign, connect(foreign, "Foreign", other, "active"), [3]string{"no_payee", "Purchase", "6"})

	mux := http.NewServeMux()
	registerDaily(mux, db)
	h := organizationBoundary(db, mux)
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all"}}
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type entry struct {
		CloseID   string `json:"close_id"`
		Source    string `json:"source"`
		SyncID    string `json:"sync_id"`
		Total     int    `json:"total"`
		Unchecked string `json:"unchecked"`
		New       []struct {
			Type string `json:"type"`
			Kind string `json:"kind"`
		} `json:"new"`
	}
	daily := func() []entry {
		t.Helper()
		var out struct{ Items []entry }
		must(json.Unmarshal(call("GET", "/v1/closes/daily", nil, 200), &out))
		return out.Items
	}
	got := daily()
	// The fixture's sign-off records a digest the close no longer has, so it no longer stands: the close is reopened
	// and listed like an open one, last. Another firm's close is left out.
	if len(got) != 3 || got[0].CloseID != dental || len(got[0].New) != 2 || got[1].CloseID != bakery || got[2].CloseID != signed {
		t.Fatalf("unreviewed closes list every exception, most new first, a reopened close among them; other firms' closes are left out: %+v", got)
	}
	// Marking a capture reviewed needs the reviewer role; firm-wide sight of the client is not enough.
	call("POST", "/v1/closes/"+dental+"/daily-review", map[string]string{"sync_id": latest}, 403)
	actor.Permissions = []string{"clients:all", "workpapers:review"}
	call("POST", "/v1/closes/"+dental+"/daily-review", map[string]string{"sync_id": first}, 409)
	call("POST", "/v1/closes/"+dental+"/daily-review", map[string]string{"sync_id": latest}, 200)
	if got = daily(); got[len(got)-1].CloseID != dental || got[len(got)-1].Total != 2 || len(got[len(got)-1].New) != 0 {
		t.Fatalf("a review clears what it covered: %+v", got)
	}
	// The same payment reported as a Bill is a different finding from the reviewed Purchase.
	capture(dental, dentalConnection, [3]string{"possible_duplicate", "Purchase", "1"}, [3]string{"no_payee", "Purchase", "3"}, [3]string{"no_payee", "Bill", "3"}, [3]string{"uncategorized", "Purchase", "4"})
	if got = daily(); got[0].CloseID != dental || len(got[0].New) != 2 || got[0].New[0].Kind != "Bill" || got[0].New[1].Type != "uncategorized" {
		t.Fatalf("only findings missing from the reviewed capture are new: %+v", got)
	}
	// After reconnecting, a reviewed capture from the old connection is no baseline.
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='disconnected' WHERE id=$1`, dentalConnection)
	must(err)
	capture(dental, connect(dental, "Dental", org, "active"), [3]string{"possible_duplicate", "Purchase", "1"}, [3]string{"no_payee", "Purchase", "3"})
	if got = daily(); got[0].CloseID != dental || len(got[0].New) != 2 {
		t.Fatalf("findings from a new connection are new: %+v", got)
	}

	// A Xero client's snapshots record their findings and are reviewed the same way.
	studio := open(org, "Studio")
	var studioXero string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) VALUES($1,$2,'Studio','fixture','active',$3,now(),'t-1') RETURNING id::text`, studio, org, rand.Text()).Scan(&studioXero))
	snapshot := func(age string, findings ...string) string {
		items := []map[string]any{}
		for _, id := range findings {
			items = append(items, map[string]any{"type": "uncategorized", "kind": "Purchase", "provider_ids": []string{id}, "question": "About " + id + "?"})
		}
		raw, _ := json.Marshal(items)
		if findings == nil {
			raw = nil
		}
		var id string
		must(db.QueryRow(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at,exceptions) VALUES($1,$2,'t-1',$3,'d','{}','scheduled-xero-refresh',now()-$4::interval,$5) RETURNING id::text`, studio, studioXero, month, age, raw).Scan(&id))
		return id
	}
	studioEntry := func() *entry {
		for _, e := range daily() {
			if e.CloseID == studio {
				return &e
			}
		}
		return nil
	}
	reviewedXero := snapshot("3 hours", "x-1")
	if e := studioEntry(); e == nil || e.Source != "xero" || e.SyncID != reviewedXero || len(e.New) != 1 {
		t.Fatalf("a Xero snapshot's findings are listed: %+v", e)
	}
	call("POST", "/v1/closes/"+studio+"/daily-review", map[string]string{"sync_id": reviewedXero}, 409)
	call("POST", "/v1/closes/"+studio+"/daily-review", map[string]string{"sync_id": reviewedXero, "source": "xero"}, 200)
	latestXero := snapshot("2 hours", "x-1", "x-2")
	if e := studioEntry(); e == nil || e.SyncID != latestXero || e.Total != 2 || len(e.New) != 1 {
		t.Fatalf("only what the reviewed snapshot lacked is new: %+v", e)
	}
	unread := snapshot("1 hour")
	if e := studioEntry(); e == nil || e.SyncID != unread || e.Unchecked == "" || len(e.New) != 0 {
		t.Fatalf("a newest snapshot without transactions is listed as not checked, never as clear or with older findings: %+v", e)
	}
	call("POST", "/v1/closes/"+studio+"/daily-review", map[string]string{"sync_id": unread, "source": "xero"}, 409)
	// A QuickBooks capture finished after the Xero snapshots is the one shown, and the only one that can be reviewed.
	quickbooksCapture := capture(studio, connect(studio, "Studio", org, "active"), [3]string{"no_payee", "Purchase", "q-1"})
	if e := studioEntry(); e == nil || e.Source != "quickbooks" || e.SyncID != quickbooksCapture {
		t.Fatalf("the newer QuickBooks capture wins: %+v", e)
	}
	call("POST", "/v1/closes/"+studio+"/daily-review", map[string]string{"sync_id": latestXero, "source": "xero"}, 409)
	call("POST", "/v1/closes/"+studio+"/daily-review", map[string]string{"sync_id": quickbooksCapture, "source": "quickbooks"}, 200)
	// A later QuickBooks capture whose evidence was superseded is passed over, as the close's own checks do.
	var stale, replacement string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'capture.json',$2,'{}','fixture') RETURNING id::text`, studio, rand.Text()).Scan(&stale))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'capture.json',$2,'{}','fixture') RETURNING id::text`, studio, rand.Text()).Scan(&replacement))
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$1 WHERE id=$2`, replacement, stale)
	must(err)
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,exceptions,evidence_id) SELECT close_id,connection_id,period,requested_by,state,now()+interval '1 minute',exceptions,$2 FROM quickbooks_entity_syncs WHERE id=$1`, quickbooksCapture, stale)
	must(err)
	if e := studioEntry(); e == nil || e.SyncID != quickbooksCapture {
		t.Fatalf("a capture with superseded evidence is not the one shown: %+v", e)
	}
	var reviewedSync, reviewedSnapshot sql.NullString
	must(db.QueryRow(`SELECT sync_id::text,xero_snapshot_id::text FROM daily_reviews WHERE close_id=$1`, studio).Scan(&reviewedSync, &reviewedSnapshot))
	if reviewedSync.String != quickbooksCapture || reviewedSnapshot.Valid {
		t.Fatalf("a review points at exactly one capture: %v %v", reviewedSync, reviewedSnapshot)
	}
}
