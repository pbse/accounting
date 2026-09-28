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
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
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
	// Accounts are the account IDs a production grant allows; sandbox posting uses the debit/credit pair.
	Accounts   []string
	Client     *http.Client
	Production bool
	// Currency is the client's home currency; empty means USD.
	Currency string
}

func (s Sandbox) currency() string {
	if s.Currency == "" {
		return "USD"
	}
	return s.Currency
}

type LedgerAccount struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Classification string      `json:"classification"`
	Balance        json.Number `json:"balance"`
	SyncToken      string      `json:"sync_token"`
}
type LedgerObservation struct {
	Company       string          `json:"company"`
	Accounts      []LedgerAccount `json:"accounts"`
	ClosedThrough string          `json:"closed_through,omitempty"`
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

func (s Sandbox) allowed() []string {
	if len(s.Accounts) > 0 {
		return s.Accounts
	}
	return []string{s.DebitAccount, s.CreditAccount}
}

func (s Sandbox) Ready() bool {
	ids := s.allowed()
	for i, id := range ids {
		if !identifier.MatchString(id) || slices.Contains(ids[:i], id) {
			return false
		}
	}
	return identifier.MatchString(s.Realm) && s.Token != "" && s.Entity != "" && len(ids) >= 2
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
	// A throttled read is asked again; a write never is.
	response, err := ledger.DoThrottled(client, req)
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

// PrepareObserved builds the full-month prepaid journal: debit an Asset, credit an Expense.
func (s Sandbox) PrepareObserved(ctx context.Context, paper prepaids.Workpaper) (Journal, LedgerObservation, error) {
	if paper.Input.Method != prepaids.Method || paper.Input.Schedule != "" || paper.Input.CreditAmount != "" || paper.Input.BookedBalance != "" || paper.Input.CancellationMethod != "" {
		if !s.Production {
			return Journal{}, LedgerObservation{}, errors.New("This accounting method supports draft review only; sandbox posting has not been enabled for it")
		}
		return Journal{}, LedgerObservation{}, errors.New("Only the initial full-month prepaid method is enabled for posting")
	}
	if paper.Proposal == nil || paper.Input.Entity != s.Entity {
		return Journal{}, LedgerObservation{}, errors.New("This draft must match the confirmed company and have a positive prepaid proposal")
	}
	journal, observation, err := s.PrepareEntries(ctx, []prepaids.Adjustment{*paper.Proposal}, paper.Input.SourceReference)
	if err != nil {
		return journal, observation, err
	}
	for _, line := range journal.Line {
		want := map[string]string{"Debit": "Asset", "Credit": "Expense"}[line.JournalEntryLineDetail.PostingType]
		for _, account := range observation.Accounts {
			if account.ID == line.JournalEntryLineDetail.AccountRef.Value && account.Classification != want {
				return Journal{}, observation, errors.New("QuickBooks account mapping, active status, classification or currency no longer matches the proposal")
			}
		}
	}
	return journal, observation, nil
}

type account struct {
	LedgerAccount
	fullName string
}

func (s Sandbox) readAccount(ctx context.Context, id string) (account, error) {
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
	if err := s.request(ctx, "GET", "/account/"+id, nil, &result); err != nil {
		return account{}, err
	}
	a := result.Account
	if a.ID != id || !a.Active || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != s.currency()) {
		return account{}, errors.New("QuickBooks account mapping, active status, classification or currency no longer matches the proposal")
	}
	if s.Production {
		if _, ok := new(big.Rat).SetString(string(a.CurrentBalance)); !ok || a.SyncToken == "" {
			return account{}, errors.New("Current account balance or version is unavailable; production posting is blocked")
		}
	}
	return account{LedgerAccount{ID: a.ID, Name: a.Name, Classification: a.Classification, Balance: a.CurrentBalance, SyncToken: a.SyncToken}, a.FullyQualifiedName}, nil
}

// AccountNamed returns the one allowed account with this name or full name.
func (s Sandbox) AccountNamed(ctx context.Context, name string) (string, error) {
	var found []string
	for _, id := range s.allowed() {
		a, err := s.readAccount(ctx, id)
		if err != nil {
			return "", err
		}
		if name != "" && (a.Name == name || a.fullName == name) {
			found = append(found, a.ID)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("Account %q must match exactly one account allowed by this client's production grant", name)
	}
	return found[0], nil
}

func (s Sandbox) company(ctx context.Context) error {
	var company struct{ CompanyInfo struct{ CompanyName string } }
	if err := s.request(ctx, "GET", "/companyinfo/"+s.Realm, nil, &company); err != nil {
		return err
	}
	if company.CompanyInfo.CompanyName != s.Entity {
		return errors.New("QuickBooks company name does not match the configured client")
	}
	return nil
}

// PrepareEntries builds one journal for same-date approved entries. Each account name must match exactly
// one allowed account; the observation holds the touched accounts' versioned balances.
func (s Sandbox) PrepareEntries(ctx context.Context, entries []prepaids.Adjustment, reference string) (Journal, LedgerObservation, error) {
	observation := LedgerObservation{Company: s.Entity, Accounts: []LedgerAccount{}}
	if !s.Ready() || len(entries) == 0 {
		return Journal{}, observation, errors.New("QuickBooks is not configured or there is nothing to post")
	}
	if err := s.company(ctx); err != nil {
		return Journal{}, observation, err
	}
	for _, entry := range entries[1:] {
		if entry.Date != entries[0].Date {
			return Journal{}, observation, errors.New("Entries posted together must share one date")
		}
	}
	closed, err := s.OpenPeriod(ctx, entries[0].Date)
	if err != nil {
		return Journal{}, observation, err
	}
	observation.ClosedThrough = closed
	byName := map[string][]account{}
	for _, id := range s.allowed() {
		a, err := s.readAccount(ctx, id)
		if err != nil {
			return Journal{}, observation, err
		}
		for _, name := range []string{a.Name, a.fullName} {
			if name != "" && !slices.ContainsFunc(byName[name], func(x account) bool { return x.ID == a.ID }) {
				byName[name] = append(byName[name], a)
			}
		}
	}
	touched := map[string]LedgerAccount{}
	resolve := func(name string) (string, error) {
		found := byName[name]
		if len(found) != 1 {
			return "", fmt.Errorf("Account %q must match exactly one account allowed by this client's production grant", name)
		}
		touched[found[0].ID] = found[0].LedgerAccount
		return found[0].ID, nil
	}
	note := "Close sandbox test / "
	if s.Production {
		note = "Reviewed close / "
	}
	journal := Journal{TxnDate: entries[0].Date, CurrencyRef: Reference{s.currency()}, PrivateNote: note + strings.TrimSpace(reference)}
	for _, entry := range entries {
		debit, err := resolve(entry.DebitAccount)
		if err != nil {
			return Journal{}, observation, err
		}
		credit, err := resolve(entry.CreditAccount)
		if err != nil {
			return Journal{}, observation, err
		}
		if debit == credit {
			return Journal{}, observation, errors.New("An entry's debit and credit accounts must differ")
		}
		journal.Line = append(journal.Line, Line{json.Number(entry.Amount), "JournalEntryLineDetail", Detail{"Debit", Reference{debit}}}, Line{json.Number(entry.Amount), "JournalEntryLineDetail", Detail{"Credit", Reference{credit}}})
	}
	for _, a := range touched {
		observation.Accounts = append(observation.Accounts, a)
	}
	slices.SortFunc(observation.Accounts, func(a, b LedgerAccount) int { return strings.Compare(a.ID, b.ID) })
	return journal, observation, nil
}

// ObserveAfterWrite rereads the observed accounts for readback; period eligibility only gates new writes.
func (s Sandbox) ObserveAfterWrite(ctx context.Context, before LedgerObservation) (LedgerObservation, error) {
	after := LedgerObservation{Company: s.Entity, Accounts: []LedgerAccount{}}
	if err := s.company(ctx); err != nil {
		return after, err
	}
	for _, previous := range before.Accounts {
		a, err := s.readAccount(ctx, previous.ID)
		if err != nil {
			return after, err
		}
		after.Accounts = append(after.Accounts, a.LedgerAccount)
	}
	return after, nil
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
	if actual.ID != id || actual.TxnDate != expected.TxnDate || actual.CurrencyRef != expected.CurrencyRef || actual.PrivateNote != expected.PrivateNote || len(actual.Line) != len(expected.Line) {
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
