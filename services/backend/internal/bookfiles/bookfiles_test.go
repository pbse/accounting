package bookfiles

import (
	"archive/zip"
	"bytes"
	"math/big"
	"slices"
	"testing"
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
	if got["B4"].Formula != "A4*2" || got["B4"].Shared || got["B5"].Formula != "A4*2" || !got["B5"].Shared || got["B5"].Value != "0.2" || got["C5"].Formula != "[1]Sheet1!A1" {
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
		{"1.2", thousands, "1200", true},
		{"0.0001", point, "0.0001", false},
		{"−5.00", point, "-5", true},
	} {
		got, err := ParseAmount(c.text, c.style)
		if err != nil || got.Value.FloatString(4) != new(big.Rat).SetFrac(mustRat(c.want).Num(), mustRat(c.want).Denom()).FloatString(4) || got.Cents != c.cents {
			t.Fatalf("%q in %+v: %v %v %v", c.text, c.style, got.Value, got.Cents, err)
		}
	}
	for _, text := range []string{"", "n/a", "1.2.3", "12abc", "--"} {
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
