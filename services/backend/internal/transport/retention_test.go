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
	json.Unmarshal(call(base+"/retention", nil, 200), &status)
	if len(status.Blockers) != 0 {
		t.Fatal(status.Blockers)
	}
	call(base+"/delete", map[string]any{"expected_version": status.Version, "entity": entity, "period": "2026-09", "reason_code": "retention_expired", "confirmed": true}, 200)
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
