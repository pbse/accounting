// Package bookfiles reads the files a client's books come in (docs/books-sources.md, Reading files): CSV in any
// delimiter and encoding the file declares or that can be told without ambiguity, and Excel workbooks with every sheet.
// It reads; it does not interpret. Each file becomes tables of cells, every cell with the location it came from (sheet
// and cell, or row and column) and its original text, hidden rows, columns and sheets marked as hidden, a stored number
// at its stored precision, and a formula's text beside its stored result. What the figures mean (which row heads the
// table, which column is a balance, how signs are written) is decided later, by a layout a person confirms.
package bookfiles

import (
	"errors"
	"strconv"
	"strings"
)

// MaxFile is the largest file read: a workbook or CSV of a month's ledger detail fits well within it.
const MaxFile = 50 << 20

// Kind is what a cell holds.
type Kind string

const (
	Number Kind = "number"
	Text   Kind = "text"
	Bool   Kind = "bool"
	Date   Kind = "date"
	Error  Kind = "error"
)

// Cell is one non-empty cell.
type Cell struct {
	// Column is zero-based; Ref names the cell as the file does (C7 in a workbook, R7C3 in a CSV).
	Column int    `json:"column"`
	Ref    string `json:"ref"`
	Kind   Kind   `json:"kind"`
	// Value is the cell's value as stored: a number at its stored precision, a date as YYYY-MM-DD, text as written.
	Value string `json:"value"`
	// Formula is the formula whose last calculation Value is, without the leading "="; Shared says it is shared from
	// another cell, whose text it repeats (Excel stores it once).
	Formula string `json:"formula,omitempty"`
	Shared  bool   `json:"shared,omitempty"`
}

// Row is one row with at least one cell.
type Row struct {
	// Number is the row's one-based number in its sheet or file.
	Number int    `json:"number"`
	Hidden bool   `json:"hidden,omitempty"`
	Cells  []Cell `json:"cells"`
}

// Table is a sheet of a workbook, or a CSV file.
type Table struct {
	Name          string `json:"name"`
	Hidden        bool   `json:"hidden,omitempty"`
	HiddenColumns []int  `json:"hidden_columns,omitempty"`
	Rows          []Row  `json:"rows"`
}

// File is what a file read gives: its tables, and for a workbook what its stored formula results depend on.
type File struct {
	Format string  `json:"format"` // csv or xlsx
	Tables []Table `json:"tables"`
	// Encoding and Delimiter are how a CSV was read; Unsure names either when the file could be read another way too,
	// for a person to confirm (ReadCSVAs reads it as they confirm).
	Encoding  string   `json:"encoding,omitempty"`
	Delimiter string   `json:"delimiter,omitempty"`
	Unsure    []string `json:"unsure,omitempty"`
	// LinkedWorkbooks are the other workbooks a workbook's formulas read (its external links), by the names it stores;
	// a formula result that depends on one is only as current as that workbook (docs/books-sources.md, Formulas).
	LinkedWorkbooks []string `json:"linked_workbooks,omitempty"`
	// RecalculateOnOpen says the workbook asks to be fully recalculated when opened, so its stored results may be stale.
	RecalculateOnOpen bool `json:"recalculate_on_open,omitempty"`
	// Formulas counts the cells whose value is a formula's stored result.
	Formulas int `json:"formulas,omitempty"`
}

// ErrFormat is a file that is neither CSV text nor an Excel workbook.
var ErrFormat = errors.New("the file is neither CSV text nor an Excel workbook (.xlsx)")

// Read reads a file by its name and content: an .xlsx workbook, or anything else as CSV text.
func Read(name string, body []byte) (File, error) {
	if len(body) > MaxFile {
		return File{}, errors.New("the file is larger than 50 MiB; send it in parts")
	}
	if strings.HasSuffix(strings.ToLower(name), ".xlsx") || (len(body) > 4 && string(body[:4]) == "PK\x03\x04") {
		return ReadWorkbook(body)
	}
	if strings.HasSuffix(strings.ToLower(name), ".xls") {
		return File{}, errors.New("the file is an older Excel workbook (.xls); save it as .xlsx or CSV and send it again")
	}
	return ReadCSV(name, body)
}

// cellRef is a workbook cell's reference for zero-based column and one-based row, such as C7.
func cellRef(column, row int) string {
	letters := ""
	for c := column + 1; c > 0; c = (c - 1) / 26 {
		letters = string(rune('A'+(c-1)%26)) + letters
	}
	return letters + strconv.Itoa(row)
}

// columnOf is a cell reference's zero-based column, such as 2 for C7 or $C$7, or -1 without one.
func columnOf(ref string) int {
	column, letters := 0, 0
	for _, r := range strings.ToUpper(strings.ReplaceAll(ref, "$", "")) {
		switch {
		case r >= 'A' && r <= 'Z':
			column, letters = min(column*26+int(r-'A')+1, 1<<20), letters+1
		case r >= '0' && r <= '9' && letters > 0:
			return column - 1
		default:
			return -1
		}
	}
	return -1
}

// rowOf is a cell reference's row number, or 0 without one.
func rowOf(ref string) int {
	digits := strings.TrimLeft(strings.ToUpper(strings.ReplaceAll(ref, "$", "")), "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 {
		return 0
	}
	return n
}
