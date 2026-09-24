package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
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
}
