package transport

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestHistoricalExportsAreLabeledAsReportedNotLive(t *testing.T) {
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
	org, other := "org_History"+rand.Text(), "org_HistoryOther"+rand.Text()
	var alpha, foreign, trial, ledger, elsewhere string
	for _, id := range []string{org, other} {
		_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Historical exports fixture')`, id)
		must(err)
	}
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-08') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Gamma','2026-08') RETURNING id::text`, other).Scan(&foreign))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'trial-balance.csv','history-tb','Account,Balance','fixture') RETURNING id::text`, alpha).Scan(&trial))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'general-ledger.csv','history-gl','Date,Amount','fixture') RETURNING id::text`, alpha).Scan(&ledger))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'other.csv','history-other','x','fixture') RETURNING id::text`, foreign).Scan(&elsewhere))
	defer func() {
		for _, q := range []string{`DELETE FROM close_export_labels WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
			db.Exec(q, other)
		}
	}()
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + alpha + "/historical-exports"
	label := func(ids []string, exported string, complete, adjusted bool, note string) map[string]any {
		return map[string]any{"evidence_ids": ids, "exported_on": exported, "complete": complete, "final_adjustments": adjusted, "note": note}
	}
	call("POST", path, label([]string{trial}, "2999-01-01", true, false, "Exported from the old system"), 422)
	call("POST", path, label([]string{trial}, "2026-09-05", true, false, "Tiny"), 422)
	call("POST", path, label([]string{trial, trial}, "2026-09-05", true, false, "Exported from the old system"), 409)
	call("POST", path, label([]string{elsewhere}, "2026-09-05", true, false, "Exported from the old system"), 409)
	call("POST", "/v1/closes/"+foreign+"/historical-exports", label([]string{elsewhere}, "2026-09-05", true, false, "Exported from the old system"), 404)
	call("GET", "/v1/closes/"+foreign+"/historical-exports", nil, 404)
	var got struct{ Label *exportLabel }
	must(json.Unmarshal(call("GET", path, nil, 200), &got))
	if got.Label != nil {
		t.Fatalf("an unlabeled close has no label: %+v", got.Label)
	}
	readiness := func() string {
		t.Helper()
		var r struct{ Digest string }
		must(json.Unmarshal(call("GET", "/v1/closes/"+alpha+"/readiness", nil, 200), &r))
		return r.Digest
	}
	// Five characters of any script make a note.
	call("POST", path, label([]string{trial}, "2026-09-05", true, false, "Ünïcö"), 200)
	before := readiness()
	must(json.Unmarshal(call("POST", path, label([]string{trial, ledger}, "2026-09-05", false, true, "Exported from the old system after year-end"), 200), &got))
	if got.Label == nil || strings.Join(got.Label.Exports, ",") != "trial-balance.csv,general-ledger.csv" || !got.Label.Current || got.Label.Actor != "user_a" {
		t.Fatalf("the label names its exports and who reported it: %+v", got.Label)
	}
	if readiness() == before {
		t.Fatal("a label is part of what a sign-off covers")
	}
	sentence := "Uploaded historical exports: user_a reported that trial-balance.csv, general-ledger.csv, exported 2026-09-05, are partial and already include the final close adjustments; this is not verified against the books."
	var brief struct {
		Headline string
		Lines    []struct{ Area, State, Text string }
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+alpha+"/brief", nil, 200), &brief))
	for _, line := range brief.Lines {
		if line.Area == "ledger" && (line.State != "attention" || line.Text != sentence) {
			t.Fatalf("the brief reports the label as reported, not verified: %+v", line)
		}
	}
	if !strings.Contains(brief.Headline, "verify the labeled exports") || strings.Contains(brief.Headline, "capture a month-end snapshot") {
		t.Fatalf("the brief asks for the exports to be verified instead of a capture: %s", brief.Headline)
	}
	var setup struct {
		Steps []struct {
			Key, Title, Detail string
			Done               bool
		}
	}
	must(json.Unmarshal(call("GET", "/v1/closes/setup", nil, 200), &setup))
	if step := setup.Steps[3]; step.Key != "ledger" || !step.Done || !strings.Contains(step.Detail, "historical exports a person labeled (exported 2026-09-05), not a live ledger connection") {
		t.Fatalf("labeled exports complete the books step without claiming a connection: %+v", step)
	}
	raw := call("GET", "/v1/closes/"+alpha+"/export", nil, 200)
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	must(err)
	var manifest struct {
		LedgerCompleteness string       `json:"ledger_completeness"`
		HistoricalExports  *exportLabel `json:"historical_exports"`
	}
	for _, f := range archive.File {
		if f.Name == "manifest.json" {
			opened, _ := f.Open()
			body, _ := io.ReadAll(opened)
			must(json.Unmarshal(body, &manifest))
		}
	}
	if manifest.HistoricalExports == nil || manifest.HistoricalExports.ExportedOn != "2026-09-05" || !strings.HasSuffix(manifest.LedgerCompleteness, sentence) {
		t.Fatalf("the package carries the label and says it: %+v", manifest)
	}
	// Replacing a labeled export leaves the label stale until someone labels the current exports.
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id=$1`, trial, ledger)
	must(err)
	must(json.Unmarshal(call("GET", "/v1/closes/setup", nil, 200), &setup))
	if setup.Steps[3].Done {
		t.Fatalf("a label on a replaced export no longer completes the books step: %+v", setup.Steps[3])
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+alpha+"/brief", nil, 200), &brief))
	for _, line := range brief.Lines {
		if line.Area == "ledger" && !strings.HasSuffix(line.Text, "A labeled export was replaced, so label the current exports again.") {
			t.Fatalf("the brief says the label is stale: %+v", line)
		}
	}
}
