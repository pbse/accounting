package documents

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestSpreadsheetTextReadsTheFirstSheetAsCSV(t *testing.T) {
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	workbook := func(sheet string) []byte {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for name, body := range map[string]string{
			"xl/workbook.xml":            `<workbook ` + main + ` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><workbookPr/><sheets><sheet name="Prepaids" sheetId="1" r:id="rId7"/><sheet name="Notes" sheetId="2" r:id="rId8"/></sheets></workbook>`,
			"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId8" Target="worksheets/sheet1.xml"/><Relationship Id="rId7" Target="worksheets/sheet2.xml"/></Relationships>`,
			"xl/sharedStrings.xml":       `<sst ` + main + `><si><t>Description</t></si><si><t>Insurance</t></si><si><r><t>Prepaid </t></r><r><t>insurance</t></r></si><si><t>Note, with comma</t></si></sst>`,
			"xl/styles.xml":              `<styleSheet ` + main + `><numFmts count="4"><numFmt numFmtId="164" formatCode="yyyy\-mm\-dd"/><numFmt numFmtId="165" formatCode="[h]:mm:ss"/><numFmt numFmtId="166" formatCode="h:mm AM/PM"/><numFmt numFmtId="167" formatCode="#,##0.00_m"/><numFmt numFmtId="168" formatCode="[h]:mm"/><numFmt numFmtId="169" formatCode="[mm]"/></numFmts><cellXfs count="8"><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="165"/><xf numFmtId="166"/><xf numFmtId="167"/><xf numFmtId="168"/><xf numFmtId="169"/></cellXfs></styleSheet>`,
			"xl/worksheets/sheet1.xml":   `<worksheet ` + main + `><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Not the first sheet</t></is></c></row></sheetData></worksheet>`,
			"xl/worksheets/sheet2.xml":   sheet,
		} {
			w, _ := z.Create(name)
			w.Write([]byte(body))
		}
		z.Close()
		return buf.Bytes()
	}
	sheet := `<worksheet ` + main + `><sheetData>
 <row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="inlineStr"><is><t>Start</t></is></c></row>
 <row r="2"><c r="A2" t="s"><v>1</v></c><c r="B2"><v>1200</v></c><c r="C2" s="1"><v>46023</v></c><c r="D2" s="2"><v>46387.75</v></c><c r="E2" t="s"><v>2</v></c><c r="F2" t="s"><v>3</v></c></row>
 <row r="3"><c r="$a$3" t="inlineStr"><is><r><t>Rich </t></r><r><t>inline</t></r></is></c><c r="b3"><v>1.2E+3</v></c><c r="C3" s="3"><v>0.5</v></c><c r="D3" s="4"><v>0.5</v></c><c r="E3" s="5"><v>1200</v></c><c r="F3" s="6"><v>0.5</v></c><c r="G3" s="7"><v>0.5</v></c></row>
</sheetData></worksheet>`
	got, err := SpreadsheetText(workbook(sheet))
	if want := "Description,,Start\nInsurance,1200,2026-01-01,2026-12-31,Prepaid insurance,\"Note, with comma\"\nRich inline,1200,0.5,0.5,1200,0.5,0.5\n"; err != nil || got != want {
		t.Fatalf("the first sheet in workbook order is CSV with columns kept, rich text joined and dates written out: %q %v", got, err)
	}
	if _, err = SpreadsheetText(workbook(strings.Replace(sheet, `<v>3</v>`, `<v>9</v>`, 1))); err == nil {
		t.Fatal("a cell naming text the workbook does not have was read")
	}
	if _, err = SpreadsheetText(workbook(strings.Replace(sheet, `r="F2"`, `r="XFE2"`, 1))); err == nil {
		t.Fatal("a cell beyond the last column was read")
	}
	if _, err = SpreadsheetText([]byte("not a workbook")); err == nil {
		t.Fatal("a file that is not a workbook was read")
	}
}

// A file is kept as the upload route keeps it: a workbook within the limit as its first sheet's CSV under a name that
// fits, anything else (a larger workbook included) as sent for the route's own checks.
func TestUploadContentKeepsWhatTheRouteKeeps(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row><c r="A1" t="inlineStr"><is><t>Insurance</t></is></c><c r="B1"><v>1200</v></c></row></sheetData></worksheet>`,
	} {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	book := buf.Bytes()
	if name, body, err := UploadContent("Schedule.XLSX", book); err != nil || name != "Schedule.XLSX.csv" || string(body) != "Insurance,1200\n" {
		t.Fatalf("a workbook: %q %q %v", name, body, err)
	}
	long := strings.Repeat("é", 210) + ".xlsx"
	if name, _, err := UploadContent(long, book); err != nil || len(name) > 200 || !strings.HasSuffix(name, ".csv") {
		t.Fatalf("a long name is shortened to fit with .csv: %d %v", len(name), err)
	}
	if name, body, err := UploadContent("notes.txt", []byte("Hello")); err != nil || name != "notes.txt" || string(body) != "Hello" {
		t.Fatalf("text is kept as sent: %q %q %v", name, body, err)
	}
	large := append(append([]byte{}, book...), make([]byte, MaxUpload)...)
	if name, body, err := UploadContent("large.xlsx", large); err != nil || name != "large.xlsx" || len(body) != len(large) {
		t.Fatalf("a workbook over the limit is kept as sent, for the route to refuse: %q %d %v", name, len(body), err)
	}
	if _, _, err := UploadContent("broken.xlsx", []byte("not a workbook")); err == nil {
		t.Fatal("an unreadable workbook is an error")
	}
}
