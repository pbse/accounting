package transport

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/llm"
)

// The sample corpus runs through the real extraction, reading and preparation with its recorded answers: the clean
// invoice goes through alone and drafts moving its unexpired months to prepaid, a reading ten times too large and a
// missing ledger capture are put to a person (the wrong amount withheld, not shown), an invoice billed to another company
// is held, and nothing is posted.
func TestTheSampleCorpusRunsThroughThePipeline(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cases, err := acceptance.Load("../../acceptance/sample")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closes := `SELECT c.id FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE o.id LIKE 'org_Acceptance%' AND o.name LIKE 'Acceptance %'`
		for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id IN (` + closes + `)`, `DELETE FROM workpapers WHERE close_id IN (` + closes + `)`,
			`DELETE FROM quickbooks_snapshots WHERE close_id IN (` + closes + `)`, `DELETE FROM quickbooks_entity_syncs WHERE close_id IN (` + closes + `)`,
			`DELETE FROM quickbooks_connections WHERE close_id IN (` + closes + `)`, `DELETE FROM evidence WHERE close_id IN (` + closes + `)`,
			`DELETE FROM client_closes WHERE id IN (` + closes + `)`, `DELETE FROM procedure_versions WHERE organization_id LIKE 'org_Acceptance%'`,
			`DELETE FROM model_calls WHERE organization_id LIKE 'org_Acceptance%'`, `DELETE FROM organizations WHERE id LIKE 'org_Acceptance%' AND name LIKE 'Acceptance %'`} {
			db.Exec(q)
		}
	})
	observed := []acceptance.CaseObserved{}
	for _, c := range cases {
		o, err := RunAcceptanceCase(context.Background(), db, c, func(files map[string]string) (*llm.Client, error) { return c.Recorded(files) })
		if err != nil {
			t.Fatal(err)
		}
		observed = append(observed, o)
	}
	report := acceptance.Score(cases, observed)
	prepaid := report.Tallies["development"]["prepaid"]
	if report.Critical() != 0 || *prepaid != (acceptance.Tally{Documents: 4, Accepted: 1, Abstained: 2, CaughtExceptions: 1}) {
		t.Fatalf("sample corpus: %+v\n%s\n%+v", *prepaid, report.Markdown(), report.Observed)
	}
	straight := report.Observed["straight-through-prepaid"].Documents[0]
	if straight.DraftAmount != "1100.00" || straight.Facts["amount"] != "1200.00" || !strings.HasPrefix(straight.Reason, "Prepared without confirmation") {
		t.Fatalf("the clean invoice's bill, booked to Insurance, moves 1,100.00 to prepaid: %+v", straight)
	}
	if held := report.Observed["billed-to-another-company"].Documents[0]; held.Accepted || !strings.HasPrefix(held.Reason, "Waiting for a person") {
		t.Fatalf("the other company's invoice waits for a person: %+v", held)
	}
	if md := report.Markdown(); !strings.Contains(md, "Critical failures: 0") || !strings.Contains(md, "| prepaid | 4 | 1 | 2 | 1 | 0 | 0 | 0 | 0 | 0 |") {
		t.Fatalf("report:\n%s", md)
	}
}
