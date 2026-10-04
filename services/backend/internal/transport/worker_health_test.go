package transport

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWorkerHealthNeedsBothLoopsBeatingRecently(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	unconfigured := httptest.NewRecorder()
	apiWithStore(nil, strings.Repeat("h", 32)).ServeHTTP(unconfigured, httptest.NewRequest("GET", "/healthz/worker", nil))
	if unconfigured.Code != 503 {
		t.Fatalf("without a database the worker is not known to be alive: %d", unconfigured.Code)
	}
	handler := apiWithStore(db, strings.Repeat("h", 32))
	check := func(want int, says string) {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/healthz/worker", nil))
		if w.Code != want || !strings.Contains(w.Body.String(), says) {
			t.Fatalf("worker health: %d %s", w.Code, w.Body.String())
		}
	}
	var saved []byte
	db.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(h)),'[]') FROM worker_heartbeats h`).Scan(&saved)
	defer func() {
		db.Exec(`DELETE FROM worker_heartbeats`)
		db.Exec(`INSERT INTO worker_heartbeats SELECT * FROM jsonb_populate_recordset(NULL::worker_heartbeats,$1)`, string(saved))
	}()
	if _, err = db.Exec(`DELETE FROM worker_heartbeats`); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO worker_heartbeats(loop,beat_at) VALUES('worker',now()),('old_loop',now()-interval '1 day')`)
	check(503, `"stale":["workflow_engine"]`)
	db.Exec(`INSERT INTO worker_heartbeats(loop,beat_at) VALUES('workflow_engine',now()-interval '6 minutes')`)
	check(503, `"stale":["workflow_engine"]`)
	db.Exec(`UPDATE worker_heartbeats SET beat_at=now() WHERE loop='workflow_engine'`)
	check(200, `"ok":true`)
}
