package bookfiles

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// delimiters are the separators CSV exports use.
var delimiters = []rune{',', ';', '\t', '|'}

// ReadCSV reads CSV text, telling its encoding and delimiter from the file itself. An encoding the file declares (a
// byte-order mark), or valid UTF-8, is read as such. Other bytes are read as Windows-1252, the code page Windows
// accounting software writes, and the file says so in Unsure, for a person to confirm: it is the likeliest reading,
// not a certain one. So is a delimiter two candidates would both split the file by. ReadCSVAs reads with a confirmed
// encoding and delimiter.
func ReadCSV(name string, body []byte) (File, error) {
	text, encoding, sure := decode(body)
	delimiter, clear := guessDelimiter(text)
	file, err := parseCSV(name, text, delimiter)
	file.Encoding, file.Delimiter = encoding, delimiterName(delimiter)
	if !sure {
		file.Unsure = append(file.Unsure, "encoding")
	}
	if !clear {
		file.Unsure = append(file.Unsure, "delimiter")
	}
	return file, err
}

// ReadCSVAs reads CSV text in the encoding and with the delimiter a person confirmed: utf-8, utf-16le, utf-16be or
// windows-1252; and comma, semicolon, tab or pipe.
func ReadCSVAs(name string, body []byte, encoding, delimiter string) (File, error) {
	var text string
	switch encoding {
	case "utf-8":
		if !utf8.Valid(bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF"))) {
			return File{}, errors.New("the file is not valid UTF-8")
		}
		text = string(bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF")))
	case "utf-16le", "utf-16be":
		text = utf16Text(body, encoding == "utf-16be")
	case "windows-1252":
		decoded, err := charmap.Windows1252.NewDecoder().Bytes(body)
		if err != nil {
			return File{}, errors.New("the file cannot be read as Windows-1252")
		}
		text = string(decoded)
	default:
		return File{}, errors.New("choose UTF-8, UTF-16 or Windows-1252")
	}
	var d rune
	for _, c := range delimiters {
		if delimiterName(c) == delimiter {
			d = c
		}
	}
	if d == 0 {
		return File{}, errors.New("choose a comma, semicolon, tab or pipe")
	}
	file, err := parseCSV(name, text, d)
	file.Encoding, file.Delimiter = encoding, delimiter
	return file, err
}

func delimiterName(d rune) string {
	return map[rune]string{',': "comma", ';': "semicolon", '\t': "tab", '|': "pipe"}[d]
}

// decode is the file's text, its encoding, and whether the encoding is certain: declared by a byte-order mark, or
// valid UTF-8.
func decode(body []byte) (string, string, bool) {
	switch {
	case bytes.HasPrefix(body, []byte("\xEF\xBB\xBF")):
		return string(body[3:]), "utf-8", utf8.Valid(body[3:])
	case bytes.HasPrefix(body, []byte("\xFF\xFE")):
		return utf16Text(body, false), "utf-16le", true
	case bytes.HasPrefix(body, []byte("\xFE\xFF")):
		return utf16Text(body, true), "utf-16be", true
	case utf8.Valid(body):
		return string(body), "utf-8", true
	}
	decoded, _ := charmap.Windows1252.NewDecoder().Bytes(body)
	return string(decoded), "windows-1252", false
}

// utf16Text decodes UTF-16, dropping its byte-order mark.
func utf16Text(body []byte, big bool) string {
	if len(body) >= 2 && ((body[0] == 0xFF && body[1] == 0xFE) || (body[0] == 0xFE && body[1] == 0xFF)) {
		body = body[2:]
	}
	units := make([]uint16, 0, len(body)/2)
	for i := 0; i+1 < len(body); i += 2 {
		if big {
			units = append(units, uint16(body[i])<<8|uint16(body[i+1]))
		} else {
			units = append(units, uint16(body[i+1])<<8|uint16(body[i]))
		}
	}
	return string(utf16.Decode(units))
}

// guessDelimiter is the separator that splits the file's first lines most consistently into more than one field, and
// whether it is the only one that does: two that both split every line the same way leave the choice to a person.
func guessDelimiter(text string) (rune, bool) {
	type score struct {
		d          rune
		consistent float64
		fields     int
	}
	var scores []score
	for _, d := range delimiters {
		r := csv.NewReader(strings.NewReader(text))
		r.Comma, r.LazyQuotes, r.FieldsPerRecord = d, true, -1
		counts := map[int]int{}
		lines := 0
		for lines < 50 {
			record, err := r.Read()
			if err != nil {
				break
			}
			if len(record) == 1 && strings.TrimSpace(record[0]) == "" {
				continue
			}
			counts[len(record)]++
			lines++
		}
		modal, most := 0, 0
		for n, c := range counts {
			if c > most || (c == most && n > modal) {
				modal, most = n, c
			}
		}
		if lines > 0 && modal > 1 {
			scores = append(scores, score{d, float64(most) / float64(lines), modal})
		}
	}
	if len(scores) == 0 {
		return ',', len(strings.TrimSpace(text)) == 0 || !strings.ContainsAny(text, ",;\t|")
	}
	best := scores[0]
	for _, s := range scores[1:] {
		if s.consistent > best.consistent || (s.consistent == best.consistent && s.fields > best.fields) {
			best = s
		}
	}
	for _, s := range scores {
		if s.d != best.d && s.consistent >= 0.9 && best.consistent-s.consistent < 0.1 {
			return best.d, false
		}
	}
	return best.d, true
}

// parseCSV reads text with delimiter into one table, a cell for each non-empty field, each with its row and column.
func parseCSV(name string, text string, delimiter rune) (File, error) {
	file := File{Format: "csv"}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma, r.LazyQuotes, r.FieldsPerRecord, r.ReuseRecord = delimiter, true, -1, false
	table := Table{Name: name, Rows: []Row{}}
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return file, errors.New("the CSV cannot be read: " + err.Error())
		}
		line, _ := r.FieldPos(0)
		row := Row{Number: line}
		for i, value := range record {
			if strings.TrimSpace(value) == "" {
				continue
			}
			row.Cells = append(row.Cells, Cell{Column: i, Ref: "R" + strconv.Itoa(line) + "C" + strconv.Itoa(i+1), Kind: Text, Value: value})
		}
		if len(row.Cells) > 0 {
			table.Rows = append(table.Rows, row)
		}
	}
	file.Tables = []Table{table}
	return file, nil
}
