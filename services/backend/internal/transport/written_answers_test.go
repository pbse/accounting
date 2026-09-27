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
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Betamax Holdings','2026-09')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO close_scopes(close_id,scope,actor) VALUES($1,'{"description":"Card statements for 4222 2222 2222 2 and the lease","required_papers":[],"external_steps":[]}','fixture')`, alpha)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM close_scopes WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organization_policy_changes WHERE organization_id=$1`, `DELETE FROM model_calls WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	calls := 0
	var payload string
	var respond func(reads map[string]string) map[string]any
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
		output, _ := json.Marshal(respond(context.Reads))
		body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
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
	ask := func(text string) (string, []citation) {
		t.Helper()
		var out struct {
			Messages []struct {
				Body      string     `json:"body"`
				Citations []citation `json:"citations"`
			} `json:"messages"`
		}
		must(json.Unmarshal(call("user_a", nil, "/v1/closes/"+alpha+"/chat", map[string]string{"text": text}, 200), &out))
		last := out.Messages[len(out.Messages)-1]
		return last.Body, last.Citations
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
	answer := func(source, quote string) map[string]any {
		return map[string]any{"answerable": true, "answer": "No vendor payments are captured yet.", "support": []any{map[string]string{"source": source, "quote": quote}}}
	}

	if body, _ := ask(question); !strings.HasPrefix(body, help) || calls != 0 {
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

	respond = func(reads map[string]string) map[string]any { return answer("status", headline(reads)) }
	body, cited := ask(question)
	if calls != 1 || body != "No vendor payments are captured yet.\n"+writtenNote || len(cited) != 1 || cited[0] != (citation{"Close overview", "/dashboard/closes/" + alpha}) {
		t.Fatalf("a grounded answer is kept with its fixed link and says it was written by the model: %q %v calls=%d", body, cited, calls)
	}
	if !strings.Contains(payload, `"store":false`) || !strings.Contains(payload, "untrusted data") || !strings.Contains(payload, "card number ending 1111 removed") || strings.Contains(payload, "4111 1111 1111 1111") || strings.Contains(payload, "Betamax") || strings.Contains(payload, "4222 2222 2222 2") || !strings.Contains(payload, "card number ending 2222 removed") || strings.Contains(payload, org) || strings.Contains(payload, "coverage_resolution") {
		t.Fatalf("the model gets the redacted question and only this close's reads, unstored: %s", payload)
	}
	for name, bad := range map[string]func(map[string]string) map[string]any{
		"a quote the read does not hold": func(map[string]string) map[string]any { return answer("status", "Everything was approved and posted") },
		"a read that was not sent":       func(reads map[string]string) map[string]any { return answer("draft", headline(reads)) },
		"an unanswerable question": func(map[string]string) map[string]any {
			return map[string]any{"answerable": false, "answer": "", "support": []any{}}
		},
	} {
		respond = bad
		if body, _ := ask(question); !strings.HasPrefix(body, help) {
			t.Fatalf("%s leaves the help reply: %q", name, body)
		}
	}
	spent := calls
	if body, _ := ask("Where does this close stand?"); strings.HasPrefix(body, help) || calls != spent {
		t.Fatalf("a question a fixed read answers never goes to the model: %q", body)
	}
	_, err = db.Exec(`INSERT INTO model_calls(organization_id,day,calls) VALUES($1,(now() AT TIME ZONE 'UTC')::date,1) ON CONFLICT DO NOTHING`, org)
	must(err)
	_, err = db.Exec(`UPDATE organizations SET max_model_calls_per_day=(SELECT calls FROM model_calls WHERE organization_id=$1 AND day=(now() AT TIME ZONE 'UTC')::date) WHERE id=$1`, org)
	must(err)
	if body, _ := ask(question); !strings.HasPrefix(body, help) || !strings.Contains(body, "daily model limit is reached") || calls != spent {
		t.Fatalf("at the firm's model limit nothing is sent and the reply says why: %q calls=%d", body, calls)
	}
}
