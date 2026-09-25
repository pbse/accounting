// Package stripe reads a client's itemized payout reconciliation report with a restricted, report-scoped key.
// Creating a report run is the only request that is not a plain read; it changes no financial data.
package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const reportType = "payout_reconciliation.itemized.5"

// Columns are exactly the ones the settlement parser reads.
var Columns = []string{"balance_transaction_id", "currency", "gross", "fee", "net", "reporting_category", "automatic_payout_id", "automatic_payout_effective_at"}

var restricted = regexp.MustCompile(`^rk_(live|test)_[A-Za-z0-9]{10,200}$`)

// ValidKey accepts only restricted keys, so the app never holds a secret key with full account access.
func ValidKey(key string) bool { return restricted.MatchString(key) }

func call(ctx context.Context, method, path, key string, form url.Values, idempotency string, limit int64) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequestWithContext(ctx, method, "https://api.stripe.com/v1/"+path, body)
	req.SetBasicAuth(key, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	return read(req, limit)
}

// ProviderError is a definitive refusal from Stripe; the request did not take effect.
type ProviderError struct{ Message string }

func (e ProviderError) Error() string { return e.Message }

func read(req *http.Request, limit int64) ([]byte, error) {
	response, err := (&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return nil, errors.New("Stripe response not received; retry later")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("Stripe response exceeds the supported size")
	}
	// A rate limit or a concurrent use of the same idempotency key passes; the saved request is asked again later.
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 409 && response.StatusCode != 429 {
		var failure struct {
			Error struct{ Message string }
		}
		json.Unmarshal(raw, &failure)
		return nil, ProviderError{"Stripe refused the request: " + failure.Error.Message}
	}
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Stripe returned HTTP %d; retry later", response.StatusCode)
	}
	return raw, nil
}

type Account struct {
	ID, Name string
	Through  time.Time
}

// Verify checks the key can read the payout reconciliation report and names the Stripe account it belongs to, so a
// person can confirm it is the client's. Through is when Stripe's report data currently ends.
func Verify(ctx context.Context, key string) (Account, error) {
	var a Account
	raw, err := call(ctx, "GET", "reporting/report_types/"+reportType, key, nil, "", 64*1024)
	if err != nil {
		return a, err
	}
	var kind struct {
		DataAvailableEnd int64 `json:"data_available_end"`
	}
	if json.Unmarshal(raw, &kind) != nil || kind.DataAvailableEnd == 0 {
		return a, errors.New("Stripe did not describe the payout reconciliation report")
	}
	raw, err = call(ctx, "GET", "account", key, nil, "", 256*1024)
	if err != nil {
		return a, err
	}
	var account struct {
		ID              string `json:"id"`
		BusinessProfile struct {
			Name string `json:"name"`
		} `json:"business_profile"`
		Settings struct {
			Dashboard struct {
				DisplayName string `json:"display_name"`
			} `json:"dashboard"`
		} `json:"settings"`
	}
	if json.Unmarshal(raw, &account) != nil || !strings.HasPrefix(account.ID, "acct_") {
		return a, errors.New("Stripe did not name the account; give the key read access to account details")
	}
	a.ID, a.Name, a.Through = account.ID, account.BusinessProfile.Name, time.Unix(kind.DataAvailableEnd, 0).UTC()
	if a.Name == "" {
		a.Name = account.Settings.Dashboard.DisplayName
	}
	return a, nil
}

// CreateRun asks Stripe for the month's itemized payout reconciliation. The idempotency key makes a retry of a lost
// response return the same run.
func CreateRun(ctx context.Context, key, idempotency string, start, end time.Time) (string, error) {
	form := url.Values{"report_type": {reportType}, "parameters[interval_start]": {fmt.Sprint(start.Unix())}, "parameters[interval_end]": {fmt.Sprint(end.Unix())}}
	for _, column := range Columns {
		form.Add("parameters[columns][]", column)
	}
	raw, err := call(ctx, "POST", "reporting/report_runs", key, form, idempotency, 64*1024)
	if err != nil {
		return "", err
	}
	var run struct{ ID string }
	if json.Unmarshal(raw, &run) != nil || !strings.HasPrefix(run.ID, "frr_") {
		return "", errors.New("Stripe did not return a report run")
	}
	return run.ID, nil
}

// Run returns a report run's status and, once it succeeded, its file URL.
func Run(ctx context.Context, key, id string) (string, string, string, error) {
	raw, err := call(ctx, "GET", "reporting/report_runs/"+url.PathEscape(id), key, nil, "", 64*1024)
	if err != nil {
		return "", "", "", err
	}
	var run struct {
		Status string
		Error  string
		Result *struct{ URL string }
	}
	if json.Unmarshal(raw, &run) != nil || run.Status == "" {
		return "", "", "", errors.New("Stripe report run is unreadable")
	}
	file := ""
	if run.Result != nil {
		file = run.Result.URL
	}
	return run.Status, file, run.Error, nil
}

// Download reads a report file, sending the key only to Stripe's own file host.
func Download(ctx context.Context, key, file string, limit int64) ([]byte, error) {
	u, err := url.Parse(file)
	if err != nil || u.Scheme != "https" || u.Host != "files.stripe.com" || u.User != nil || !strings.HasPrefix(u.Path, "/v1/files/file_") || !strings.HasSuffix(u.Path, "/contents") {
		return nil, ProviderError{"Stripe returned an unexpected report file location; the report was not downloaded."}
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.SetBasicAuth(key, "")
	return read(req, limit)
}
