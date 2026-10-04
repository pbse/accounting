package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The expected statements below are worked by hand from the two balanced trial balances, not from the code.
func TestManagementPackTiesStatementsToTheTrialBalances(t *testing.T) {
	accounts := map[string]packAccount{
		"1": {"Checking", "Asset", "Bank", ""}, "2": {"Receivables", "Asset", "Accounts Receivable", ""}, "3": {"Prepaid", "Asset", "Other Current Asset", ""},
		"4": {"Equipment", "Asset", "Fixed Asset", ""}, "5": {"Payables", "Liability", "Accounts Payable", ""}, "6": {"Card", "Liability", "Credit Card", ""},
		"7": {"Loan", "Liability", "Long Term Liability", ""}, "8": {"Owner", "Equity", "Equity", ""}, "9": {"Retained earnings", "Equity", "Equity", "RetainedEarnings"},
		"10": {"Sales", "Revenue", "Income", ""}, "11": {"Materials", "Expense", "Cost of Goods Sold", ""}, "12": {"Rent", "Expense", "Expense", ""}, "13": {"Interest", "Revenue", "Other Income", ""},
	}
	rats := func(m map[string]string) map[string]*big.Rat {
		out := map[string]*big.Rat{}
		for k, v := range m {
			out[k], _ = new(big.Rat).SetString(v)
		}
		return out
	}
	// Last month also had an advertising expense that is gone from this month's books, and filed the prepaid under
	// another type.
	lastAccounts := map[string]packAccount{"15": {"Advertising", "Expense", "Expense", ""}}
	for id, a := range accounts {
		lastAccounts[id] = a
	}
	lastAccounts["3"] = packAccount{"Prepaid", "Asset", "Other Asset", ""}
	last := &books{Currency: "USD", Typed: true, Accounts: lastAccounts,
		Balances: rats(map[string]string{"1": "9800", "2": "5000", "3": "1200", "4": "6000", "5": "-3000", "6": "-500", "7": "-8000", "8": "-5000", "9": "-2000", "10": "-9000", "11": "3000", "12": "2300", "15": "200"}),
		Activity: rats(map[string]string{"10": "3000", "11": "1000", "12": "800", "15": "200"})}
	this := &books{Currency: "USD", Typed: true, Accounts: accounts,
		Balances: rats(map[string]string{"1": "9900", "2": "6000", "3": "1000", "4": "8000", "5": "-3700", "6": "-400", "7": "-9000", "8": "-4500", "9": "-2000", "10": "-13000", "11": "4500", "12": "3300", "13": "-100"}),
		Activity: rats(map[string]string{"10": "4000", "11": "1500", "12": "1000", "13": "100"})}

	var got struct {
		Complete      bool
		ProfitAndLoss statement `json:"profit_and_loss"`
		BalanceSheet  struct {
			Sections         []packSection
			Earnings         packLine
			TotalAssets      packLine `json:"total_assets"`
			TotalLiabilities packLine `json:"total_liabilities"`
			TotalEquity      packLine `json:"total_equity"`
			Difference       string
		} `json:"balance_sheet"`
		WorkingCapital map[string]packLine `json:"working_capital"`
		CashFlow       map[string]string   `json:"cash_flow"`
		Reclassified   []string
	}
	read := func(pack map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(pack)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
	}
	read(managementPack(this, last))

	measures := map[string]packLine{}
	for _, m := range got.ProfitAndLoss.Measures {
		measures[m.Name] = m
	}
	for name, want := range map[string][2]string{"Gross profit": {"2500.00", "2000.00"}, "Operating profit": {"1500.00", "1000.00"}, "Net income": {"1600.00", "1000.00"}} {
		if m := measures[name]; m.Amount != want[0] || m.Prior == nil || *m.Prior != want[1] {
			t.Errorf("%s: %+v, want %v", name, m, want)
		}
	}
	bs := got.BalanceSheet
	if bs.TotalAssets.Amount != "24900.00" || bs.TotalLiabilities.Amount != "13100.00" || bs.TotalEquity.Amount != "11800.00" || bs.Earnings.Amount != "5300.00" || bs.Difference != "0.00" {
		t.Errorf("balance sheet: %+v", bs)
	}
	if *bs.TotalAssets.Prior != "22000.00" || *bs.TotalAssets.Change != "2900.00" || *bs.TotalEquity.Prior != "10500.00" {
		t.Errorf("total assets compares with last month: %+v", bs.TotalAssets)
	}
	for _, s := range bs.Sections {
		if s.Name == "Equity" && (len(s.Lines) != 2 || s.Total.Amount != "6500.00") {
			t.Errorf("equity holds owner and retained earnings: %+v", s)
		}
	}
	if wc := got.WorkingCapital["working_capital"]; wc.Amount != "12800.00" || *wc.Prior != "12500.00" || got.WorkingCapital["book_cash"].Amount != "9900.00" {
		t.Errorf("working capital: %+v", got.WorkingCapital)
	}
	for _, section := range got.ProfitAndLoss.Sections {
		for _, l := range section.Lines {
			if l.Name == "Advertising" && (section.Name != "Expense" || l.Amount != "0.00" || *l.Prior != "200.00") {
				t.Errorf("an expense only last month keeps last month's classification: %s %+v", section.Name, l)
			}
		}
	}
	if len(got.Reclassified) != 1 || got.Reclassified[0] != "Prepaid" {
		t.Errorf("an account classified differently in the two months is listed: %v", got.Reclassified)
	}
	if got.CashFlow["unavailable"] == "" {
		t.Error("no cash-flow statement is derived from balance changes")
	}
	if got.Complete {
		t.Error("a pack without a reconciled cash-flow statement is not complete")
	}

	// An account the ledger does not classify is shown, never folded into a total that then looks complete.
	this.Accounts = map[string]packAccount{"14": {Name: "Suspense"}}
	for id, a := range accounts {
		this.Accounts[id] = a
	}
	this.Balances["14"], _ = new(big.Rat).SetString("50")
	this.Balances["1"], _ = new(big.Rat).SetString("9850")
	got.Complete = true
	read(managementPack(this, last))
	if got.Complete || got.BalanceSheet.Difference != "-50.00" || got.BalanceSheet.Sections[len(got.BalanceSheet.Sections)-1].Name != "Unclassified" {
		t.Errorf("an unclassified balance stays visible and incomplete: %+v", got.BalanceSheet)
	}

	// Without account types (Xero) there is no current/long-term split, so neither is guessed.
	pack := managementPack(&books{Currency: "USD", Accounts: map[string]packAccount{"a": {"Bank", "Asset", "", ""}, "b": {"Sales", "Revenue", "", ""}}, Balances: rats(map[string]string{"a": "70", "b": "-70"}), Activity: rats(map[string]string{"b": "70"})}, nil)
	if _, ok := pack["working_capital"].(map[string]string)["unavailable"]; !ok || pack["complete"] != false {
		t.Errorf("an untyped ledger states working capital is unavailable and, with no cash-flow statement, is not complete: %+v", pack)
	}
}

func TestManagementPackReadsTheCapturedReports(t *testing.T) {
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
	var closeID, connection, evidence string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Pack "+rand.Text()).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
	row := func(id, name, a, b string) string {
		return `{"type":"Data","ColData":[{"id":"` + id + `","value":"` + name + `"},{"value":"` + a + `"}` + map[bool]string{true: `,{"value":"` + b + `"}`, false: ``}[b != "-"] + `]}`
	}
	accounts := `[{"Id":"1","Name":"Checking","Classification":"Asset","AccountType":"Bank"},{"Id":"2","Name":"Card","Classification":"Liability","AccountType":"Credit Card"},{"Id":"3","Name":"Owner","Classification":"Equity","AccountType":"Equity"},{"Id":"4","Name":"Sales","Classification":"Revenue","AccountType":"Income"},{"Id":"5","Name":"Rent","Classification":"Expense","AccountType":"Expense"}]`
	tb := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[` + strings.Join([]string{row("1", "Checking", "1500.00", ""), row("2", "Card", "", "200.00"), row("3", "Owner", "", "1000.00"), row("4", "Sales", "", "800.00"), row("5", "Rent", "500.00", "")}, ",") + `]}}`
	pl := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[` + row("4", "Sales", "800.00", "-") + `,` + row("5", "Rent", "500.00", "-") + `]}}`
	payload := `{"period":"2026-09","source_id":"` + evidence + `","accounts":` + accounts + `,"reports":{"TrialBalance":` + tb + `,"ProfitAndLoss":` + pl + `,"GeneralLedger":{}}}`
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), payload)
	must(err)

	token := strings.Repeat("x", 32)
	h := apiWithStore(db, token)
	call := func(method, path string, body any) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	w := httptest.NewRecorder()
	w.Body.Write(call("GET", "/v1/closes/"+closeID+"/management-pack", nil))
	w.Code = 200
	var got struct {
		State, Status, Currency, Basis, Source string
		Comparison                             string `json:"comparison_unavailable"`
		Complete                               bool
		ProfitAndLoss                          statement `json:"profit_and_loss"`
		BalanceSheet                           struct {
			TotalAssets packLine `json:"total_assets"`
			TotalEquity packLine `json:"total_equity"`
			Difference  string
		} `json:"balance_sheet"`
		CashFlow map[string]any `json:"cash_flow"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("pack: %d %s", w.Code, w.Body.String())
	}
	net := got.ProfitAndLoss.Measures[len(got.ProfitAndLoss.Measures)-1]
	if got.State != "ready" || got.Status != "provisional" || got.Currency != "USD" || got.Basis != "Accrual" || got.Source != "quickbooks" || got.Complete {
		t.Fatalf("pack metadata: %s", w.Body.String())
	}
	if net.Name != "Net income" || net.Amount != "300.00" || net.Prior != nil || got.BalanceSheet.TotalAssets.Amount != "1500.00" || got.BalanceSheet.TotalEquity.Amount != "1300.00" || got.BalanceSheet.Difference != "0.00" {
		t.Fatalf("statements from the captured reports: %s", w.Body.String())
	}
	if got.Comparison == "" || got.CashFlow["unavailable"] == nil {
		t.Fatalf("without last month there is no comparison or cash flow, and it says so: %s", w.Body.String())
	}

	// A sign-off covers the capture it was made over.
	defer db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, closeID)
	defer db.Exec(`DELETE FROM close_scopes WHERE close_id=$1`, closeID)
	status := func() string {
		t.Helper()
		var pack struct{ Status string }
		must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/management-pack", nil), &pack))
		return pack.Status
	}
	var current readiness
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/readiness", nil), &current))
	call("POST", "/v1/closes/"+closeID+"/scope", map[string]any{"digest": current.Digest, "scope": closeScope{Description: "Management accounts", RequiredPapers: []string{}, ExternalSteps: []externalStep{{Name: "Ledger review", Complete: true, Reference: "Reviewed"}}}})
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/readiness", nil), &current))
	call("POST", "/v1/closes/"+closeID+"/signoff", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "action": "sign", "note": "Synthetic sign-off", "confirmed": true})
	if got := status(); got != "signed_off" {
		t.Fatalf("the capture the sign-off was made over is signed off: %s", got)
	}
	// A newer Xero capture, which the signed digest does not bind, leaves the sign-off standing but stays provisional.
	defer db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, closeID)
	defer db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, closeID)
	var xeroConnection string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&xeroConnection))
	cell := func(id, name, debit, credit, ytdDebit, ytdCredit string) string {
		return `{"RowType":"Row","Cells":[{"Value":"` + name + `","Attributes":[{"Id":"account","Value":"` + id + `"}]},{"Value":"` + debit + `"},{"Value":"` + credit + `"},{"Value":"` + ytdDebit + `"},{"Value":"` + ytdCredit + `"}]}`
	}
	xeroPayload := `{"date":"2026-09-30","organisation":{"Organisations":[{"BaseCurrency":"USD"}]},"accounts":{"Accounts":[{"AccountID":"a-1","Name":"Checking","Class":"ASSET"},{"AccountID":"a-2","Name":"Card","Class":"LIABILITY"},{"AccountID":"a-3","Name":"Owner","Class":"EQUITY"},{"AccountID":"a-4","Name":"Sales","Class":"REVENUE"},{"AccountID":"a-5","Name":"Rent","Class":"EXPENSE"}]},"trial_balance":{"Reports":[{"Rows":[{"RowType":"Header","Cells":[{"Value":"Account"},{"Value":"Debit"},{"Value":"Credit"},{"Value":"YTD Debit"},{"Value":"YTD Credit"}]},` +
		strings.Join([]string{cell("a-1", "Checking", "", "", "1500.00", ""), cell("a-2", "Card", "", "", "", "200.00"), cell("a-3", "Owner", "", "", "", "1000.00"), cell("a-4", "Sales", "", "800.00", "", "800.00"), cell("a-5", "Rent", "500.00", "", "500.00", "")}, ",") + `]}]}}`
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d',$3,'fixture',now()+interval '1 minute')`, closeID, xeroConnection, xeroPayload)
	must(err)
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/readiness", nil), &current))
	if got := status(); got != "provisional" || current.State != "signed_off" {
		t.Fatalf("a capture newer than a sign-off that still stands is provisional: %s (readiness %s)", got, current.State)
	}

	// Readiness binds the QuickBooks capture with the highest ID; a pack showing a different capture is not covered.
	_, err = db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, closeID)
	must(err)
	if got := status(); got != "signed_off" {
		t.Fatalf("the signed QuickBooks capture is shown again: %s", got)
	}
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now()-interval '2 hours',now()-interval '1 hour','fixture')`, closeID, connection, rand.Text(), payload)
	must(err)
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/readiness", nil), &current))
	call("POST", "/v1/closes/"+closeID+"/signoff", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "action": "reopen", "note": "Recapture", "confirmed": true})
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/readiness", nil), &current))
	call("POST", "/v1/closes/"+closeID+"/signoff", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "action": "sign", "note": "Signed over the later-ID capture", "confirmed": true})
	if got := status(); got != "provisional" {
		t.Fatalf("a sign-off bound to another capture does not cover the one shown: %s", got)
	}
}

func TestPublishedManagementPacksAreFrozenAndCorrectedAsNewVersions(t *testing.T) {
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
	var closeID, connection, evidence string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Published "+rand.Text()).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM management_publications WHERE close_id=$1`, `DELETE FROM close_signoffs WHERE close_id=$1`, `DELETE FROM close_scopes WHERE close_id=$1`,
			`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
	// Each capture is a balanced month: cash 2000 less rent, card 200, owner 1000, sales 800.
	capture := func(rent, cash string) {
		t.Helper()
		row := func(id, name, a, b string) string {
			return `{"type":"Data","ColData":[{"id":"` + id + `","value":"` + name + `"},{"value":"` + a + `"}` + map[bool]string{true: `,{"value":"` + b + `"}`, false: ``}[b != "-"] + `]}`
		}
		accounts := `[{"Id":"1","Name":"Checking","Classification":"Asset","AccountType":"Bank"},{"Id":"2","Name":"Card","Classification":"Liability","AccountType":"Credit Card"},{"Id":"3","Name":"Owner","Classification":"Equity","AccountType":"Equity"},{"Id":"4","Name":"Sales","Classification":"Revenue","AccountType":"Income"},{"Id":"5","Name":"Rent","Classification":"Expense","AccountType":"Expense"}]`
		tb := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[` + strings.Join([]string{row("1", "Checking", cash, ""), row("2", "Card", "", "200.00"), row("3", "Owner", "", "1000.00"), row("4", "Sales", "", "800.00"), row("5", "Rent", rent, "")}, ",") + `]}}`
		pl := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[` + row("4", "Sales", "800.00", "-") + `,` + row("5", "Rent", rent, "-") + `]}}`
		// The ledger's own Statement of Cash Flows: net income is the month's change in cash.
		r, _ := strconv.ParseFloat(rent, 64)
		c, _ := strconv.ParseFloat(cash, 64)
		net, begin := fmt.Sprintf("%.2f", 800-r), fmt.Sprintf("%.2f", c-(800-r))
		flow := `{"Header":{"ReportName":"CashFlow","StartPeriod":"2026-09-01","EndPeriod":"2026-09-30","Currency":"USD"},"Rows":{"Row":[
 {"type":"Section","group":"OperatingActivities","Rows":{"Row":[{"type":"Data","group":"NetIncome","ColData":[{"value":"Net Income"},{"value":"` + net + `"}]}]},"Summary":{"ColData":[{"value":"Net cash provided by operating activities"},{"value":"` + net + `"}]}},
 {"type":"Section","group":"CashIncrease","Summary":{"ColData":[{"value":"NET CASH INCREASE FOR PERIOD"},{"value":"` + net + `"}]}},
 {"type":"Section","group":"BeginningCash","Summary":{"ColData":[{"value":"Cash at beginning of period"},{"value":"` + begin + `"}]}},
 {"type":"Data","group":"EndingCash","ColData":[{"value":"CASH AT END OF PERIOD"},{"value":"` + cash + `"}]}]}}`
		payload := `{"period":"2026-09","source_id":"` + evidence + `","accounts":` + accounts + `,"reports":{"TrialBalance":` + tb + `,"ProfitAndLoss":` + pl + `,"CashFlow":` + flow + `,"GeneralLedger":{}}}`
		_, err := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), payload)
		must(err)
	}
	token := strings.Repeat("m", 32)
	h := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	base := "/v1/closes/" + closeID
	digest := func() string {
		var pack struct{ Digest string }
		must(json.Unmarshal(call("GET", base+"/management-pack", nil, 200), &pack))
		return pack.Digest
	}
	sign := func(action string) {
		var current readiness
		must(json.Unmarshal(call("GET", base+"/readiness", nil, 200), &current))
		call("POST", base+"/signoff", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "action": action, "note": "Synthetic " + action, "confirmed": true}, 200)
	}
	type listed struct {
		Publications []struct {
			ID, State, Reason string
			Version           int
			Supersedes        *string
			Acknowledgments   []acknowledgment
		}
	}
	list := func() listed {
		var out listed
		must(json.Unmarshal(call("GET", base+"/management-pack/publications", nil, 200), &out))
		return out
	}
	publish := map[string]any{"action": "publish", "commentary": "Rent held at 500; net income 300.", "confirmed": true}

	capture("500.00", "1500.00")
	publish["digest"] = digest()
	if body := call("POST", base+"/management-pack/publications", publish, 409); !strings.Contains(string(body), "sign-off stands") {
		t.Fatalf("a provisional pack is not published: %s", body)
	}
	var current readiness
	must(json.Unmarshal(call("GET", base+"/readiness", nil, 200), &current))
	call("POST", base+"/scope", map[string]any{"digest": current.Digest, "scope": closeScope{Description: "Management accounts", RequiredPapers: []string{}, ExternalSteps: []externalStep{{Name: "Ledger review", Complete: true, Reference: "Reviewed"}}}}, 200)
	sign("sign")
	publish["digest"] = digest()
	var first struct {
		ID      string
		Version int
	}
	must(json.Unmarshal(call("POST", base+"/management-pack/publications", publish, 200), &first))
	if body := call("POST", base+"/management-pack/publications", publish, 409); !strings.Contains(string(body), "already published") {
		t.Fatalf("an unchanged pack is published once: %s", body)
	}
	export := func(id, format string) string {
		return string(call("GET", base+"/management-pack/publications/"+id+"/export?format="+format, nil, 200))
	}
	if csv := export(first.ID, "csv"); first.Version != 1 || !strings.Contains(csv, "Net income,300.00") || !strings.Contains(csv, "Rent held at 500") || !strings.Contains(csv, "Total assets,1500.00") || !strings.Contains(csv, "Total liabilities,200.00") || !strings.Contains(csv, "captured from quickbooks") {
		t.Fatalf("the CSV export is the published version with its commentary: %d %s", first.Version, csv)
	}
	if !strings.HasPrefix(export(first.ID, "pdf"), "%PDF") {
		t.Fatal("the PDF export is a PDF")
	}
	call("POST", base+"/management-pack/publications", map[string]any{"action": "acknowledge", "publication_id": first.ID}, 200)
	if got := list(); len(got.Publications) != 1 || got.Publications[0].State != "current" || len(got.Publications[0].Acknowledgments) != 1 {
		t.Fatalf("the publication is current and acknowledged: %+v", got)
	}

	// The books change after publication: the version is flagged, and a correction is a new version with its reason.
	// Reopening the signed close leaves the published pack as it was, flagged: its approval no longer stands.
	sign("reopen")
	if got := list(); got.Publications[0].State != "signoff_lapsed" {
		t.Fatalf("a reopened close's publication is not current: %+v", got)
	}
	sign("sign")
	if got := list(); got.Publications[0].State != "current" {
		t.Fatalf("signed again with the same figures, the publication is current again: %+v", got)
	}
	capture("600.00", "1400.00")
	if got := list(); got.Publications[0].State != "needs_correction" {
		t.Fatalf("changed books flag the published version: %+v", got)
	}
	sign("reopen")
	sign("sign")
	publish["digest"] = digest()
	if body := call("POST", base+"/management-pack/publications", publish, 422); !strings.Contains(string(body), "Say why") {
		t.Fatalf("a correction says why: %s", body)
	}
	publish["reason"] = "Rent for September was 600, not 500."
	call("POST", base+"/management-pack/publications", publish, 200)
	got := list()
	if len(got.Publications) != 2 || got.Publications[0].Version != 2 || got.Publications[0].State != "current" || *got.Publications[0].Supersedes != first.ID || got.Publications[1].State != "superseded" {
		t.Fatalf("the correction supersedes the original, which is kept: %+v", got)
	}
	if csv := export(first.ID, "csv"); !strings.Contains(csv, "Net income,300.00") {
		t.Fatalf("the original version's export is unchanged: %s", csv)
	}
}

func TestManagementPackShowsTheLedgersCashFlowOnlyAsItTies(t *testing.T) {
	accounts := map[string]packAccount{"1": {"Checking", "Asset", "Bank", ""}, "8": {"Owner", "Equity", "Equity", ""}, "10": {"Sales", "Revenue", "Income", ""}}
	rats := func(m map[string]string) map[string]*big.Rat {
		out := map[string]*big.Rat{}
		for k, v := range m {
			out[k], _ = new(big.Rat).SetString(v)
		}
		return out
	}
	statement := func(ending string, payable ...string) json.RawMessage {
		payable = append(payable, "200.00")
		return json.RawMessage(`{"Header":{"ReportName":"CashFlow","StartPeriod":"2026-09-01","EndPeriod":"2026-09-30","Currency":"USD"},"Rows":{"Row":[
 {"type":"Section","group":"OperatingActivities","Header":{"ColData":[{"value":"OPERATING ACTIVITIES"},{"value":""}]},"Rows":{"Row":[{"type":"Data","group":"NetIncome","ColData":[{"value":"Net Income"},{"value":"100.00"}]},
  {"type":"Section","group":"OperatingAdjustments","Header":{"ColData":[{"value":"Adjustments"},{"value":""}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"Accounts Payable"},{"value":"` + payable[0] + `"}]},{"type":"Data","ColData":[{"value":"Note"}]}]},"Summary":{"ColData":[{"value":"Total adjustments"},{"value":"200.00"}]}}]},
  "Summary":{"ColData":[{"value":"Net cash provided by operating activities"},{"value":"300.00"}]}},
 {"type":"Section","group":"InvestingActivities","Rows":{"Row":[{"type":"Data","ColData":[{"value":"Equipment"},{"value":"-250.00"}]}]},"Summary":{"ColData":[{"value":"Net cash provided by investing activities"},{"value":"-250.00"}]}},
 {"type":"Section","group":"FinancingActivities","Rows":{"Row":[{"type":"Data","ColData":[{"value":"Owner contribution"},{"value":"50.00"}]}]},"Summary":{"ColData":[{"value":"Net cash provided by financing activities"},{"value":"50.00"}]}},
 {"type":"Section","group":"CashIncrease","Summary":{"ColData":[{"value":"NET CASH INCREASE FOR PERIOD"},{"value":"100.00"}]}},
 {"type":"Section","group":"BeginningCash","Summary":{"ColData":[{"value":"Cash at beginning of period"},{"value":"9,800.00"}]}},
 {"type":"Data","group":"EndingCash","ColData":[{"value":"CASH AT END OF PERIOD"},{"value":"` + ending + `"}]}]}}`)
	}
	last := &books{Currency: "USD", Typed: true, Accounts: accounts, Balances: rats(map[string]string{"1": "9800", "8": "-9800"}), Activity: rats(nil)}
	var pack map[string]any
	month := func(report json.RawMessage) map[string]any {
		this := &books{Currency: "USD", Typed: true, Accounts: accounts, Balances: rats(map[string]string{"1": "9900", "8": "-9800", "10": "-100"}), Activity: rats(map[string]string{"10": "100"}), CashFlow: report}
		pack = managementPack(this, last)
		raw, _ := json.Marshal(pack["cash_flow"])
		var flow map[string]any
		json.Unmarshal(raw, &flow)
		return flow
	}
	flow := month(statement("9,900.00"))
	sections, _ := flow["sections"].([]any)
	if flow["reconciled"] != true || pack["complete"] != true || len(sections) != 3 || !strings.Contains(fmt.Sprint(sections[0]), "Accounts Payable") || flow["book_cash"] != "9900.00" {
		t.Fatalf("a statement that ties to the books is shown: %v", flow)
	}
	// Published, it is in the export with its reconciliation.
	frozen, _ := json.Marshal(pack)
	rows, err := publicationRows(publication{Pack: frozen})
	if err != nil || !strings.Contains(fmt.Sprint(rows), "Cash flow Operating activities Accounts Payable 200.00") || !strings.Contains(fmt.Sprint(rows), "Ties to the books") {
		t.Fatalf("the export carries the statement: %v %v", rows, err)
	}
	// A month without activity sections is still a statement, and is exported.
	quiet := json.RawMessage(`{"Rows":{"Row":[{"group":"CashIncrease","Summary":{"ColData":[{"value":"Net change"},{"value":"0.00"}]}},{"group":"BeginningCash","Summary":{"ColData":[{"value":"Beginning"},{"value":"9900.00"}]}},{"group":"EndingCash","Summary":{"ColData":[{"value":"Ending"},{"value":"9900.00"}]}}]}}`)
	this := &books{Currency: "USD", Typed: true, Accounts: accounts, Balances: rats(map[string]string{"1": "9900", "8": "-9900"}), Activity: rats(nil), CashFlow: quiet}
	frozen, _ = json.Marshal(managementPack(this, nil))
	if rows, err = publicationRows(publication{Pack: frozen}); err != nil || !strings.Contains(fmt.Sprint(rows), "Ties to the books") {
		t.Fatalf("a statement without activity is exported: %v %v", rows, err)
	}
	flow = month(statement("9950.00"))
	if flow["reconciled"] != false || pack["complete"] != false || !strings.Contains(fmt.Sprint(flow["differences"]), "Ending cash against the month-end book cash: the statement says 9950.00, the books 9900.00") {
		t.Fatalf("a statement that does not tie says where and leaves the pack incomplete: %v", flow)
	}
	if flow = month(statement("9900.00", "999.00")); flow["reconciled"] != false || !strings.Contains(fmt.Sprint(flow["differences"]), "Operating activities total against its lines: the statement says 300.00, the books 1099.00") {
		t.Fatalf("lines that do not add to their section's total are a difference: %v", flow)
	}
	if flow = month(json.RawMessage(`{"Rows":{"Row":[]}}`)); !strings.Contains(fmt.Sprint(flow["unavailable"]), "could not be read") {
		t.Fatalf("an unreadable statement is not shown: %v", flow)
	}
	// Without a statement the pack, and the digest a publication binds, are as before.
	if flow = month(nil); !strings.Contains(fmt.Sprint(flow["unavailable"]), "neither is captured yet") {
		t.Fatalf("a capture without a statement says so: %v", flow)
	}
	plain := map[string]any{"entity": "E", "period": "2026-09", "cash_flow": map[string]any{"unavailable": "x"}}
	shown := map[string]any{"entity": "E", "period": "2026-09", "cash_flow": map[string]any{"sections": []packSection{}}}
	if figuresDigest(plain) != figuresDigest(map[string]any{"entity": "E", "period": "2026-09"}) || figuresDigest(shown) == figuresDigest(plain) {
		t.Fatal("only a shown statement is bound by the digest")
	}
}
