package bookfiles

import (
	"archive/zip"
	"bytes"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"
)

// cells is a table's cells by reference, for checking.
func cells(t Table) map[string]Cell {
	out := map[string]Cell{}
	for _, r := range t.Rows {
		for _, c := range r.Cells {
			out[c.Ref] = c
		}
	}
	return out
}

// A CSV is read in the encoding and delimiter the file declares or that can be told without ambiguity; one that could
// be read another way says so, for a person to confirm, and is read as they confirm.
func TestCSVIsReadAsItWasWritten(t *testing.T) {
	file, err := Read("tb.csv", []byte("Account,Debit,Credit\nChecking,\"1,500.25\",\n\nSales,,800.25\n"))
	if err != nil || file.Encoding != "utf-8" || file.Delimiter != "comma" || len(file.Unsure) != 0 {
		t.Fatalf("a plain CSV: %+v %v", file, err)
	}
	got := cells(file.Tables[0])
	if got["R2C2"].Value != "1,500.25" || got["R4C3"].Value != "800.25" || got["R4C1"].Value != "Sales" || len(file.Tables[0].Rows) != 3 {
		t.Fatalf("each value as written, at its row and column; blank lines skipped: %+v", file.Tables[0])
	}
	// Semicolons with decimal commas, as a European export writes them.
	file, _ = Read("tb.csv", []byte("Konto;Soll;Haben\nBank;1.500,25;\nUmsatz;;800,25\n"))
	if file.Delimiter != "semicolon" || cells(file.Tables[0])["R2C2"].Value != "1.500,25" || len(file.Unsure) != 0 {
		t.Fatalf("semicolons: %+v", file)
	}
	// UTF-16 with its byte-order mark, tab-separated, as some ledgers export.
	utf16 := []byte{0xFF, 0xFE}
	for _, r := range "Account\tBalance\nCafé\t10.00\n" {
		utf16 = append(utf16, byte(r), byte(r>>8))
	}
	file, _ = Read("tb.txt", utf16)
	if file.Encoding != "utf-16le" || file.Delimiter != "tab" || cells(file.Tables[0])["R2C1"].Value != "Café" {
		t.Fatalf("UTF-16: %+v", file)
	}
	// Bytes that are not UTF-8 are read as Windows-1252, and the reading is marked unsure.
	file, _ = Read("tb.csv", []byte("Account,Balance\nCaf\xe9,10.00\n"))
	if file.Encoding != "windows-1252" || cells(file.Tables[0])["R2C1"].Value != "Café" || !slices.Contains(file.Unsure, "encoding") {
		t.Fatalf("Windows-1252: %+v", file)
	}
	// Commas and semicolons that both split every line the same way leave the delimiter to a person.
	file, _ = Read("tb.csv", []byte("a,b;c\nd,e;f\n"))
	if !slices.Contains(file.Unsure, "delimiter") {
		t.Fatalf("an ambiguous delimiter is marked unsure: %+v", file)
	}
	confirmed, err := ReadCSVAs("tb.csv", []byte("a,b;c\nd,e;f\n"), "utf-8", "semicolon")
	if err != nil || confirmed.Delimiter != "semicolon" || cells(confirmed.Tables[0])["R1C1"].Value != "a,b" || len(confirmed.Unsure) != 0 {
		t.Fatalf("read as confirmed: %+v %v", confirmed, err)
	}
	if _, err = Read("old.xls", []byte("\xD0\xCF\x11\xE0")); err == nil {
		t.Fatal("an older .xls workbook is asked to be saved as .xlsx")
	}
}

// workbook builds a minimal .xlsx from its parts.
func workbook(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	z.Close()
	return buf.Bytes()
}

// A workbook is read sheet by sheet, hidden ones marked; each cell with its stored value at its stored precision, a
// date as a date, and a formula's text beside its stored result; and the workbook says what its results depend on.
func TestAWorkbookIsReadWhole(t *testing.T) {
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	body := workbook(t, map[string]string{
		"xl/workbook.xml":                               `<workbook ` + main + `><calcPr fullCalcOnLoad="1"/><sheets><sheet name="Trial Balance" sheetId="1" r:id="rId1"/><sheet name="Workings" sheetId="2" state="hidden" r:id="rId2"/></sheets><externalReferences><externalReference r:id="rId3"/></externalReferences></workbook>`,
		"xl/_rels/workbook.xml.rels":                    `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Target="externalLinks/externalLink1.xml"/></Relationships>`,
		"xl/externalLinks/_rels/externalLink1.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="Schedules%20Sept.xlsx" TargetMode="External"/></Relationships>`,
		"xl/externalLinks/externalLink1.xml":            `<externalLink ` + main + `/>`,
		"xl/sharedStrings.xml":                          `<sst ` + main + `><si><t>Account</t></si><si><t>Balance</t></si><si><r><t>Check</t></r><r><t>ing</t></r></si></sst>`,
		"xl/styles.xml":                                 `<styleSheet ` + main + `><numFmts><numFmt numFmtId="164" formatCode="d-mmm-yy"/></numFmts><cellXfs><xf numFmtId="0"/><xf numFmtId="164"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml": `<worksheet ` + main + `><cols><col min="3" max="3" hidden="1"/></cols><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>1234.5678901234</v></c><c r="C2" s="1"><v>46295</v></c></row>
<row r="4" hidden="1"><c r="A4" t="inlineStr"><is><t>Suspense</t></is></c><c r="B4"><f t="shared" ref="B4:B5" si="0">A4*2</f><v>0.1</v></c></row>
<row r="5"><c r="B5"><f t="shared" si="0"/><v>0.2</v></c><c r="C5"><f>[1]Sheet1!A1</f><v>7</v></c></row>
</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet ` + main + `><sheetData><row r="1"><c r="A1" t="b"><v>1</v></c></row></sheetData></worksheet>`,
	})
	file, err := Read("tb.xlsx", body)
	if err != nil {
		t.Fatal(err)
	}
	if file.Format != "xlsx" || len(file.Tables) != 2 || file.Tables[0].Name != "Trial Balance" || file.Tables[0].Hidden || !file.Tables[1].Hidden {
		t.Fatalf("every sheet, the hidden one marked: %+v", file.Tables)
	}
	if !file.RecalculateOnOpen || len(file.LinkedWorkbooks) != 1 || file.LinkedWorkbooks[0] != "Schedules%20Sept.xlsx" || file.Formulas != 3 {
		t.Fatalf("what stored results depend on: %+v", file)
	}
	tb := file.Tables[0]
	got := cells(tb)
	if got["A2"].Value != "Checking" || got["B2"].Value != "1234.5678901234" || got["B2"].Kind != Number || got["C2"].Kind != Date || got["C2"].Value != "2026-09-30" {
		t.Fatalf("values at stored precision, dates as dates: %+v", got)
	}
	if !slices.Equal(tb.HiddenColumns, []int{2}) || tb.Rows[2].Number != 4 || !tb.Rows[2].Hidden || got["A4"].Value != "Suspense" {
		t.Fatalf("hidden rows and columns are read and marked: %+v", tb)
	}
	if got["B4"].Formula != "A4*2" || got["B4"].Shared || got["B5"].Formula != "A5*2" || !got["B5"].Shared || got["B5"].Value != "0.2" || got["C5"].Formula != "[1]Sheet1!A1" {
		t.Fatalf("formulas beside their stored results, shared ones named: %+v", got)
	}
	if cells(file.Tables[1])["A1"].Value != "TRUE" {
		t.Fatalf("booleans: %+v", file.Tables[1])
	}
}

// Amounts are read exactly in the style a person confirmed: every way of writing a negative, thousands separators,
// decimal commas, a stated scale; and an amount rounded at its source is told apart from one exact to the cent.
func TestAmountsAreReadExactly(t *testing.T) {
	point, comma, thousands := NumberStyle{Scale: 1}, NumberStyle{DecimalComma: true, Scale: 1}, NumberStyle{Scale: 1000}
	for _, c := range []struct {
		text  string
		style NumberStyle
		want  string
		cents bool
	}{
		{"1,234.56", point, "1234.56", true},
		{"(1,234.56)", point, "-1234.56", true},
		{"1,234.56-", point, "-1234.56", true},
		{"-$1,234.56", point, "-1234.56", true},
		{"$ (12.00)", point, "-12", true},
		{"1 234.56 Cr", point, "-1234.56", true},
		{"Dr 99", point, "99", true},
		{"1'234.50", point, "1234.5", true},
		{"1.234,56", comma, "1234.56", true},
		{"(1.234,56)", comma, "-1234.56", true},
		{"EUR 12,5", comma, "12.5", true},
		{"1.23456", thousands, "1234.56", true},
		{"1.2", thousands, "1200", false},
		{"1.20000", thousands, "1200", true},
		{"12,345,678.90", point, "12345678.9", true},
		{"1500", point, "1500", true},
		{".5", point, "0.5", true},
		{"0.0001", point, "0.0001", false},
		{"−5.00", point, "-5", true},
	} {
		got, err := ParseAmount(c.text, c.style)
		if err != nil || got.Value.FloatString(4) != new(big.Rat).SetFrac(mustRat(c.want).Num(), mustRat(c.want).Denom()).FloatString(4) || got.Cents != c.cents {
			t.Fatalf("%q in %+v: %v %v %v", c.text, c.style, got.Value, got.Cents, err)
		}
	}
	for _, text := range []string{"", "n/a", "1.2.3", "12abc", "--", "-", "1,2", "1,234,56", "12,34.00", "1.234,56"} {
		if _, err := ParseAmount(text, point); err == nil {
			t.Fatalf("%q is not an amount", text)
		}
	}
	if style, unsure := GuessNumberStyle([]string{"1.234,56", "12,00", "(3,50)"}); !style.DecimalComma || unsure {
		t.Fatalf("decimal commas are proposed: %+v %v", style, unsure)
	}
	if style, unsure := GuessNumberStyle([]string{"1,234.56", "12.00"}); style.DecimalComma || unsure {
		t.Fatalf("decimal points are proposed: %+v %v", style, unsure)
	}
	if _, unsure := GuessNumberStyle([]string{"1,234", "5,000"}); !unsure {
		t.Fatal("whole thousands could be either, and are left to a person")
	}
}

func mustRat(s string) *big.Rat {
	r, _ := new(big.Rat).SetString(s)
	return r
}

// A Trial Balance export is read with a proposed layout a person confirms: its header found, its columns' meanings
// proposed, its totals checked against what they total and left out, its sides compared to the cent.
func TestATrialBalanceIsReadWithItsLayout(t *testing.T) {
	// QuickBooks Online's export: a title block, an empty header over the accounts, and a TOTAL row of each side.
	qbo, _ := Read("tb.csv", []byte("Acme Co\nTrial Balance\nAs of September 30, 2026\n\n,Debit,Credit\nChecking,\"1,500.25\",\nOwner's Equity,,\"1,000.00\"\nSales,,800.25\nRent,300.00,\nTOTAL,\"$1,800.25\",\"$1,800.25\"\n"))
	layout, questions := ProposeLayout(qbo.Tables[0])
	if layout.HeaderRow != 5 || layout.Columns[0] != AccountName || layout.Columns[1] != Debit || layout.Columns[2] != Credit || layout.Style.DecimalComma || len(questions) == 0 {
		t.Fatalf("the header, the account column and the sides are proposed, and asked about: %+v %v", layout, questions)
	}
	reading := layout.Read(qbo.Tables[0])
	if !reading.Balanced || reading.Debits != "1800.25" || reading.Credits != "1800.25" || len(reading.Problems) != 0 {
		t.Fatalf("balanced, with the TOTAL row checked side by side: %+v", reading)
	}
	kinds := map[string]RowKind{}
	for _, l := range reading.Lines {
		kinds[l.Name] = l.Kind
	}
	if kinds["Checking"] != Posting || kinds["TOTAL"] != Total || reading.Lines[0].Amount != "1500.25" || reading.Lines[1].Amount != "-1000" ||
		reading.Lines[0].Ref != "R6C2" || reading.Lines[1].Ref != "R7C3" || reading.Lines[1].Cells[AccountName] != "R7C1" || reading.Lines[1].Cells[Credit] != "R7C3" {
		t.Fatalf("lines with their balances (debit minus credit), each at the cell it came from: %+v", reading.Lines)
	}
	// A ledger with sections, nested totals and a parent account that has its own balance, and a signed balance column
	// in which a credit is positive.
	nested, _ := Read("tb.csv", []byte("Code;Account;Balance\n;Assets;\n;Current Assets;\n1000;Bank;-1.500,00\n1100;Debtors;-250,00\n;Total Current Assets;-1.750,00\n1200;Equipment;-400,00\n1210;Equipment: depreciation;100,00\n;Total Equipment;-300,00\n;Total Assets;-2.050,00\n3000;Capital;2.050,00\n;Total;0,00\n"))
	layout, _ = ProposeLayout(nested.Tables[0])
	layout.BalanceSign = "credit"
	if layout.Columns[0] != AccountNumber || layout.Columns[1] != AccountName || layout.Columns[2] != Balance || !layout.Style.DecimalComma {
		t.Fatalf("a semicolon file with decimal commas: %+v", layout)
	}
	reading = layout.Read(nested.Tables[0])
	if !reading.Balanced || len(reading.Problems) != 0 || reading.Lines[2].Amount != "1500" || reading.Lines[2].Number != "1000" {
		t.Fatalf("nested totals, a parent with its own balance, credit-positive signs: %+v", reading)
	}
	// A subtotal written as two columns is checked side by side, and an account left blank is a zero balance, said so
	// when it has no number.
	sides, _ := Read("tb.csv", []byte("No,Account,Debit,Credit\n,Current Assets,,\n1000,Bank,100.00,\n1010,Petty cash,,\n,Total Current Assets,110.00,10.00\n,Suspense,,\n3000,Capital,,100.00\n"))
	layout, _ = ProposeLayout(sides.Tables[0])
	reading = layout.Read(sides.Tables[0])
	if len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "Total Current Assets") || len(reading.Notes) != 1 || !strings.Contains(reading.Notes[0], "Suspense") || reading.Lines[2].Kind != Posting || reading.Lines[2].Amount != "0" || reading.Lines[0].Kind != Heading {
		t.Fatalf("both sides of a subtotal, and blank accounts: %+v", reading)
	}
	layout.Headings = []int{6}
	if reading = layout.Read(sides.Tables[0]); len(reading.Notes) != 0 || reading.Lines[4].Kind != Heading {
		t.Fatalf("a row a person marked a heading: %+v", reading)
	}
	// What holds a reading: an amount that cannot be read, a total that does not match, sides that do not agree, and an
	// amount rounded at its source.
	broken, _ := Read("tb.csv", []byte("Account,Debit,Credit\nBank,100.00,\nSales,,90.00\nOther,n/a,\nTotal Bank,99.00,\nFees,0.005,\n"))
	layout, _ = ProposeLayout(broken.Tables[0])
	reading = layout.Read(broken.Tables[0])
	if reading.Balanced || len(reading.Problems) != 4 {
		t.Fatalf("every problem is named with its place: %+v", reading.Problems)
	}
}

// A copied formula's relative references move with it; fixed parts, text, sheet names and function names do not.
func TestSharedFormulasMoveWithTheirCells(t *testing.T) {
	for _, c := range []struct{ formula, want string }{
		{"A4*2", "B6*2"},
		{"$A$4+A$4+$A4", "$A$4+B$4+$A6"},
		{"SUM(A1:A3)+LOG10(B2)", "SUM(B3:B5)+LOG10(C4)"},
		{"'Sheet 1'!A1&\"A1\"", "'Sheet 1'!B3&\"A1\""},
		{"Sheet2!C3", "Sheet2!D5"},
	} {
		if got := shiftFormula(c.formula, 2, 1); got != c.want {
			t.Fatalf("%s moved two rows and a column: %s, not %s", c.formula, got, c.want)
		}
	}
	if got := shiftFormula("A1", -1, 0); got != "#REF!" {
		t.Fatalf("a reference moved off the sheet: %s", got)
	}
}

// A Trial Balance is read for its month-end balances: a layout without them reads nothing; a total is known by its
// name, not by its amount being a formula; totals in two columns add their columns as written; Dr and Cr markers say
// the side whatever column they sit in; and localized built-in date formats are dates.
func TestTheReaderTakesNothingForGranted(t *testing.T) {
	movement, _ := Read("tb.csv", []byte("Account,Opening,Movement\nBank,100,20\nCapital,-100,-20\n"))
	layout := Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountName, 1: Opening, 2: Movement}, Style: NumberStyle{Scale: 1}}
	if reading := layout.Read(movement.Tables[0]); len(reading.Lines) != 0 || len(reading.Problems) != 1 || reading.Balanced {
		t.Fatalf("no month-end balance column, nothing read: %+v", reading)
	}
	summed := Table{Name: "TB", Rows: []Row{
		{Number: 1, Cells: []Cell{{Column: 0, Ref: "A1", Kind: Text, Value: "Account"}, {Column: 1, Ref: "B1", Kind: Text, Value: "Balance"}}},
		{Number: 2, Cells: []Cell{{Column: 0, Ref: "A2", Kind: Text, Value: "Bank"}, {Column: 1, Ref: "B2", Kind: Number, Value: "150", Formula: "SUM(D2:D9)"}}},
		{Number: 3, Cells: []Cell{{Column: 0, Ref: "A3", Kind: Text, Value: "Capital"}, {Column: 1, Ref: "B3", Kind: Number, Value: "-150"}}},
	}}
	layout, _ = ProposeLayout(summed)
	if reading := layout.Read(summed); reading.Lines[0].Kind != Posting || !reading.Balanced || len(reading.Problems) != 0 {
		t.Fatalf("an account whose balance is a SUM formula is an account: %+v", reading)
	}
	both, _ := Read("tb.csv", []byte("Account,Debit,Credit\nBank,100.00,50.00\nCapital,,50.00\nTOTAL,100.00,100.00\n"))
	layout, _ = ProposeLayout(both.Tables[0])
	if reading := layout.Read(both.Tables[0]); len(reading.Problems) != 0 || !reading.Balanced {
		t.Fatalf("a total adds its columns as written: %+v", reading)
	}
	marked, _ := Read("tb.csv", []byte("Account,Debit,Credit\nBank,100.00,\nCapital,,100.00 Cr\n"))
	layout, _ = ProposeLayout(marked.Tables[0])
	if reading := layout.Read(marked.Tables[0]); !reading.Balanced || reading.Credits != "100" || reading.Lines[1].Amount != "-100" {
		t.Fatalf("a Cr marker in the credit column is one credit: %+v", reading)
	}
	signedBalance, _ := Read("tb.csv", []byte("Account,Balance\nBank,100.00 Dr\nCapital,100.00 Cr\n"))
	layout, _ = ProposeLayout(signedBalance.Tables[0])
	layout.BalanceSign = "credit"
	if reading := layout.Read(signedBalance.Tables[0]); !reading.Balanced || reading.Lines[0].Amount != "100" {
		t.Fatalf("a marked balance keeps its marked side whatever the column's sign: %+v", reading)
	}
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	file, err := Read("tb.xlsx", workbook(t, map[string]string{
		"xl/workbook.xml":            `<workbook ` + main + `><sheets><sheet name="TB" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/styles.xml":              `<styleSheet ` + main + `><cellXfs><xf numFmtId="0"/><xf numFmtId="30"/><xf numFmtId="57"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml":   `<worksheet ` + main + `><sheetData><row r="1"><c r="A1" s="1"><v>46295</v></c><c r="B1" s="2"><v>46295</v></c></row></sheetData></worksheet>`,
	}))
	if err != nil || cells(file.Tables[0])["A1"].Value != "2026-09-30" || cells(file.Tables[0])["B1"].Kind != Date {
		t.Fatalf("localized built-in date formats: %+v %v", file.Tables, err)
	}
}

// Amounts marked in another currency are held; a total combining sections covers each; UTF-16 without a byte-order
// mark is read so, for a person to confirm; and a shared formula written in lower case moves with its copy.
func TestTheReaderHoldsWhatDoesNotAddUp(t *testing.T) {
	marked, _ := Read("tb.csv", []byte("Account,Balance\nBank,$100.00\nCapital,-100.00 EUR\n"))
	layout, _ := ProposeLayout(marked.Tables[0])
	layout.Currency = "USD"
	if reading := layout.Read(marked.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "another currency than USD") {
		t.Fatalf("an amount in euros in a dollar Trial Balance: %+v", reading)
	}
	layout.Currency = "CAD"
	if reading := layout.Read(marked.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "R3C2") {
		t.Fatalf("a dollar sign can be Canadian; EUR cannot: %+v", reading)
	}
	if !MarkAgrees("USD $", "usd") || MarkAgrees("€", "USD") || !MarkAgrees("", "GBP") {
		t.Fatal("marks agree with the currencies they can stand for")
	}
	sections, _ := Read("tb.csv", []byte("Account,Balance\nBank,300.00\nTotal Assets,300.00\nLoan,-100.00\nTotal Liabilities,-100.00\nCapital,-200.00\nTotal Equity,-200.00\nTotal Liabilities & Equity,-300.00\n"))
	layout, _ = ProposeLayout(sections.Tables[0])
	if reading := layout.Read(sections.Tables[0]); !reading.Balanced || len(reading.Problems) != 0 {
		t.Fatalf("a total of liabilities and equity covers both: %+v", reading)
	}
	wrong, _ := Read("tb.csv", []byte("Account,Balance\nBank,300.00\nTotal Assets,300.00\nLoan,-100.00\nTotal Liabilities,-100.00\nCapital,-200.00\nTotal Equity,-200.00\nTotal Liabilities & Equity,-200.00\n"))
	if reading := layout.Read(wrong.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "Row 8") {
		t.Fatalf("a wrong combined total is caught: %+v", reading)
	}
	var utf16 []byte
	for _, r := range "Account,Balance\nBank,1.00\nCapital,-1.00\n" {
		utf16 = append(utf16, byte(r), 0)
	}
	file, err := Read("tb.csv", utf16)
	if err != nil || file.Encoding != "utf-16le" || cells(file.Tables[0])["R2C1"].Value != "Bank" || !slices.ContainsFunc(file.Unsure, func(s string) bool { return strings.Contains(s, "encoding") }) {
		t.Fatalf("UTF-16 without a byte-order mark: %+v %v", file, err)
	}
	if got := shiftFormula("a1*2+$b$1+sum(c1:c3)", 2, 1); got != "b3*2+$b$1+sum(d3:d5)" {
		t.Fatalf("lower-case references move and keep their case: %s", got)
	}
}

// A balance beside its debit and credit must agree with them; a total with no amount is a problem, not a heading; and
// "Total for Assets" totals the Assets section.
func TestTotalsAndBalancesAgree(t *testing.T) {
	three, _ := Read("tb.csv", []byte("Account,Debit,Credit,Balance\nBank,90.00,,100.00\nCapital,,100.00,-100.00\n"))
	layout := Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountName, 1: Debit, 2: Credit, 3: Balance}, Style: NumberStyle{Scale: 1}}
	if reading := layout.Read(three.Tables[0]); len(reading.Problems) == 0 || !strings.Contains(reading.Problems[0], "Row 2's balance (100) is not its debit less its credit (90)") {
		t.Fatalf("a balance that is not its debit less its credit: %+v", reading)
	}
	cut, _ := Read("tb.csv", []byte("Account,Balance\nBank,100.00\nTotal Assets,\nCapital,-100.00\n"))
	layout, _ = ProposeLayout(cut.Tables[0])
	if reading := layout.Read(cut.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "total with no amount") {
		t.Fatalf("a total with no amount: %+v", reading)
	}
	sections, _ := Read("tb.csv", []byte("Account,Balance\nAssets,\nCurrent Assets,\nBank,100.00\nTotal Current Assets,100.00\nVan,50.00\nTotal for Assets,150.00\nCapital,-150.00\n"))
	layout, _ = ProposeLayout(sections.Tables[0])
	if reading := layout.Read(sections.Tables[0]); len(reading.Problems) != 0 || !reading.Balanced {
		t.Fatalf("Total for Assets covers the Assets section: %+v", reading)
	}
}

// Totals by column use the columns as written even beside a balance column, a blank side of a total counts as nothing,
// and a workbook's number cells are read exactly as stored, exponent notation included, whatever the layout's style.
func TestColumnsAndStoredNumbers(t *testing.T) {
	gross, _ := Read("tb.csv", []byte("Account,Debit,Credit,Balance\nBank,150.00,50.00,100.00\nCapital,,100.00,-100.00\nTotal,150.00,150.00,0.00\n"))
	layout := Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountName, 1: Debit, 2: Credit, 3: Balance}, Style: NumberStyle{Scale: 1}}
	if reading := layout.Read(gross.Tables[0]); len(reading.Problems) != 0 || !reading.Balanced {
		t.Fatalf("a total of gross columns beside a balance: %+v", reading)
	}
	blank, _ := Read("tb.csv", []byte("Account,Debit,Credit\nBank,150.00,\nCapital,,50.00\nLoan,,100.00\nTotal,100.00,\n"))
	layout, _ = ProposeLayout(blank.Tables[0])
	if reading := layout.Read(blank.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "Row 5") {
		t.Fatalf("a total with a blank side is checked on both: %+v", reading)
	}
	sheet := Table{Name: "TB", Rows: []Row{
		{Number: 1, Cells: []Cell{{Column: 0, Ref: "A1", Kind: Text, Value: "Account"}, {Column: 1, Ref: "B1", Kind: Text, Value: "Balance"}}},
		{Number: 2, Cells: []Cell{{Column: 0, Ref: "A2", Kind: Text, Value: "Bank"}, {Column: 1, Ref: "B2", Kind: Number, Value: "1.23E+07"}}},
		{Number: 3, Cells: []Cell{{Column: 0, Ref: "A3", Kind: Text, Value: "Capital"}, {Column: 1, Ref: "B3", Kind: Number, Value: "-12300000.5"}}},
		{Number: 4, Cells: []Cell{{Column: 0, Ref: "A4", Kind: Text, Value: "Fees"}, {Column: 1, Ref: "B4", Kind: Number, Value: "0.5"}}},
	}}
	layout = Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountName, 1: Balance}, Style: NumberStyle{DecimalComma: true, Scale: 1}}
	if reading := layout.Read(sheet); len(reading.Problems) != 0 || reading.Lines[0].Amount != "12300000" || reading.Lines[1].Amount != "-12300000.5" {
		t.Fatalf("stored numbers are exact, in their own notation: %+v", reading)
	}
	if a, err := cellAmount(Cell{Kind: Number, Value: "1.23456E+00"}, NumberStyle{Scale: 1000}); err != nil || a.Value.Cmp(big.NewRat(123456, 100)) != 0 || !a.Cents {
		t.Fatalf("a stored number in thousands, to the cent: %+v %v", a, err)
	}
	if a, err := cellAmount(Cell{Kind: Number, Value: "1.5E-03"}, NumberStyle{Scale: 1000}); err != nil || a.Value.Cmp(big.NewRat(3, 2)) != 0 || a.Cents {
		t.Fatalf("a stored number in thousands too short to say each cent: %+v %v", a, err)
	}
}

// Nothing is dropped unseen: a row shifted outside the chosen columns is a problem until a person marks it ignored; two
// amounts on one side are a problem, not an overwrite; and a date-time keeps its time.
func TestNothingIsDroppedUnseen(t *testing.T) {
	shifted, _ := Read("tb.csv", []byte("Account,Balance\nBank,100.00\nCapital,-100.00\n,,Cash,50\nLoan,,-50\n"))
	layout, _ := ProposeLayout(shifted.Tables[0])
	reading := layout.Read(shifted.Tables[0])
	if len(reading.Problems) != 2 || !strings.Contains(reading.Problems[0], "Row 4") || !strings.Contains(reading.Problems[1], "Row 5 (Loan)") {
		t.Fatalf("rows shifted outside the columns: %+v", reading.Problems)
	}
	layout.Ignored = []int{4, 5}
	if reading = layout.Read(shifted.Tables[0]); len(reading.Problems) != 0 {
		t.Fatalf("rows a person marked ignored: %+v", reading.Problems)
	}
	twice, _ := Read("tb.csv", []byte("Account,Debit,Credit\nBank,100 Cr,50\nCapital,150.00,\n"))
	layout, _ = ProposeLayout(twice.Tables[0])
	if reading = layout.Read(twice.Tables[0]); len(reading.Problems) == 0 || !strings.Contains(reading.Problems[0], "more than one credit") {
		t.Fatalf("two credits on one row: %+v", reading)
	}
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	for serial, want := range map[float64]string{46295: "2026-09-30", 46295.5: "2026-09-30T12:00:00", 46295.25 + 1.0/86400000*7: "2026-09-30T06:00:00.007"} {
		if got, _ := serialDate(epoch, serial); got != want {
			t.Fatalf("%v: %s, not %s", serial, got, want)
		}
	}
}

// A repeated header is asked about, not read twice, and a layout naming two balance columns reads nothing; a row a
// person marked a total with no amount is a problem, not a zero.
func TestEachMeaningIsOneColumn(t *testing.T) {
	months, _ := Read("tb.csv", []byte("Account,Balance,Balance\nBank,100.00,90.00\nCapital,-100.00,-90.00\n"))
	layout, questions := ProposeLayout(months.Tables[0])
	if len(layout.Columns) != 2 || layout.Columns[1] != Balance || !strings.Contains(questions[0], "Columns B, C") {
		t.Fatalf("a repeated header: %+v %v", layout, questions)
	}
	layout.Columns[2] = Balance
	if reading := layout.Read(months.Tables[0]); len(reading.Lines) != 0 || len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "more than one column for: balance") {
		t.Fatalf("two balance columns: %+v", reading)
	}
	marked, _ := Read("tb.csv", []byte("Account,Balance\nBank,100.00\nCapital,-100.00\nAll accounts,\n"))
	layout, _ = ProposeLayout(marked.Tables[0])
	layout.Totals = []int{4}
	if reading := layout.Read(marked.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "Row 4 (All accounts) is a total with no amount") {
		t.Fatalf("a marked total with no amount: %+v", reading)
	}
}

// A total encloses the sections whose totals name a kind of what it totals, without a heading of its own; and a file
// that is not text is refused by its signature, not read as a CSV.
func TestEnclosingTotalsAndUnsupportedFiles(t *testing.T) {
	file, _ := Read("tb.csv", []byte("Account,Balance\nBank,100.00\nReceivables,50.00\nTotal Current Assets,150.00\nVan,30.00\nTotal Fixed Assets,30.00\nDeposit,20.00\nTotal Assets,200.00\nCapital,-200.00\n"))
	layout, _ := ProposeLayout(file.Tables[0])
	if reading := layout.Read(file.Tables[0]); len(reading.Problems) != 0 || !reading.Balanced {
		t.Fatalf("Total Assets encloses current and fixed assets: %+v", reading)
	}
	for name, body := range map[string]string{"statement.pdf": "%PDF-1.7\n", "scan.csv": "\x89PNG\r\n", "old.dat": "\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"} {
		if _, err := Read(name, []byte(body)); err == nil {
			t.Fatalf("%s is refused", name)
		}
	}
}

// A Trial Balance with codes alone writes its totals in the code column; an amount with a side marker and a sign is not
// read; and UTF-16 cut short is refused, not trimmed.
func TestCodesSignsAndCutText(t *testing.T) {
	codes, _ := Read("tb.csv", []byte("Code,Debit,Credit\n1000,100.00,\nTOTAL ASSETS,100.00,\n3000,,100.00\nTOTAL EQUITY,,100.00\n"))
	layout, _ := ProposeLayout(codes.Tables[0])
	layout.Columns = map[int]Meaning{0: AccountNumber, 1: Debit, 2: Credit}
	if reading := layout.Read(codes.Tables[0]); reading.Debits != "100" || reading.Credits != "100" || len(reading.Problems) != 0 || reading.Lines[1].Kind != Total {
		t.Fatalf("totals in the code column: %+v", reading)
	}
	for _, text := range []string{"-100 Cr", "(100) Cr", "Dr -5"} {
		if _, err := ParseAmount(text, NumberStyle{Scale: 1}); err == nil {
			t.Fatalf("%q is not read", text)
		}
	}
	whole := []byte{0xFF, 0xFE}
	for _, r := range "Account,Balance\nBank,100.5" {
		whole = append(whole, byte(r), 0)
	}
	if _, err := Read("tb.csv", whole[:len(whole)-1]); err == nil {
		t.Fatal("UTF-16 cut short is refused")
	}
	if _, err := ReadCSVAs("tb.csv", whole[2:len(whole)-1], "utf-16le", "comma"); err == nil {
		t.Fatal("UTF-16 cut short is refused when confirmed too")
	}
	if _, err := Read("tb.csv", whole); err != nil {
		t.Fatalf("whole UTF-16: %v", err)
	}
}

// A workbook forcing a full recalculation says so; a rounded comparative column holds only itself; and Indian grouping
// is read.
func TestRecalculationRoundingAndGrouping(t *testing.T) {
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	file, err := Read("tb.xlsx", workbook(t, map[string]string{
		"xl/workbook.xml":            `<workbook ` + main + `><sheets><sheet name="TB" sheetId="1" r:id="rId1"/></sheets><calcPr forceFullCalc="1"/></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet ` + main + `><sheetData><row r="1"><c r="A1"><v>1</v></c></row></sheetData></worksheet>`,
	}))
	if err != nil || !file.RecalculateOnOpen {
		t.Fatalf("forceFullCalc asks for recalculation: %+v %v", file, err)
	}
	layout := Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountName, 1: Opening, 2: Balance}, Style: NumberStyle{Scale: 1}}
	var reading Reading
	rounded2, _ := Read("tb.csv", []byte("Account,Opening,Balance\nBank,1.234,100.00\nCapital,-1.234,-100.00\n"))
	if reading = layout.Read(rounded2.Tables[0]); len(reading.Problems) != 0 || !reading.Lines[0].Cents || len(reading.Lines[0].Rounded) != 1 || reading.Lines[0].Rounded[0] != Opening {
		t.Fatalf("a rounded opening column holds only itself: %+v", reading)
	}
	for text, want := range map[string]string{"₹12,34,567.89": "1234567.89", "1,00,000": "100000", "12,345.67": "12345.67"} {
		a, err := ParseAmount(text, NumberStyle{Scale: 1})
		if err != nil || exact(a.Value) != want {
			t.Fatalf("%s: %v %v", text, a.Value, err)
		}
	}
	if _, err := ParseAmount("1,23,45", NumberStyle{Scale: 1}); err == nil {
		t.Fatal("wrong grouping is still refused")
	}
}

// A month-end balance missing beside other balances is a problem; amounts in a column the layout does not name are a
// problem until a person marks the column ignored; and the totals add each side as written.
func TestMissingBalancesStrayColumnsAndSides(t *testing.T) {
	missing, _ := Read("tb.csv", []byte("Number,Account,Opening,Balance\n1000,Bank,90.00,100.00\n1100,Petty cash,5.00,\n3000,Capital,-95.00,-100.00\n"))
	layout := Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountNumber, 1: AccountName, 2: Opening, 3: Balance}, Style: NumberStyle{Scale: 1}}
	if reading := layout.Read(missing.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "Row 3 (Petty cash) has other balances but no month-end balance") {
		t.Fatalf("a missing month-end balance: %+v", reading.Problems)
	}
	stray, _ := Read("tb.csv", []byte("Account,Balance,Prior,Memo\nBank,100.00,80.00,checked\nCapital,-100.00,-80.00,\n"))
	layout, _ = ProposeLayout(stray.Tables[0])
	layout.Columns = map[int]Meaning{0: AccountName, 1: Balance}
	if reading := layout.Read(stray.Tables[0]); len(reading.Problems) != 2 || !strings.Contains(reading.Problems[0], "Column C holds values in rows 2, 3") || !strings.Contains(reading.Problems[1], "Column D holds values in rows 2") {
		t.Fatalf("values beside the chosen columns: %+v", reading.Problems)
	}
	layout.IgnoredColumns = []int{2, 3}
	if reading := layout.Read(stray.Tables[0]); len(reading.Problems) != 0 {
		t.Fatalf("a column marked ignored: %+v", reading.Problems)
	}
	gross, _ := Read("tb.csv", []byte("Account,Debit,Credit\nBank,150.00,50.00\nCapital,,100.00\n"))
	layout, _ = ProposeLayout(gross.Tables[0])
	if reading := layout.Read(gross.Tables[0]); reading.Debits != "150" || reading.Credits != "150" || !reading.Balanced {
		t.Fatalf("each side as written: %+v", reading)
	}
}

// An open quote is refused, never left to swallow rows; two signs are not read; and the 1900 date system's early serials
// are the dates Excel shows.
func TestQuotesSignsAndEarlyDates(t *testing.T) {
	if _, err := Read("tb.csv", []byte("Account,Balance\n\"Bank,100\nCapital,-100\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("an open quote: %v", err)
	}
	if file, err := Read("tb.csv", []byte("Account,Memo,Balance\nBank,\"Main, checking\",100\nCapital,,-100\n")); err != nil || cells(file.Tables[0])["R2C2"].Value != "Main, checking" {
		t.Fatalf("a quoted field: %+v %v", file, err)
	}
	for _, text := range []string{"--100", "(100)-", "-(100)"} {
		if _, err := ParseAmount(text, NumberStyle{Scale: 1}); err == nil {
			t.Fatalf("%q is not read", text)
		}
	}
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	for serial, want := range map[float64]string{1: "1900-01-01", 59: "1900-02-28", 61: "1900-03-01", 46295: "2026-09-30"} {
		if got, ok := serialDate(epoch, serial); !ok || got != want {
			t.Fatalf("serial %v: %s, not %s", serial, got, want)
		}
	}
	if _, ok := serialDate(epoch, 60); ok {
		t.Fatal("serial 60 is no date")
	}
	if got, _ := serialDate(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC), 1); got != "1904-01-02" {
		t.Fatalf("the 1904 system: %s", got)
	}
}

// A sheet's name is not a reference to shift, and bytes Windows-1252 leaves undefined are refused, not replaced.
func TestSheetNamesAndUndefinedBytes(t *testing.T) {
	if got := shiftFormula("Q1!A1+'Q 2'!B2+SUM(Q1!C1:C3)", 2, 1); got != "Q1!B3+'Q 2'!C4+SUM(Q1!D3:D5)" {
		t.Fatalf("sheet names stay: %s", got)
	}
	body := []byte("Account,Balance\nCaf\xe9,100\nCapital,-100\n")
	if file, err := Read("tb.csv", body); err != nil || file.Encoding != "windows-1252" || cells(file.Tables[0])["R2C1"].Value != "Café" {
		t.Fatalf("Windows-1252: %+v %v", file, err)
	}
	undefined := []byte("Account,Balance\nBank\x81,100\nCapital,-100\n")
	if _, err := Read("tb.csv", undefined); err == nil {
		t.Fatal("an undefined byte is refused")
	}
	if _, err := ReadCSVAs("tb.csv", undefined, "windows-1252", "comma"); err == nil {
		t.Fatal("an undefined byte is refused when confirmed too")
	}
}

// An account named like a total is a total until a person marks it an account.
func TestAnAccountNamedLikeATotal(t *testing.T) {
	file, _ := Read("tb.csv", []byte("Account,Balance\nBank,100.00\nTotal Rewards,-40.00\nCapital,-60.00\n"))
	layout, _ := ProposeLayout(file.Tables[0])
	if reading := layout.Read(file.Tables[0]); len(reading.Problems) == 0 {
		t.Fatalf("read as a total first: %+v", reading)
	}
	layout.Postings = []int{3}
	if reading := layout.Read(file.Tables[0]); len(reading.Problems) != 0 || !reading.Balanced || reading.Lines[1].Kind != Posting {
		t.Fatalf("marked an account: %+v", reading)
	}
}

// An entity column beside the chosen ones is the person's to place or ignore; a comparative that cannot be read is left
// out with a note, holding nothing else.
func TestScopeColumnsAndUnreadComparatives(t *testing.T) {
	two, _ := Read("tb.csv", []byte("Entity,Account,Balance\nAcme US,Bank,100.00\nAcme UK,Bank,-100.00\n"))
	layout := Layout{HeaderRow: 1, Columns: map[int]Meaning{1: AccountName, 2: Balance}, Style: NumberStyle{Scale: 1}}
	if reading := layout.Read(two.Tables[0]); len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "Column A holds values") {
		t.Fatalf("two entities read as one: %+v", reading.Problems)
	}
	comparative, _ := Read("tb.csv", []byte("Account,Opening,Balance\nBank,N/A,100.00\nCapital,-90.00,-100.00\n"))
	layout = Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountName, 1: Opening, 2: Balance}, Style: NumberStyle{Scale: 1}}
	reading := layout.Read(comparative.Tables[0])
	if len(reading.Problems) != 0 || !reading.Balanced || len(reading.Notes) != 1 || len(reading.Lines[0].Unread) != 1 {
		t.Fatalf("an unread comparative holds nothing else: %+v", reading)
	}
}

// A number no amount has is refused before it is built.
func TestHugeNumbersAreRefused(t *testing.T) {
	for _, v := range []string{"1e1000000000", "1E-1000000000", strings.Repeat("9", 100)} {
		if _, err := cellAmount(Cell{Kind: Number, Value: v}, NumberStyle{Scale: 1}); err == nil {
			t.Fatalf("%s is refused", v[:min(len(v), 20)])
		}
	}
	if _, err := ParseAmount(strings.Repeat("9", 100), NumberStyle{Scale: 1}); err == nil {
		t.Fatal("a hundred digits are refused")
	}
	if a, err := cellAmount(Cell{Kind: Number, Value: "1.5E+09"}, NumberStyle{Scale: 1}); err != nil || exact(a.Value) != "1500000000" {
		t.Fatalf("an ordinary exponent: %v %v", a, err)
	}
}

// A file claiming UTF-8 that is not is refused; a table's column names are not cells to shift.
func TestDeclaredUTF8AndTableReferences(t *testing.T) {
	if _, err := Read("tb.csv", []byte("\xEF\xBB\xBFAccount,Balance\nBank\xff,100\n")); err == nil {
		t.Fatal("invalid UTF-8 behind a UTF-8 mark is refused")
	}
	if got := shiftFormula("SUM(Table1[Jan24])+A1+Table1[[#This Row],[Q1]]", 1, 1); got != "SUM(Table1[Jan24])+B2+Table1[[#This Row],[Q1]]" {
		t.Fatalf("table references stay: %s", got)
	}
}

// A confirmed UTF-16 byte order against the file's own mark is refused.
func TestAByteOrderAgainstTheMark(t *testing.T) {
	big := []byte{0xFE, 0xFF}
	for _, r := range "Account,Balance\n" {
		big = append(big, 0, byte(r))
	}
	if _, err := ReadCSVAs("tb.csv", big, "utf-16le", "comma"); err == nil {
		t.Fatal("little-endian against a big-endian mark is refused")
	}
	if _, err := ReadCSVAs("tb.csv", big, "utf-16be", "comma"); err != nil {
		t.Fatalf("the marked order reads: %v", err)
	}
}

// A negative amount in a side's column counts on the other side, said in a note.
func TestNegativeSides(t *testing.T) {
	file, _ := Read("tb.csv", []byte("Account,Debit,Credit\nCash,100.00,\nContra,-100.00,\n"))
	layout, _ := ProposeLayout(file.Tables[0])
	reading := layout.Read(file.Tables[0])
	if reading.Debits != "100" || reading.Credits != "100" || !reading.Balanced || len(reading.Notes) != 1 || reading.Lines[1].Amount != "-100" {
		t.Fatalf("a negative debit is a credit: %+v", reading)
	}
}

// An unlabeled total can close a section; a confirmed account named by a later total stays an account.
func TestSectionTotalsAndConfirmedParents(t *testing.T) {
	file, _ := Read("tb.csv", []byte("Account,Balance\nBank,100.00\nVan,50.00\nTotal,150.00\nLoan,-50.00\nCapital,-100.00\nTotal,-150.00\n"))
	layout, _ := ProposeLayout(file.Tables[0])
	if reading := layout.Read(file.Tables[0]); len(reading.Problems) != 0 || !reading.Balanced {
		t.Fatalf("section totals: %+v", reading.Problems)
	}
	parent, _ := Read("tb.csv", []byte("Number,Account,Balance\n1000,Bank,\n1010,Checking,100.00\n,Total Bank,100.00\n3000,Capital,-100.00\n"))
	layout = Layout{HeaderRow: 1, Columns: map[int]Meaning{0: AccountNumber, 1: AccountName, 2: Balance}, Style: NumberStyle{Scale: 1}, Postings: []int{2}}
	reading := layout.Read(parent.Tables[0])
	if reading.Lines[0].Kind != Posting || len(reading.Problems) != 0 {
		t.Fatalf("a confirmed parent account stays an account: %+v", reading)
	}
}

// Whole-column and whole-row references move with a copied formula; a row without its number takes its cells'.
func TestRangesAndUnnumberedRows(t *testing.T) {
	for formula, want := range map[string]string{"SUM(A:A)": "SUM(B:B)", "SUM($A:B)": "SUM($A:C)", "SUM(1:1)": "SUM(3:3)", "SUM(a:c)": "SUM(b:d)", "A1:B2": "B3:C4"} {
		if got := shiftFormula(formula, 2, 1); got != want {
			t.Fatalf("%s: %s, not %s", formula, got, want)
		}
	}
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	file, err := Read("tb.xlsx", workbook(t, map[string]string{
		"xl/workbook.xml":            `<workbook ` + main + `><sheets><sheet name="TB" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet ` + main + `><sheetData><row r="1"><c r="A1"><v>1</v></c></row><row><c r="A5"><v>5</v></c></row></sheetData></worksheet>`,
	}))
	if err != nil || file.Tables[0].Rows[1].Number != 5 || cells(file.Tables[0])["A5"].Value != "5" {
		t.Fatalf("a row numbered by its cells: %+v %v", file.Tables, err)
	}
}

// A total adds its columns as written, a negative amount included, while the reading counts it on its side.
func TestATotalOfSignedColumns(t *testing.T) {
	file, _ := Read("tb.csv", []byte("Account,Debit,Credit\nCash,100.00,\nContra,-10.00,\nCapital,,90.00\nTOTAL,90.00,90.00\n"))
	layout, _ := ProposeLayout(file.Tables[0])
	reading := layout.Read(file.Tables[0])
	if len(reading.Problems) != 0 || !reading.Balanced || reading.Debits != "100" || reading.Credits != "100" {
		t.Fatalf("signed columns and their total: %+v", reading)
	}
}
