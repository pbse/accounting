package transport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/bookfiles"
)

// A client's books as files (docs/books-sources.md, Reading files; delivery step 6). A file is uploaded for a month from
// the client's page, kept whole, and read: the reader proposes how to read it (the client's last confirmed layout when
// the file still fits it), a person adjusts and previews the reading, and a member who reviews confirms the layout, which
// is saved for the client as a version. Nothing here becomes a month's books: accepting an import and making it the
// month's books arrive with later steps. It all stays behind the firm's file-imports setting, off for every firm.

// importSummary is one import as listed: the file, its month, who sent it, and its confirmed layout's reading, if any.
type importSummary struct {
	ID        string         `json:"id"`
	Period    string         `json:"period"`
	Name      string         `json:"name"`
	Format    string         `json:"format"`
	Replaces  string         `json:"replaces,omitempty"`
	Actor     string         `json:"actor"`
	At        time.Time      `json:"at"`
	Confirmed *confirmedRead `json:"confirmed,omitempty"`
}

// csvChoice is how a person chose to read a CSV: its encoding (utf-8, utf-16le, utf-16be, windows-1252) and delimiter
// (comma, semicolon, tab, pipe); empty, as the reader reads it.
type csvChoice struct {
	Encoding  string `json:"encoding"`
	Delimiter string `json:"delimiter"`
}

// listedImport is an import as the client's list shows it: what was sent, and whether its confirmed reading is clean,
// without the reading itself, which an opened import reads.
type listedImport struct {
	ID        string           `json:"id"`
	Period    string           `json:"period"`
	Name      string           `json:"name"`
	Format    string           `json:"format"`
	Replaces  string           `json:"replaces,omitempty"`
	Actor     string           `json:"actor"`
	At        time.Time        `json:"at"`
	Confirmed *confirmedStatus `json:"confirmed,omitempty"`
}

// confirmedStatus is a confirmed layout's outcome: who confirmed it and why, whether it balanced, and how many problems
// its reading names.
type confirmedStatus struct {
	ID       string    `json:"id"`
	Reason   string    `json:"reason"`
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
	Balanced bool      `json:"balanced"`
	Problems int       `json:"problems"`
}

// confirmedRead is a layout a person confirmed for an import, with what it read.
type confirmedRead struct {
	ID       string            `json:"id"`
	Layout   bookfiles.Layout  `json:"layout"`
	Reading  bookfiles.Reading `json:"reading"`
	Reason   string            `json:"reason"`
	Actor    string            `json:"actor"`
	At       time.Time         `json:"at"`
	CSV      csvChoice         `json:"csv"`
	Balanced bool              `json:"balanced"`
	// Problems is how many problems the reading names: a balanced reading with problems still holds what depends on it.
	Problems int `json:"problems"`
}

var (
	importPeriod   = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)
	importCurrency = regexp.MustCompile(`^[A-Z]{3}$`)
)

// fileImportsOn says whether the firm has file imports switched on.
func fileImportsOn(ctx context.Context, q rowReader, org string) (bool, error) {
	var on bool
	err := q.QueryRowContext(ctx, `SELECT file_imports FROM organizations WHERE id=$1`, org).Scan(&on)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return on, err
}

// importClient is the client a file request names (?name=), seen whole by the reader, in a firm with file imports on;
// otherwise it answers and ok is false.
func importClient(w http.ResponseWriter, r *http.Request, db *sql.DB) (string, bool) {
	asked := strings.TrimSpace(r.URL.Query().Get("name"))
	if asked == "" || utf8.RuneCountInString(asked) > 200 {
		problem(w, 404, "Client not found.")
		return "", false
	}
	org := organization(r)
	on, err := fileImportsOn(r.Context(), db, org)
	if err != nil {
		problem(w, 503, "File imports are unavailable.")
		return "", false
	}
	if !on {
		problem(w, 403, "File imports are not switched on for your firm yet.")
		return "", false
	}
	name, known, allowed, whole, err := clientNamed(r.Context(), db, org, asked, firmAccess(r), subject(r))
	if err != nil {
		problem(w, 503, "The client is unavailable.")
		return "", false
	}
	if !known || !allowed || !whole {
		problem(w, 404, "Client not found.")
		return "", false
	}
	return name, true
}

// readImport is import id of client name in firm org: its stored file, read into tables; a CSV in the encoding and with
// the delimiter a person chose, when they chose them, else as the reader reads it.
func readImport(ctx context.Context, q rowReader, org, name, id string, as csvChoice) (importSummary, bookfiles.File, error) {
	var s importSummary
	var content []byte
	var replaces sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id::text,period,name,format,replaces::text,actor,created_at,content FROM books_imports
 WHERE id::text=$1 AND organization_id=$2 AND client_identity(organization_id,client_entity)=client_identity($2,$3)`, id, org, name).
		Scan(&s.ID, &s.Period, &s.Name, &s.Format, &replaces, &s.Actor, &s.At, &content)
	if err != nil {
		return s, bookfiles.File{}, err
	}
	s.Replaces = replaces.String
	if s.Format == "csv" && as.Encoding != "" && as.Delimiter != "" {
		file, err := bookfiles.ReadCSVAs(s.Name, content, as.Encoding, as.Delimiter)
		return s, file, err
	}
	file, err := bookfiles.Read(s.Name, content)
	return s, file, err
}

// latestLayout is the layout confirmed last for client name (for import id when id is not ""), or nil.
func latestLayout(ctx context.Context, q rowReader, org, name, id string) (*confirmedRead, error) {
	var c confirmedRead
	var layout, reading []byte
	err := q.QueryRowContext(ctx, `SELECT id::text,layout,reading,reason,actor,created_at,encoding,delimiter FROM books_layouts
 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND ($3='' OR import_id::text=$3) ORDER BY id DESC LIMIT 1`, org, name, id).
		Scan(&c.ID, &layout, &reading, &c.Reason, &c.Actor, &c.At, &c.CSV.Encoding, &c.CSV.Delimiter)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(layout, &c.Layout)
	json.Unmarshal(reading, &c.Reading)
	c.Balanced, c.Problems = c.Reading.Balanced, len(c.Reading.Problems)
	return &c, nil
}

// previewTable is a table's first rows, as the person sees them to confirm a layout.
type previewTable struct {
	Name          string          `json:"name"`
	Hidden        bool            `json:"hidden,omitempty"`
	HiddenColumns []int           `json:"hidden_columns,omitempty"`
	Rows          []bookfiles.Row `json:"rows"`
	More          int             `json:"more"`
}

const previewRows = 200

// proposal is how the reader proposes to read a table: the client's last confirmed layout when the file still fits it
// (its header row says the same thing in the same columns), else a fresh guess, with the questions a person answers.
func proposal(file bookfiles.File, saved *confirmedRead) (bookfiles.Layout, []string, bool) {
	if saved != nil {
		for _, t := range file.Tables {
			if t.Name != saved.Layout.Table && len(file.Tables) > 1 {
				continue
			}
			// The header row's text over the same columns is what makes it the same layout, whether the reader found that
			// row itself or a person chose it. The date is this file's, and rows marked by number were the last file's.
			if sameHeaders(t, saved.Layout) {
				layout := saved.Layout
				layout.Table, layout.AsOf = t.Name, ""
				layout.Headings, layout.Totals, layout.Postings, layout.Ignored = nil, nil, nil, nil
				return layout, []string{"As of which date is this file, and is the layout the same as last time?"}, true
			}
		}
	}
	best, questions := bookfiles.Layout{}, []string{"Which table holds the Trial Balance?"}
	for _, t := range file.Tables {
		if t.Hidden {
			continue
		}
		layout, asked := bookfiles.ProposeLayout(t)
		if layout.HeaderRow > 0 {
			return layout, asked, false
		}
		if best.Table == "" {
			best, questions = layout, asked
		}
	}
	return best, questions, false
}

// sameHeaders says the table's header row names each of the layout's columns as the layout's own table did, by the
// headers' text the layout kept when it was confirmed.
func sameHeaders(t bookfiles.Table, l bookfiles.Layout) bool {
	if len(l.Headers) == 0 {
		return false
	}
	for _, r := range t.Rows {
		if r.Number != l.HeaderRow {
			continue
		}
		seen := map[int]string{}
		for _, c := range r.Cells {
			seen[c.Column] = strings.TrimSpace(c.Value)
		}
		for column, header := range l.Headers {
			if !strings.EqualFold(seen[column], header) {
				return false
			}
		}
		return true
	}
	return false
}

// headersOf is the table's header row's text for each of the layout's columns.
func headersOf(t bookfiles.Table, l bookfiles.Layout) map[int]string {
	headers := map[int]string{}
	for _, r := range t.Rows {
		if r.Number != l.HeaderRow {
			continue
		}
		for _, c := range r.Cells {
			if _, ok := l.Columns[c.Column]; ok {
				headers[c.Column] = strings.TrimSpace(c.Value)
			}
		}
	}
	return headers
}

// tableOf is the file's table the layout names, or the first.
func tableOf(file bookfiles.File, l bookfiles.Layout) (bookfiles.Table, bool) {
	for _, t := range file.Tables {
		if t.Name == l.Table {
			return t, true
		}
	}
	return bookfiles.Table{}, false
}

func registerImports(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/client/imports", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT i.id::text,i.period,i.name,i.format,COALESCE(i.replaces::text,''),i.actor,i.created_at,
 l.id::text,COALESCE(l.reason,''),COALESCE(l.actor,''),l.created_at,COALESCE((l.reading->>'balanced')::boolean,false),COALESCE(jsonb_array_length(l.reading->'problems'),0)
 FROM books_imports i LEFT JOIN LATERAL (SELECT * FROM books_layouts b WHERE b.import_id=i.id ORDER BY b.id DESC LIMIT 1) l ON true
 WHERE i.organization_id=$1 AND client_identity(i.organization_id,i.client_entity)=client_identity($1,$2) ORDER BY i.period DESC,i.id DESC LIMIT 100`, organization(r), name)
		if err != nil {
			problem(w, 503, "The client's files are unavailable.")
			return
		}
		imports := []listedImport{}
		for rows.Next() {
			var s listedImport
			var c confirmedStatus
			var layoutID sql.NullString
			var at sql.NullTime
			if err = rows.Scan(&s.ID, &s.Period, &s.Name, &s.Format, &s.Replaces, &s.Actor, &s.At, &layoutID, &c.Reason, &c.Actor, &at, &c.Balanced, &c.Problems); err != nil {
				break
			}
			if layoutID.Valid {
				c.ID, c.At = layoutID.String, at.Time
				s.Confirmed = &c
			}
			imports = append(imports, s)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			problem(w, 503, "The client's files are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"imports": imports, "can_confirm": canReview(r)})
	})
	mux.HandleFunc("POST /v1/closes/client/imports", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, bookfiles.MaxFile+1<<20)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			problem(w, 413, "Send one file of 50 MiB or less, with its month.")
			return
		}
		period := strings.TrimSpace(r.FormValue("period"))
		if !importPeriod.MatchString(period) {
			problem(w, 422, "Say which month the file holds, as YYYY-MM.")
			return
		}
		// A file holding several clients' books would show them all to whoever opens this client: it waits for their split.
		if r.FormValue("one_client") != "true" {
			problem(w, 422, "Confirm the file holds only "+name+"'s books. A file holding several clients' books cannot be sent yet.")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			problem(w, 422, "Choose a CSV or Excel workbook (.xlsx) to send.")
			return
		}
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, bookfiles.MaxFile+1))
		if err != nil || len(body) == 0 || len(body) > bookfiles.MaxFile {
			problem(w, 413, "Send one file of 50 MiB or less.")
			return
		}
		filename := strings.TrimSpace(header.Filename)
		if filename == "" || utf8.RuneCountInString(filename) > 255 {
			filename = "books"
		}
		read, err := bookfiles.Read(filename, body)
		if err != nil {
			problem(w, 422, "The file cannot be read: "+err.Error()+".")
			return
		}
		format := "csv"
		if read.Format == "xlsx" {
			format = "xlsx"
		}
		sum := sha256.Sum256(body)
		replaces := strings.TrimSpace(r.FormValue("replaces"))
		var id string
		err = db.QueryRowContext(r.Context(), `INSERT INTO books_imports(organization_id,client_entity,period,name,digest,content,format,replaces,actor)
 SELECT $1,$2,$3,$4,$5,$6,$7,(SELECT p.id FROM books_imports p WHERE p.id::text=$8 AND p.organization_id=$1 AND client_identity(p.organization_id,p.client_entity)=client_identity($1,$2)),$9 RETURNING id::text`,
			organization(r), name, period, filename, hex.EncodeToString(sum[:]), body, format, replaces, reviewerName(r)).Scan(&id)
		if err != nil {
			problem(w, 503, "The file could not be kept.")
			return
		}
		jsonReply(w, 201, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /v1/closes/client/import", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		org := organization(r)
		id := r.URL.Query().Get("id")
		confirmed, err := latestLayout(r.Context(), db, org, name, id)
		if err != nil {
			problem(w, 503, "The file's layout is unavailable.")
			return
		}
		// A CSV is read as asked (?encoding=&delimiter=), else as its layout was confirmed, else as the reader reads it.
		as := csvChoice{Encoding: r.URL.Query().Get("encoding"), Delimiter: r.URL.Query().Get("delimiter")}
		if as.Encoding == "" && confirmed != nil {
			as = confirmed.CSV
		}
		summary, file, err := readImport(r.Context(), db, org, name, id, as)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		}
		if err != nil {
			problem(w, 422, "The file cannot be read that way: "+err.Error()+".")
			return
		}
		var saved *confirmedRead
		if err == nil && confirmed == nil {
			saved, err = latestLayout(r.Context(), db, org, name, "")
		}
		if err != nil {
			problem(w, 503, "The file's layout is unavailable.")
			return
		}
		summary.Confirmed = confirmed
		// Rows come in pages: from the first, or from the row a person asks for (?from=), so any row can be seen and
		// marked.
		from := 1
		if f := r.URL.Query().Get("from"); f != "" {
			if from, err = strconv.Atoi(f); err != nil || from < 1 {
				problem(w, 422, "Say which row to show from, as a number.")
				return
			}
		}
		tables := []previewTable{}
		for _, t := range file.Tables {
			p := previewTable{Name: t.Name, Hidden: t.Hidden, HiddenColumns: t.HiddenColumns, Rows: []bookfiles.Row{}}
			for i, row := range t.Rows {
				if row.Number < from {
					continue
				}
				if len(p.Rows) == previewRows {
					p.More = len(t.Rows) - i
					break
				}
				p.Rows = append(p.Rows, row)
			}
			tables = append(tables, p)
		}
		out := map[string]any{"import": summary, "tables": tables, "encoding": file.Encoding, "delimiter": file.Delimiter, "unsure": file.Unsure,
			"linked_workbooks": file.LinkedWorkbooks, "recalculate_on_open": file.RecalculateOnOpen, "formulas": file.Formulas, "can_confirm": canReview(r)}
		if confirmed == nil {
			layout, questions, reused := proposal(file, saved)
			out["proposal"] = map[string]any{"layout": layout, "questions": questions, "reused": reused}
		}
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("POST /v1/closes/client/import", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		var in struct {
			Action string           `json:"action"`
			Layout bookfiles.Layout `json:"layout"`
			Reason string           `json:"reason"`
			CSV    csvChoice        `json:"csv"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Action != "preview" && in.Action != "confirm" {
			problem(w, 422, "Preview or confirm a layout.")
			return
		}
		// Confirming a layout is a reviewer's, as confirming any reading is (docs/books-sources.md, Who does what).
		if in.Action == "confirm" && !canReview(r) {
			problem(w, 403, "Confirming a layout requires workpapers:review.")
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		if in.Action == "confirm" && (in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500) {
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		org := organization(r)
		summary, file, err := readImport(r.Context(), db, org, name, r.URL.Query().Get("id"), in.CSV)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		}
		if err != nil {
			problem(w, 422, "The file cannot be read that way: "+err.Error()+".")
			return
		}
		// A confirmed reading says the month-end it is as of, in the import's month, and its currency.
		if in.Action == "confirm" && (!strings.HasPrefix(in.Layout.AsOf, summary.Period+"-") || in.Layout.Currency == "") {
			problem(w, 422, "Say the date the file is as of, in "+summary.Period+", and its currency, before confirming.")
			return
		}
		table, found := tableOf(file, in.Layout)
		if !found {
			problem(w, 422, "Choose the table the layout reads.")
			return
		}
		if problem422 := checkLayout(in.Layout); problem422 != "" {
			problem(w, 422, problem422)
			return
		}
		in.Layout.Headers = headersOf(table, in.Layout)
		reading := in.Layout.Read(table)
		if in.Action == "preview" {
			jsonReply(w, 200, map[string]any{"reading": reading})
			return
		}
		layout, _ := json.Marshal(in.Layout)
		read, _ := json.Marshal(reading)
		var id string
		// The CSV choice kept is the one the file was read with: chosen, else the reader's own.
		as := in.CSV
		if as.Encoding == "" || as.Delimiter == "" {
			as = csvChoice{Encoding: file.Encoding, Delimiter: file.Delimiter}
		}
		if err = db.QueryRowContext(r.Context(), `INSERT INTO books_layouts(organization_id,client_entity,import_id,layout,reading,reason,actor,encoding,delimiter) VALUES($1,$2,$3::bigint,$4::jsonb,$5::jsonb,$6,$7,$8,$9) RETURNING id::text`,
			org, name, summary.ID, string(layout), string(read), in.Reason, reviewerName(r), as.Encoding, as.Delimiter).Scan(&id); err != nil {
			problem(w, 503, "The layout could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"id": id, "reading": reading})
	})
}

// checkLayout is why a layout cannot be read, or "": a header row, columns of known meaning, a scale of one, a thousand
// or a million, a sign convention, and an as-of date and currency when given.
func checkLayout(l bookfiles.Layout) string {
	if l.HeaderRow < 1 {
		return "Choose the row that heads the Trial Balance."
	}
	if len(l.Columns) == 0 || len(l.Columns) > 200 {
		return "Say what the columns hold."
	}
	for column, meaning := range l.Columns {
		if column < 0 || column > 16383 || !strings.Contains(" account_number account_name account_type debit credit balance opening movement year_to_date ", " "+string(meaning)+" ") {
			return "Choose each column's meaning from the list."
		}
	}
	if l.Style.Scale != 1 && l.Style.Scale != 1000 && l.Style.Scale != 1000000 {
		return "Amounts are in units, thousands or millions."
	}
	if l.BalanceSign != "" && l.BalanceSign != "debit" && l.BalanceSign != "credit" {
		return "Say whether a positive balance is a debit or a credit."
	}
	if l.AsOf != "" {
		if _, err := time.Parse("2006-01-02", l.AsOf); err != nil {
			return "Enter the as-of date as YYYY-MM-DD."
		}
	}
	if l.Currency != "" && !importCurrency.MatchString(l.Currency) {
		return "Enter the currency as its three-letter code."
	}
	for _, rows := range [][]int{l.Headings, l.Totals, l.Postings, l.Ignored, l.IgnoredColumns} {
		if len(rows) > 10000 {
			return "Mark fewer rows."
		}
	}
	return ""
}
