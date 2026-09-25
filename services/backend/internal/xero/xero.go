// Package xero reads one Xero organisation's accounts and month-end Trial Balance, and posts a manual journal only
// when the caller has a person's approval.
package xero

import (
	"bytes"
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

// Granular scopes are required for apps created after 2 March 2026. Manual journals are available on every Xero plan.
const scopes = "offline_access accounting.settings.read accounting.reports.trialbalance.read accounting.manualjournals"

// CanPostJournals reports whether an access token carries the manual journal scope; older connections must reconnect.
func CanPostJournals(access string) bool {
	parts := strings.Split(access, ".")
	var claims struct {
		Scope any `json:"scope"`
	}
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return false
	}
	granted := []string{}
	switch s := claims.Scope.(type) {
	case string:
		granted = strings.Fields(s)
	case []any:
		for _, v := range s {
			if text, ok := v.(string); ok {
				granted = append(granted, text)
			}
		}
	}
	return slices.Contains(granted, "accounting.manualjournals") || slices.Contains(granted, "accounting.transactions")
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

// Capture reads the organisation, its accounts and the Trial Balance as at monthEnd.
// The reads are sequential, not an atomic ledger copy.
func Capture(ctx context.Context, access, tenant string, monthEnd time.Time) (json.RawMessage, error) {
	organisation, err := Get(ctx, access, tenant, "Organisation", 256*1024)
	if err != nil {
		return nil, err
	}
	accounts, err := Get(ctx, access, tenant, "Accounts", 4*1024*1024)
	if err != nil {
		return nil, err
	}
	report, err := TrialBalanceAt(ctx, access, tenant, monthEnd)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"date": monthEnd.Format("2006-01-02"), "tenant_id": tenant, "organisation": organisation, "accounts": accounts, "trial_balance": report})
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
