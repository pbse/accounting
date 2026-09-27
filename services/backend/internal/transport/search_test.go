package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestSearchFindsMatchesOnlyInAccessibleCloses(t *testing.T) {
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
	org := "org_Search" + rand.Text()
	var alpha, beta, invoice, old, reply, paper, revised, connection, capture, sync string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Search fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1)`, `DELETE FROM work_replies WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`, `DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`, `DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_entity_syncs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM procedure_versions WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	evidence := func(close, name, content string) string {
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, close, name, rand.Text(), content).Scan(&id))
		return id
	}
	old = evidence(alpha, "premium-old.txt", "Earlier Zurich draft invoice")
	invoice = evidence(alpha, "premium.txt", "Invoice from Zurich Insurance for the annual policy. Portal password: hunter2. Discount code 50_off applies.")
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2,replacement_reason='Revised' WHERE id=$1`, old, invoice)
	must(err)
	evidence(beta, "beta.txt", "Zurich invoice for another client")
	evidence(alpha, "other.txt", "Discount code 50xoff does not apply.")
	reply = evidence(alpha, "reply.txt", "Client reply: the Zurich premium covers October to September.")
	var draft, request string
	must(db.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please confirm the policy.','[]','d','fixture') RETURNING id::text`, alpha).Scan(&draft))
	must(db.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by) VALUES($1,1,'client@example.test','fixture') RETURNING id::text`, draft).Scan(&request))
	_, err = db.Exec(`INSERT INTO work_replies(request_id,source_id,recorded_by,received_from) VALUES($1,$2,'fixture','client@example.test')`, request, reply)
	must(err)
	// A reply recorded against another close's request does not make this close's document a reply.
	stray := evidence(alpha, "note.txt", "Zurich broker note")
	var betaDraft, betaRequest string
	must(db.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Other question','[]','d','fixture') RETURNING id::text`, beta).Scan(&betaDraft))
	must(db.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by) VALUES($1,1,'client@example.test','fixture') RETURNING id::text`, betaDraft).Scan(&betaRequest))
	_, err = db.Exec(`INSERT INTO work_replies(request_id,source_id,recorded_by,received_from) VALUES($1,$2,'fixture','client@example.test')`, betaRequest, stray)
	must(err)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','s1','{"input":{"source_reference":"Zurich policy (old)"}}') RETURNING id::text`, alpha).Scan(&revised))
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','s2',$2) RETURNING id::text`, alpha, `{"revises":"`+revised+`","input":{"source_reference":"Zurich policy 2026"}}`).Scan(&paper))
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'s2','approved','Matches the Zurich schedule','user_reviewer')`, paper)
	must(err)
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,$2,'Alpha','fixture','active',$3,now(),'123') RETURNING id::text`, alpha, org, rand.Text()).Scan(&connection))
	capture = evidence(alpha, "transactions.json", "{}")
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at) VALUES($1,$2,'2026-09','fixture','completed',$3,now()) RETURNING id::text`, alpha, connection, capture).Scan(&sync))
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill','41','1','2026-09-10',now(),'USD','1200.00','{"VendorRef":{"name":"Zurich Insurance"}}')`, sync)
	must(err)
	evidence(alpha, "Zurich card 4111 1111 1111 1111.txt", "Scanned card")
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor) VALUES($1,'Alpha','prepaid','Amortize Zurich premiums monthly',true,'Synthetic','fixture'),($1,'Alpha','accrual','Accrue Zurich audit fees',true,'Synthetic','fixture'),($1,'Alpha','accrual','Accrue Zurich audit fees',false,'Disabled','fixture'),($1,'Beta','prepaid','Beta Zurich rule',true,'Synthetic','fixture')`, org)
	must(err)

	store := savedCloses(db, strings.Repeat("a", 32))
	get := func(path string, want int, permissions ...string) (out struct {
		Results  []searchHit `json:"results"`
		Searched string      `json:"searched"`
	}) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: permissions}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return
	}
	kinds := func(hits []searchHit) map[string][]searchHit {
		out := map[string][]searchHit{}
		for _, h := range hits {
			out[h.Kind] = append(out[h.Kind], h)
		}
		return out
	}
	firm := get("/v1/closes/search?q="+url.QueryEscape("zurich"), 200)
	found := kinds(firm.Results)
	if len(found["document"]) != 3 || strings.Contains(found["document"][0].Title, "4111 1111") || found["document"][2].Href != "/dashboard/closes/"+alpha+"?tab=evidence#source-"+invoice || strings.Contains(found["document"][2].Snippet, "hunter2") || found["document"][1].Title != "note.txt" {
		t.Fatalf("the current document is found, linked to itself, with its password redacted: %+v", found["document"])
	}
	if len(found["reply"]) != 1 || len(found["draft"]) != 1 || found["draft"][0].Title != "prepaid draft #"+paper || found["draft"][0].Snippet != "Zurich policy 2026" {
		t.Fatalf("a client reply is marked, and only the current draft is found: %+v %+v", found["reply"], found["draft"])
	}
	if len(found["transaction"]) != 1 || found["transaction"][0].Title != "Bill 41 · 2026-09-10 · captured 1200.00 USD" || found["transaction"][0].Snippet != "Zurich Insurance" || found["transaction"][0].Href != "/dashboard/closes/"+alpha+"?tab=evidence#quickbooks-ledger" {
		t.Fatalf("a transaction of the latest capture is found with its captured amount: %+v", found["transaction"])
	}
	if len(found["procedure"]) != 1 || found["procedure"][0].Title != "prepaid procedure" || len(found["decision"]) != 1 || !strings.HasPrefix(found["decision"][0].Title, "approved by user_reviewer on ") {
		t.Fatalf("only the enabled current procedure of an accessible client and the review decision are found: %+v %+v", found["procedure"], found["decision"])
	}
	for _, h := range firm.Results {
		if strings.Contains(h.Close, "Beta") || strings.Contains(h.Href, "/"+beta) {
			t.Fatalf("nothing from a client the member cannot access: %+v", h)
		}
	}
	if !strings.Contains(firm.Searched, "the supported ledger rows of the latest QuickBooks and Xero transaction captures") || !strings.Contains(firm.Searched, "Stripe report documents") {
		t.Fatalf("the search states what it covered: %s", firm.Searched)
	}
	if hits := get("/v1/closes/search?q="+url.QueryEscape("50_off"), 200).Results; len(hits) != 1 {
		t.Fatalf("an underscore matches only itself: %+v", hits)
	}
	get("/v1/closes/search?q=ab", 422)
	get("/v1/closes/search?q="+url.QueryEscape("one two three four five six seven"), 422)
	get("/v1/closes/"+beta+"/search?q=zurich", 404)
	wide := get("/v1/closes/search?q=zurich", 200, "clients:all").Results
	narrow := get("/v1/closes/"+alpha+"/search?q=zurich", 200, "clients:all").Results
	if len(narrow) != len(firm.Results) || len(wide) <= len(narrow) {
		t.Fatalf("a close search stays in its close, even for a member who can see every client: %d %d %d", len(narrow), len(firm.Results), len(wide))
	}

	// A capture whose evidence was replaced is no longer searched.
	newer := evidence(alpha, "transactions-2.json", "{}")
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2,replacement_reason='Replaced' WHERE id=$1`, capture, newer)
	must(err)
	if hits := kinds(get("/v1/closes/search?q=zurich", 200).Results)["transaction"]; len(hits) != 0 {
		t.Fatalf("an invalidated capture is not searched: %+v", hits)
	}
	chat := func(path, text string) (out struct {
		Messages []struct {
			Body      string         `json:"body"`
			Citations []chatCitation `json:"citations"`
		} `json:"messages"`
		LiveCitations []chatCitation `json:"live_citations"`
	}) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"text": text})
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return
	}
	inClose := chat("/v1/closes/"+alpha+"/chat", "Find the Zurich invoice")
	answer := inClose.Messages[len(inClose.Messages)-1]
	if !strings.Contains(answer.Body, "for “Zurich invoice” in this close") || len(answer.Citations) == 0 || !strings.HasPrefix(answer.Citations[0].Label, "document: premium.txt") {
		t.Fatalf("a find question in a close links each match: %+v", answer)
	}
	firmChat := chat("/v1/closes/chat", "Search for Zurich")
	if last := firmChat.Messages[len(firmChat.Messages)-1]; len(last.Citations) != 0 || strings.Contains(last.Body, "Zurich") || len(firmChat.LiveCitations) == 0 {
		t.Fatalf("firm-wide matches are shown once and never stored: %+v %+v", last, firmChat.LiveCitations)
	}
}

func TestSearchFindsTheNewestXeroCaptureRows(t *testing.T) {
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
	org := "org_XeroSearch" + rand.Text()
	var alpha, beta string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Xero search fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	defer func() {
		for _, q := range []string{`DELETE FROM xero_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM xero_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	capture := func(close, entity, payee string) string {
		t.Helper()
		var connection, snapshot string
		if err := db.QueryRow(`SELECT id::text FROM xero_connections WHERE close_id=$1`, close).Scan(&connection); err == sql.ErrNoRows {
			must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) VALUES($1,$2,$3,'fixture','active',$4,now(),'t') RETURNING id::text`, close, org, entity, rand.Text()).Scan(&connection))
		}
		must(db.QueryRow(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor) VALUES($1,$2,'t','2026-09',$3,'{}','fixture') RETURNING id::text`, close, connection, rand.Text()).Scan(&snapshot))
		if payee != "" {
			_, err := db.Exec(`INSERT INTO xero_transaction_rows(snapshot_id,kind,provider_id,txn_date,currency,amount,record) VALUES($1,'Bill','b-9','2026-09-12','NZD','40.00',jsonb_build_object('DocNumber','B-9','VendorRef',jsonb_build_object('name',$2::text)))`, snapshot, payee)
			must(err)
		}
		return snapshot
	}
	capture(alpha, "Alpha", "Harbor Printing (older capture)")
	capture(alpha, "Alpha", "Harbor Printing")
	capture(beta, "Beta", "Harbor Printing")
	store := savedCloses(db, strings.Repeat("a", 32))
	search := func(q string) []searchHit {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/search?q="+url.QueryEscape(q), nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		var out struct {
			Results []searchHit `json:"results"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("search %q: %d %s", q, w.Code, w.Body.String())
		}
		return out.Results
	}
	hits := search("harbor printing")
	if len(hits) != 1 || hits[0].Kind != "transaction" || hits[0].Title != "Xero Bill B-9 · 2026-09-12 · captured 40.00 NZD" || hits[0].Href != "/dashboard/closes/"+alpha+"?tab=evidence#xero" || hits[0].Close != "Alpha · 2026-09" || !strings.Contains(hits[0].Snippet, "Harbor Printing") || strings.Contains(hits[0].Snippet, "older") {
		t.Fatalf("only the newest capture of an accessible client is found, linked to its Xero evidence: %+v", hits)
	}
	if hits := search("xero bill"); len(hits) != 1 {
		t.Fatalf("a Xero row is found by the word Xero: %+v", hits)
	}
	capture(alpha, "Alpha", "")
	if hits := search("harbor printing"); len(hits) != 0 {
		t.Fatalf("a newer capture without transactions leaves no older rows in the results: %+v", hits)
	}
}
