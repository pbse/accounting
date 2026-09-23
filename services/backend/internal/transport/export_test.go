package transport

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/reconciliation"
)

func TestPackagePreservesBothWorkpapersAndEscapesSources(t *testing.T) {
	prepaid, _ := json.Marshal(prepaids.Workpaper{Input: prepaids.Input{SourceReference: "<script>alert(1)</script>", Method: prepaids.Method}, Schedule: []prepaids.Row{{Period: "2026-09", Expense: "50.00", Remaining: "50.00"}, {Period: "2026-10", Expense: "50.00", Remaining: "0.00"}}})
	recon, _ := json.Marshal(reconciliation.Workpaper{Method: "exact_bank_month_v1", Blockers: []string{"Statement page 2 is missing."}, Matches: []reconciliation.Match{{StatementID: "=1+1", LedgerID: "L1", Reason: "Exact candidate"}}, UnmatchedLedger: []reconciliation.Line{{ID: "L2", Date: "2026-09-30", Amount: "-50.00"}}, Exceptions: []reconciliation.Exception{{Side: "ledger", ID: "=row", Kind: "grouped_exact", CandidateIDs: []string{"@candidate", "L3"}, CandidateCount: 2, CandidateGroups: [][]string{{"=group", "L3"}}, GroupCount: 1, Question: "Check both source rows."}}})
	data, err := buildClosePackage(closePackage{Close: savedClose{ID: "1", Entity: "Example", Period: "2026-09"}, Workpapers: []savedPaper{{ID: "1", Kind: "prepaid", Paper: prepaid}, {ID: "2", Kind: "reconciliation", Paper: recon}}, LedgerSnapshots: []packageLedger{{ID: "7", Payload: []byte(`{"period":"2026-09","accounts":[]}`)}, {ID: "8", Payload: []byte(`{"period":"2026-09","reports":{}}`)}}, SandboxPostings: json.RawMessage(`[{"state":"unknown","external_id":"123"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = string(body)
	}
	var manifest closePackage
	if json.Unmarshal([]byte(files["manifest.json"]), &manifest) != nil || len(manifest.Workpapers) != 2 {
		t.Fatal("missing workpaper from manifest")
	}
	schedule, err := csv.NewReader(strings.NewReader(files["workpapers/1-schedule.csv"])).ReadAll()
	if err != nil || len(schedule) != 3 || schedule[2][2] != "0.00" {
		t.Fatal("schedule rows lost")
	}
	matches, err := csv.NewReader(strings.NewReader(files["workpapers/2-matches.csv"])).ReadAll()
	if err != nil || matches[1][0] != "'=1+1" {
		t.Fatal("unsafe spreadsheet formula")
	}
	html := files["review.html"]
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "Statement page 2 is missing.") || !strings.Contains(html, "unknown") {
		t.Fatal("unsafe or incomplete review summary")
	}
	if !strings.Contains(files["workpapers/2-unmatched-ledger.csv"], "-50.00") {
		t.Fatal("unmatched negative amount lost")
	}
	if !strings.Contains(files["workpapers/2-exceptions.csv"], "'=row") || !strings.Contains(files["workpapers/2-exceptions.csv"], "'@candidate") || !strings.Contains(files["workpapers/2-exceptions.csv"], "'=group + L3") {
		t.Fatal("exception leads were lost or unsafe for spreadsheets")
	}
	if !strings.Contains(files["ledger/snapshot-7.json"], `"accounts":[]`) || !strings.Contains(files["ledger/snapshot-8.json"], `"reports":{}`) {
		t.Fatal("captured ledger report files were omitted")
	}
	if !strings.HasPrefix(files["workpapers.pdf"], "%PDF-") {
		t.Fatal("portable workpaper PDF missing")
	}
	path := filepath.Join(t.TempDir(), "workpapers.pdf")
	if err := os.WriteFile(path, []byte(files["workpapers.pdf"]), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := exec.Command("pdftotext", path, "-").Output()
	if err != nil || !strings.Contains(string(text), "Draft #1") || !strings.Contains(string(text), "Draft #2") || !strings.Contains(string(text), "Statement page 2 is missing") {
		t.Fatalf("PDF content incomplete: %s %v", text, err)
	}
}
