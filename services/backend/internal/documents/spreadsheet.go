package documents

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxUpload is the most an uploaded file may hold: a larger one is refused as sent.
const MaxUpload = 5 * 1024 * 1024

// UploadContent is a file as the upload route keeps it: an Excel workbook (.xlsx) within MaxUpload becomes the CSV
// text of its first sheet, under its name (shortened to fit 200 characters) with .csv added; anything else is kept as
// sent, for the route's own checks to accept or refuse. The error is a workbook that cannot be read.
func UploadContent(name string, body []byte) (string, []byte, error) {
	if !strings.HasSuffix(strings.ToLower(name), ".xlsx") || len(body) > MaxUpload {
		return name, body, nil
	}
	text, err := SpreadsheetText(body)
	if err != nil {
		return name, body, err
	}
	for len(name)+4 > 200 {
		name = string([]rune(name)[:len([]rune(name))-1])
	}
	return name + ".csv", []byte(text), nil
}

// SpreadsheetText reads the first sheet of an Excel workbook (.xlsx) as CSV text: cell values as shown in their columns,
// shared and inline strings, and cells formatted as dates written as YYYY-MM-DD. Formulas give their cached values.
func SpreadsheetText(body []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", errors.New("the file is not an Excel workbook")
	}
	// What the archive declares, and what is actually unpacked across every part read, are each held to 64 MiB.
	files := map[string]*zip.File{}
	var declared uint64
	for _, f := range archive.File {
		files[f.Name] = f
		if f.UncompressedSize64 > 64<<20 || declared > 64<<20-f.UncompressedSize64 {
			return "", errors.New("the workbook is too large to read")
		}
		declared += f.UncompressedSize64
	}
	budget := int64(64 << 20)
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
			return errors.New("the workbook is too large to read")
		}
		return err
	}
	var workbook struct {
		Pr struct {
			Date1904 bool `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheet []struct {
			ID     string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
			Strict string `xml:"http://purl.oclc.org/ooxml/officeDocument/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err = read("xl/workbook.xml", &workbook); err != nil || len(workbook.Sheet) == 0 {
		return "", errors.New("the workbook lists no sheets")
	}
	if err = read("xl/_rels/workbook.xml.rels", &rels); err != nil {
		return "", errors.New("the workbook's sheets cannot be found")
	}
	sheet, first := "", workbook.Sheet[0].ID+workbook.Sheet[0].Strict
	for _, r := range rels.Rel {
		if target := strings.ReplaceAll(r.Target, "\\", "/"); r.ID == first {
			sheet = path.Join("xl", target)
			if strings.HasPrefix(target, "/") {
				sheet = strings.TrimPrefix(path.Clean(target), "/")
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
			return "", errors.New("the workbook's text cannot be read")
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
			return "", errors.New("the workbook's formats cannot be read")
		}
	}
	custom := map[int]string{}
	for _, f := range styles.Formats {
		custom[f.ID] = f.Code
	}
	dated := make([]bool, len(styles.Cells))
	for i, c := range styles.Cells {
		dated[i] = (c.Format >= 14 && c.Format <= 17) || c.Format == 22 || showsDate(custom[c.Format])
	}
	var data struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Type   string `xml:"t,attr"`
				Style  int    `xml:"s,attr"`
				Value  string `xml:"v"`
				Inline string `xml:"is>t"`
				Runs   []struct {
					T string `xml:"t"`
				} `xml:"is>r"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err = read(sheet, &data); err != nil {
		return "", errors.New("the first sheet cannot be read")
	}
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if workbook.Pr.Date1904 {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	var out boundedOutput
	w := csv.NewWriter(&out)
	for _, row := range data.Rows {
		record := []string{}
		for _, c := range row.Cells {
			column := columnIndex(c.Ref)
			if column < 0 {
				column = len(record)
			}
			if column >= 16384 {
				return "", errors.New("a cell is beyond the last column a sheet can have")
			}
			for len(record) < column {
				record = append(record, "")
			}
			value := c.Value
			switch c.Type {
			case "s":
				i, err := strconv.Atoi(c.Value)
				if err != nil || i < 0 || i >= len(strs) {
					return "", errors.New("a cell refers to text the workbook does not have")
				}
				value = strs[i]
			case "inlineStr":
				value = c.Inline
				for _, run := range c.Runs {
					value += run.T
				}
			case "b":
				value = map[string]string{"1": "TRUE", "0": "FALSE"}[c.Value]
			case "", "n":
				serial, err := strconv.ParseFloat(c.Value, 64)
				switch {
				case err == nil && c.Style >= 0 && c.Style < len(dated) && dated[c.Style]:
					value = epoch.AddDate(0, 0, int(math.Floor(serial+1e-9))).Format("2006-01-02")
				case err == nil && strings.ContainsAny(c.Value, "eE"):
					value = strconv.FormatFloat(serial, 'f', -1, 64)
				}
			}
			if column < len(record) {
				record[column] = value
			} else {
				record = append(record, value)
			}
		}
		if err = w.Write(record); err != nil {
			return "", err
		}
	}
	w.Flush()
	return out.String(), w.Error()
}

// formatNoise is what a number format shows without reading the number: literals, colours and conditions, escaped and
// padding or fill characters, and AM/PM markers.
var formatNoise = regexp.MustCompile(`(?i)"[^"]*"|\[[^\]]*\]|\\.|[_*].|am/pm|a/p`)

var formatTokens = regexp.MustCompile(`(?i)y+|m+|d+|h+|s+`)

// elapsed is an elapsed-time part, such as [h] in [h]:mm or [mm], read as an hour so neither it nor the minutes after it
// count as a month.
var elapsed = regexp.MustCompile(`(?i)\[(h+|m+|s+)\]`)

// showsDate says whether a number format shows a calendar date: a year or day, or a month (an m that is not the minutes
// after an hour or before seconds).
func showsDate(code string) bool {
	tokens := formatTokens.FindAllString(formatNoise.ReplaceAllString(elapsed.ReplaceAllString(code, "h"), ""), -1)
	for i, t := range tokens {
		switch strings.ToLower(t[:1]) {
		case "y", "d":
			return true
		case "m":
			if !(i > 0 && strings.EqualFold(tokens[i-1][:1], "h")) && !(i+1 < len(tokens) && strings.EqualFold(tokens[i+1][:1], "s")) {
				return true
			}
		}
	}
	return false
}

// columnIndex is a cell reference's zero-based column, such as 2 for C7 or $c$7, or -1 without one.
func columnIndex(ref string) int {
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
