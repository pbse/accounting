package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"close.local/backend/internal/bookfiles"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/payroll"
	"close.local/backend/internal/prepaids"
	currency_ "golang.org/x/text/currency"
)

// commonCodes are the currencies a summary may name in any case ("eur"); any other ISO code counts only in capitals,
// so words such as "all" or "top" are not read as currencies.
var commonCodes = regexp.MustCompile(`(?i)\b(usd|cad|aud|nzd|eur|gbp|chf|jpy|cny|inr|mxn|sgd|hkd|sek|nok|dkk|zar)\b`)

// dollarMarks are a dollar sign with its country's letters: US$, C$, CA$, A$, NZ$ and the like.
var dollarMarks = regexp.MustCompile(`(?i)\b[a-z]{1,2}\$`)

// printsAmount says whether a quote prints the amount (in cents) as a whole figure, grouped or not, with or without
// cents; a run of digits inside a date or with more decimals is no figure.
func printsAmount(quote string, cents int64) bool {
	figure := regexp.MustCompile(`^(\d{1,3}(,\d{3})+|\d+)(\.\d{1,2})?$`)
	for _, run := range regexp.MustCompile(`\d[\d,.\-/]*`).FindAllString(quote, -1) {
		run = strings.TrimRight(run, ".,")
		if !figure.MatchString(run) {
			continue
		}
		if c, err := prepaids.Amount(strings.ReplaceAll(run, ",", "")); err == nil && c == cents {
			return true
		}
	}
	return false
}

// payrollAmounts are the figures whose quote must print them.
var payrollAmounts = []string{"gross_wages", "employee_taxes", "employee_deductions", "net_pay", "employer_taxes", "employer_contributions", "provider_debits", "ledger_debits"}

// payrollFacts are what a payroll entry needs read from the provider's summary, and from the ledger page.
var payrollFacts = []string{"provider", "source_reference", "first_pay_date", "last_pay_date", "gross_wages", "employee_taxes", "employee_deductions", "net_pay", "employer_taxes", "employer_contributions", "provider_debits"}
var payrollLedgerFacts = []string{"ledger_debits", "ledger_reference"}

// StraightPayroll records a month's payroll without a person confirming the agent's reading, when the provider's summary
// and the ledger page give every figure the entry needs and agree, under the client's approved payroll procedure, with
// the accounts of the client's last approved payroll from the same provider (prior). Anything else (a first payroll from
// a provider, a summary that does not tie, debits the ledger does not show, another currency) is a person's to confirm.
// Approving the draft stays a person's, with both of its confirmations. It returns the plan, or the reason a person is
// needed.
func StraightPayroll(p Proposal, selected []IntakeSource, procedure Procedure, procedureSource Source, currency, entity, period string, unread bool, otherMatter map[string]bool, prior *PriorPayroll) (Plan, string) {
	switch {
	case !llm.IsModelEngine(p.Engine) || p.Kind != "payroll":
		return Plan{}, "only an agent-read payroll summary can be prepared without confirmation"
	case len(p.Questions) > 0:
		return Plan{}, "the agent has open questions"
	case unread:
		return Plan{}, "evidence in the close is not readable yet, or belongs to another job's request or answer"
	case currency == "":
		return Plan{}, "the client's home currency is not detected from a connected ledger"
	case !procedure.Enabled || procedure.Kind != "payroll" || procedure.Method != payroll.Method:
		return Plan{}, "no enabled payroll procedure for this client authorizes recording payroll summaries"
	case procedureSource.ID == "":
		return Plan{}, "the approved payroll procedure has not been applied to this close as evidence"
	}
	// The accounts come first: without an approved payroll from this provider there are none to use, whatever else holds.
	if prior == nil {
		provider := "this provider"
		for _, f := range p.Facts {
			if f.Field == "provider" && f.Value != "" {
				provider = f.Value
			}
		}
		return Plan{}, "no earlier approved payroll from " + provider + " gives the accounts; a person chooses them once"
	}
	unrelated := []string{}
	for _, s := range p.Unselected {
		if s.ID == procedureSource.ID {
			continue
		}
		if !otherMatter[s.ID] {
			return Plan{}, "evidence in the close (" + s.Name + ") was not used; a person confirms it does not bear on this payroll"
		}
		unrelated = append(unrelated, s.ID)
	}
	sources, documents := map[string]IntakeSource{}, 0
	for _, s := range p.Sources {
		if s.ID == procedureSource.ID {
			continue
		}
		documents++
		if s.Role != "support" && s.Role != "ledger" {
			return Plan{}, "evidence (" + s.Name + ") was selected as " + s.Role + "; a person reviews it"
		}
		for _, c := range selected {
			if c.ID == s.ID {
				c.Role = s.Role
				sources[s.Role] = c
			}
		}
	}
	summary, ledger := sources["support"], sources["ledger"]
	if summary.ID == "" || ledger.ID == "" || documents != 2 {
		return Plan{}, "a payroll needs the provider's summary and the ledger page, each once"
	}
	facts := map[string]string{}
	for _, f := range p.Facts {
		if f.Value == "" || len(f.Candidates) != 1 {
			return Plan{}, "the " + f.Label + " is not one candidate read from the source"
		}
		want := summary.ID
		if strings.HasPrefix(f.Field, "ledger_") {
			want = ledger.ID
		}
		if f.Candidates[0].SourceID != want {
			return Plan{}, "the " + f.Label + " is not cited from its own document"
		}
		// The quote must print what was read: the provider's name as written, each amount as a figure on its line.
		quote, value := f.Candidates[0].Quote, strings.TrimSpace(f.Value)
		if f.Field == "provider" && !strings.Contains(strings.ToLower(quote), strings.ToLower(value)) {
			return Plan{}, "the provider (" + value + ") is not printed in the line it is quoted from"
		}
		if slices.Contains(payrollAmounts, f.Field) {
			if cents, err := prepaids.Amount(value); err != nil || !printsAmount(quote, cents) {
				return Plan{}, "the " + f.Label + " (" + value + ") is not printed in the line it is quoted from"
			}
		}
		facts[f.Field] = value
	}
	for _, field := range slices.Concat(payrollFacts, payrollLedgerFacts) {
		if facts[field] == "" {
			return Plan{}, "the " + strings.ReplaceAll(field, "_", " ") + " was not read from the source"
		}
	}
	// A summary naming another currency, by its ISO code or its sign, is not vouched for by the client's: its figures may
	// be in that one.
	home := strings.ToUpper(currency)
	codes := regexp.MustCompile(`\b[A-Z]{3}\b`).FindAllString(summary.Content, -1)
	for _, code := range append(codes, commonCodes.FindAllString(summary.Content, -1)...) {
		if code = strings.ToUpper(code); code != home {
			if _, err := currency_.ParseISO(code); err == nil {
				return Plan{}, "the summary names another currency (" + code + "); a person confirms its figures are in " + home
			}
		}
	}
	// Each currency mark it prints, a sign or a dollar with its country's letters, must be one the client's currency writes.
	marks := dollarMarks.FindAllString(summary.Content, -1)
	for _, r := range summary.Content {
		if unicode.Is(unicode.Sc, r) {
			marks = append(marks, string(r))
		}
	}
	for _, mark := range marks {
		if !bookfiles.MarkAgrees(strings.ToUpper(mark), home) {
			return Plan{}, "the summary prints another currency's mark (" + mark + "); a person confirms its figures are in " + home
		}
	}
	location := func(s IntakeSource) string {
		if s.Location != "" {
			return s.Name + " · " + s.Location
		}
		return s.Name
	}
	input := payroll.Input{Entity: entity, ClosePeriod: period, Currency: strings.ToUpper(currency), Provider: facts["provider"], SourceReference: facts["source_reference"] + " (" + location(summary) + ")",
		FirstPayDate: facts["first_pay_date"], LastPayDate: facts["last_pay_date"], GrossWages: facts["gross_wages"], EmployeeTaxes: facts["employee_taxes"], EmployeeDeductions: facts["employee_deductions"],
		NetPay: facts["net_pay"], EmployerTaxes: facts["employer_taxes"], EmployerContributions: facts["employer_contributions"], ProviderDebits: facts["provider_debits"],
		LedgerDebits: facts["ledger_debits"], LedgerReference: facts["ledger_reference"] + " (" + location(ledger) + ")",
		WageAccount: prior.Input.WageAccount, TaxExpenseAccount: prior.Input.TaxExpenseAccount, BenefitsAccount: prior.Input.BenefitsAccount, ClearingAccount: prior.Input.ClearingAccount, LiabilityAccount: prior.Input.LiabilityAccount,
		Method: payroll.Method}
	paper, err := payroll.Prepare(input)
	if err != nil {
		return Plan{}, "the payroll could not be prepared from these facts: " + err.Error()
	}
	if len(paper.Blockers) > 0 {
		return Plan{}, paper.Blockers[0]
	}
	raw, _ := json.Marshal(input)
	cited := []Source{}
	for _, s := range p.Sources {
		if s.ID == summary.ID || s.ID == ledger.ID {
			cited = append(cited, s)
		}
	}
	cited = append(cited, Source{ID: procedureSource.ID, Digest: procedureSource.Digest, Name: procedureSource.Name, Role: "procedure", Location: "approved procedure"})
	return Plan{Kind: "payroll", Input: raw, Procedure: procedure.ID, Sources: cited, Unrelated: unrelated, PayrollPrecedent: &prior.Precedent}, ""
}

// Precedent is the approved draft whose accounts a straight-through payroll uses, as approved: its digest and review. The
// draft records it, so the precedent's approval no longer standing reopens the draft's review (workpaper_review_state).
type Precedent struct {
	CloseID  string `json:"close_id"`
	PaperID  string `json:"paper_id"`
	Digest   string `json:"digest"`
	ReviewID string `json:"review_id"`
	Period   string `json:"period"`
}

// PriorPayroll is the client's last approved, unrevised payroll draft from a provider in an earlier month: its accounts
// are the ones a straight-through payroll uses.
type PriorPayroll struct {
	Precedent
	ID     string
	Period string
	Input  payroll.Input
}

// LastApprovedPayroll finds the client's PriorPayroll for provider before this close's month; nil when there is none.
func LastApprovedPayroll(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID, provider string) (*PriorPayroll, error) {
	var prior PriorPayroll
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT w.id::text,c.id::text,c.period,r.digest,COALESCE(r.review_id::text,''),w.paper->'input' FROM workpapers w JOIN client_closes c ON c.id=w.close_id JOIN client_closes this ON this.id::text=$1
 JOIN workpaper_review_state r ON r.id=w.id AND r.review_state='approved'
 WHERE w.kind='payroll' AND c.organization_id=this.organization_id AND client_identity(c.organization_id,c.entity)=client_identity(this.organization_id,this.entity) AND c.period<this.period
 AND lower(btrim(w.paper->'input'->>'provider'))=lower(btrim($2))
 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)
 ORDER BY c.period DESC,w.id DESC LIMIT 1`, closeID, provider).Scan(&prior.ID, &prior.CloseID, &prior.Period, &prior.Digest, &prior.ReviewID, &raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &prior.Input); err != nil {
		return nil, err
	}
	prior.Precedent.PaperID, prior.Precedent.Period = prior.ID, prior.Period
	return &prior, nil
}
