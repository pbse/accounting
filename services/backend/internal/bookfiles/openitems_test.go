package bookfiles

import (
	"fmt"
	"strings"
	"testing"
)

// An aged receivables export, laid out as QuickBooks and Xero lay theirs out, is read document by document.
func TestOpenItemsAreReadFromAnAgedExport(t *testing.T) {
	quickbooks := "A/R Aging Detail\nAs of January 31, 2027\n\n,Date,Transaction Type,Num,Customer,Due Date,Amount,Open Balance\nCurrent,,,,,,,\n" +
		",01/15/2027,Invoice,1001,Acme Co,02/14/2027,500.00,500.00\n,01/20/2027,Invoice,1002,Beta LLC,02/19/2027,\"1,200.00\",700.00\n" +
		"Total for Current,,,,,,\"$1,700.00\",\"$1,200.00\"\n1 - 30 days past due,,,,,,,\n,12/10/2026,Invoice,998,Acme Co,01/09/2027,300.00,300.00\n" +
		"Total for 1 - 30 days past due,,,,,,300.00,300.00\n,01/25/2027,Invoice,1003,Total Energy,02/24/2027,50.00,50.00\nTOTAL,,,,,,\"$2,000.00\",\"$1,500.00\"\nGrand Total,,,,,,\"$2,050.00\",\"$1,550.00\"\n"
	xero := "Aged Receivables Detail\nAs at 31 January 2027\n\nDate,Reference,Due Date,Current,< 1 Month,Total\nAcme Co,,,,,\n" +
		"15 Jan 2027,INV-1001,14 Feb 2027,500.00,,500.00\n10 Dec 2026,INV-998,9 Jan 2027,,300.00,300.00\nTotal Acme Co,,,,,800.00\nTotal,,,,,800.00\n"
	for _, c := range []struct {
		name, content, want string
	}{
		{"QuickBooks", quickbooks, "[{6 Acme Co 1001 2027-01-15 2027-02-14 500.00 } {7 Beta LLC 1002 2027-01-20 2027-02-19 700.00 } {10 Acme Co 998 2026-12-10 2027-01-09 300.00 } {12 Total Energy 1003 2027-01-25 2027-02-24 50.00 }]"},
		{"Xero", xero, "[{6 Acme Co INV-1001 2027-01-15 2027-02-14 500.00 } {7 Acme Co INV-998 2026-12-10 2027-01-09 300.00 }]"},
	} {
		f, err := Read(c.name+".csv", []byte(c.content))
		if err != nil {
			t.Fatal(err)
		}
		layout, ok := ProposeOpenItems(f)
		if !ok {
			t.Fatalf("%s: no layout proposed", c.name)
		}
		items, problems := ReadOpenItems(f, layout)
		if got := fmt.Sprint(items); got != c.want || len(problems) != 0 {
			t.Fatalf("%s: %v %s %v", c.name, layout, got, problems)
		}
	}
}

// A credit is a document apart from an invoice of the same number, and a document whose date cannot be read is
// reported, never taken for a summary.
func TestOpenItemsKeepCreditsAndUnreadableDocuments(t *testing.T) {
	invoice, credit := OpenItem{Party: "Acme Co", Document: "1001", Open: "100.00"}, OpenItem{Party: "Acme Co", Document: "1001", Open: "-100.00"}
	if invoice.Key("receivable") == credit.Key("receivable") {
		t.Fatal("an invoice and a credit of one number share a name")
	}
	f, err := Read("unnumbered.csv", []byte("Customer,Num,Date,Open Balance\nAcme Co,1001,01/02/2027,100.00\nBeta LLC,,03/04/2027?,50.00\nAcme Co,,Total 2027,150.00\n"))
	if err != nil {
		t.Fatal(err)
	}
	layout, _ := ProposeOpenItems(f)
	items, problems := ReadOpenItems(f, layout)
	// The numbered document is read (its ambiguous date named); the unnumbered one is reported as left out, never
	// counted; the summary is left aside.
	if len(items) != 1 || items[0].Document != "1001" || len(problems) != 2 || !strings.Contains(problems[1], "Row 3 has no document number") {
		t.Fatalf("an export's unnumbered rows: %v %v", items, problems)
	}
}

// A summary section above a detailed one is not taken for it; headings naming no number are still proposed, for a
// person to name the number's column.
func TestOpenItemsProposeTheDetailedHeadings(t *testing.T) {
	f, err := Read("sections.csv", []byte("Customer,Open Balance\nAcme Co,500.00\n\nDate,Reference,Customer,Open Balance\n15 Jan 2027,INV-1,Acme Co,500.00\n"))
	if err != nil {
		t.Fatal(err)
	}
	if layout, ok := ProposeOpenItems(f); !ok || layout.HeaderRow != 4 {
		t.Fatalf("the detailed headings: %v %v", layout, ok)
	}
	// A person may say the headings are elsewhere: their columns are proposed afresh.
	if layout, ok := ProposeOpenItemsAt(f, f.Tables[0].Name, 1); !ok || layout.HeaderRow != 1 || layout.Columns[0] != RoleParty || layout.Columns[1] != RoleOpen {
		t.Fatalf("the headings a person chose: %v %v", layout, ok)
	}
	f, _ = Read("unnamed.csv", []byte("Customer,Transaction ID,Open Balance\nAcme Co,T-1,500.00\n"))
	if layout, ok := ProposeOpenItems(f); !ok || layout.HeaderRow != 1 {
		t.Fatalf("headings naming no number: %v %v", layout, ok)
	}
}

// A column a person names the open amount is read in its own style; a file whose amounts none read is not one with
// none open.
func TestOpenItemsAmountsAreReadInTheirColumnsStyle(t *testing.T) {
	f, err := Read("saldo.csv", []byte("Kunde;Rechnung;Saldo\nAcme GmbH;R-1;12,50\nBeta AG;R-2;1.000,25\n"))
	if err != nil {
		t.Fatal(err)
	}
	layout, _ := ProposeOpenItemsAt(f, f.Tables[0].Name, 1)
	layout.Columns = map[int]Role{0: RoleParty, 1: RoleDocument, 2: RoleOpen}
	items, problems := ReadOpenItems(f, RestyleOpenItems(f, layout))
	if len(items) != 2 || items[1].Open != "1000.25" || len(problems) != 0 {
		t.Fatalf("amounts in the column's style: %v %v", items, problems)
	}
	layout.Style = NumberStyle{Scale: 1}
	if items, problems = ReadOpenItems(f, layout); items != nil || len(problems) == 0 {
		t.Fatalf("a file whose amounts none read: %v %v", items, problems)
	}
}
