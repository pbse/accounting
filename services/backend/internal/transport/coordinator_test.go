package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestClosePreparationScopeReusesDurableJobs(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Coordinator "+rand.Text()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"close_preparations", "preparation_jobs", "evidence", "client_closes"} {
			key := "close_id"
			if table == "client_closes" {
				key = "id"
			}
			if _, e := db.Exec("DELETE FROM "+table+" WHERE "+key+"=$1", id); e != nil {
				t.Error(e)
			}
		}
	}()
	token := strings.Repeat("c", 32)
	h := apiWithStore(db, token)
	base := "/v1/closes/" + id + "/prepare-close"
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	source := func(name string) string {
		t.Helper()
		tx, e := db.BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		var sid string
		if e = tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$2,'Invoice amount: 1200.00','fixture') RETURNING id::text`, id, name).Scan(&sid); e != nil {
			t.Fatal(e)
		}
		if _, e = preparation.StartIntake(context.Background(), tx, id, sid, "fixture"); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		return sid
	}
	type item struct {
		Job   string `json:"job_id"`
		Stale bool   `json:"stale"`
		State string `json:"state"`
	}
	type status struct {
		Digest    string `json:"inventory_digest"`
		Inventory []item `json:"inventory"`
		Run       *struct {
			ID     string `json:"id"`
			Digest string `json:"inventory_digest"`
			Items  []item `json:"items"`
		} `json:"run"`
	}
	read := func() status {
		t.Helper()
		var s status
		if e := json.Unmarshal(call("GET", base, nil, 200), &s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	empty := read()
	call("POST", base, map[string]any{"inventory_digest": empty.Digest, "confirmed": true}, 422)
	first := source("first invoice")
	source("second invoice")
	s := read()
	if len(s.Inventory) != 2 {
		t.Fatal("scope dropped evidence")
	}
	approve := map[string]any{"inventory_digest": s.Digest, "confirmed": true}
	call("POST", base, approve, 200)
	run := read()
	if run.Run == nil || len(run.Run.Items) != 2 {
		t.Fatal("scope not durable")
	}
	call("POST", base, approve, 200)
	if read().Run.ID != run.Run.ID {
		t.Fatal("retry created another parent")
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id=$1`, id).Scan(&count); e != nil || count != 2 {
		t.Fatal("parent duplicated child jobs", e)
	}
	if _, e := db.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, s.Inventory[0].Job); e != nil {
		t.Fatal(e)
	}
	if read().Run.Items[0].State != "waiting_client" {
		t.Fatal("parent did not follow child state")
	}
	replacement := source("third invoice")
	call("POST", base, approve, 409)
	if _, e := db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id=$1`, first, replacement); e != nil {
		t.Fatal(e)
	}
	changed := read()
	if !changed.Run.Items[0].Stale || changed.Digest == changed.Run.Digest {
		t.Fatal("replaced evidence did not invalidate pinned scope")
	}
	call("POST", base, map[string]any{"inventory_digest": changed.Digest, "confirmed": true}, 200)
	next := read()
	if next.Run.ID == run.Run.ID || len(next.Run.Items) != 2 {
		t.Fatal("updated scope did not preserve history and exclude replaced source")
	}
	call("POST", "/v1/closes/0/prepare-close", approve, 404)
}
