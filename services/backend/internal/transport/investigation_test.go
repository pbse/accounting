package transport

import (
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

// The assistant reads a close's records through the read-only tools before writing, only when the firm turned
// investigations on; a refused tool is told so; the answer cites what was read and the trace is kept.
func TestAssistantInvestigatesBeforeWriting(t *testing.T) {
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
	org := "org_Investigate" + rand.Text()
	var closeID, evidenceID string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Investigation fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&closeID))
	// A document whose whole text is a bare number would parse as JSON; it is still one text to redact.
	var cardID string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'card.txt','card-digest','4111111111111111','fixture') RETURNING id::text`, closeID).Scan(&cardID))
	text := "Acme Insurance Co\nInvoice INV-778\nBill to: Alpha\nAnnual premium USD 1,200.00 for coverage 2026-09-01 to 2027-08-31\nCard 4111 1111 1111 1111 on file"
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'insurance.txt','inv-digest',$2,'fixture') RETURNING id::text`, closeID, text).Scan(&evidenceID))
	defer func() {
		for _, q := range []string{`DELETE FROM assistant_answers WHERE organization_id=$1`, `DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organization_policy_changes WHERE organization_id=$1`, `DELETE FROM model_calls WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	calls, toolRounds := 0, 0
	var toolsOffered []string
	var outputsSeen []string
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var sent struct {
			Stream bool `json:"stream"`
			Tools  []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Input []map[string]any `json:"input"`
		}
		json.Unmarshal(raw, &sent)
		if strings.Contains(string(raw), "4111 1111 1111 1111") || strings.Contains(string(raw), "4111111111111111") {
			t.Fatalf("a card number reached the model: %s", raw)
		}
		if len(sent.Tools) > 0 {
			toolRounds++
			toolsOffered = nil
			for _, tool := range sent.Tools {
				toolsOffered = append(toolsOffered, tool.Name)
			}
			for _, item := range sent.Input {
				if item["type"] == "function_call_output" {
					outputsSeen = append(outputsSeen, item["output"].(string))
				}
			}
			var body string
			if toolRounds == 1 {
				body = `{"status":"completed","output":[{"type":"function_call","call_id":"c1","name":"read_evidence_text","arguments":"{\"evidence_id\":\"` + evidenceID + `\",\"close_id\":\"999\"}"},{"type":"function_call","call_id":"c2","name":"export_close_package","arguments":"{}"},{"type":"function_call","call_id":"c3","name":"read_evidence_text","arguments":"{\"evidence_id\":\"` + cardID + `\"}"}]}`
			} else {
				body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Read the invoice."}]}]}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
		}
		// The final, streamed answer quotes the document the model read.
		var given struct {
			Reads map[string]string `json:"reads"`
		}
		json.Unmarshal([]byte(sent.Input[1]["content"].(string)), &given)
		if toolRounds > 0 && (!strings.Contains(given.Reads["read_evidence_text#1"], "Annual premium USD 1,200.00") || strings.Contains(given.Reads["read_evidence_text#1"], "4111")) {
			t.Fatalf("the document text is a read for the final answer, redacted: %q", given.Reads["read_evidence_text#1"])
		}
		answer, _ := json.Marshal(map[string]any{"answerable": true, "answer": "The invoice is an annual premium of USD 1,200.00.", "support": []map[string]string{{"source": "read_evidence_text#1", "quote": "Annual premium USD 1,200.00"}}})
		lines := []string{}
		for i := 0; i < len(answer); i += 9 {
			delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": string(answer[i:min(i+9, len(answer))])})
			lines = append(lines, "event: response.output_text.delta\ndata: "+string(delta))
		}
		lines = append(lines, `event: response.completed`+"\n"+`data: {"type":"response.completed","response":{"status":"completed"}}`)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Join(lines, "\n\n") + "\n\n")), Header: http.Header{"Content-Type": {"text/event-stream"}}}, nil
	})
	store := modelHandler(t, savedCloses(db, strings.Repeat("a", 32)))
	call := func(subject string, permissions []string, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, strings.NewReader(string(raw))).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %v: %d %s", path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	ask := func(text string) (string, []string, []map[string]string) {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(string(call("user_a", nil, "/v1/closes/"+closeID+"/chat", map[string]string{"text": text}, 200))), "\n")
		var out struct {
			Messages []struct {
				Body      string              `json:"body"`
				Citations []map[string]string `json:"citations"`
			} `json:"messages"`
		}
		must(json.Unmarshal([]byte(lines[len(lines)-1]), &out))
		last := out.Messages[len(out.Messages)-1]
		return last.Body, strings.Split(last.Body, "\n"), last.Citations
	}
	question := "Does the insurance invoice cover the whole policy year?"
	// Investigations cannot be turned on before written answers, and until they are on the model gets no tools.
	call("user_admin", []string{"org:manage"}, "/v1/closes/organization/policy", map[string]any{"model_answers": true, "reason": "Pilot written answers"}, 200)
	if body, _, _ := ask(question); calls != 1 || toolRounds != 0 || strings.Contains(body, "Investigated") {
		t.Fatalf("with investigations off the model gets no tools: calls=%d rounds=%d %q", calls, toolRounds, body)
	}
	call("user_admin", []string{"org:manage"}, "/v1/closes/organization/policy", map[string]any{"investigations": true, "reason": "Pilot investigations"}, 200)
	var recorded bool
	must(db.QueryRow(`SELECT assistant_investigations FROM organization_policy_changes WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&recorded))
	if !recorded {
		t.Fatal("the policy change is recorded")
	}
	calls, toolRounds = 0, 0
	body, lines, cited := ask(question)
	if calls != 3 || toolRounds != 2 {
		t.Fatalf("two tool rounds then the final answer: calls=%d rounds=%d", calls, toolRounds)
	}
	if !strings.HasPrefix(body, "The invoice is an annual premium of USD 1,200.00.") || lines[1] != "Investigated: read_evidence_text "+evidenceID+" (ok); export_close_package (refused); read_evidence_text "+cardID+" (ok)" || !strings.Contains(body, "Written by the model after it read") {
		t.Fatalf("the kept answer lists what was read and says it was investigated: %q", body)
	}
	if len(cited) != 1 || cited[0]["label"] != "Evidence #"+evidenceID || cited[0]["href"] != "/dashboard/closes/"+closeID+"?tab=evidence" {
		t.Fatalf("the read document is linked: %v", cited)
	}
	for _, tool := range toolsOffered {
		if tool == "export_close_package" || tool == "prepare_close" || tool == "add_evidence" || tool == "list_closes" {
			t.Fatalf("only this close's read-only tools are offered: %v", toolsOffered)
		}
	}
	if len(outputsSeen) != 3 || !strings.Contains(outputsSeen[0], "Annual premium") || !strings.HasPrefix(outputsSeen[1], "refused: not a read this assistant offers") || !strings.Contains(outputsSeen[2], "removed") {
		t.Fatalf("the model is told each read's result, and why a read was refused: %v", outputsSeen)
	}
	// Turning written answers off turns investigations off with them, and they cannot be turned on alone.
	call("user_admin", []string{"org:manage"}, "/v1/closes/organization/policy", map[string]any{"model_answers": false, "reason": "Pause written answers"}, 200)
	var still bool
	must(db.QueryRow(`SELECT assistant_investigations FROM organizations WHERE id=$1`, org).Scan(&still))
	call("user_admin", []string{"org:manage"}, "/v1/closes/organization/policy", map[string]any{"investigations": true, "reason": "Try investigations alone"}, 200)
	var alone bool
	must(db.QueryRow(`SELECT assistant_investigations FROM organizations WHERE id=$1`, org).Scan(&alone))
	if still || alone {
		t.Fatalf("investigations follow written answers off and cannot be on without them: still=%v alone=%v", still, alone)
	}
	var trace string
	must(db.QueryRow(`SELECT trace::text FROM assistant_answers WHERE organization_id=$1 AND outcome='kept' ORDER BY id DESC LIMIT 1`, org).Scan(&trace))
	var steps []struct {
		Tool, Args, Status string
		Bytes              int
	}
	must(json.Unmarshal([]byte(trace), &steps))
	if len(steps) != 3 || steps[0].Tool != "read_evidence_text" || steps[0].Args != evidenceID || steps[0].Status != "ok" || steps[0].Bytes == 0 || steps[1].Tool != "export_close_package" || steps[1].Status != "refused" || steps[2].Status != "ok" {
		t.Fatalf("the trace keeps each read, its outcome and size, not its content: %s", trace)
	}
	if strings.Contains(trace, "Annual premium") {
		t.Fatalf("the trace holds no document content: %s", trace)
	}
}
