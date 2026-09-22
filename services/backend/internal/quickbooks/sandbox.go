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
	// The production QuickBooks hostname is deliberately not configurable in this sandbox capability.
	req, err := http.NewRequestWithContext(ctx, method, "https://sandbox-quickbooks.api.intuit.com/v3/company/"+s.Realm+path, bytes.NewReader(data))
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
	if paper.Input.Method != prepaids.Method || paper.Input.CreditAmount != "" || paper.Input.BookedBalance != "" || paper.Input.CancellationMethod != "" {
		return Journal{}, errors.New("This accounting method supports draft review only; sandbox posting has not been enabled for it")
	}
	if !s.Ready() || paper.Proposal == nil || paper.Input.Entity != s.Entity {
		return Journal{}, errors.New("This draft must match the configured sandbox entity and have a positive prepaid proposal")
	}
	var company struct{ CompanyInfo struct{ CompanyName string } }
	if err := s.request(ctx, "GET", "/companyinfo/"+s.Realm, nil, &company); err != nil {
		return Journal{}, err
	}
	if company.CompanyInfo.CompanyName != s.Entity {
		return Journal{}, errors.New("Sandbox company name does not match the configured client")
	}
	for _, mapping := range []struct{ id, name, classification string }{{s.DebitAccount, paper.Proposal.DebitAccount, "Asset"}, {s.CreditAccount, paper.Proposal.CreditAccount, "Expense"}} {
		var result struct {
			Account struct {
				ID                                       string `json:"Id"`
				Name, FullyQualifiedName, Classification string
				Active                                   bool
				CurrencyRef                              Reference
			}
		}
		if err := s.request(ctx, "GET", "/account/"+mapping.id, nil, &result); err != nil {
			return Journal{}, err
		}
		account := result.Account
		if account.ID != mapping.id || !account.Active || account.Classification != mapping.classification || (account.Name != mapping.name && account.FullyQualifiedName != mapping.name) || (account.CurrencyRef.Value != "" && account.CurrencyRef.Value != "USD") {
			return Journal{}, errors.New("Sandbox account mapping, active status, classification or currency no longer matches the proposal")
		}
	}
	p := paper.Proposal
	return Journal{TxnDate: p.Date, CurrencyRef: Reference{"USD"}, PrivateNote: "Close sandbox test / " + strings.TrimSpace(paper.Input.SourceReference), Line: []Line{
		{json.Number(p.Amount), "JournalEntryLineDetail", Detail{"Debit", Reference{s.DebitAccount}}},
		{json.Number(p.Amount), "JournalEntryLineDetail", Detail{"Credit", Reference{s.CreditAccount}}},
	}}, nil
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
	if !identifier.MatchString(id) {
		return errors.New("External journal ID is required for readback")
	}
	var result struct{ JournalEntry Journal }
	if err := s.request(ctx, "GET", "/journalentry/"+id, nil, &result); err != nil {
		return err
	}
	actual := result.JournalEntry
	if actual.ID != id || actual.TxnDate != expected.TxnDate || actual.CurrencyRef.Value != "USD" || actual.PrivateNote != expected.PrivateNote || len(actual.Line) != 2 {
		return errors.New("Journal readback does not match the approved draft")
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
			return errors.New("Journal line readback does not match the approved accounts and exact amounts")
		}
	}
	return nil
}
