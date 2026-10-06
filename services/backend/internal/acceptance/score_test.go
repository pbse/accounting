package acceptance

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// Each outcome lands in its own count, with the document as the denominator; only the critical ones fail the run.
func TestScoreCountsEachOutcomeAndFailsOnlyOnCriticalOnes(t *testing.T) {
	prepaid := func(file, exception string) Document {
		return Document{File: file, Label: Label{WorkType: "prepaid", Facts: map[string]string{"amount": "1200.00", "service_start": "2026-09-01"}, Drafts: []Entry{{DebitAccount: "Prepaid Expenses", Amount: "1100.00"}}, Exception: exception}}
	}
	cases := []Case{{ID: "c1", Split: "development", Documents: []Document{
		prepaid("right.txt", ""), prepaid("wrong-amount.txt", ""), prepaid("extra-draft.txt", ""), prepaid("asked.txt", ""),
		prepaid("held.txt", "wrong_client"), prepaid("missed.txt", "wrong_client"), prepaid("held-elsewhere.txt", "wrong_client"), prepaid("refused.txt", ""),
		prepaid("unread.txt", "wrong_client"),
		{File: "letter.txt", Label: Label{WorkType: "none"}}, prepaid("again.txt", "duplicate"),
	}}, {ID: "c2", Split: "held_out", ExpectReady: false, Documents: []Document{prepaid("ok.txt", "")}}}
	good := map[string]string{"amount": "1,200.00", "service_start": "2026-09-01"}
	right := []Entry{{Date: "2026-09-30", DebitAccount: "Prepaid Expenses", CreditAccount: "Insurance", Amount: "1100"}}
	observed := []CaseObserved{{ReadinessState: "open", Blockers: []string{"Define the supported scope"}, Documents: []Observed{
		{File: "right.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, Drafts: right},
		{File: "wrong-amount.txt", JobState: "completed", Kind: "prepaid", Facts: map[string]string{"amount": "12000.00", "service_start": "2026-09-01"}, Accepted: true, Drafts: []Entry{{DebitAccount: "Insurance", Amount: "11000.00"}}},
		{File: "extra-draft.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, Drafts: append(right, Entry{DebitAccount: "Prepaid Expenses", Amount: "50.00"})},
		{File: "asked.txt", JobState: "needs_confirmation", Kind: "prepaid", Facts: map[string]string{"amount": "1300.00"}},
		{File: "held.txt", JobState: "needs_confirmation", Kind: "unsupported", Engine: "identity_blocked"},
		{File: "missed.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, Drafts: right},
		{File: "held-elsewhere.txt", JobState: "needs_confirmation", Kind: "prepaid", Facts: good, Reason: "Waiting for a person: the close has no current QuickBooks capture."},
		{File: "refused.txt", JobState: "needs_confirmation", Kind: "prepaid", Engine: "openai", Reason: `Waiting for a person: The invoice is billed to "Harbour LLC", not the client or a confirmed alias.`},
		{File: "unread.txt", JobState: "failed"},
		{File: "letter.txt", JobState: "needs_confirmation", Kind: "unsupported"},
		{File: "again.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, SameAs: "right.txt"},
	}}, {ReadinessState: "open", ExternalActions: 1, Documents: []Observed{{File: "ok.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, Drafts: right}}}}

	r := Score(cases, observed)
	dev := r.Tallies["development"]["prepaid"]
	// A problem held for another reason (held-elsewhere.txt) is missed; one never decided on (unread.txt) is not processed.
	if *dev != (Tally{Documents: 10, Accepted: 3, Abstained: 1, CaughtExceptions: 2, FalseAcceptedFacts: 1, IncorrectDrafts: 2, MissedExceptions: 2, FalseExceptions: 1, WrongProposals: 1, Unprocessed: 1}) {
		t.Fatalf("development prepaid tally: %+v", *dev)
	}
	if none := r.Tallies["development"]["none"]; none.Documents != 1 || none.CaughtExceptions != 1 {
		t.Fatalf("a document with no work, held for a person: %+v", *none)
	}
	if r.FalseReady != 1 || r.ExternalActions != 1 {
		t.Fatalf("close checks: %+v", r)
	}
	kinds := []string{}
	for _, f := range r.Failures {
		kinds = append(kinds, f.Case+":"+f.Document+":"+f.Kind)
	}
	if got := strings.Join(kinds, ","); got != "c1:wrong-amount.txt:false_accepted_fact,c1:wrong-amount.txt:incorrect_draft,c1:extra-draft.txt:incorrect_draft,c1:missed.txt:missed_exception,c1:held-elsewhere.txt:missed_exception,c2::false_ready,c2::external_action" {
		t.Fatalf("critical failures: %s", got)
	}
	if detail := r.Failures[1].Detail; !strings.Contains(detail, `debit account "Insurance"`) || !strings.Contains(detail, `amount "11000.00"`) {
		t.Fatalf("the wrong entry names its wrong fields: %s", detail)
	}
	if detail := r.Failures[2].Detail; detail != "2 draft(s); the label expects 1" {
		t.Fatalf("an extra draft is incorrect: %s", detail)
	}
	if detail := r.Failures[4].Detail; !strings.Contains(detail, "not for wrong_client: Waiting for a person: the close has no current QuickBooks capture") {
		t.Fatalf("a problem held for another reason says so: %s", detail)
	}
	if r.Passed() || len(r.NotProcessed) != 1 || r.NotProcessed[0].Document != "unread.txt" {
		t.Fatalf("a document never decided on fails the run: %+v", r.NotProcessed)
	}
	if md := r.Markdown(); !strings.Contains(md, "| prepaid | 10 | 3 | 1 | 2 | 1 | 2 | 2 | 1 | 1 | 1 |") || !strings.Contains(md, "Critical failures: 7. Not processed: 1.") || !strings.Contains(md, "- c1 / unread.txt: not_processed, job failed") {
		t.Fatalf("report:\n%s", md)
	}
}

// Labeled entries are matched as a whole: a broad label never takes the only draft a narrower one could match.
func TestDraftsAreMatchedAsAWhole(t *testing.T) {
	want := []Entry{{Amount: "100"}, {Amount: "100", DebitAccount: "Prepaid Expenses"}}
	if problems := compareDrafts([]Entry{{DebitAccount: "Prepaid Expenses", Amount: "100.00"}, {DebitAccount: "Other", Amount: "100.00"}}, want); len(problems) != 0 {
		t.Fatalf("both labeled entries have a draft of their own: %v", problems)
	}
	problems := compareDrafts([]Entry{{DebitAccount: "Other", Amount: "100.00"}, {DebitAccount: "Other", Amount: "100.00"}}, want)
	if len(problems) != 1 || problems[0] != `no draft matches the labeled entry: debit account "Other", the label says "Prepaid Expenses"` {
		t.Fatalf("one labeled entry is unmatched: %v", problems)
	}
}

func TestLoadRefusesACaseItCannotScore(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := writeFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	write("bad/case.json", `{"id":"Bad Case","split":"tuning","client":"","period":"2026-13","setup":{},"signoff":{"description":" "},"documents":[{"file":"missing.txt","label":{"work_type":""}},{"file":"a.txt","label":{"work_type":"prepaid","exception":"ocr_decimal"}},{"file":"b.txt","label":{"work_type":"prepaid"}}]}`)
	write("bad/a.txt", "Same invoice")
	write("bad/b.txt", "Same invoice")
	_, err := Load(dir)
	for _, want := range []string{"id \"Bad Case\"", "split \"tuning\"", "client and its month", "missing.txt", "work type", "b.txt repeats a.txt byte for byte as uploaded", "what sign-off covers",
		`cannot yet tell whether the app found "ocr_decimal"; known: duplicate, wrong_client`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q in %v", want, err)
		}
	}
	dir = t.TempDir()
	write("ready/case.json", `{"id":"ready","split":"development","client":"Harbor","period":"2026-09","setup":{},"expect_ready":true,"documents":[{"file":"a.txt","label":{"work_type":"prepaid"}}]}`)
	write("ready/a.txt", "Invoice")
	if _, err = Load(dir); err == nil || !strings.Contains(err.Error(), "a close expected ready needs its sign-off step") {
		t.Fatalf("a case expected ready without a sign-off step: %v", err)
	}
}

// Two workbooks whose bytes differ but whose first sheets match are one uploaded document, as the app keeps them.
func TestWorkbooksAreComparedAsUploaded(t *testing.T) {
	book := func(extra string) string {
		var raw bytes.Buffer
		w := zip.NewWriter(&raw)
		for name, body := range map[string]string{
			"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet r:id="rId1"/></sheets></workbook>`,
			"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
			"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row><c r="A1" t="inlineStr"><is><t>Insurance</t></is></c><c r="B1"><v>1200</v></c></row></sheetData></worksheet>`,
			"docProps/app.xml":           extra,
		} {
			f, _ := w.Create(name)
			f.Write([]byte(body))
		}
		w.Close()
		return raw.String()
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"wb/a.xlsx": book("<Properties>first</Properties>"), "wb/b.xlsx": book("<Properties>saved again</Properties>"),
		"wb/case.json": `{"id":"wb","split":"development","client":"Harbor","period":"2026-09","setup":{},"documents":[{"file":"a.xlsx","label":{"work_type":"prepaid"}},{"file":"b.xlsx","label":{"work_type":"prepaid"}}]}`} {
		if err := writeFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	if a, b := book("<Properties>first</Properties>"), book("<Properties>saved again</Properties>"); a == b {
		t.Fatal("the workbooks' bytes differ")
	}
	if name, body, err := (Case{Dir: dir + "/wb"}).Upload("a.xlsx"); err != nil || name != "a.xlsx.csv" || string(body) != "Insurance,1200\n" {
		t.Fatalf("a workbook uploads as its first sheet's CSV: %q %q %v", name, body, err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "b.xlsx repeats a.xlsx byte for byte as uploaded") {
		t.Fatalf("the second workbook is the same upload: %v", err)
	}
}

// A recorded run needs an answer for every document the app keeps; a byte-for-byte repeat is kept as the earlier one.
func TestARecordedRunNeedsEveryKeptDocumentsAnswer(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"a.txt": "Invoice", "b.txt": "Invoice", "c.txt": "Another invoice", "c.model.json": "{}"} {
		if err := writeFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	c := Case{ID: "rec", Dir: dir, Documents: []Document{{File: "a.txt"}, {File: "b.txt", Label: Label{Exception: "duplicate"}}, {File: "c.txt", Model: "c.model.json"}}}
	if err := c.RecordedComplete(); err == nil || err.Error() != "case rec: document a.txt has no recorded answer" {
		t.Fatalf("only a.txt lacks an answer: %v", err)
	}
	c.Documents[0].Model = "c.model.json"
	if err := c.RecordedComplete(); err != nil {
		t.Fatal(err)
	}
}
