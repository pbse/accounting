package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"slices"
	"strings"
)

type VendorCredit struct {
	ID           string       `json:"Id,omitempty"`
	DocNumber    string       `json:"DocNumber"`
	TxnDate      string       `json:"TxnDate"`
	VendorRef    Reference    `json:"VendorRef"`
	VendorName   string       `json:"-"`
	APAccountRef Reference    `json:"APAccountRef"`
	CurrencyRef  Reference    `json:"CurrencyRef"`
	PrivateNote  string       `json:"PrivateNote"`
	Line         []CreditLine `json:"Line"`
}

type CreditLine struct {
	Amount                        json.Number `json:"Amount"`
	DetailType                    string      `json:"DetailType"`
	AccountBasedExpenseLineDetail struct {
		AccountRef Reference `json:"AccountRef"`
	} `json:"AccountBasedExpenseLineDetail"`
}

func (s Sandbox) PrepareVendorCredit(ctx context.Context, vendorID, apID, prepaidID, prepaidName, date, amount, source string) (VendorCredit, error) {
	// The sandbox credits its configured prepaid account; production credits accounts its grant allows.
	mapped := prepaidID == s.DebitAccount
	if s.Production {
		mapped = slices.Contains(s.Accounts, apID) && slices.Contains(s.Accounts, prepaidID)
	}
	if !s.Ready() || !identifier.MatchString(vendorID) || !identifier.MatchString(apID) || !identifier.MatchString(prepaidID) || vendorID == apID || apID == prepaidID || !mapped {
		return VendorCredit{}, errors.New("An exact vendor and distinct A/P and prepaid accounts allowed for this company are required")
	}
	parsed, ok := new(big.Rat).SetString(amount)
	if !ok || parsed.Sign() <= 0 || len(source) < 3 || len(source) > 21 || strings.TrimSpace(source) != source || strings.ContainsAny(source, "'\\\r\n") {
		return VendorCredit{}, errors.New("A positive reviewed supplier credit with an exact 3–21 character QuickBooks reference is required")
	}
	var company struct{ CompanyInfo struct{ CompanyName string } }
	if err := s.request(ctx, "GET", "/companyinfo/"+s.Realm, nil, &company); err != nil {
		return VendorCredit{}, err
	}
	if company.CompanyInfo.CompanyName != s.companyName() {
		return VendorCredit{}, errors.New("QuickBooks company does not match the client")
	}
	if _, err := s.OpenPeriod(ctx, date); err != nil {
		return VendorCredit{}, err
	}
	var vendor struct {
		Vendor struct {
			ID          string `json:"Id"`
			Active      bool
			DisplayName string
		}
	}
	if err := s.request(ctx, "GET", "/vendor/"+vendorID, nil, &vendor); err != nil {
		return VendorCredit{}, err
	}
	if vendor.Vendor.ID != vendorID || !vendor.Vendor.Active || strings.TrimSpace(vendor.Vendor.DisplayName) == "" {
		return VendorCredit{}, errors.New("Vendor is missing or inactive")
	}
	for _, expected := range []struct{ id, kind, subtype, name string }{{apID, "Accounts Payable", "AccountsPayable", ""}, {prepaidID, "Other Current Asset", "PrepaidExpenses", prepaidName}} {
		var response struct {
			Account struct {
				ID                                                    string `json:"Id"`
				Name, FullyQualifiedName, AccountType, AccountSubType string
				Active                                                bool
				CurrencyRef                                           Reference
			}
		}
		if err := s.request(ctx, "GET", "/account/"+expected.id, nil, &response); err != nil {
			return VendorCredit{}, err
		}
		a := response.Account
		if a.ID != expected.id || !a.Active || a.AccountType != expected.kind || a.AccountSubType != expected.subtype || (expected.name != "" && a.Name != expected.name && a.FullyQualifiedName != expected.name) || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != s.currency()) {
			return VendorCredit{}, errors.New("Vendor credit account identity, type, active status, name or currency changed")
		}
	}
	query := "SELECT * FROM VendorCredit WHERE DocNumber = '" + source + "' MAXRESULTS 2"
	var existing struct {
		QueryResponse *struct {
			VendorCredit []struct {
				ID string `json:"Id"`
			}
		} `json:"QueryResponse"`
	}
	if err := s.request(ctx, "GET", "/query?query="+url.QueryEscape(query), nil, &existing); err != nil || existing.QueryResponse == nil {
		return VendorCredit{}, errors.New("Could not verify whether this supplier credit is already booked")
	}
	if len(existing.QueryResponse.VendorCredit) > 0 {
		return VendorCredit{}, errors.New("A QuickBooks VendorCredit already uses this reference; inspect the booked credit before preparing")
	}
	credit := VendorCredit{DocNumber: source, TxnDate: date, VendorRef: Reference{vendorID}, VendorName: vendor.Vendor.DisplayName, APAccountRef: Reference{apID}, CurrencyRef: Reference{s.currency()}, PrivateNote: "Reviewed supplier credit / " + source}
	line := CreditLine{Amount: json.Number(amount), DetailType: "AccountBasedExpenseLineDetail"}
	line.AccountBasedExpenseLineDetail.AccountRef = Reference{prepaidID}
	credit.Line = []CreditLine{line}
	return credit, nil
}

func (s Sandbox) CreateVendorCredit(ctx context.Context, credit VendorCredit, requestID string) (string, error) {
	var result struct{ VendorCredit VendorCredit }
	if err := s.request(ctx, "POST", "/vendorcredit?requestid="+url.QueryEscape(requestID), credit, &result); err != nil {
		return "", err
	}
	if !identifier.MatchString(result.VendorCredit.ID) {
		return "", errors.New("QuickBooks did not return a VendorCredit ID")
	}
	return result.VendorCredit.ID, nil
}

func (s Sandbox) VerifyVendorCredit(ctx context.Context, id string, expected VendorCredit) (VendorCredit, error) {
	if !identifier.MatchString(id) {
		return VendorCredit{}, errors.New("VendorCredit ID is required for readback")
	}
	var result struct{ VendorCredit VendorCredit }
	if err := s.request(ctx, "GET", "/vendorcredit/"+id, nil, &result); err != nil {
		return VendorCredit{}, err
	}
	got := result.VendorCredit
	if got.ID != id || got.DocNumber != expected.DocNumber || got.TxnDate != expected.TxnDate || got.VendorRef != expected.VendorRef || got.APAccountRef != expected.APAccountRef || got.CurrencyRef != expected.CurrencyRef || got.PrivateNote != expected.PrivateNote || len(got.Line) != 1 || got.Line[0].DetailType != expected.Line[0].DetailType || got.Line[0].AccountBasedExpenseLineDetail != expected.Line[0].AccountBasedExpenseLineDetail {
		return VendorCredit{}, errors.New("VendorCredit readback differs from the approved identity, accounts or date")
	}
	a, aOK := new(big.Rat).SetString(string(got.Line[0].Amount))
	b, bOK := new(big.Rat).SetString(string(expected.Line[0].Amount))
	if !aOK || !bOK || a.Cmp(b) != 0 {
		return VendorCredit{}, errors.New("VendorCredit readback amount differs from approval")
	}
	return got, nil
}
