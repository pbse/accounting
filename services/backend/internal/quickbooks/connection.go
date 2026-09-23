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
)

type Credentials struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Expires int    `json:"expires_in"`
	Type    string `json:"token_type"`
}

func OAuthConfigured() bool {
	u, e := url.Parse(os.Getenv("QBO_REDIRECT_URI"))
	app, appErr := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	if appErr != nil || app.Scheme != "https" || app.Host == "" || app.User != nil || app.RawQuery != "" || app.Fragment != "" || (app.Path != "" && app.Path != "/") {
		return false
	}
	return os.Getenv("QBO_CLIENT_ID") != "" && os.Getenv("QBO_CLIENT_SECRET") != "" && e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "/v1/quickbooks/callback"
}
func AuthorizationURL(state string) string {
	return "https://appcenter.intuit.com/connect/oauth2?" + url.Values{"client_id": {os.Getenv("QBO_CLIENT_ID")}, "redirect_uri": {os.Getenv("QBO_REDIRECT_URI")}, "response_type": {"code"}, "scope": {"com.intuit.quickbooks.accounting"}, "state": {state}}.Encode()
}
func exchange(ctx context.Context, values url.Values) (Credentials, error) {
	var result Credentials
	if !OAuthConfigured() {
		return result, errors.New("Configure the sandbox Intuit app and exact HTTPS callback first")
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer", strings.NewReader(values.Encode()))
	req.SetBasicAuth(os.Getenv("QBO_CLIENT_ID"), os.Getenv("QBO_CLIENT_SECRET"))
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
	return exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {os.Getenv("QBO_REDIRECT_URI")}})
}
func Refresh(ctx context.Context, token string) (Credentials, error) {
	return exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}})
}
func readResponse(req *http.Request, limit int64) ([]byte, error) {
	req.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
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

// Sandbox only. This reader exposes fixed GET operations, never arbitrary paths or writes.
func readSandbox(ctx context.Context, realm, token, path string) ([]byte, error) {
	return readSandboxLimit(ctx, realm, token, path, 2*1024*1024)
}
func readSandboxLimit(ctx context.Context, realm, token, path string, limit int64) ([]byte, error) {
	if !ValidRealm(realm) {
		return nil, errors.New("Invalid company identifier")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://sandbox-quickbooks.api.intuit.com/v3/company/"+realm+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return readResponse(req, limit)
}
func Company(ctx context.Context, realm, token string) (string, error) {
	raw, err := readSandbox(ctx, realm, token, "/companyinfo/"+realm)
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
func Accounts(ctx context.Context, realm, token string) (json.RawMessage, error) {
	accounts := []json.RawMessage{}
	seen := map[string]bool{}
	for start := 1; start <= 10001; start += 1000 {
		q := fmt.Sprintf("SELECT * FROM Account WHERE Active IN (true,false) ORDERBY Id STARTPOSITION %d MAXRESULTS 1000", start)
		raw, err := readSandbox(ctx, realm, token, "/query?query="+url.QueryEscape(q))
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
