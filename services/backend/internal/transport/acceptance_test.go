package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
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
			`DELETE FROM model_calls WHERE organization_id LIKE 'org_Acceptance%'`, `DELETE FROM client_access WHERE organization_id LIKE 'org_Acceptance%'`,
			`DELETE FROM organization_member_access WHERE organization_id LIKE 'org_Acceptance%'`, `DELETE FROM organizations WHERE id LIKE 'org_Acceptance%' AND name LIKE 'Acceptance %'`} {
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
	if !report.Passed() || *prepaid != (acceptance.Tally{Documents: 9, Accepted: 3, Abstained: 3, CaughtExceptions: 3}) {
		t.Fatalf("sample corpus: %+v\n%s\n%+v", *prepaid, report.Markdown(), report.Observed)
	}
	// An accrual, a payroll summary and a loan statement are each read exactly and put to a person, never drafted alone.
	for _, kind := range []string{"accrual", "payroll", "loan"} {
		if tally := report.Tallies["development"][kind]; tally == nil || *tally != (acceptance.Tally{Documents: 1, Abstained: 1}) {
			t.Fatalf("the %s case is read exactly and asked about: %+v", kind, tally)
		}
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
	// The ready close tied out against its Trial Balance once its draft is counted; one already holding 500.00 in
	// Prepaid Expenses would not, so it is blocked.
	if blocked := report.Observed["trial-balance-does-not-tie"]; len(blocked.Blockers) != 1 || !strings.HasPrefix(blocked.Blockers[0], "Tie-out: Prepaid Expenses is 500.00 in the Trial Balance but 1100.00 in its support") {
		t.Fatalf("a Trial Balance that does not tie blocks sign-off: %+v", blocked.Blockers)
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
	if md := report.Markdown(); !strings.Contains(md, "Critical failures: 0") || !strings.Contains(md, "| prepaid | 9 | 3 | 3 | 3 | 0 | 0 | 0 | 0 | 0 | 0 |") {
		t.Fatalf("report:\n%s", md)
	}
}

// Every entry a workpaper proposes is observed, in whichever key its kind keeps it, and none from a workpaper with
// nothing to post.
func TestEveryProposedEntryIsObserved(t *testing.T) {
	entry := func(amount string) string {
		return `{"date":"2026-09-30","debit_account":"Prepaid Expenses","credit_account":"Insurance","amount":"` + amount + `"}`
	}
	for paper, want := range map[string][]string{
		`{"status":"ok","proposal":null}`:                                                                            {},
		`{"proposal":` + entry("1100.00") + `}`:                                                                      {"1100.00"},
		`{"proposal":` + entry("400") + `,"reversal":` + entry("-400") + `}`:                                         {"400", "-400"},
		`{"proposals":[` + entry("10") + `,` + entry("20") + `]}`:                                                    {"10", "20"},
		`{"proposal":` + entry("5") + `,"disposal":[` + entry("6") + `]}`:                                            {"5", "6"},
		`{"proposal":` + entry("1") + `,"refund_proposal":` + entry("2") + `,"revenue_proposal":` + entry("3") + `}`: {"1", "2", "3"},
	} {
		entries, err := paperEntries([]byte(paper))
		got := []string{}
		for _, e := range entries {
			got = append(got, e.Amount)
		}
		if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: %v %v", paper, got, err)
		}
	}
}

// An evaluation's ceiling counts every model call in the database today: under it, the firm may make only what the run
// has left; once it is spent, the next case is not run at all.
func TestAnEvaluationStopsAtItsDailyModelCallCeiling(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	org := "org_EvalCeiling" + rand.Text()
	t.Cleanup(func() {
		db.Exec(`DELETE FROM model_calls WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	})
	if _, err = db.Exec(`INSERT INTO organizations(id,name,max_model_calls_per_day) VALUES($1,'Ceiling',100000)`, org); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO model_calls(organization_id,day,calls) VALUES($1,(now() AT TIME ZONE 'UTC')::date,50)`, org); err != nil {
		t.Fatal(err)
	}
	var used, limit int
	if err = db.QueryRow(`SELECT sum(calls) FROM model_calls WHERE day=(now() AT TIME ZONE 'UTC')::date`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err = CapEvaluationCalls(ctx, db, org, used+1000); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT max_model_calls_per_day FROM organizations WHERE id=$1`, org).Scan(&limit); err != nil || limit <= 50 || limit > 50+1000+used {
		t.Fatalf("the firm may make its 50 calls plus what the run has left, about 1,000: %d, %v", limit, err)
	}
	c := acceptance.Case{ID: "ceiling", Client: "Ceiling LLC", Period: "2026-09"}
	if _, err = RunAcceptanceCase(ctx, db, c, AcceptanceFirm{Organization: org, Subject: "user_ceiling", ModelCalls: 50}, nil); !errors.Is(err, ErrEvaluationCeiling) {
		t.Fatalf("a spent ceiling runs nothing: %v", err)
	}
	var closes int
	if db.QueryRow(`SELECT count(*) FROM client_closes WHERE organization_id=$1`, org).Scan(&closes); closes != 0 {
		t.Fatalf("no close is made once the ceiling is spent: %d", closes)
	}
}
