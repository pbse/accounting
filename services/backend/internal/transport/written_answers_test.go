package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestAssistantWritesAnswersOnlyWhenTheFirmAllows(t *testing.T) {
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
	org := "org_Written" + rand.Text()
	var alpha string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Written answers fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2024-06') RETURNING id::text`, org).Scan(&alpha))
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Betamax Holdings','2026-09')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO close_scopes(close_id,scope,actor) VALUES($1,'{"description":"Card statements for 4222 2222 2222 2 and the lease\\nBank login: owner, password: hunter2","required_papers":[],"external_steps":[]}','fixture')`, alpha)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM assistant_answers WHERE organization_id=$1`, `DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM close_scopes WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organization_policy_changes WHERE organization_id=$1`, `DELETE FROM model_calls WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	calls := 0
	var payload string
	var readsSent map[string]string
	type support struct {
		Source string `json:"source"`
		Quote  string `json:"quote"`
	}
	// The model writes "answerable" first, as the schema orders it.
	type modelAnswer struct {
		Answerable bool      `json:"answerable"`
		Answer     string    `json:"answer"`
		Support    []support `json:"support"`
	}
	var respond func(reads map[string]string) modelAnswer
	// events, when set, replaces the streamed events; revoke runs while the model is answering.
	var events []string
	revoke := ""
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		payload = string(raw)
		var sent struct {
			Input []struct {
				Content string `json:"content"`
			} `json:"input"`
		}
		var context struct {
			Reads map[string]string `json:"reads"`
		}
		json.Unmarshal(raw, &sent)
		json.Unmarshal([]byte(sent.Input[1].Content), &context)
		readsSent = context.Reads
		if revoke != "" {
			_, err := db.Exec(revoke, org)
			must(err)
		}
		output, _ := json.Marshal(respond(context.Reads))
		lines := events
		if lines == nil {
			// Small pieces split escapes across events, as a real stream can.
			for i := 0; i < len(output); i += 7 {
				delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": string(output[i:min(i+7, len(output))])})
				lines = append(lines, "event: response.output_text.delta\ndata: "+string(delta))
			}
			lines = append(lines, `event: response.completed`+"\n"+`data: {"type":"response.completed","response":{"status":"completed"}}`)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Join(lines, "\n\n") + "\n\n")), Header: http.Header{"Content-Type": {"text/event-stream"}}}, nil
	})
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %v: %d %s", path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type citation struct{ Label, Href string }
	// ask returns the saved reply, its links and the text that streamed before it.
	ask := func(text string) (string, []citation, string) {
		t.Helper()
		var out struct {
			Messages []struct {
				Body      string     `json:"body"`
				Citations []citation `json:"citations"`
			} `json:"messages"`
		}
		lines := strings.Split(strings.TrimSpace(string(call("user_a", nil, "/v1/closes/"+alpha+"/chat", map[string]string{"text": text}, 200))), "\n")
		streamed := ""
		for _, line := range lines[:len(lines)-1] {
			var delta struct{ Delta string }
			must(json.Unmarshal([]byte(line), &delta))
			streamed += delta.Delta
		}
		must(json.Unmarshal([]byte(lines[len(lines)-1]), &out))
		last := out.Messages[len(out.Messages)-1]
		return last.Body, last.Citations, streamed
	}
	help := "I answer from this close's saved work"
	question := "Which vendor did card 4111 1111 1111 1111 pay most this month?"
	// headline quotes a phrase the status read really holds.
	headline := func(reads map[string]string) string {
		var brief struct {
			Headline string `json:"headline"`
		}
		json.Unmarshal([]byte(reads["status"]), &brief)
		return brief.Headline[:20]
	}
	written := "No vendor payments are \"captured\" yet.\nThe ledger has no capture."
	answer := func(source, quote string) modelAnswer {
		return modelAnswer{true, written, []support{{source, quote}}}
	}

	if body, _, _ := ask(question); !strings.HasPrefix(body, help) || calls != 0 {
		t.Fatalf("with the policy off the model is never asked: %q calls=%d", body, calls)
	}
	call("user_a", nil, "/v1/closes/organization/policy", map[string]any{"model_answers": true, "reason": "Pilot written answers"}, 403)
	call("user_admin", []string{"org:manage"}, "/v1/closes/organization/policy", map[string]any{"model_answers": true, "reason": "Pilot written answers"}, 200)
	var recorded bool
	must(db.QueryRow(`SELECT assistant_model_answers FROM organization_policy_changes WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&recorded))
	if !recorded {
		t.Fatal("the policy change is recorded")
	}
	security := httptest.NewRecorder()
	store.ServeHTTP(security, httptest.NewRequest("GET", "/v1/closes/security", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_admin"}, Organization: org, Permissions: []string{"clients:all", "members:manage"}})))
	if security.Code != 200 || !strings.Contains(security.Body.String(), "Written assistant answers are on") {
		t.Fatalf("the security center says written answers are on: %d %s", security.Code, security.Body.String())
	}

	respond = func(reads map[string]string) modelAnswer { return answer("status", headline(reads)) }
	body, cited, streamed := ask(question)
	if calls != 1 || body != written+"\n"+writtenNote || len(cited) != 1 || cited[0] != (citation{"Close overview", "/dashboard/closes/" + alpha}) {
		t.Fatalf("a grounded answer is kept with its fixed link and says it was written by the model: %q %v calls=%d", body, cited, calls)
	}
	if streamed != written {
		t.Fatalf("the answer streams as it is written, escapes and all, before the saved reply: %q", streamed)
	}
	if !strings.Contains(payload, `"store":false`) || !strings.Contains(payload, "untrusted data") || !strings.Contains(payload, "card number ending 1111 removed") || strings.Contains(payload, "4111 1111 1111 1111") || strings.Contains(payload, "Betamax") || strings.Contains(payload, "4222 2222 2222 2") || !strings.Contains(payload, "card number ending 2222 removed") || strings.Contains(payload, org) || strings.Contains(payload, "coverage_resolution") {
		t.Fatalf("the model gets the redacted question and only this close's reads, unstored: %s", payload)
	}
	var readiness map[string]any
	if json.Unmarshal([]byte(readsSent["blockers"]), &readiness) != nil || readiness["state"] == nil || strings.Contains(payload, "hunter2") {
		t.Fatalf("a sign-in detail is removed from its own value and the rest of the read still arrives whole: %s", readsSent["blockers"])
	}
	// A streamed answer that fails its checks is withdrawn: the saved reply says so and never holds its text.
	for name, bad := range map[string]func(map[string]string) modelAnswer{
		"a quote the read does not hold": func(map[string]string) modelAnswer { return answer("status", "Everything was approved and posted") },
		"a read that was not sent":       func(reads map[string]string) modelAnswer { return answer("draft", headline(reads)) },
	} {
		respond = bad
		if body, _, streamed := ask(question); streamed != written || !strings.HasPrefix(body, "The written answer could not be checked against this close's saved work, so it was withdrawn. "+help) {
			t.Fatalf("%s is withdrawn after streaming: %q %q", name, body, streamed)
		}
	}
	var leaked int
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1 AND role='assistant' AND body LIKE '%captured%yet%'`, org).Scan(&leaked))
	if leaked != 1 {
		t.Fatalf("only the kept answer is stored: %d", leaked)
	}
	// Nothing streams for an unanswerable question, a refusal or a failed response.
	respond = func(map[string]string) modelAnswer {
		return modelAnswer{false, "Perhaps nothing was captured.", []support{}}
	}
	if body, _, streamed := ask(question); streamed != "" || !strings.HasPrefix(body, help) {
		t.Fatalf("an unanswerable question streams nothing and leaves the help reply: %q %q", body, streamed)
	}
	events = []string{`data: {"type":"response.refusal.delta","delta":"I can't help with that."}`, `data: {"type":"response.completed","response":{"status":"completed"}}`}
	if body, _, streamed := ask(question); streamed != "" || !strings.HasPrefix(body, help) {
		t.Fatalf("a refusal streams nothing and leaves the help reply: %q %q", body, streamed)
	}
	events = []string{`data: {"type":"response.failed","response":{"status":"failed"}}`}
	if body, _, streamed := ask(question); streamed != "" || !strings.Contains(body, "The agent model did not answer") {
		t.Fatalf("a failed response says the model did not answer: %q %q", body, streamed)
	}
	events = nil
	// A member removed while the model is answering sees none of its text, and nothing is saved.
	var before, after int
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1`, org).Scan(&before))
	respond, revoke = func(reads map[string]string) modelAnswer { return answer("status", headline(reads)) }, `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',false,'Left the firm','fixture')`
	if denied := string(call("user_a", nil, "/v1/closes/"+alpha+"/chat", map[string]string{"text": question}, 403)); strings.Contains(denied, "delta") {
		t.Fatalf("a removed member gets no streamed text: %s", denied)
	}
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Back','fixture')`, org)
	must(err)
	// Losing the client while an answer that never streams is written also saves nothing.
	respond, revoke = func(map[string]string) modelAnswer { return modelAnswer{false, "", []support{}} }, `INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',false,'Moved','fixture')`
	call("user_a", nil, "/v1/closes/"+alpha+"/chat", map[string]string{"text": question}, 403)
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1`, org).Scan(&after))
	revoke = ""
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Back','fixture')`, org)
	must(err)
	if after != before {
		t.Fatalf("nothing is saved for a member who lost access: %d %d", before, after)
	}
	spent := calls
	if body, _, _ := ask("Where does this close stand?"); strings.HasPrefix(body, help) || calls != spent {
		t.Fatalf("a question a fixed read answers never goes to the model: %q", body)
	}
	_, err = db.Exec(`INSERT INTO model_calls(organization_id,day,calls) VALUES($1,(now() AT TIME ZONE 'UTC')::date,1) ON CONFLICT DO NOTHING`, org)
	must(err)
	_, err = db.Exec(`UPDATE organizations SET max_model_calls_per_day=(SELECT calls FROM model_calls WHERE organization_id=$1 AND day=(now() AT TIME ZONE 'UTC')::date) WHERE id=$1`, org)
	must(err)
	if body, _, _ := ask(question); !strings.HasPrefix(body, help) || !strings.Contains(body, "daily model limit is reached") || calls != spent {
		t.Fatalf("at the firm's model limit nothing is sent and the reply says why: %q calls=%d", body, calls)
	}
	// Each attempt leaves its outcome and time, and no text; a limit hit has no time.
	var outcomes string
	must(db.QueryRow(`SELECT string_agg(outcome||':'||(latency_ms IS NOT NULL)::text,',' ORDER BY id) FROM assistant_answers WHERE organization_id=$1`, org).Scan(&outcomes))
	if outcomes != "kept:true,not_kept:true,not_kept:true,unanswerable:true,not_kept:true,failed:true,limit:false" {
		t.Fatalf("each written attempt is measured without its text: %s", outcomes)
	}
	tracked := httptest.NewRecorder()
	store.ServeHTTP(tracked, httptest.NewRequest("GET", "/v1/closes/track-record", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org})))
	var track struct {
		Assistant struct {
			Asked, Kept   int
			MedianSeconds *float64 `json:"median_seconds"`
		}
	}
	if json.Unmarshal(tracked.Body.Bytes(), &track) != nil || track.Assistant.Asked != 7 || track.Assistant.Kept != 1 || track.Assistant.MedianSeconds == nil {
		t.Fatalf("the track record counts written answers on closes the member can open: %s", tracked.Body.String())
	}
}
