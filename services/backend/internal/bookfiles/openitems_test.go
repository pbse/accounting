package bookfiles

import (
	"fmt"
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
