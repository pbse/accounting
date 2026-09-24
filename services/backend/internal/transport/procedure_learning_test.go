package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestModelProcedureDraftUsesOnlySelectedSource(t *testing.T) {
	document := "Policy: Equal full months for annual prepaid insurance."
	quote := "Equal full months for annual prepaid insurance."
	output := map[string]string{"instructions": "Apply equal full months to annual prepaid insurance; confirm exclusions with the accountant.", "quote": quote}
	escapedDocument := ""
	calls := 0
	client := &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var payload struct {
			Store bool `json:"store"`
			Input []struct {
				Content string `json:"content"`
			} `json:"input"`
		}
		if r.URL.String() != "https://api.openai.com/v1/responses" || json.Unmarshal(raw, &payload) != nil || payload.Store || len(payload.Input) != 2 || strings.Contains(string(raw), "synthetic-key") || strings.Contains(string(raw), "Foreign client") {
			t.Fatal("model request exceeded its source boundary")
		}
		if calls <= 2 && !strings.Contains(payload.Input[1].Content, document) || calls == 3 && (strings.Contains(payload.Input[1].Content, document) || !strings.Contains(payload.Input[1].Content, "input.expense_account")) {
			t.Fatal("wrong model source context")
		}
		if calls == 4 {
			var contextData map[string]any
			if json.Unmarshal([]byte(payload.Input[1].Content), &contextData) != nil || contextData["document"] != escapedDocument {
				t.Fatal("bounded document was not sent intact")
			}
		}
		result, _ := json.Marshal(output)
		body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": string(result)}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	instructions, cited, err := modelProcedureDraft(context.Background(), client, "synthetic-key", "synthetic-model", "prepaid", prepaids.Method, document, nil)
	if err != nil || cited != quote || !strings.Contains(instructions, "equal full months") {
		t.Fatal("grounded proposal was lost", err)
	}
	output["quote"] = "A passage absent from the policy source"
	if _, _, err = modelProcedureDraft(context.Background(), client, "synthetic-key", "synthetic-model", "prepaid", prepaids.Method, document, nil); err == nil {
		t.Fatal("fabricated citation was accepted")
	}
	output = map[string]string{"instructions": "Reviewer changed the expense account; confirm policy and applicability.", "quote": ""}
	edits := []paperChange{{Field: "input.expense_account", Before: "Old", After: "New"}}
	if _, _, err = modelProcedureDraft(context.Background(), client, "synthetic-key", "synthetic-model", "prepaid", prepaids.Method, "", edits); err != nil {
		t.Fatal("bounded approved edits could not be drafted", err)
	}
	edits[0].After = strings.Repeat("A", 17000)
	if _, _, err = modelProcedureDraft(context.Background(), client, "synthetic-key", "synthetic-model", "prepaid", prepaids.Method, "", edits); err == nil || calls != 3 {
		t.Fatal("oversized edit payload reached the model")
	}
	escapedDocument = "Policy: " + strings.Repeat("<", 16384-len("Policy: ")-len(quote)-1) + "\n" + quote
	output = map[string]string{"instructions": "Review annual insurance under the cited equal-month policy.", "quote": quote}
	if _, _, err = modelProcedureDraft(context.Background(), client, "synthetic-key", "synthetic-model", "prepaid", prepaids.Method, escapedDocument, nil); err != nil || calls != 4 {
		t.Fatal("valid 16 KiB policy was rejected after JSON escaping", err)
	}
}

func TestLearnedExampleMustReplayExactApprovedResult(t *testing.T) {
	in := prepaids.Input{Entity: "Synthetic", SourceReference: "Policy example", Amount: "1200.00", ServiceStart: "2026-01-01", ServiceEnd: "2026-12-31", ClosePeriod: "2026-01", Currency: "USD", Basis: "accrual", ExpenseAccount: "Expense", PrepaidAccount: "Prepaid", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
	paper, err := prepaids.Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(paper)
	if method, err := replayLearnedExample("prepaid", raw); err != nil || method != prepaids.Method {
		t.Fatalf("approved baseline failed: %s %v", method, err)
	}
	paper.PeriodExpense = "999.99"
	raw, _ = json.Marshal(paper)
	if _, err := replayLearnedExample("prepaid", raw); err == nil {
		t.Fatal("changed expected result passed regression replay")
	}
}

func TestLearnedRuleRequiresApprovedExampleBeforeActivation(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_Learned" + rand.Text()
	var closeID, sourceID, paperID, reviewID string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Synthetic procedure learning')`, org)
	must(err)
	defer func() {
		db.Exec(`DELETE FROM procedure_example_approvals WHERE draft_id IN (SELECT id FROM procedure_drafts WHERE organization_id=$1)`, org)
		db.Exec(`UPDATE procedure_drafts SET activated_version_id=NULL WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM procedure_drafts WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Synthetic client','2026-01') RETURNING id::text`, org).Scan(&closeID))
	document := "Approved policy says equal full months for annual prepaid insurance."
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,purpose) VALUES($1,'policy.txt',$2,$3,'fixture','procedure') RETURNING id::text`, closeID, preparation.Digest([]byte(document)), document).Scan(&sourceID))
	in := prepaids.Input{Entity: "Synthetic client", SourceReference: "Synthetic invoice", Amount: "1200.00", ServiceStart: "2026-01-01", ServiceEnd: "2026-12-31", ClosePeriod: "2026-01", Currency: "USD", Basis: "accrual", ExpenseAccount: "Insurance expense", PrepaidAccount: "Prepaid insurance", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
	paper, err := prepaids.Prepare(in)
	must(err)
	raw, _ := json.Marshal(paper)
	var value map[string]any
	must(json.Unmarshal(raw, &value))
	value["sources"] = []map[string]string{{"id": sourceID, "digest": preparation.Digest([]byte(document)), "name": "policy.txt", "role": "support", "location": "line 1"}}
	raw, _ = json.Marshal(value)
	digest := preparation.Digest(raw)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, digest, string(raw)).Scan(&paperID))
	must(db.QueryRow(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic reviewed example','user_accountant') RETURNING id::text`, paperID, digest).Scan(&reviewID))
	mux := http.NewServeMux()
	registerEvidence(mux, db)
	registerProcedures(mux, db)
	registerProcedureLearning(mux, db)
	h := organizationBoundary(db, mux)
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "workpapers:review", "procedures:manage"}}
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req = req.WithContext(auth.WithActor(req.Context(), actor))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.Bytes()
	}
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "checklist.txt")
	must(err)
	_, err = part.Write([]byte("Firm checklist requires reviewed accounting policy."))
	must(err)
	must(writer.WriteField("purpose", "procedure"))
	must(writer.Close())
	req := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/evidence", &upload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(auth.WithActor(req.Context(), actor))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("policy upload: %d %s", recorder.Code, recorder.Body.String())
	}
	var jobs int
	must(db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id=$1`, closeID).Scan(&jobs))
	if jobs != 0 {
		t.Fatal("policy upload started an accounting job")
	}
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	must(err)
	inventory, _, err := preparation.AgentInventory(context.Background(), tx, closeID, "0")
	must(err)
	tx.Rollback()
	if len(inventory) != 0 {
		t.Fatal("policy source entered ordinary agent evidence")
	}
	base := "/v1/closes/" + closeID + "/procedures/drafts"
	var draft learnedDraft
	must(json.Unmarshal(call("POST", base, map[string]any{"client_entity": "Synthetic client", "kind": "prepaid", "method": prepaids.Method, "source_id": sourceID, "from_edits": false}, 200), &draft))
	if draft.Engine != "local" {
		t.Fatal("unconfigured proposal was not labeled local")
	}
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	previousTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = previousTransport }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		result, _ := json.Marshal(map[string]string{"instructions": "Proposed equal-month policy; accountant must review applicability.", "quote": document})
		body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": string(result)}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})
	call("POST", base, map[string]any{"client_entity": "Synthetic client", "kind": "prepaid", "method": prepaids.Method, "source_id": "", "from_edits": true}, 422)
	var modeled learnedDraft
	must(json.Unmarshal(call("POST", base, map[string]any{"client_entity": "Synthetic client", "kind": "prepaid", "method": prepaids.Method, "source_id": sourceID, "from_edits": false}, 200), &modeled))
	if modeled.Engine != "openai" || modeled.Model != "mock-only-model" || modeled.SourceQuote != document || modeled.ActivatedID != "" {
		t.Fatal("model draft was not saved with inactive provenance")
	}
	var impact struct {
		PreviousID string `json:"previous_id"`
		Digest     string `json:"impact_digest"`
	}
	must(json.Unmarshal(call("GET", "/v1/closes/procedures?impact_kind=prepaid&client_entity=Synthetic%20client", nil, 200), &impact))
	activation := map[string]any{"previous_id": impact.PreviousID, "impact_digest": impact.Digest, "draft_revision": draft.Revision, "reason": "Client accountant approved this rule", "confirmed": true}
	call("POST", base+"/"+draft.ID+"/activate", activation, 409)
	call("POST", base+"/"+draft.ID+"/examples", map[string]any{"workpaper_id": paperID, "digest": digest, "review_id": reviewID, "draft_revision": draft.Revision, "reason": "Approved monthly schedule and adjustment", "confirmed": true}, 200)
	call("POST", base+"/"+draft.ID+"/activate", activation, 200)
	var method string
	must(db.QueryRow(`SELECT method FROM procedure_versions WHERE draft_id=$1`, draft.ID).Scan(&method))
	if method != prepaids.Method {
		t.Fatal("activated version lost its reviewed method boundary")
	}
	retentionTx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	must(err)
	status, err := readRetention(context.Background(), retentionTx, closeID, org)
	must(err)
	retentionTx.Rollback()
	if !slices.ContainsFunc(status.Blockers, func(b string) bool { return strings.Contains(b, "active procedure rule") }) {
		t.Fatal("retention did not protect an active rule's source and approved example")
	}
	other := in
	other.Method = prepaids.DailyMethod
	other.ExamplesConfirmed = true
	otherPaper, err := prepaids.Prepare(other)
	must(err)
	otherRaw, _ := json.Marshal(otherPaper)
	if err = preparation.CheckProcedureMethod(context.Background(), db, closeID, "prepaid", otherRaw); err == nil {
		t.Fatal("new work could bypass the approved method")
	}
	var connection string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,environment,home_currency,fiscal_year_start) VALUES($1,$2,'Synthetic client','fixture','active',$3,now(),'123','production','CAD',1) RETURNING id::text`, closeID, org, rand.Text()).Scan(&connection))
	defer db.Exec(`DELETE FROM quickbooks_connections WHERE id=$1`, connection)
	usd, err := prepaids.Prepare(in)
	must(err)
	usdRaw, _ := json.Marshal(usd)
	if err = preparation.CheckProcedureMethod(context.Background(), db, closeID, "prepaid", usdRaw); err == nil || !strings.Contains(err.Error(), "kept in CAD") {
		t.Fatalf("a USD draft for a client whose books are kept in CAD must be refused: %v", err)
	}
	cad := in
	cad.Currency = "CAD"
	cadPaper, err := prepaids.Prepare(cad)
	must(err)
	cadRaw, _ := json.Marshal(cadPaper)
	must(preparation.CheckProcedureMethod(context.Background(), db, closeID, "prepaid", cadRaw))
}
