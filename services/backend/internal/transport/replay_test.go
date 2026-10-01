package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestReplayComparesTheCurrentModelWithConfirmedReadings(t *testing.T) {
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
	var closeID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Replay "+rand.Text()).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("x", 32)
	handler := modelHandler(t, apiWithStore(db, token))
	replayOnce := func(want int) (out struct {
		Items []struct {
			Source  string   `json:"source_name"`
			Kept    bool     `json:"kept"`
			Changed []string `json:"changed_fields"`
			Error   string   `json:"error"`
		}
	}) {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/replay", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("replay: %d %s", w.Code, w.Body.String())
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	document := func(name, content string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, closeID, name, rand.Text(), content).Scan(&id))
		return id
	}
	allianz := document("allianz.txt", "Insurance coverage costs USD 1,200.00.\nBook to Prepaid insurance.")
	hiscox := document("hiscox.txt", "Hiscox premium USD 800.00.\nBook to Prepaid insurance.")
	lease := document("lease.txt", "Lease deposit USD 500.00.")
	globex := document("globex.txt", "Client entity: Globex LLC\nInsurance USD 50.00.")
	rules := document("rules.txt", "Insurance USD 70.00.")
	var inventory string
	tx, err := db.BeginTx(context.Background(), nil)
	must(err)
	_, inventory, err = preparation.AgentInventory(context.Background(), tx, closeID, "0")
	tx.Rollback()
	must(err)
	confirm := func(source, engine, digest, facts, input, at string) {
		t.Helper()
		var job string
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,$3,'completed','user_partner') RETURNING id::text`, closeID, rand.Text(), `[{"kind":"prepaid","input":`+input+`,"sources":[]}]`).Scan(&job))
		_, e := db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal,agent_checkpoint,confirmed_by,confirmed_at) VALUES($1,$2,$3,$4,'user_partner',$5::timestamptz)`, job, source,
			`{"kind":"prepaid","engine":"`+engine+`","inventory_digest":"`+digest+`","facts":`+facts+`}`, `{"inventory_digest":"`+inventory+`","selected_ids":["`+source+`"]}`, at)
		must(e)
	}
	confirm(allianz, "openai", inventory, `[{"field":"prepaid_account","value":"Prepaid insurance"},{"field":"amount","value":"1200.00"}]`, `{"prepaid_account":"Prepaid insurance","amount":"1200.00"}`, "2026-10-01T10:00:00Z")
	confirm(hiscox, "openai", inventory, `[{"field":"prepaid_account","value":"Prepaid insurance"},{"field":"amount","value":"800.00"},{"field":"service_end","value":""}]`, `{"prepaid_account":"Prepaid insurance","amount":"800.00","service_end":"2027-08-31","method":"straight_line"}`, "2026-10-01T12:00:00Z")
	confirm(lease, "openai", "stale-inventory", `[{"field":"amount","value":"500.00"}]`, `{"amount":"500.00"}`, "2026-10-01T11:00:00Z")
	confirm(globex, "openai", inventory, `[{"field":"amount","value":"50.00"}]`, `{"amount":"50.00"}`, "2026-10-01T09:00:00Z")
	confirm(rules, "local", inventory, `[{"field":"amount","value":"70.00"}]`, `{"amount":"70.00"}`, "2026-10-02T09:00:00Z")

	replayOnce(503)
	t.Setenv("OPENAI_API_KEY", "synthetic-key")
	t.Setenv("OPENAI_MODEL", "synthetic-model")
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	var mock sync.Mutex
	calls, changeDuringRead := 0, false
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		mock.Lock()
		calls++
		if changeDuringRead {
			document("late.txt", "A document added while the model was reading.")
			changeDuringRead = false
		}
		mock.Unlock()
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Input []struct{ Content string } `json:"input"`
		}
		json.Unmarshal(raw, &request)
		var context struct {
			Primary string `json:"primary_source_id"`
		}
		json.Unmarshal([]byte(request.Input[1].Content), &context)
		output := `{"kind":"prepaid","summary":"Insurance schedule.","questions":[],"sources":[{"id":"` + allianz + `","role":"invoice"}],"facts":[{"field":"prepaid_account","value":"Prepaid insurance","source_id":"` + allianz + `","quote":"Book to Prepaid insurance."},{"field":"amount","value":"1200.00","source_id":"` + allianz + `","quote":"Insurance coverage costs USD 1,200.00."}]}`
		if context.Primary == hiscox {
			output = `{"kind":"prepaid","summary":"Hiscox schedule.","questions":[],"sources":[{"id":"` + hiscox + `","role":"invoice"}],"facts":[{"field":"prepaid_account","value":"Prepaid rent","source_id":"` + hiscox + `","quote":"Book to Prepaid insurance."}]}`
		}
		body, _ := json.Marshal(map[string]any{"id": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": output}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})
	counts := func() string {
		t.Helper()
		var out string
		must(db.QueryRow(`SELECT (SELECT count(*) FROM workpapers WHERE close_id=$1)||'/'||(SELECT count(*) FROM preparation_jobs WHERE close_id=$1)||'/'||(SELECT count(*) FROM evidence WHERE close_id=$1)||'/'||(SELECT string_agg(md5(proposal::text),',' ORDER BY job_id) FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id=$1)`, closeID).Scan(&out))
		return out
	}
	before := counts()
	got := replayOnce(200)
	outcome := map[string]string{}
	for _, item := range got.Items {
		slices.Sort(item.Changed)
		outcome[item.Source] = strings.Join(item.Changed, ",") + "|" + item.Error
		if item.Kept {
			outcome[item.Source] = "kept"
		}
	}
	if len(got.Items) != 3 || outcome["allianz.txt"] != "kept" || outcome["hiscox.txt"] != "amount,prepaid_account,service_end|" || !strings.Contains(outcome["lease.txt"], "changed since the reading was confirmed") || calls != 2 {
		t.Fatalf("the three newest model readings are replayed: same; differs, including a dropped amount and an end date the person supplied; evidence changed; %d calls: %v", calls, outcome)
	}
	replaying.Store("local-workspace (development)", true)
	replayOnce(429)
	replaying.Delete("local-workspace (development)")
	_, err = db.Exec(`UPDATE preparation_intakes SET confirmed_at='2026-10-03T00:00:00Z' WHERE source_id=$1`, globex)
	must(err)
	got = replayOnce(200)
	for _, item := range got.Items {
		if item.Source == "globex.txt" && !strings.Contains(item.Error, "names another client") {
			t.Fatalf("a document naming another client is not replayed: %+v", item)
		}
		if item.Source == "allianz.txt" {
			t.Fatal("only the three most recently confirmed readings are replayed")
		}
	}
	if calls != 3 || counts() != before {
		t.Fatalf("replay spends one call per readable document and saves nothing: %d calls, %s → %s", calls, before, counts())
	}
	changeDuringRead = true
	for _, item := range replayOnce(200).Items {
		if item.Source == "hiscox.txt" && !strings.Contains(item.Error, "changed during the replay") {
			t.Fatalf("evidence added during the model call leaves the reading unscored: %+v", item)
		}
	}

	// Each run is saved and the latest can be read again.
	r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/replay", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var saved struct {
		Run *struct {
			Model string
			Items []struct {
				Source string `json:"source_name"`
			}
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Run == nil || saved.Run.Model != "synthetic-model" || len(saved.Run.Items) != 3 {
		t.Fatalf("the latest replay is saved: %d %s", w.Code, w.Body.String())
	}
	runs := func(model string) (n int) {
		t.Helper()
		must(db.QueryRow(`SELECT count(*) FROM replay_runs WHERE close_id=$1 AND model=$2 AND actor='scheduled-replay'`, closeID, model).Scan(&n))
		return n
	}
	ctx := context.Background()
	must(sweepReplays(modelContext(t, ctx), db))
	if runs("synthetic-model") != 0 {
		t.Fatal("a firm already checked on this model is not replayed again")
	}
	t.Setenv("OPENAI_MODEL", "synthetic-model-2")
	must(sweepReplays(modelContext(t, ctx), db))
	if runs("synthetic-model-2") != 0 {
		t.Fatal("a run that scores no reading (the late document changed the evidence) does not count as a check")
	}
	_, err = db.Exec(`DELETE FROM evidence WHERE close_id=$1 AND name='late.txt'`, closeID)
	must(err)
	must(sweepReplays(modelContext(t, ctx), db))
	must(sweepReplays(modelContext(t, ctx), db))
	if runs("synthetic-model-2") != 1 {
		t.Fatalf("a new model is checked once per firm: %d", runs("synthetic-model-2"))
	}

	// The same model with a different per-purpose effort needs one fresh check.
	t.Setenv("LLM_REASONING_EFFORTS", `{"preparation":"high"}`)
	must(sweepReplays(modelContext(t, ctx), db))
	must(sweepReplays(modelContext(t, ctx), db))
	var highRuns int
	must(db.QueryRow(`SELECT count(*) FROM replay_runs WHERE close_id=$1 AND model='synthetic-model-2' AND reasoning_effort='high' AND actor='scheduled-replay'`, closeID).Scan(&highRuns))
	if runs("synthetic-model-2") != 2 || highRuns != 1 {
		t.Fatalf("a changed effort is checked once: total=%d high=%d", runs("synthetic-model-2"), highRuns)
	}

	// A confirmation that is not one readable plan is never sent to the model, so it cannot count as scored.
	_, err = db.Exec(`UPDATE preparation_jobs SET plans='[{}]' WHERE id=(SELECT job_id FROM preparation_intakes WHERE source_id=$1)`, hiscox)
	must(err)
	before = fmt.Sprint(calls)
	for _, item := range replayOnce(200).Items {
		if item.Source == "hiscox.txt" && !strings.Contains(item.Error, "not one readable plan") {
			t.Fatalf("an unreadable confirmation is unscored: %+v", item)
		}
	}
	if fmt.Sprint(calls) != before {
		t.Fatal("no model call is spent on an unreadable confirmation")
	}

	// At the firm's daily limit nothing is replayed, and the model is not marked checked.
	defer db.Exec(`UPDATE organizations SET max_model_calls_per_day=2000 WHERE id='local-development'`)
	_, err = db.Exec(`UPDATE organizations SET max_model_calls_per_day=(SELECT calls FROM model_calls WHERE organization_id='local-development' AND day=(now() AT TIME ZONE 'UTC')::date) WHERE id='local-development'`)
	must(err)
	spent := calls
	replayOnce(429)
	t.Setenv("OPENAI_MODEL", "synthetic-model-3")
	must(sweepReplays(modelContext(t, ctx), db))
	if calls != spent || runs("synthetic-model-3") != 0 {
		t.Fatalf("no model call is made past the limit: %d → %d, %d runs", spent, calls, runs("synthetic-model-3"))
	}
}
