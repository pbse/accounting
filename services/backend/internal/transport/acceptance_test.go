package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/llm"
)

// The sample corpus runs through the real extraction, reading and preparation with its recorded answers: the clean
// invoice goes through alone and drafts moving its unexpired months to prepaid, and once the accountant approves it and
// defines the scope the close is ready; a reading ten times too large and a missing ledger capture are put to a person
// (the wrong amount withheld, not shown); an invoice billed to another company is held and keeps its close from being
// ready; the same invoice uploaded twice is one document; and nothing is posted.
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
	// In a named firm, as a live Context Machine run uses, the case is a close of that firm worked as its person, and the
	// firm stays enabled for the next case.
	firm := AcceptanceFirm{Organization: "org_AcceptanceShared" + strings.ToLower(rand.Text()[:8]), Subject: "user_owner"}
	for _, c := range cases {
		if c.ID != "straight-through-prepaid" {
			continue
		}
		o, err := RunAcceptanceCase(context.Background(), db, c, firm, func(files map[string]string) (*llm.Client, error) { return c.Recorded(files) })
		if err != nil {
			t.Fatal(err)
		}
		var enabled bool
		var actor string
		if err = db.QueryRow(`SELECT o.enabled,j.created_by FROM organizations o JOIN client_closes c ON c.organization_id=o.id JOIN preparation_jobs j ON j.close_id=c.id WHERE o.id=$1`, firm.Organization).Scan(&enabled, &actor); err != nil {
			t.Fatal(err)
		}
		if one := acceptance.Score([]acceptance.Case{c}, []acceptance.CaseObserved{o}); !one.Passed() || !o.Documents[0].Accepted || !enabled || actor != "user_owner" {
			t.Fatalf("in a named firm: enabled %v, worked as %q, %+v", enabled, actor, o)
		}
	}

	observed := []acceptance.CaseObserved{}
	for _, c := range cases {
		o, err := RunAcceptanceCase(context.Background(), db, c, AcceptanceFirm{}, func(files map[string]string) (*llm.Client, error) { return c.Recorded(files) })
		if err != nil {
			t.Fatal(err)
		}
		observed = append(observed, o)
	}
	report := acceptance.Score(cases, observed)
	prepaid := report.Tallies["development"]["prepaid"]
	if !report.Passed() || *prepaid != (acceptance.Tally{Documents: 8, Accepted: 2, Abstained: 3, CaughtExceptions: 3}) {
		t.Fatalf("sample corpus: %+v\n%s\n%+v", *prepaid, report.Markdown(), report.Observed)
	}
	straight := report.Observed["straight-through-prepaid"].Documents[0]
	if len(straight.Drafts) != 1 || straight.Drafts[0].Amount != "1100.00" || straight.Facts["amount"] != "1200.00" || !strings.HasPrefix(straight.Reason, "Prepared without confirmation") {
		t.Fatalf("the clean invoice's bill, booked to Insurance, moves 1,100.00 to prepaid: %+v", straight)
	}
	if held := report.Observed["billed-to-another-company"].Documents[0]; held.Accepted || !strings.HasPrefix(held.Reason, "Waiting for a person") {
		t.Fatalf("the other company's invoice waits for a person: %+v", held)
	}
	if ready := report.Observed["straight-through-prepaid"]; report.NotReady != 0 || len(ready.Blockers) != 0 {
		t.Fatalf("with its draft approved and the scope defined, the clean close is ready: %+v", ready)
	}
	held := report.Observed["held-document-blocks-signoff"]
	if len(held.Blockers) == 0 || !strings.Contains(strings.Join(held.Blockers, " "), "needs confirmation") {
		t.Fatalf("a held document keeps the close from being ready: %+v", held.Blockers)
	}
	// Held first because the clean invoice went unused, the other company's invoice still leads with whose it is.
	for _, d := range held.Documents {
		if d.File == "other-company-invoice.txt" && (!strings.HasPrefix(d.Reason, `Waiting for a person: The invoice is billed to "Northwind Other Inc"`) || !strings.Contains(d.Reason, "it was also held because evidence in the close (acme-invoice.txt) was not used")) {
			t.Fatalf("the other company's invoice names the wrong client first: %s", d.Reason)
		}
	}
	twice := report.Observed["same-invoice-uploaded-twice"]
	if len(twice.Documents) != 2 || twice.Duplicates != 0 || (twice.Documents[0].SameAs == "") == (twice.Documents[1].SameAs == "") {
		t.Fatalf("the same invoice uploaded twice is one document: %+v", twice)
	}
	if md := report.Markdown(); !strings.Contains(md, "Critical failures: 0") || !strings.Contains(md, "| prepaid | 8 | 2 | 3 | 3 | 0 | 0 | 0 | 0 | 0 | 0 |") {
		t.Fatalf("report:\n%s", md)
	}
}
