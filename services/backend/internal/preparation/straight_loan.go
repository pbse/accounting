package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
)

// StraightLoan checks one fully paid month-end instalment; other treatments retain their reviewed loan workflow.
func StraightLoan(p Proposal, selected []IntakeSource, procedure Procedure, rule Source, currency, entity, period string, aliases []string, unread bool, ledger *Ledger) (Plan, string) {
	if !llm.IsModelEngine(p.Engine) || p.Kind != "loan" || len(p.Questions) > 0 || len(p.Lines) > 0 || unread {
		return Plan{}, "one agent-read lender statement with no open questions or unread evidence is required"
	}
	if currency == "" || !procedure.Enabled || procedure.Kind != "loan" || procedure.Method != loans.Method || !strings.EqualFold(procedure.ClientEntity, entity) || rule.ID == "" || ledger == nil || ledger.ChartSource.ID == "" || ledger.ReportPeriod != period {
		return Plan{}, "apply this client's approved fixed-loan policy and obtain current home-currency books"
	}
	machine := map[string]bool{rule.ID: true, ledger.Source.ID: true, ledger.ChartSource.ID: true}
	for id := range ledger.Machine {
		machine[id] = true
	}
	statement := IntakeSource{}
	for _, source := range p.Sources {
		if source.Role == "statement" {
			if statement.ID != "" {
				return Plan{}, "more than one lender statement needs a person"
			}
			for _, s := range selected {
				if s.ID == source.ID {
					statement = s
					statement.Source = source
				}
			}
		} else if !machine[source.ID] {
			return Plan{}, "additional loan support needs a person to weigh it"
		}
	}
	if statement.ID == "" || strings.Contains(statement.Content, documents.UnreadPage) || len(documents.ImageDisputed(statement.Content)) > 0 {
		return Plan{}, "one fully read, undisputed lender statement is required"
	}
	for _, s := range p.Unselected {
		if !machine[s.ID] {
			return Plan{}, "evidence in the close (" + s.Name + ") was not used; a person confirms it is unrelated"
		}
	}
	facts, quotes, lines := map[string]string{}, map[string]string{}, map[string]string{}
	for _, f := range p.Facts {
		if len(f.Candidates) == 0 {
			continue
		}
		if len(f.Candidates) != 1 || f.Value == "" || f.Candidates[0].Value != f.Value {
			return Plan{}, "each loan fact must be one verified candidate"
		}
		source := statement
		for _, s := range selected {
			if s.ID == f.Candidates[0].SourceID {
				source = s
			}
		}
		if source.ID != f.Candidates[0].SourceID {
			return Plan{}, "a loan fact's source is unavailable"
		}
		if _, ok := documents.QuoteLocation(source.Name, source.Content, f.Candidates[0].Quote); !ok || !valueShown(f.Field, f.Value, f.Candidates[0].Quote) {
			return Plan{}, "a loan fact's quote or value is not in its source"
		}
		want := statement.ID
		if slices.Contains([]string{"loan_account", "interest_account", "agreement_reference"}, f.Field) {
			want = rule.ID
		}
		if f.Field == "book_balance" {
			want = ledger.ChartSource.ID
		}
		if source.ID != want {
			return Plan{}, "lender facts, policy choices and booked balances must retain their source authority"
		}
		quote := f.Candidates[0].Quote
		if strings.Contains(quote, "\n") {
			return Plan{}, "cite each loan fact's complete labelled line"
		}
		at := strings.Index(source.Content, quote)
		start := strings.LastIndex(source.Content[:at], "\n") + 1
		end := strings.Index(source.Content[at:], "\n")
		if end < 0 {
			end = len(source.Content) - at
		}
		facts[f.Field], quotes[f.Field], lines[f.Field] = f.Value, quote, source.Content[start:at+end]
	}
	for _, field := range []string{"lender", "borrower", "loan_reference", "currency", "annual_rate", "opening_principal", "closing_principal", "interest_paid_through", "payment_count", "payment_date", "payment_total", "principal_paid", "interest_paid"} {
		if facts[field] == "" {
			return Plan{}, "confirm " + strings.ReplaceAll(field, "_", " ") + " from the lender statement"
		}
	}
	if err := BilledToClient(facts["borrower"], entity, aliases); err != nil {
		return Plan{}, "the statement must name this client as borrower"
	}

	borrower := documents.NormalizeEntity(lines["borrower"])
	reference := documents.NormalizeEntity(lines["loan_reference"])
	if !issuerLine(quotes["lender"], facts["lender"], statement.Content) || !slices.Contains([]string{"borrower " + documents.NormalizeEntity(facts["borrower"]), "account holder " + documents.NormalizeEntity(facts["borrower"])}, borrower) || !slices.Contains([]string{"loan reference " + documents.NormalizeEntity(facts["loan_reference"]), "loan number " + documents.NormalizeEntity(facts["loan_reference"])}, reference) {
		return Plan{}, "the complete borrower, lender and loan reference must be explicit"
	}

	count := documents.NormalizeEntity(lines["payment_count"])
	if facts["payment_count"] != "1" || (count != "number of payments 1" && count != "payment count 1") {
		return Plan{}, "this automatic path needs the statement to explicitly report one payment"
	}
	month, err := time.Parse("2006-01", period)
	if err != nil {
		return Plan{}, "the loan close month is invalid"
	}
	end := month.AddDate(0, 1, -1).Format("2006-01-02")
	if facts["payment_date"] != end || facts["currency"] != currency {
		return Plan{}, "the one payment must be received at month-end in home currency"
	}
	covered := false
	for _, line := range strings.Split(statement.Content, "\n") {
		if dateLineSays(line, period+"-01", end, []string{"statement period", "period covered"}, nil, nil) && documents.DateIn(line, period+"-01", false) && documents.DateIn(line, end, false) {
			covered = true
		}
		normal := documents.NormalizeEntity(line)
		if mentionsAny(normal, []string{"rate change", "variable rate", "floating rate", "new rate", "previous rate", "prior rate", "amended", "modified", "refinanc", "forgiv", "waiv", "restruct", "balloon", "co borrower", "joint borrower"}) {
			return Plan{}, "changed or shared loan terms need a person"
		}
		if mentionsAny(normal, []string{"fee", "penalt", "escrow", "charge", "past due", "overdue", "delinquen"}) {
			for amount := range distinctFigures(line) {
				if amount != 0 {
					return Plan{}, "loan fees or escrow need their separately approved treatment"
				}
			}
		}
	}
	if !covered {
		return Plan{}, "the complete statement period must match this close month"
	}
	labels := map[string][]string{"opening_principal": {"opening principal"}, "closing_principal": {"closing principal"}, "principal_paid": {"principal paid", "principal received"}, "interest_paid": {"interest paid", "interest received"}, "payment_total": {"total payments received", "total payments paid"}, "payment_date": {"payment received date", "payment date"}, "annual_rate": {"annual rate", "interest rate"}, "interest_paid_through": {"opening interest paid through", "interest paid through before"}}
	for field, terms := range labels {
		if !lineSays(quotes[field], facts[field], terms, nil) || negated(documents.NormalizeEntity(lines[field])) || mentionsAny(lines[field], []string{"expected", "scheduled", "due", "projected", "forecast", "planned"}) {
			return Plan{}, "the " + field + " must describe actual statement activity"
		}
	}
	amounts := map[string]int64{}
	for _, field := range []string{"opening_principal", "closing_principal", "principal_paid", "interest_paid", "payment_total"} {
		v, e := prepaids.Amount(facts[field])
		if e != nil || moneyFigures(quotes[field]) != 1 || negativeFigure(quotes[field], v) || !currencyBefore(quotes[field], currency, v) {
			return Plan{}, "the lender's amounts must be explicit nonnegative home-currency money"
		}
		amounts[field] = v
	}
	if amounts["principal_paid"] <= 0 || amounts["payment_total"] <= 0 || amounts["principal_paid"]+amounts["interest_paid"] != amounts["payment_total"] || amounts["opening_principal"]-amounts["principal_paid"] != amounts["closing_principal"] {
		return Plan{}, "the lender's stated principal, interest, payment total and balances must tie"
	}
	value := func(label string) string {
		v, ok := procedureValue(procedure.Instructions, label)
		if !ok {
			return ""
		}
		return v
	}
	prefix := "Loan " + facts["loan_reference"] + " "
	company, lenderID := value("QuickBooks company"), value("Lender ID for "+facts["lender"])
	id, idErr := strconv.ParseUint(lenderID, 10, 64)
	if company == "" || company != ledger.Realm || company != ledger.CaptureRealm || company != ledger.ChartRealm || idErr != nil || id == 0 || strconv.FormatUint(id, 10) != lenderID {
		return Plan{}, "the approved policy must bind this lender's ID to the current QuickBooks company"
	}
	a, ok1 := new(big.Rat).SetString(facts["annual_rate"])
	b, ok2 := new(big.Rat).SetString(value(prefix + "annual rate"))
	if !ok1 || !ok2 || a.Cmp(b) != 0 || value(prefix+"accrue interest") != "false" || value(prefix+"reclass current portion") != "false" {
		return Plan{}, "the current rate and explicit accrual/current-portion choices must match the approved loan policy"
	}
	in := loans.Input{Entity: entity, ClosePeriod: period, Lender: facts["lender"], SourceReference: "Evidence #" + statement.ID + "; loan " + facts["loan_reference"], AgreementReference: value(prefix + "agreement reference"), Currency: currency, AnnualRate: facts["annual_rate"], DayCount: value(prefix + "day count"), OpeningPrincipal: prepaids.Money(amounts["opening_principal"]), InterestPaidThrough: facts["interest_paid_through"], Payments: []loans.Payment{{Date: facts["payment_date"], Principal: prepaids.Money(amounts["principal_paid"]), Interest: prepaids.Money(amounts["interest_paid"])}}, ClosingPrincipal: prepaids.Money(amounts["closing_principal"]), BookBalance: prepaids.Money(amounts["closing_principal"]), LoanAccount: value(prefix + "account"), InterestAccount: value(prefix + "interest account"), Method: loans.Method, MethodConfirmed: true, RateConfirmed: true, BookingConfirmed: true}
	if in.AgreementReference == "" || in.DayCount == "" {
		return Plan{}, "name the agreement and day-count explicitly in this loan's policy"
	}
	for field, want := range map[string]string{"loan_account": in.LoanAccount, "interest_account": in.InterestAccount, "agreement_reference": in.AgreementReference, "book_balance": in.BookBalance} {
		if got := facts[field]; got != "" && !strings.EqualFold(got, want) {
			return Plan{}, "the cited " + field + " conflicts with the approved policy or current books"
		}
	}
	example := LoanExample{}
	for _, e := range procedure.LoanExamples {
		if e.Input.Method == loans.Method && e.Input.DayCount == in.DayCount && e.Input.Currency == currency && !e.Input.AccrueInterest && !e.Input.ReclassCurrentPortion && len(e.Input.RateChanges) == 0 && len(e.Input.Fees) == 0 && e.Input.IssuanceCosts == nil {
			example = e
			break
		}
	}
	if example.PaperID == "" {
		return Plan{}, "activate a current approved fixed-loan example with this day-count and choices"
	}
	loanIDs, interestIDs, cashIDs := ledger.IDs[strings.ToLower(in.LoanAccount)], ledger.IDs[strings.ToLower(in.InterestAccount)], ledger.IDs[strings.ToLower(value(prefix+"payment account"))]
	if len(loanIDs) != 1 || len(interestIDs) != 1 || len(cashIDs) != 1 || !slices.Contains([]string{"Other Current Liability", "Long Term Liability"}, ledger.Types[loanIDs[0]]) || !slices.Contains([]string{"Expense", "Other Expense"}, ledger.Types[interestIDs[0]]) || ledger.Types[cashIDs[0]] != "Bank" {
		return Plan{}, "the policy must name unique current loan, interest and bank accounts"
	}
	location, why := loanPaymentBooking(ledger, in, lenderID, loanIDs[0], interestIDs[0], cashIDs[0], amounts["payment_total"])
	if why != "" {
		return Plan{}, why
	}
	paper, err := loans.Prepare(in)
	if err != nil {
		return Plan{}, err.Error()
	}
	if len(paper.Questions) > 0 {
		return Plan{}, paper.Questions[0]
	}
	if len(paper.Blockers) > 0 || len(paper.Proposals) > 0 || paper.BookDifference != "0.00" {
		return Plan{}, "the loan needs a reviewed difference or treatment"
	}
	raw, _ := json.Marshal(in)
	capture, chart := ledger.Source, ledger.ChartSource
	capture.Role, capture.Location = "period_ledger", location
	chart.Role, chart.Location = "ledger", "Loan account "+loanIDs[0]+"; current credit balance "+in.BookBalance
	rule.Role, rule.Location = "procedure", "Version "+procedure.ID+"; example #"+example.PaperID+" review #"+example.ReviewID
	return Plan{Kind: "loan", Procedure: procedure.ID, Chart: ledger.Chart, BilledTo: facts["borrower"], Input: raw, Sources: []Source{statement.Source, capture, chart, rule}}, ""
}

func loanPaymentBooking(l *Ledger, in loans.Input, lender, loan, interest, cash string, total int64) (string, string) {
	fail := "the current loan balance, one lender payment and its cash/principal/interest booking must agree"
	principal, _ := prepaids.Amount(in.Payments[0].Principal)
	interestPaid, _ := prepaids.Amount(in.Payments[0].Interest)
	paymentID := ""
	for _, r := range l.Rows {
		var row struct {
			TotalAmt    json.Number
			Credit      bool
			PaymentType string
			AccountRef  accountRef
			VendorRef   accountRef
			EntityRef   struct{ Value, Name, Type string }
			Line        []struct {
				Amount                        json.Number
				AccountBasedExpenseLineDetail *struct{ AccountRef accountRef }
				JournalEntryLineDetail        *struct{ AccountRef accountRef }
			}
			TxnTaxDetail struct{ TotalTax json.Number }
		}
		if json.Unmarshal(r.Record, &row) != nil {
			return "", fail
		}
		party, partyName := row.VendorRef.Value, row.VendorRef.Name
		if party == "" {
			party, partyName = row.EntityRef.Value, row.EntityRef.Name
		}
		touches := false
		for _, line := range row.Line {
			touches = touches || line.AccountBasedExpenseLineDetail != nil && line.AccountBasedExpenseLineDetail.AccountRef.Value == loan || line.JournalEntryLineDetail != nil && line.JournalEntryLineDetail.AccountRef.Value == loan
		}
		if !touches && party != lender && !sameName(partyName, in.Lender) {
			continue
		}
		if paymentID != "" || r.Kind != "Purchase" || !touches || party != lender || !sameName(partyName, in.Lender) || row.VendorRef.Value == "" && row.EntityRef.Type != "Vendor" || row.Credit || !slices.Contains([]string{"Cash", "Check"}, row.PaymentType) || r.Currency != in.Currency || r.Date != in.Payments[0].Date || row.AccountRef.Value != cash {
			return "", fail
		}
		amount, err := prepaids.Amount(row.TotalAmt.String())
		if err != nil || amount != total {
			return "", fail
		}
		tax := row.TxnTaxDetail.TotalTax.String()
		if tax != "" && tax != "0" && tax != "0.00" {
			return "", fail
		}
		seen := map[string]bool{}
		for _, line := range row.Line {
			if line.AccountBasedExpenseLineDetail == nil {
				return "", fail
			}
			id := line.AccountBasedExpenseLineDetail.AccountRef.Value
			amount, err := prepaids.Amount(line.Amount.String())
			if seen[id] || err != nil || id != loan && id != interest || id == loan && amount != principal || id == interest && amount != interestPaid {
				return "", fail
			}
			seen[id] = true
		}
		if !seen[loan] || interestPaid > 0 && !seen[interest] {
			return "", fail
		}
		paymentID = r.ProviderID
	}
	if paymentID == "" {
		return "", fail
	}
	_, tbLocation, cells, err := quickbooks.TrialBalanceAccount(l.AccountCatalog, l.TrialBalance, loan, in.Currency, l.Types[loan])
	if err != nil {
		return "", err.Error()
	}
	var tb struct {
		Columns struct{ Column []struct{ ColTitle string } }
	}
	var values []struct{ Value string }
	if json.Unmarshal(l.TrialBalance, &tb) != nil || json.Unmarshal(cells, &values) != nil {
		return "", fail
	}
	debit, credit := -1, -1
	for i, c := range tb.Columns.Column {
		if c.ColTitle == "Debit" {
			debit = i
		}
		if c.ColTitle == "Credit" {
			credit = i
		}
	}
	if debit < 1 || credit < 1 || len(values) <= max(debit, credit) {
		return "", fail
	}
	d := strings.TrimSpace(values[debit].Value)
	if d == "" {
		d = "0.00"
	}
	dr, e1 := prepaids.Amount(strings.ReplaceAll(d, ",", ""))
	cr, e2 := prepaids.Amount(strings.ReplaceAll(values[credit].Value, ",", ""))
	closing, _ := prepaids.Amount(in.ClosingPrincipal)
	if e1 != nil || e2 != nil || dr != 0 || cr != closing {
		return "", fail
	}
	var directory []struct {
		ID          string
		CurrencyRef accountRef
	}
	if json.Unmarshal(l.AccountCatalog, &directory) != nil {
		return "", fail
	}
	bankCurrency := ""
	for _, a := range directory {
		if a.ID == cash {
			bankCurrency = a.CurrencyRef.Value
		}
	}
	if bankCurrency != in.Currency {
		return "", fail
	}
	var gl struct {
		Header  struct{ ReportName, Currency, ReportBasis, StartPeriod, EndPeriod string }
		Columns struct{ Column []struct{ ColTitle string } }
	}
	month, _ := time.Parse("2006-01", in.ClosePeriod)
	if json.Unmarshal(l.GeneralLedger, &gl) != nil || gl.Header.ReportName != "GeneralLedger" || gl.Header.Currency != in.Currency || gl.Header.ReportBasis != "Accrual" || gl.Header.StartPeriod != in.ClosePeriod+"-01" || gl.Header.EndPeriod != month.AddDate(0, 1, -1).Format("2006-01-02") {
		return "", fail
	}
	at := map[string]int{}
	for i, c := range gl.Columns.Column {
		key := strings.ToLower(c.ColTitle)
		if _, ok := at[key]; ok {
			return "", fail
		}
		at[key] = i
	}
	for _, key := range []string{"date", "transaction type", "name", "amount", "balance"} {
		if _, ok := at[key]; !ok {
			return "", fail
		}
	}
	rows, err := quickbooks.LedgerLinesOf(l.GeneralLedger)
	if err != nil {
		return "", err.Error()
	}
	opened, paid := false, false
	location := ""
	opening, _ := prepaids.Amount(in.OpeningPrincipal)
	for _, line := range rows {
		if line.AccountID != loan {
			continue
		}
		var row struct {
			Type    string
			ColData []struct{ ID, Value string }
		}
		if json.Unmarshal(line.Cells, &row) != nil {
			return "", fail
		}
		if row.Type == "Section" && len(row.ColData) == 0 {
			continue
		}
		if row.Type != "Data" || len(row.ColData) != len(gl.Columns.Column) {
			return "", fail
		}
		cell := func(key string) string { return strings.TrimSpace(row.ColData[at[key]].Value) }
		money := func(key string) (int64, error) {
			return prepaids.Amount(strings.ReplaceAll(strings.TrimPrefix(cell(key), "-"), ",", ""))
		}
		if slices.ContainsFunc(row.ColData, func(c struct{ ID, Value string }) bool { return c.Value == "Beginning Balance" }) {
			v, err := money("balance")
			if opened || paid || err != nil || v != opening {
				return "", fail
			}
			opened = true
			continue
		}
		a, e1 := money("amount")
		b, e2 := money("balance")
		if !opened || paid || e1 != nil || e2 != nil || a != principal || b != closing || cell("date") != in.Payments[0].Date || !slices.Contains([]string{"Expense", "Check", "Cash Expense"}, cell("transaction type")) || row.ColData[at["transaction type"]].ID != paymentID || row.ColData[at["name"]].ID != lender || !sameName(cell("name"), in.Lender) {
			return "", fail
		}
		paid, location = true, line.Location
	}
	if !opened || !paid {
		return "", fail
	}
	return location + "; " + tbLocation, ""
}

func LoanDrafted(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID string, p Plan) (string, error) {
	var in loans.Input
	if Strict(p.Input, &in) != nil {
		return "loan input is invalid", nil
	}
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workpapers w WHERE w.close_id::text=$1 AND w.kind='loan' AND lower(w.paper->'input'->>'loan_account')=lower($2) AND w.id::text<>$3 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text))`, closeID, in.LoanAccount, p.Revises).Scan(&exists)
	if err != nil {
		return "", err
	}
	if exists {
		return "this loan account already has a current draft; a person places any correction", nil
	}
	return "", nil
}
