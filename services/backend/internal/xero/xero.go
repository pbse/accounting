// Package xero reads one Xero organisation's accounts and month-end Trial Balance, and posts a manual journal only
// when the caller has a person's approval.
package xero

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
)

// Granular scopes are required for apps created after 2 March 2026. Manual journals are available on every Xero plan;
// accounting.manualjournals also reads them.
const scopes = "offline_access accounting.settings.read accounting.reports.trialbalance.read accounting.manualjournals accounting.invoices.read accounting.payments.read accounting.banktransactions.read"

// granted lists the scopes an access token carries.
func granted(access string) []string {
	parts := strings.Split(access, ".")
	var claims struct {
		Scope any `json:"scope"`
	}
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	out := []string{}
	switch s := claims.Scope.(type) {
	case string:
		out = strings.Fields(s)
	case []any:
		for _, v := range s {
			if text, ok := v.(string); ok {
				out = append(out, text)
			}
		}
	}
	return out
}

// CanPostJournals reports whether an access token carries the manual journal scope; older connections must reconnect.
func CanPostJournals(access string) bool {
	g := granted(access)
	return slices.Contains(g, "accounting.manualjournals") || slices.Contains(g, "accounting.transactions")
}

// CanReadTransactions reports whether an access token can read invoices, credit notes, payments, bank transactions
// and manual journals; connections made before these scopes were requested must reconnect.
func CanReadTransactions(access string) bool {
	g := granted(access)
	if slices.Contains(g, "accounting.transactions") || slices.Contains(g, "accounting.transactions.read") {
		return true
	}
	for _, scope := range []string{"accounting.invoices", "accounting.payments", "accounting.banktransactions", "accounting.manualjournals"} {
		if !slices.Contains(g, scope) && !slices.Contains(g, scope+".read") {
			return false
		}
	}
	return true
}

type Credentials struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Expires int    `json:"expires_in"`
	Type    string `json:"token_type"`
}

type Tenant struct {
	ID        string `json:"tenantId"`
	Name      string `json:"tenantName"`
	Type      string `json:"tenantType"`
	AuthEvent string `json:"authEventId"`
}

func Configured() bool {
	callback, err := url.Parse(os.Getenv("XERO_REDIRECT_URI"))
	app, appErr := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	return err == nil && appErr == nil && os.Getenv("XERO_CLIENT_ID") != "" && os.Getenv("XERO_CLIENT_SECRET") != "" &&
		callback.Scheme == "https" && callback.Host != "" && callback.User == nil && callback.RawQuery == "" && callback.Path == "/v1/xero/callback" &&
		app.Scheme == "https" && app.Host != "" && app.User == nil && app.RawQuery == "" && app.Fragment == "" && (app.Path == "" || app.Path == "/")
}

func AuthorizationURL(state string) string {
	return "https://login.xero.com/identity/connect/authorize?" + url.Values{"response_type": {"code"}, "client_id": {os.Getenv("XERO_CLIENT_ID")}, "redirect_uri": {os.Getenv("XERO_REDIRECT_URI")}, "scope": {scopes}, "state": {state}}.Encode()
}

func token(ctx context.Context, values url.Values) (Credentials, error) {
	var result Credentials
	if !Configured() {
		return result, errors.New("Configure the Xero app and exact HTTPS callback first")
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://identity.xero.com/connect/token", strings.NewReader(values.Encode()))
	req.SetBasicAuth(os.Getenv("XERO_CLIENT_ID"), os.Getenv("XERO_CLIENT_SECRET"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	raw, err := read(req, 128*1024)
	if err != nil {
		return result, err
	}
	if json.Unmarshal(raw, &result) != nil || result.Access == "" || result.Refresh == "" || !strings.EqualFold(result.Type, "bearer") || result.Expires < 60 || result.Expires > 86400 {
		return result, errors.New("Xero returned an invalid token response; reconnect")
	}
	return result, nil
}

func Exchange(ctx context.Context, code string) (Credentials, error) {
	return token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {os.Getenv("XERO_REDIRECT_URI")}})
}

// Refresh returns a new refresh token; Xero invalidates the old one, so persist it before use.
func Refresh(ctx context.Context, refresh string) (Credentials, error) {
	return token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

// Tenants lists the organisations authorised in this sign-in. /connections also returns
// tenants from earlier consents, so only the access token's authentication event counts.
func Tenants(ctx context.Context, access string) ([]Tenant, error) {
	parts := strings.Split(access, ".")
	var claims struct {
		Event string `json:"authentication_event_id"`
	}
	if len(parts) != 3 {
		return nil, errors.New("Xero access token is not readable; reconnect")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Event == "" {
		return nil, errors.New("Xero access token has no sign-in event; reconnect")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.xero.com/connections", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	raw, err := read(req, 256*1024)
	if err != nil {
		return nil, err
	}
	var all []Tenant
	if json.Unmarshal(raw, &all) != nil {
		return nil, errors.New("Xero returned an invalid organisation list")
	}
	tenants := []Tenant{}
	for _, t := range all {
		if t.AuthEvent == claims.Event && t.Type == "ORGANISATION" && t.ID != "" {
			tenants = append(tenants, t)
		}
	}
	return tenants, nil
}

// Get reads one Accounting API path for a tenant.
func Get(ctx context.Context, access, tenant, path string, limit int64) (json.RawMessage, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.xero.com/api.xro/2.0/"+path, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("xero-tenant-id", tenant)
	return read(req, limit)
}

// TrialBalanceAt reads the Trial Balance as at date and checks Xero returned that report for that date.
func TrialBalanceAt(ctx context.Context, access, tenant string, date time.Time) (json.RawMessage, error) {
	day := date.Format("2006-01-02")
	report, err := Get(ctx, access, tenant, "Reports/TrialBalance?"+url.Values{"date": {day}}.Encode(), 8*1024*1024)
	if err != nil {
		return nil, err
	}
	var check struct {
		Reports []struct{ ReportType, ReportDate string }
	}
	if json.Unmarshal(report, &check) != nil || len(check.Reports) != 1 || check.Reports[0].ReportType != "TrialBalance" {
		return nil, errors.New("Xero did not return a Trial Balance")
	}
	if at, e := time.Parse("2 January 2006", check.Reports[0].ReportDate); e != nil || !at.Equal(date) {
		return nil, fmt.Errorf("Xero returned a Trial Balance for %q, not %s", check.Reports[0].ReportDate, day)
	}
	return report, nil
}

// Capture reads the organisation, its accounts and the Trial Balance as at monthEnd, then the month's transactions
// and the open items. The reads are sequential, not an atomic ledger copy. Transactions only add checks: when they
// cannot be read in the time left, the Trial Balance is still returned with the reason.
func Capture(ctx context.Context, access, tenant string, monthEnd time.Time) (json.RawMessage, error) {
	organisation, err := Get(ctx, access, tenant, "Organisation", 256*1024)
	if err != nil {
		return nil, err
	}
	accounts, err := Get(ctx, access, tenant, "Accounts", 4*1024*1024)
	if err != nil {
		return nil, err
	}
	requested := time.Now().UTC()
	report, err := TrialBalanceAt(ctx, access, tenant, monthEnd)
	if err != nil {
		return nil, err
	}
	snapshot := map[string]any{"date": monthEnd.Format("2006-01-02"), "tenant_id": tenant, "organisation": organisation, "accounts": accounts, "trial_balance": report, "trial_balance_requested_at": requested}
	if !CanReadTransactions(access) {
		snapshot["transactions_error"] = "This connection was made before transactions could be read; disconnect and connect Xero again to read them."
		return json.Marshal(snapshot)
	}
	// Leave time to save the snapshot after the transaction reads.
	budget, cancel := ctx, context.CancelFunc(func() {})
	if deadline, ok := ctx.Deadline(); ok {
		budget, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
	}
	defer cancel()
	transactions, err := Transactions(budget, access, tenant, monthEnd)
	switch {
	case budget.Err() != nil && ctx.Err() == nil:
		snapshot["transactions_error"] = "Transactions took longer than this capture allows; the daily capture reads them with more time."
	case err != nil:
		snapshot["transactions_error"] = err.Error()
	default:
		snapshot["transactions"] = transactions
	}
	return json.Marshal(snapshot)
}

// transactionReads are the month's records and, apart, the documents still open at capture time. Statuses exclude
// drafts, voided and deleted records.
var transactionReads = []struct{ set, endpoint, where, statuses string }{
	{"records", "Invoices", "%s", "AUTHORISED,PAID"},
	{"records", "CreditNotes", `%s&&(Status=="AUTHORISED"||Status=="PAID")`, ""},
	{"records", "BankTransactions", `%s&&Status=="AUTHORISED"`, ""},
	{"records", "ManualJournals", `%s&&Status=="POSTED"`, ""},
	{"records", "Payments", `%s&&Status=="AUTHORISED"`, ""},
	{"open", "Invoices", "%s", "AUTHORISED"},
	{"open", "CreditNotes", `%s&&Status=="AUTHORISED"`, ""},
	{"open", "Overpayments", `%s&&Status=="AUTHORISED"`, ""},
	{"open", "Prepayments", `%s&&Status=="AUTHORISED"`, ""},
}

// Transactions reads the month's records and the open items dated on or before monthEnd. Each type is paged to its
// end, up to 5000 records, and the count Xero reports must match what was read. A record updated after the reads
// began asks for a new capture. A record deleted and another added while paging can still be missed.
func Transactions(ctx context.Context, access, tenant string, monthEnd time.Time) (map[string]any, error) {
	started := time.Now()
	first := monthEnd.AddDate(0, 0, 1-monthEnd.Day())
	day := func(t time.Time) string { return fmt.Sprintf("DateTime(%d,%02d,%02d)", t.Year(), t.Month(), t.Day()) }
	month := "Date>=" + day(first) + "&&Date<=" + day(monthEnd)
	out := map[string]any{"read_at": started.UTC(), "records": map[string][]json.RawMessage{}, "open": map[string][]json.RawMessage{}}
	size := 0
	for _, read := range transactionReads {
		where := month
		if read.set == "open" {
			where = "Date<=" + day(monthEnd)
		}
		query := url.Values{"where": {fmt.Sprintf(read.where, where)}, "pageSize": {"1000"}}
		if read.statuses != "" {
			query.Set("Statuses", read.statuses)
		}
		rows := []json.RawMessage{}
		for page := 1; ; page++ {
			query.Set("page", fmt.Sprint(page))
			raw, err := Get(ctx, access, tenant, read.endpoint+"?"+query.Encode(), 16*1024*1024)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", read.endpoint, err)
			}
			var response map[string]json.RawMessage
			var records []json.RawMessage
			var paging struct{ ItemCount *int }
			if json.Unmarshal(raw, &response) != nil || json.Unmarshal(response[read.endpoint], &records) != nil || len(records) > 1000 {
				return nil, fmt.Errorf("Xero returned an unreadable %s page", read.endpoint)
			}
			if json.Unmarshal(response["pagination"], &paging) != nil || paging.ItemCount == nil {
				return nil, fmt.Errorf("Xero did not count the %s it returned; capture again", read.endpoint)
			}
			for _, record := range records {
				var stamp struct{ UpdatedDateUTC string }
				json.Unmarshal(record, &stamp)
				if updated, ok := instant(stamp.UpdatedDateUTC); ok && updated.After(started) {
					return nil, fmt.Errorf("%s changed in Xero during the capture; capture again", read.endpoint)
				}
			}
			rows = append(rows, records...)
			size += len(raw)
			if len(rows) > 5000 {
				return nil, fmt.Errorf("%s exceeds the 5000-record limit", read.endpoint)
			}
			if size > 12*1024*1024 {
				return nil, errors.New("Xero transactions exceed the 12 MiB capture limit")
			}
			if len(records) < 1000 {
				if *paging.ItemCount != len(rows) {
					return nil, fmt.Errorf("Xero counted %d %s but %d were read; capture again", *paging.ItemCount, read.endpoint, len(rows))
				}
				break
			}
		}
		out[read.set].(map[string][]json.RawMessage)[read.endpoint] = rows
	}
	out["completeness"] = "Each type was paged to its end and matched Xero's count. Reads are sequential, not an atomic copy: a record deleted and another added while paging can be missed, and later edits need a new capture. Open items are as of the capture."
	return out, nil
}

// instant reads a Xero timestamp such as "/Date(1727654400000+0000)/".
func instant(value string) (time.Time, bool) {
	var millis int64
	ms, ok := strings.CutPrefix(value, "/Date(")
	if !ok {
		return time.Time{}, false
	}
	if _, err := fmt.Sscanf(ms, "%d", &millis); err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(millis), true
}

// RequestedAt is when a snapshot's Trial Balance was requested: anything posted earlier is in it.
func RequestedAt(payload json.RawMessage) (time.Time, bool) {
	var snapshot struct {
		At time.Time `json:"trial_balance_requested_at"`
	}
	return snapshot.At, json.Unmarshal(payload, &snapshot) == nil && !snapshot.At.IsZero()
}

// Day reads a Xero date, which arrives as "/Date(1727654400000+0000)/" or "2026-09-30T00:00:00".
func Day(value string) (time.Time, error) {
	if ms, ok := strings.CutPrefix(value, "/Date("); ok {
		var millis int64
		if _, err := fmt.Sscanf(ms, "%d", &millis); err != nil {
			return time.Time{}, err
		}
		return time.UnixMilli(millis).UTC().Truncate(24 * time.Hour), nil
	}
	if len(value) >= 10 {
		return time.Parse("2006-01-02", value[:10])
	}
	return time.Time{}, errors.New("unreadable Xero date")
}

// Net returns each listed account's YTD net (debit positive) from a Trial Balance report. An account with no row
// has no balance; an account listed twice or an unreadable amount refuses.
func Net(report json.RawMessage, ids []string) (map[string]string, error) {
	wrapped, _ := json.Marshal(map[string]any{"trial_balance": report})
	_, _, rows, err := Balances(wrapped)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "0.00"
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if _, wanted := out[row.AccountID]; !wanted {
			continue
		}
		if seen[row.AccountID] {
			return nil, fmt.Errorf("Xero Trial Balance lists account %s twice", row.AccountID)
		}
		seen[row.AccountID] = true
		net, err := ledger.Net(row.YTDDebit, row.YTDCredit)
		if err != nil {
			return nil, fmt.Errorf("Xero Trial Balance %v for %s", err, row.Name)
		}
		out[row.AccountID] = net.FloatString(2)
	}
	return out, nil
}

type JournalLine struct {
	AccountID   string      `json:"AccountID"`
	LineAmount  json.Number `json:"LineAmount"`
	Description string      `json:"Description,omitempty"`
}

type ManualJournal struct {
	ManualJournalID string        `json:"ManualJournalID,omitempty"`
	Narration       string        `json:"Narration"`
	Date            string        `json:"Date"`
	Status          string        `json:"Status"`
	LineAmountTypes string        `json:"LineAmountTypes"`
	JournalLines    []JournalLine `json:"JournalLines"`
}

// PostManualJournal creates one manual journal. Xero keeps the idempotency key for only a few minutes, so the
// caller's saved attempt, not the key, is what prevents a second write.
func PostManualJournal(ctx context.Context, access, tenant, key string, journal ManualJournal) (string, error) {
	body, _ := json.Marshal(map[string]any{"ManualJournals": []ManualJournal{journal}})
	req, _ := http.NewRequestWithContext(ctx, "PUT", "https://api.xero.com/api.xro/2.0/ManualJournals", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("xero-tenant-id", tenant)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	raw, err := read(req, 1024*1024)
	if err != nil {
		return "", err
	}
	var result struct{ ManualJournals []ManualJournal }
	if json.Unmarshal(raw, &result) != nil || len(result.ManualJournals) != 1 || result.ManualJournals[0].ManualJournalID == "" {
		return "", errors.New("Xero did not return the created manual journal")
	}
	return result.ManualJournals[0].ManualJournalID, nil
}

// ReadManualJournal reads one manual journal back by its ID.
func ReadManualJournal(ctx context.Context, access, tenant, id string) (ManualJournal, error) {
	raw, err := Get(ctx, access, tenant, "ManualJournals/"+url.PathEscape(id), 1024*1024)
	var result struct{ ManualJournals []ManualJournal }
	if err == nil && (json.Unmarshal(raw, &result) != nil || len(result.ManualJournals) != 1) {
		err = errors.New("Xero did not return the manual journal")
	}
	if err != nil {
		return ManualJournal{}, err
	}
	return result.ManualJournals[0], nil
}

// SameJournal reports whether a read-back journal posted exactly the intended lines on the intended date.
func SameJournal(want, got ManualJournal) bool {
	wantDay, err1 := Day(want.Date)
	gotDay, err2 := Day(got.Date)
	if err1 != nil || err2 != nil || !wantDay.Equal(gotDay) || got.Status != "POSTED" || got.Narration != want.Narration || got.LineAmountTypes != want.LineAmountTypes || len(want.JournalLines) != len(got.JournalLines) {
		return false
	}
	remaining := slices.Clone(got.JournalLines)
	for _, line := range want.JournalLines {
		amount, ok := new(big.Rat).SetString(string(line.LineAmount))
		i := slices.IndexFunc(remaining, func(l JournalLine) bool {
			other, good := new(big.Rat).SetString(string(l.LineAmount))
			return ok && good && strings.EqualFold(l.AccountID, line.AccountID) && other.Cmp(amount) == 0
		})
		if i < 0 {
			return false
		}
		remaining = slices.Delete(remaining, i, i+1)
	}
	return true
}

func read(req *http.Request, limit int64) ([]byte, error) {
	req.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return nil, errors.New("Xero response not received; retry or reconnect")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Xero returned HTTP %d; check the connection before retrying", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit || !json.Valid(raw) {
		return nil, errors.New("Xero response invalid or exceeds supported size")
	}
	return raw, nil
}

// Balance is one Trial Balance row joined to its account by Xero account ID. Columns keep
// Xero's own titles: YTD is the balance as at the date for balance-sheet accounts and the
// year-to-date movement for revenue and expense accounts.
type Balance struct {
	AccountID string `json:"account_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Class     string `json:"class"`
	Debit     string `json:"debit"`
	Credit    string `json:"credit"`
	YTDDebit  string `json:"ytd_debit"`
	YTDCredit string `json:"ytd_credit"`
	System    string `json:"system_account,omitempty"`
}

// Profile returns the organisation's base currency and first fiscal month from a snapshot.
func Profile(payload json.RawMessage) (string, int, error) {
	var snapshot struct {
		Organisation struct {
			Organisations []struct {
				BaseCurrency          string
				FinancialYearEndMonth int
			}
		}
	}
	if json.Unmarshal(payload, &snapshot) != nil || len(snapshot.Organisation.Organisations) != 1 {
		return "", 0, errors.New("Xero organisation currency or financial year is unreadable")
	}
	o := snapshot.Organisation.Organisations[0]
	if o.BaseCurrency == "" || o.FinancialYearEndMonth < 1 || o.FinancialYearEndMonth > 12 {
		return "", 0, errors.New("Xero organisation currency or financial year is unreadable")
	}
	return o.BaseCurrency, o.FinancialYearEndMonth%12 + 1, nil
}

func Balances(payload json.RawMessage) (currency, date string, rows []Balance, err error) {
	var snapshot struct {
		Date         string
		Organisation struct {
			Organisations []struct{ BaseCurrency string }
		}
		Accounts struct {
			Accounts []struct {
				AccountID, Code, Name, Class, SystemAccount string
			}
		}
		TrialBalance struct {
			Reports []struct{ Rows []row }
		} `json:"trial_balance"`
	}
	if json.Unmarshal(payload, &snapshot) != nil || len(snapshot.TrialBalance.Reports) != 1 {
		return "", "", nil, errors.New("Xero snapshot is unreadable")
	}
	if len(snapshot.Organisation.Organisations) == 1 {
		currency = snapshot.Organisation.Organisations[0].BaseCurrency
	}
	columns := map[string]int{}
	rows = []Balance{}
	var visit func([]row)
	visit = func(group []row) {
		for _, r := range group {
			visit(r.Rows)
			if r.RowType == "Header" {
				for i, cell := range r.Cells {
					columns[cell.Value] = i
				}
				continue
			}
			if r.RowType != "Row" || len(r.Cells) == 0 {
				continue
			}
			id := ""
			for _, a := range r.Cells[0].Attributes {
				if a.ID == "account" {
					id = a.Value
				}
			}
			if id == "" {
				continue
			}
			cell := func(title string) string {
				if i, ok := columns[title]; ok && i < len(r.Cells) {
					return r.Cells[i].Value
				}
				return ""
			}
			b := Balance{AccountID: id, Name: r.Cells[0].Value, Debit: cell("Debit"), Credit: cell("Credit"), YTDDebit: cell("YTD Debit"), YTDCredit: cell("YTD Credit")}
			if i := slices.IndexFunc(snapshot.Accounts.Accounts, func(a struct{ AccountID, Code, Name, Class, SystemAccount string }) bool { return a.AccountID == id }); i >= 0 {
				account := snapshot.Accounts.Accounts[i]
				b.Code, b.Name, b.Class, b.System = account.Code, account.Name, account.Class, account.SystemAccount
			}
			rows = append(rows, b)
		}
	}
	visit(snapshot.TrialBalance.Reports[0].Rows)
	for _, title := range []string{"Debit", "Credit", "YTD Debit", "YTD Credit"} {
		if _, ok := columns[title]; !ok {
			return "", "", nil, errors.New("Xero Trial Balance has no " + title + " column")
		}
	}
	return currency, snapshot.Date, rows, nil
}

type row struct {
	RowType string
	Cells   []struct {
		Value      string
		Attributes []struct {
			Value string
			ID    string `json:"Id"`
		}
	}
	Rows []row
}

var classes = map[string]string{"ASSET": "Asset", "LIABILITY": "Liability", "EQUITY": "Equity", "REVENUE": "Revenue", "EXPENSE": "Expense"}

// WrongSide lists accounts whose YTD net sits opposite their normal side. Xero has no contra
// subtype, so accumulated depreciation and amortisation are recognised by name; retained
// earnings and owner drawings normally sit on the opposite side and are skipped.
func WrongSide(payload json.RawMessage) (string, []ledger.BalanceException, error) {
	currency, date, rows, err := Balances(payload)
	if err != nil {
		return "", nil, err
	}
	out := []ledger.BalanceException{}
	for _, row := range rows {
		class, lower := classes[row.Class], strings.ToLower(row.Name)
		if class == "" || row.System == "RETAINEDEARNINGS" || strings.Contains(lower, "accumulated") || class == "Equity" && (strings.Contains(lower, "draw") || strings.Contains(lower, "distribution")) {
			continue
		}
		net, err := ledger.Net(row.YTDDebit, row.YTDCredit)
		if err != nil {
			return "", nil, fmt.Errorf("Xero Trial Balance %v for %s", err, row.Name)
		}
		basis := "balance"
		if class == "Revenue" || class == "Expense" {
			basis = "year-to-date balance"
		}
		if item := ledger.Holding(row.AccountID, row.Name, class, net, currency, basis); item != nil {
			out = append(out, *item)
		} else if item := ledger.WrongSide(row.AccountID, row.Name, class, net, currency, basis); item != nil {
			out = append(out, *item)
		}
	}
	slices.SortFunc(out, func(a, b ledger.BalanceException) int { return strings.Compare(a.Account, b.Account) })
	return date, out, nil
}

// AccountBalances returns each Trial Balance account's year-to-date net; an account missing from the directory has no class.
func AccountBalances(payload json.RawMessage) (string, []ledger.AccountBalance, error) {
	currency, _, rows, err := Balances(payload)
	if err != nil {
		return "", nil, err
	}
	out := []ledger.AccountBalance{}
	for _, row := range rows {
		net, err := ledger.Net(row.YTDDebit, row.YTDCredit)
		if err != nil {
			return "", nil, fmt.Errorf("Xero Trial Balance %v for %s", err, row.Name)
		}
		out = append(out, ledger.AccountBalance{ID: row.AccountID, Name: row.Name, Class: classes[row.Class], Net: net})
	}
	return currency, out, nil
}

// MonthlyActivity returns each revenue and expense account's movement in the Trial Balance's month, signed
// as a Profit and Loss shows it. Xero's Debit and Credit columns cover the current month up to the report date.
func MonthlyActivity(payload json.RawMessage) (string, []ledger.AccountBalance, error) {
	currency, _, rows, err := Balances(payload)
	if err != nil {
		return "", nil, err
	}
	out := []ledger.AccountBalance{}
	for _, row := range rows {
		class := classes[row.Class]
		if class != "Revenue" && class != "Expense" {
			continue
		}
		net, err := ledger.Net(row.Debit, row.Credit)
		if err != nil {
			return "", nil, fmt.Errorf("Xero Trial Balance %v for %s", err, row.Name)
		}
		if class == "Revenue" {
			net.Neg(net)
		}
		out = append(out, ledger.AccountBalance{ID: row.AccountID, Name: row.Name, Class: class, Net: net})
	}
	return currency, out, nil
}

// NativeStatus counts, for one bank account, the month's captured authorised bank transactions (transfer sides
// included) and payments that Xero flags reconciled, not reconciled, or leaves unflagged.
type NativeStatus struct {
	AccountID    string `json:"account_id"`
	Account      string `json:"account"`
	Reconciled   int    `json:"reconciled"`
	Unreconciled int    `json:"unreconciled"`
	Unknown      int    `json:"unknown"`
}

// NativeReconciliation reads Xero's own reconciled flags from a snapshot's captured records, per bank account. It is an
// observation of record flags at capture time, not a statement reconciliation.
func NativeReconciliation(payload json.RawMessage) ([]NativeStatus, error) {
	type flagged struct {
		Status       string
		IsReconciled *bool
		BankAccount  struct{ AccountID string }
		Account      struct{ AccountID string }
	}
	var snapshot struct {
		Accounts struct {
			Accounts []struct{ AccountID, Name, Type string }
		}
		Transactions *struct {
			Records struct {
				BankTransactions []flagged
				Payments         []flagged
			} `json:"records"`
		} `json:"transactions"`
		TransactionsError string `json:"transactions_error"`
	}
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, errors.New("The Xero snapshot is unreadable")
	}
	if snapshot.Transactions == nil {
		return nil, errors.New(cmp.Or(snapshot.TransactionsError, "Transactions were not read in this capture"))
	}
	statuses, byID := []NativeStatus{}, map[string]int{}
	for _, a := range snapshot.Accounts.Accounts {
		if a.Type == "BANK" && a.AccountID != "" {
			byID[a.AccountID] = len(statuses)
			statuses = append(statuses, NativeStatus{AccountID: a.AccountID, Account: a.Name})
		}
	}
	count := func(id string, r flagged) {
		i, bank := byID[id]
		if !bank || r.Status != "AUTHORISED" {
			return
		}
		switch {
		case r.IsReconciled == nil:
			statuses[i].Unknown++
		case *r.IsReconciled:
			statuses[i].Reconciled++
		default:
			statuses[i].Unreconciled++
		}
	}
	for _, r := range snapshot.Transactions.Records.BankTransactions {
		count(r.BankAccount.AccountID, r)
	}
	for _, r := range snapshot.Transactions.Records.Payments {
		count(r.Account.AccountID, r)
	}
	return statuses, nil
}
