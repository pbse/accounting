package preparation

import (
	"encoding/json"
	"strconv"
	"strings"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/prepaids"
)

type supplierCredit struct {
	Number, Vendor, Currency string
	Amount                   int64
}

// otherSupplierCredits identifies a credit of another supplier in the same current company capture.
// It does not select a credit treatment or establish a balance for the invoice being prepared.
func otherSupplierCredits(ledger *Ledger, vendor, number, currency, period string) map[string]supplierCredit {
	out := map[string]supplierCredit{}
	if ledger == nil || ledger.Realm == "" || ledger.Realm != ledger.CaptureRealm || ledger.Realm != ledger.ChartRealm || currency == "" || number == "" {
		return out
	}
	type record struct {
		Credit                            bool
		Id, DocNumber                     string
		TotalAmt                          json.Number
		VendorRef, EntityRef, CurrencyRef accountRef
		LinkedTxn                         []json.RawMessage
		Line                              []struct{ LinkedTxn []json.RawMessage }
	}
	validID := func(value string) bool {
		n, err := strconv.ParseUint(value, 10, 64)
		return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
	}
	ownID := ""
	matches := 0
	for _, row := range ledger.Rows {
		if row.Kind != "Bill" && row.Kind != "Purchase" || !strings.HasPrefix(row.Date, period+"-") || row.Currency != currency {
			continue
		}
		var r record
		if json.Unmarshal(row.Record, &r) != nil || r.Credit || !strings.EqualFold(strings.TrimSpace(r.DocNumber), strings.TrimSpace(number)) {
			continue
		}
		party := r.VendorRef
		if row.Kind == "Purchase" && party.Value == "" {
			if !strings.EqualFold(r.EntityRef.Type, "Vendor") {
				continue
			}
			party = r.EntityRef
		}
		if !sameName(party.Name, vendor) || !validID(party.Value) {
			continue
		}
		ownID = party.Value
		matches++
	}
	if matches != 1 {
		return out
	}
	for _, row := range ledger.Rows {
		if row.Kind != "VendorCredit" || !strings.HasPrefix(row.Date, period+"-") || row.Currency != currency || row.ProviderID == "" {
			continue
		}
		var r record
		if json.Unmarshal(row.Record, &r) != nil || r.Id != "" && r.Id != row.ProviderID || r.DocNumber == "" || !validID(r.VendorRef.Value) || r.VendorRef.Value == ownID || r.VendorRef.Name == "" || sameName(r.VendorRef.Name, vendor) || len(r.LinkedTxn) > 0 || r.CurrencyRef.Value != "" && r.CurrencyRef.Value != currency {
			continue
		}
		linked := false
		for _, line := range r.Line {
			linked = linked || len(line.LinkedTxn) > 0
		}
		if linked || wholeWord(strings.ToLower(number)).MatchString(strings.ToLower(string(row.Record))) || wholeWord(documents.NormalizeEntity(vendor)).MatchString(documents.NormalizeEntity(string(row.Record))) {
			continue
		}
		amount, err := prepaids.Amount(r.TotalAmt.String())
		if err != nil || amount <= 0 {
			continue
		}
		out[row.ProviderID] = supplierCredit{r.DocNumber, r.VendorRef.Name, currency, amount}
	}
	return out
}

func (credit supplierCredit) ownsDocument(text, billedTo string) bool {
	if billedTo == "" {
		return false
	}
	// The vendor must occur as its letterhead or an issuer label, never only in a footer.
	issuer := false
	header := false
	for _, line := range strings.Split(text, "\n") {
		issuer = issuer || issuerLine(line, credit.Vendor, text)
		header = header || lineSays(line, credit.Number, []string{"credit"}, []string{"original", "invoice", "prior", "reference", "applies"})
	}
	names := billToNames(text)
	return issuer && header && len(names) == 1 && names[documents.NormalizeEntity(billedTo)] && currencyBefore(text, credit.Currency, credit.Amount)
}
