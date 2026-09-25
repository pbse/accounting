// Package xero reads one Xero organisation's month-end Trial Balance. It never writes to Xero.
package xero

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
)

// Granular scopes are required for apps created after 2 March 2026.
const scopes = "offline_access accounting.settings.read accounting.reports.trialbalance.read"

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

// Capture reads the organisation, its accounts and the Trial Balance as at monthEnd.
// The reads are sequential, not an atomic ledger copy.
func Capture(ctx context.Context, access, tenant string, monthEnd time.Time) (json.RawMessage, error) {
	get := func(path string, limit int64) (json.RawMessage, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.xero.com/api.xro/2.0/"+path, nil)
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("xero-tenant-id", tenant)
		return read(req, limit)
	}
	organisation, err := get("Organisation", 256*1024)
	if err != nil {
		return nil, err
	}
	accounts, err := get("Accounts", 4*1024*1024)
	if err != nil {
		return nil, err
	}
	date := monthEnd.Format("2006-01-02")
	report, err := get("Reports/TrialBalance?"+url.Values{"date": {date}}.Encode(), 8*1024*1024)
	if err != nil {
		return nil, err
	}
	var check struct {
		Reports []struct{ ReportType, ReportDate string }
	}
	if json.Unmarshal(report, &check) != nil || len(check.Reports) != 1 || check.Reports[0].ReportType != "TrialBalance" {
		return nil, errors.New("Xero did not return a Trial Balance")
	}
	if at, e := time.Parse("2 January 2006", check.Reports[0].ReportDate); e != nil || !at.Equal(monthEnd) {
		return nil, fmt.Errorf("Xero returned a Trial Balance for %q, not %s", check.Reports[0].ReportDate, date)
	}
	return json.Marshal(map[string]any{"date": date, "tenant_id": tenant, "organisation": organisation, "accounts": accounts, "trial_balance": report})
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
	_, debit := columns["YTD Debit"]
	_, credit := columns["YTD Credit"]
	if !debit || !credit {
		return "", "", nil, errors.New("Xero Trial Balance has no YTD Debit and YTD Credit columns")
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
