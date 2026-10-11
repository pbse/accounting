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

	"close.local/backend/internal/preparation"
)

func TestCloseDeletionRequiresPolicyAndSettledWork(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Retention fixture " + rand.Text()
	var closeID, sourceID, paperID, jobID, emailConnection string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO close_effort(close_id,actor,minute,activity,seconds) VALUES($1,'fixture',date_trunc('minute',now()),'review',30)`, closeID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO close_effort_entries(close_id,actor,activity,minutes,note) VALUES($1,'fixture','prepare',5,'Offline fixture work')`, closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM email_connections WHERE id::text=$1`, emailConnection)
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, closeID)
		db.Exec(`DELETE FROM close_deletion_receipts WHERE close_id::text=$1`, closeID)
	}()
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Synthetic invoice','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,'{}') RETURNING id::text`, closeID, rand.Text()).Scan(&paperID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,'[]','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("r", 32)
	h := apiWithStore(db, token)
	base := "/v1/closes/" + closeID
	call := func(path string, body any, want int) []byte {
		t.Helper()
		method := "GET"
		if body != nil {
			method = "POST"
		}
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: HTTP %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var status retentionStatus
	json.Unmarshal(call(base+"/retention", nil, 200), &status)
	if len(status.Blockers) == 0 {
		t.Fatal("close without policy looked deletable")
	}
	call(base+"/retention", map[string]any{"retain_until": "2000-01-01", "legal_hold": true, "expected_version": 0, "reason": "Approved synthetic retention policy", "confirmed": true}, 200)
	json.Unmarshal(call(base+"/retention", nil, 200), &status)
	call(base+"/delete", map[string]any{"expected_version": status.Version, "entity": entity, "period": "2026-09", "reason_code": "retention_expired", "confirmed": true}, 409)
	call(base+"/retention", map[string]any{"retain_until": "2000-01-01", "legal_hold": false, "expected_version": status.Version, "reason": "Hold released by synthetic accountant", "confirmed": true}, 200)
	json.Unmarshal(call(base+"/retention", nil, 200), &status)
	if len(status.Blockers) == 0 {
		t.Fatal("active job did not block deletion")
	}
	if _, err = db.Exec(`UPDATE preparation_jobs SET state='cancelled' WHERE id::text=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,enabled,actor) VALUES('local-development','fixture@example.test','synthetic', $1,false,'fixture') RETURNING id::text`, []byte("fixture")).Scan(&emailConnection); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO email_delivery_events(connection_id,event_id,body_digest,provider_id,recipient,kind,occurred_at) VALUES($1,'unrelated','synthetic','unrelated-provider','other@example.test','bounced',now())`, emailConnection); err != nil {
		t.Fatal(err)
	}
	// A collection email that bounced: the address stays do not contact for the client after its month is deleted.
	var message string
	if err = db.QueryRow(`INSERT INTO collection_messages(organization_id,close_id,client_entity,company,capture_id,item_id,label,customer,amount,currency,recipient,subject,body,created_by) VALUES('local-development',$1,$2,'quickbooks:fixture','1','2','Invoice INV-2','Beta',10,'USD','ap@beta.example','Reminder','Synthetic reminder','fixture') RETURNING id::text`, closeID, entity).Scan(&message); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM collection_contact_preferences WHERE organization_id='local-development' AND lower(client_entity)=lower($1)`, entity)
	if _, err = db.Exec(`INSERT INTO email_outbox(id,collection_id,connection_id,payload,approved_by,firm_authority,approved_until,state,provider_id) VALUES($1,$2,$3,'x','fixture',true,now(),'accepted','collect-provider')`, rand.Text(), message, emailConnection); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO email_delivery_events(connection_id,event_id,body_digest,provider_id,recipient,kind,occurred_at,close_id) VALUES($1,'collect-bounce','synthetic','collect-provider','AP@beta.example','bounced',now(),$2)`, emailConnection, closeID); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(call(base+"/retention", nil, 200), &status)
	if len(status.Blockers) != 0 {
		t.Fatal(status.Blockers)
	}
	if _, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,retired_by,retired_at,retire_reason) VALUES('local-development',$1,'fixture trading name','fixture',NULL,NULL,NULL),('local-development',upper($1),'fixture old name','fixture','fixture',now(),'Old name')`, entity); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM client_aliases WHERE organization_id='local-development' AND lower(client_entity)=lower($1)`, entity)
	aliases := func() (n int) {
		t.Helper()
		if e := db.QueryRow(`SELECT count(*) FROM client_aliases WHERE organization_id='local-development' AND lower(client_entity)=lower($1)`, entity).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	// The client's books sent as files for the month, and their readings, go with it; another month's stay.
	var monthFile, otherFile string
	for period, id := range map[string]*string{"2026-09": &monthFile, "2026-08": &otherFile} {
		if err = db.QueryRow(`INSERT INTO books_imports(organization_id,client_entity,period,name,digest,content,format,actor) VALUES('local-development',$1,$2,'tb.csv',md5(random()::text)||md5(random()::text),'x','csv','fixture') RETURNING id::text`, entity, period).Scan(id); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO books_layouts(organization_id,client_entity,import_id,layout,reading,reason,actor) VALUES('local-development',$1,$2::bigint,'{}','{}','Synthetic','fixture')`, entity, *id); err != nil {
			t.Fatal(err)
		}
	}
	defer db.Exec(`DELETE FROM books_layouts WHERE import_id::text=$1`, otherFile)
	defer db.Exec(`DELETE FROM books_imports WHERE id::text=$1`, otherFile)
	// As the client's last month, off the roster, it would leave August's file out of reach: it waits for that file.
	json.Unmarshal(call(base+"/retention", nil, 200), &status)
	if !strings.Contains(strings.Join(status.Blockers, " "), "delete the files it sent for other months") {
		t.Fatalf("the last month waits for the client's other files: %v", status.Blockers)
	}
	var august string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-08') RETURNING id::text`, entity).Scan(&august); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, august)
	json.Unmarshal(call(base+"/retention", nil, 200), &status)
	call(base+"/delete", map[string]any{"expected_version": status.Version, "entity": entity, "period": "2026-09", "reason_code": "retention_expired", "confirmed": true}, 200)
	var files, readings int
	if e := db.QueryRow(`SELECT (SELECT count(*) FROM books_imports WHERE id::text IN ($1,$2)),(SELECT count(*) FROM books_layouts WHERE import_id::text IN ($1,$2))`, monthFile, otherFile).Scan(&files, &readings); e != nil || files != 1 || readings != 1 {
		t.Fatalf("the month's files go with it, another month's stay: %d %d %v", files, readings, e)
	}
	if aliases() != 2 {
		t.Fatal("deleting a month whose retention expired keeps the client's other names")
	}
	var kept bool
	if e := db.QueryRow(`SELECT do_not_contact FROM collection_contact_preferences WHERE organization_id='local-development' AND lower(client_entity)=lower($1) AND recipient='ap@beta.example' ORDER BY id DESC LIMIT 1`, entity).Scan(&kept); e != nil || !kept {
		t.Fatalf("a deleted month's bounced collection address stays do not contact: %v %v", kept, e)
	}
	// Deleting a month at the client's request forgets the other names its documents use, retired ones too.
	var requested string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-10') RETURNING id::text`, entity).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, requested)
	defer db.Exec(`DELETE FROM close_deletion_receipts WHERE close_id::text=$1`, requested)
	call("/v1/closes/"+requested+"/retention", map[string]any{"retain_until": "2000-01-01", "legal_hold": false, "expected_version": 0, "reason": "Client asked for deletion", "confirmed": true}, 200)
	var other retentionStatus
	json.Unmarshal(call("/v1/closes/"+requested+"/retention", nil, 200), &other)
	call("/v1/closes/"+requested+"/delete", map[string]any{"expected_version": other.Version, "entity": entity, "period": "2026-10", "reason_code": "client_request", "confirmed": true}, 200)
	if aliases() != 0 {
		t.Fatal("deleting a month at the client's request forgets its other names")
	}
	var digest, period string
	var productionCount int
	if err = db.QueryRow(`SELECT entity_digest,period,production_postings FROM close_deletion_receipts WHERE close_id::text=$1`, closeID).Scan(&digest, &period, &productionCount); err != nil || digest != preparation.Digest([]byte("local-development\x00"+strings.ToLower(entity)+"\x002026-09")) || period != "2026-09" || productionCount != 0 {
		t.Fatalf("deletion receipt missing exact scope: %s %s %d %v", digest, period, productionCount, err)
	}
	var remaining int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM client_closes WHERE id::text=$1)+(SELECT count(*) FROM evidence WHERE id::text=$2)+(SELECT count(*) FROM workpapers WHERE id::text=$3)+(SELECT count(*) FROM preparation_jobs WHERE id::text=$4)`, closeID, sourceID, paperID, jobID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("derived close records remained: %d %v", remaining, err)
	}
}
