package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
)

type Credentials struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Expires int    `json:"expires_in"`
	Type    string `json:"token_type"`
}

func OAuthConfigured() bool {
	return OAuthConfiguredFor("sandbox")
}
func envPrefix(environment string) string {
	switch environment {
	case "sandbox":
		return "QBO_"
	case "production":
		return "QBO_PRODUCTION_"
	default:
		return ""
	}
}
func OAuthConfiguredFor(environment string) bool {
	prefix := envPrefix(environment)
	if prefix == "" {
		return false
	}
	u, e := url.Parse(os.Getenv(prefix + "REDIRECT_URI"))
	app, appErr := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	if appErr != nil || app.Scheme != "https" || app.Host == "" || app.User != nil || app.RawQuery != "" || app.Fragment != "" || (app.Path != "" && app.Path != "/") {
		return false
	}
	return os.Getenv(prefix+"CLIENT_ID") != "" && os.Getenv(prefix+"CLIENT_SECRET") != "" && e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "/v1/quickbooks/callback"
}
func AuthorizationURL(state string) string {
	return AuthorizationURLFor("sandbox", state)
}
func AuthorizationURLFor(environment, state string) string {
	prefix := envPrefix(environment)
	if prefix == "" {
		return ""
	}
	return "https://appcenter.intuit.com/connect/oauth2?" + url.Values{"client_id": {os.Getenv(prefix + "CLIENT_ID")}, "redirect_uri": {os.Getenv(prefix + "REDIRECT_URI")}, "response_type": {"code"}, "scope": {"com.intuit.quickbooks.accounting"}, "state": {state}}.Encode()
}
func exchange(ctx context.Context, environment string, values url.Values) (Credentials, error) {
	var result Credentials
	if !OAuthConfiguredFor(environment) {
		return result, errors.New("Configure the Intuit app and exact HTTPS callback first")
	}
	prefix := envPrefix(environment)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer", strings.NewReader(values.Encode()))
	req.SetBasicAuth(os.Getenv(prefix+"CLIENT_ID"), os.Getenv(prefix+"CLIENT_SECRET"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	raw, err := readResponse(req, 128*1024)
	if err != nil {
		return result, err
	}
	if json.Unmarshal(raw, &result) != nil || result.Access == "" || result.Refresh == "" || !strings.EqualFold(result.Type, "bearer") || result.Expires < 60 || result.Expires > 86400 {
		return result, errors.New("Intuit returned an invalid token response; reconnect")
	}
	return result, nil
}
func Exchange(ctx context.Context, code string) (Credentials, error) {
	return ExchangeFor(ctx, "sandbox", code)
}
func ExchangeFor(ctx context.Context, environment, code string) (Credentials, error) {
	return exchange(ctx, environment, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {os.Getenv(envPrefix(environment) + "REDIRECT_URI")}})
}
func Refresh(ctx context.Context, token string) (Credentials, error) {
	return RefreshFor(ctx, "sandbox", token)
}
func RefreshFor(ctx context.Context, environment, token string) (Credentials, error) {
	return exchange(ctx, environment, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}})
}
func readResponse(req *http.Request, limit int64) ([]byte, error) {
	req.Header.Set("Accept", "application/json")
	response, err := ledger.DoThrottled(&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, req)
	if err != nil {
		return nil, errors.New("QuickBooks response not received; reconnect if token exchange was in progress")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("QuickBooks returned HTTP %d; inspect connection before retrying", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit || !json.Valid(raw) {
		return nil, errors.New("QuickBooks response invalid or exceeds supported size")
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil || body == nil || body["Fault"] != nil {
		return nil, errors.New("QuickBooks reported an API fault")
	}
	return raw, nil
}

var realmPattern = regexp.MustCompile(`^[0-9]{1,30}$`)

func ValidRealm(realm string) bool { return realmPattern.MatchString(realm) }

// read performs fixed GET operations against the connection's environment, never arbitrary paths or writes.
func read(ctx context.Context, environment, realm, token, path string, limit int64) ([]byte, error) {
	if environment != "sandbox" && environment != "production" {
		return nil, errors.New("Invalid QuickBooks environment")
	}
	host := "sandbox-quickbooks.api.intuit.com"
	if environment == "production" {
		host = "quickbooks.api.intuit.com"
	}
	if !ValidRealm(realm) {
		return nil, errors.New("Invalid company identifier")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+host+"/v3/company/"+realm+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return readResponse(req, limit)
}

// ProfileFor reads the company's home currency and first fiscal month (1-12).
func ProfileFor(ctx context.Context, environment, realm, token string) (string, int, error) {
	raw, err := read(ctx, environment, realm, token, "/preferences", 2*1024*1024)
	if err != nil {
		return "", 0, err
	}
	var result struct {
		Preferences struct {
			CurrencyPrefs struct {
				HomeCurrency struct {
					Value string `json:"value"`
				}
			}
			AccountingInfoPrefs struct{ FiscalYearStartMonth string }
		}
	}
	json.Unmarshal(raw, &result)
	month, err := time.Parse("January", result.Preferences.AccountingInfoPrefs.FiscalYearStartMonth)
	currency := result.Preferences.CurrencyPrefs.HomeCurrency.Value
	if err != nil || currency == "" {
		return "", 0, errors.New("QuickBooks company currency or fiscal year could not be read")
	}
	return currency, int(month.Month()), nil
}

func CompanyFor(ctx context.Context, environment, realm, token string) (string, error) {
	raw, err := read(ctx, environment, realm, token, "/companyinfo/"+realm, 2*1024*1024)
	if err != nil {
		return "", err
	}
	var result struct {
		CompanyInfo struct {
			ID          string `json:"Id"`
			CompanyName string
		}
	}
	if json.Unmarshal(raw, &result) != nil || result.CompanyInfo.ID == "" || strings.TrimSpace(result.CompanyInfo.CompanyName) == "" {
		return "", errors.New("Company identity could not be verified")
	}
	return result.CompanyInfo.CompanyName, nil
}

// Account records preserve provider JSON numbers. Pagination is bounded and a
// partial/inconsistent result is never stored as a complete snapshot.
func Accounts(ctx context.Context, env, realm, token string) (json.RawMessage, error) {
	accounts := []json.RawMessage{}
	seen := map[string]bool{}
	for start := 1; start <= 10001; start += 1000 {
		q := fmt.Sprintf("SELECT * FROM Account WHERE Active IN (true,false) ORDERBY Id STARTPOSITION %d MAXRESULTS 1000", start)
		raw, err := read(ctx, env, realm, token, "/query?query="+url.QueryEscape(q), 2*1024*1024)
		if err != nil {
			return nil, err
		}
		var result struct {
			QueryResponse *struct {
				Account []json.RawMessage
				Start   int `json:"startPosition"`
				Max     int `json:"maxResults"`
			}
		}
		if json.Unmarshal(raw, &result) != nil || result.QueryResponse == nil {
			return nil, errors.New("Account query response is missing")
		}
		page := result.QueryResponse
		if len(page.Account) > 1000 || (len(page.Account) > 0 && (page.Start != start || page.Max != len(page.Account))) {
			return nil, errors.New("Account pagination could not be verified")
		}
		for _, item := range page.Account {
			var a struct {
				ID string `json:"Id"`
			}
			if json.Unmarshal(item, &a) != nil || a.ID == "" || seen[a.ID] {
				return nil, errors.New("Account snapshot contains missing or duplicate identities")
			}
			seen[a.ID] = true
			accounts = append(accounts, item)
		}
		if len(accounts) > 10000 {
			return nil, errors.New("Company exceeds the 10000-account snapshot limit")
		}
		if len(page.Account) < 1000 {
			body, err := json.Marshal(accounts)
			if len(body) > 8*1024*1024 {
				return nil, errors.New("Account snapshot exceeds 8 MiB")
			}
			return body, err
		}
	}
	return nil, errors.New("Account pagination did not complete")
}
