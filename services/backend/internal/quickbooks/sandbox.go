package quickbooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
)

type Reference struct {
	Value string `json:"value"`
}
type Detail struct {
	PostingType string
	AccountRef  Reference
}
type Line struct {
	Amount                 json.Number
	DetailType             string
	JournalEntryLineDetail Detail
}
type Journal struct {
	ID          string `json:"Id,omitempty"`
	TxnDate     string
	CurrencyRef Reference
	PrivateNote string
	Line        []Line
}
type Sandbox struct {
	Realm, Token, Entity, DebitAccount, CreditAccount string
	Client                                            *http.Client
	Production                                        bool
}
type LedgerAccount struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Balance   json.Number `json:"balance"`
	SyncToken string      `json:"sync_token"`
}
type LedgerObservation struct {
	Company       string        `json:"company"`
	Prepaid       LedgerAccount `json:"prepaid"`
	Expense       LedgerAccount `json:"expense"`
	ClosedThrough string        `json:"closed_through,omitempty"`
}

// OpenPeriod reads the company's book closing date and refuses a transaction dated on or before it.
func (s Sandbox) OpenPeriod(ctx context.Context, date string) (string, error) {
	var result struct {
		Preferences *struct {
			AccountingInfoPrefs struct{ BookCloseDate string }
		}
	}
	if err := s.request(ctx, "GET", "/preferences", nil, &result); err != nil {
		return "", err
	}
	if result.Preferences == nil {
		return "", errors.New("QuickBooks closing date is unavailable; no write was made")
	}
	closed := result.Preferences.AccountingInfoPrefs.BookCloseDate
	if closed == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", closed); err != nil {
		return "", errors.New("QuickBooks closing date is unreadable; no write was made")
	}
	if date <= closed {
		return closed, fmt.Errorf("QuickBooks books are closed through %s, so a transaction dated %s cannot be written. Choose a later date or reopen the period in QuickBooks under the firm's policy", closed, date)
	}
	return closed, nil
}

var identifier = regexp.MustCompile(`^[0-9]{1,30}$`)

func (s Sandbox) Ready() bool {
	return identifier.MatchString(s.Realm) && s.Token != "" && s.Entity != "" && identifier.MatchString(s.DebitAccount) && identifier.MatchString(s.CreditAccount) && s.DebitAccount != s.CreditAccount
}
func (s Sandbox) request(ctx context.Context, method, path string, body any, result any) error {
	if !s.Ready() {
		return errors.New("QuickBooks sandbox is not configured")
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	host := "sandbox-quickbooks.api.intuit.com"
	if s.Production {
		host = "quickbooks.api.intuit.com"
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+host+"/v3/company/"+s.Realm+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("QuickBooks request outcome could not be confirmed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("QuickBooks returned HTTP %d; no automatic retry was made", response.StatusCode)
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(result) != nil {
		return errors.New("QuickBooks response could not be read")
	}
	return nil
}
func (s Sandbox) Prepare(ctx context.Context, paper prepaids.Workpaper) (Journal, error) {
	journal, _, err := s.PrepareObserved(ctx, paper)
	return journal, err
}
func (s Sandbox) PrepareObserved(ctx context.Context, paper prepaids.Workpaper) (Journal, LedgerObservation, error) {
	var observation LedgerObservation
	if paper.Input.Method != prepaids.Method || paper.Input.CreditAmount != "" || paper.Input.BookedBalance != "" || paper.Input.CancellationMethod != "" {
		if !s.Production {
			return Journal{}, observation, errors.New("This accounting method supports draft review only; sandbox posting has not been enabled for it")
		}
		return Journal{}, observation, errors.New("Only the initial full-month prepaid method is enabled for posting")
	}
	if !s.Ready() || paper.Proposal == nil || paper.Input.Entity != s.Entity {
		return Journal{}, observation, errors.New("This draft must match the confirmed company and have a positive prepaid proposal")
	}
	var company struct{ CompanyInfo struct{ CompanyName string } }
	if err := s.request(ctx, "GET", "/companyinfo/"+s.Realm, nil, &company); err != nil {
		return Journal{}, observation, err
	}
	if company.CompanyInfo.CompanyName != s.Entity {
		return Journal{}, observation, errors.New("QuickBooks company name does not match the configured client")
	}
	observation.Company = company.CompanyInfo.CompanyName
	closed, err := s.OpenPeriod(ctx, paper.Proposal.Date)
	if err != nil {
		return Journal{}, observation, err
	}
	observation.ClosedThrough = closed
	for _, mapping := range []struct{ id, name, classification string }{{s.DebitAccount, paper.Proposal.DebitAccount, "Asset"}, {s.CreditAccount, paper.Proposal.CreditAccount, "Expense"}} {
		var result struct {
			Account struct {
				ID                                       string `json:"Id"`
				Name, FullyQualifiedName, Classification string
				Active                                   bool
				CurrencyRef                              Reference
				CurrentBalance                           json.Number
				SyncToken                                string
			}
		}
		if err := s.request(ctx, "GET", "/account/"+mapping.id, nil, &result); err != nil {
			return Journal{}, observation, err
		}
		account := result.Account
		if account.ID != mapping.id || !account.Active || account.Classification != mapping.classification || (account.Name != mapping.name && account.FullyQualifiedName != mapping.name) || (account.CurrencyRef.Value != "" && account.CurrencyRef.Value != "USD") {
			return Journal{}, observation, errors.New("QuickBooks account mapping, active status, classification or currency no longer matches the proposal")
		}
		if s.Production {
			if _, ok := new(big.Rat).SetString(string(account.CurrentBalance)); !ok || account.SyncToken == "" {
				return Journal{}, observation, errors.New("Current account balance or version is unavailable; production posting is blocked")
			}
		}
		saved := LedgerAccount{ID: account.ID, Name: account.Name, Balance: account.CurrentBalance, SyncToken: account.SyncToken}
		if mapping.id == s.DebitAccount {
			observation.Prepaid = saved
		} else {
			observation.Expense = saved
		}
	}
	p := paper.Proposal
	note := "Close sandbox test / "
	if s.Production {
		note = "Reviewed close / "
	}
	return Journal{TxnDate: p.Date, CurrencyRef: Reference{"USD"}, PrivateNote: note + strings.TrimSpace(paper.Input.SourceReference), Line: []Line{
		{json.Number(p.Amount), "JournalEntryLineDetail", Detail{"Debit", Reference{s.DebitAccount}}},
		{json.Number(p.Amount), "JournalEntryLineDetail", Detail{"Credit", Reference{s.CreditAccount}}},
	}}, observation, nil
}
func (s Sandbox) Create(ctx context.Context, journal Journal, requestID string) (string, error) {
	var result struct{ JournalEntry Journal }
	if err := s.request(ctx, "POST", "/journalentry?requestid="+url.QueryEscape(requestID), journal, &result); err != nil {
		return "", err
	}
	if !identifier.MatchString(result.JournalEntry.ID) {
		return "", errors.New("QuickBooks did not return a journal ID")
	}
	return result.JournalEntry.ID, nil
}
func (s Sandbox) Verify(ctx context.Context, id string, expected Journal) error {
	_, err := s.VerifyReadback(ctx, id, expected)
	return err
}
func (s Sandbox) VerifyReadback(ctx context.Context, id string, expected Journal) (Journal, error) {
	if !identifier.MatchString(id) {
		return Journal{}, errors.New("External journal ID is required for readback")
	}
	var result struct{ JournalEntry Journal }
	if err := s.request(ctx, "GET", "/journalentry/"+id, nil, &result); err != nil {
		return Journal{}, err
	}
	actual := result.JournalEntry
	if actual.ID != id || actual.TxnDate != expected.TxnDate || actual.CurrencyRef.Value != "USD" || actual.PrivateNote != expected.PrivateNote || len(actual.Line) != 2 {
		return Journal{}, errors.New("Journal readback does not match the approved draft")
	}
	used := map[int]bool{}
	for _, want := range expected.Line {
		found := false
		for i, got := range actual.Line {
			a, aok := new(big.Rat).SetString(string(got.Amount))
			b, bok := new(big.Rat).SetString(string(want.Amount))
			if !used[i] && aok && bok && a.Cmp(b) == 0 && got.DetailType == want.DetailType && got.JournalEntryLineDetail == want.JournalEntryLineDetail {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return Journal{}, errors.New("Journal line readback does not match the approved accounts and exact amounts")
		}
	}
	return actual, nil
}
