package bookfiles

import "testing"

// The reader proposes an account's class and type from what the file says of it, the narrowest words first, and says
// where the proposal came from.
func TestAnAccountsClassIsProposedFromWhatTheFileSays(t *testing.T) {
	for _, c := range []struct {
		typeText, heading, name, number string
		want                            Proposal
	}{
		// A type a ledger writes out is that type; a broader one is refined by a name of the same class.
		{"Accounts Receivable", "", "Trade debtors", "", Proposal{"Asset", "Accounts Receivable", "type", "Accounts Receivable"}},
		{"Current Asset", "", "Business Checking", "", Proposal{"Asset", "Bank", "type", "Current Asset"}},
		{"Direct Costs", "", "Materials", "", Proposal{"Expense", "Cost of Goods Sold", "type", "Direct Costs"}},
		{"Overhead", "", "Bank charges", "", Proposal{"Expense", "Expense", "type", "Overhead"}},
		// The heading the account sits under, refined by its name.
		{"", "Current Liabilities", "Amex Business Card", "", Proposal{"Liability", "Credit Card", "heading", "Current Liabilities"}},
		{"", "Income", "Consulting", "", Proposal{"Revenue", "Income", "heading", "Income"}},
		// The name alone, narrower words before broader.
		{"", "", "Bank service charges", "", Proposal{"Expense", "Expense", "name", ""}},
		{"", "", "Sales tax payable", "", Proposal{"Liability", "Other Current Liability", "name", ""}},
		{"", "", "Cost of sales", "", Proposal{"Expense", "Cost of Goods Sold", "name", ""}},
		{"", "", "Accumulated depreciation - equipment", "", Proposal{"Asset", "Fixed Asset", "name", ""}},
		{"", "", "Depreciation expense", "", Proposal{"Expense", "Expense", "name", ""}},
		{"", "", "Equipment rental", "", Proposal{"Expense", "Expense", "name", ""}},
		{"", "", "Sales Commissions", "", Proposal{"Expense", "Expense", "name", ""}},
		{"", "", "Loan Interest Expense", "", Proposal{"Expense", "Other Expense", "name", ""}},
		{"", "", "Bank Overdraft", "", Proposal{"Liability", "Other Current Liability", "name", ""}},
		{"", "", "Credit Card Processing Fees", "", Proposal{"Expense", "Expense", "name", ""}},
		{"", "", "Office equipment", "", Proposal{"Asset", "Fixed Asset", "name", ""}},
		{"", "", "Interest income", "", Proposal{"Revenue", "Other Income", "name", ""}},
		{"", "", "Rental income", "", Proposal{"Revenue", "Income", "name", ""}},
		{"", "", "Owner's Draw", "", Proposal{"Equity", "Equity", "name", ""}},
		{"", "", "Shareholder loan", "", Proposal{"Liability", "Long Term Liability", "name", ""}},
		{"", "", "Notes payable", "", Proposal{"Liability", "Long Term Liability", "name", ""}},
		{"", "", "Customer deposits", "", Proposal{"Liability", "Other Current Liability", "name", ""}},
		{"", "", "Security deposit", "", Proposal{"Asset", "Other Asset", "name", ""}},
		{"", "", "Prepaid insurance", "", Proposal{"Asset", "Other Current Asset", "name", ""}},
		{"", "", "Accrued expenses", "", Proposal{"Liability", "Other Current Liability", "name", ""}},
		{"", "", "Cash", "", Proposal{"Asset", "Bank", "name", ""}},
		// The common numbering, when nothing else says.
		{"", "", "Clearing", "1450", Proposal{"Asset", "Other Current Asset", "number", ""}},
		{"", "", "Misc", "5100", Proposal{"Expense", "Cost of Goods Sold", "number", ""}},
		{"", "", "Suspense", "0030", Proposal{}},
		{"", "Acme Co", "Suspense", "", Proposal{}},
	} {
		if got := Propose(c.typeText, c.heading, c.name, c.number); got != c.want {
			t.Errorf("Propose(%q, %q, %q, %q) = %+v, want %+v", c.typeText, c.heading, c.name, c.number, got, c.want)
		}
	}
	for class, types := range Types {
		for _, typ := range types {
			if !ValidType(class, typ) {
				t.Fatalf("%s is a %s type", typ, class)
			}
		}
	}
	if ValidType("Asset", "Credit Card") || ValidType("Nonsense", "Bank") {
		t.Fatal("a type belongs to its class")
	}
	if AccountKey(" 1 000 ", "Bank") != "n:1000" || AccountKey("", "  Business  Checking ") != "a:business checking" {
		t.Fatal("an account is known by its number, else its name")
	}
}
