package bookfiles

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/documents"
)

// ReadWorkbook reads an Excel workbook (.xlsx): every sheet, in the workbook's order, hidden ones marked; each sheet's
// hidden rows and columns marked; each cell's stored value at its stored precision (a cell formatted as a date as
// YYYY-MM-DD), and a formula's text beside the result it stored. It also says which other workbooks the formulas read
// (external links), and whether the workbook asks to be recalculated when opened, since either can leave a stored
// result stale.
func ReadWorkbook(body []byte) (File, error) {
	file := File{Format: "xlsx", Tables: []Table{}}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return file, errors.New("the file is not an Excel workbook")
	}
	// What the archive declares, and what is actually unpacked across every part read, are each held to 256 MiB.
	files := map[string]*zip.File{}
	var declared uint64
	for _, f := range archive.File {
		files[f.Name] = f
		if f.UncompressedSize64 > 256<<20 || declared > 256<<20-f.UncompressedSize64 {
			return file, errors.New("the workbook is too large to read; send it in parts")
		}
		declared += f.UncompressedSize64
	}
	budget := int64(256 << 20)
	read := func(name string, v any) error {
		f := files[name]
		if f == nil {
			return fmt.Errorf("the workbook has no %s", name)
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		limited := &io.LimitedReader{R: r, N: budget}
		err = xml.NewDecoder(limited).Decode(v)
		if budget = limited.N; budget <= 0 {
			return errors.New("the workbook is too large to read; send it in parts")
		}
		return err
	}
	var workbook struct {
		Pr struct {
			Date1904 bool `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Calc struct {
			FullCalcOnLoad bool `xml:"fullCalcOnLoad,attr"`
		} `xml:"calcPr"`
		Sheet []struct {
			Name   string `xml:"name,attr"`
			State  string `xml:"state,attr"`
			ID     string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
			Strict string `xml:"http://purl.oclc.org/ooxml/officeDocument/relationships id,attr"`
		} `xml:"sheets>sheet"`
		External []struct {
			ID     string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
			Strict string `xml:"http://purl.oclc.org/ooxml/officeDocument/relationships id,attr"`
		} `xml:"externalReferences>externalReference"`
	}
	type relationships struct {
		Rel []struct {
			ID         string `xml:"Id,attr"`
			Target     string `xml:"Target,attr"`
			TargetMode string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	var rels relationships
	if err = read("xl/workbook.xml", &workbook); err != nil || len(workbook.Sheet) == 0 {
		return file, errors.New("the workbook lists no sheets")
	}
	if err = read("xl/_rels/workbook.xml.rels", &rels); err != nil {
		return file, errors.New("the workbook's sheets cannot be found")
	}
	file.RecalculateOnOpen = workbook.Calc.FullCalcOnLoad
	target := func(id string) string {
		for _, r := range rels.Rel {
			if r.ID == id {
				t := strings.ReplaceAll(r.Target, "\\", "/")
				if strings.HasPrefix(t, "/") {
					return strings.TrimPrefix(path.Clean(t), "/")
				}
				return path.Join("xl", t)
			}
		}
		return ""
	}
	// Each external link names the workbook it reads in its own relationships.
	for _, e := range workbook.External {
		part := target(e.ID + e.Strict)
		var linked relationships
		if part == "" || read(path.Join(path.Dir(part), "_rels", path.Base(part)+".rels"), &linked) != nil {
			file.LinkedWorkbooks = append(file.LinkedWorkbooks, "an external workbook")
			continue
		}
		for _, r := range linked.Rel {
			if r.TargetMode == "External" || strings.Contains(r.Target, ".xls") {
				file.LinkedWorkbooks = append(file.LinkedWorkbooks, r.Target)
			}
		}
	}
	// Shared strings, rich text included, in order.
	var shared struct {
		Item []struct {
			T string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if files["xl/sharedStrings.xml"] != nil {
		if err = read("xl/sharedStrings.xml", &shared); err != nil {
			return file, errors.New("the workbook's text cannot be read")
		}
	}
	strs := make([]string, len(shared.Item))
	for i, item := range shared.Item {
		strs[i] = item.T
		for _, run := range item.R {
			strs[i] += run.T
		}
	}
	// Which cell styles show a date: the built-in date formats and custom formats made of date parts.
	var styles struct {
		Formats []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Cells []struct {
			Format int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	if files["xl/styles.xml"] != nil {
		if err = read("xl/styles.xml", &styles); err != nil {
			return file, errors.New("the workbook's formats cannot be read")
		}
	}
	custom := map[int]string{}
	for _, f := range styles.Formats {
		custom[f.ID] = f.Code
	}
	dated := make([]bool, len(styles.Cells))
	for i, c := range styles.Cells {
		dated[i] = (c.Format >= 14 && c.Format <= 17) || c.Format == 22 || documents.ShowsDate(custom[c.Format])
	}
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if workbook.Pr.Date1904 {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	for _, s := range workbook.Sheet {
		part := target(s.ID + s.Strict)
		if part == "" || files[part] == nil {
			return file, fmt.Errorf("the sheet %q cannot be found", s.Name)
		}
		var data struct {
			Cols []struct {
				Min    int  `xml:"min,attr"`
				Max    int  `xml:"max,attr"`
				Hidden bool `xml:"hidden,attr"`
			} `xml:"cols>col"`
			Rows []struct {
				Number int  `xml:"r,attr"`
				Hidden bool `xml:"hidden,attr"`
				Cells  []struct {
					Ref     string `xml:"r,attr"`
					Type    string `xml:"t,attr"`
					Style   int    `xml:"s,attr"`
					Value   string `xml:"v"`
					Formula *struct {
						Text string `xml:",chardata"`
						Type string `xml:"t,attr"`
						Si   string `xml:"si,attr"`
					} `xml:"f"`
					Inline string `xml:"is>t"`
					Runs   []struct {
						T string `xml:"t"`
					} `xml:"is>r"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if err = read(part, &data); err != nil {
			return file, fmt.Errorf("the sheet %q cannot be read", s.Name)
		}
		table := Table{Name: s.Name, Hidden: s.State == "hidden" || s.State == "veryHidden", Rows: []Row{}}
		hiddenColumns := map[int]bool{}
		for _, c := range data.Cols {
			if c.Hidden && c.Min >= 1 && c.Max >= c.Min && c.Max <= 16384 {
				for i := c.Min; i <= c.Max; i++ {
					hiddenColumns[i-1] = true
				}
			}
		}
		for column := range hiddenColumns {
			table.HiddenColumns = append(table.HiddenColumns, column)
		}
		sort.Ints(table.HiddenColumns)
		// A shared formula's text is stored once, on its first cell; the others name it by its index.
		sharedText := map[string]string{}
		previous := 0
		for _, r := range data.Rows {
			number := r.Number
			if number == 0 {
				number = previous + 1
			}
			previous = number
			row := Row{Number: number, Hidden: r.Hidden}
			next := 0
			for _, c := range r.Cells {
				column := columnOf(c.Ref)
				if column < 0 {
					column = next
				}
				if column >= 16384 {
					return file, errors.New("a cell is beyond the last column a sheet can have")
				}
				next = column + 1
				cell := Cell{Column: column, Ref: cellRef(column, number)}
				switch c.Type {
				case "s":
					i, err := strconv.Atoi(c.Value)
					if err != nil || i < 0 || i >= len(strs) {
						return file, errors.New("a cell refers to text the workbook does not have")
					}
					cell.Kind, cell.Value = Text, strs[i]
				case "inlineStr":
					cell.Kind, cell.Value = Text, c.Inline
					for _, run := range c.Runs {
						cell.Value += run.T
					}
				case "str":
					cell.Kind, cell.Value = Text, c.Value
				case "b":
					cell.Kind, cell.Value = Bool, map[string]string{"1": "TRUE", "0": "FALSE"}[c.Value]
				case "e":
					cell.Kind, cell.Value = Error, c.Value
				case "d":
					cell.Kind, cell.Value = Date, c.Value
				default:
					cell.Kind, cell.Value = Number, c.Value
					if c.Value != "" && c.Style >= 0 && c.Style < len(dated) && dated[c.Style] {
						if serial, err := strconv.ParseFloat(c.Value, 64); err == nil {
							cell.Kind, cell.Value = Date, epoch.AddDate(0, 0, int(math.Floor(serial+1e-9))).Format("2006-01-02")
						}
					}
				}
				if c.Formula != nil {
					text := strings.TrimSpace(c.Formula.Text)
					if c.Formula.Type == "shared" {
						if text != "" {
							sharedText[c.Formula.Si] = text
						} else {
							text, cell.Shared = sharedText[c.Formula.Si], true
						}
					}
					if text == "" {
						text = "?"
					}
					cell.Formula = text
					file.Formulas++
				}
				if cell.Value == "" && cell.Formula == "" {
					continue
				}
				row.Cells = append(row.Cells, cell)
			}
			if len(row.Cells) > 0 {
				table.Rows = append(table.Rows, row)
			}
		}
		file.Tables = append(file.Tables, table)
	}
	return file, nil
}
