package transport

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"slices"
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
	Why       string           `json:"replaces_reason,omitempty"`
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
	// LineCount is how many lines the reading has, and Accounts how many are accounts, when they are sent apart from it.
	LineCount int `json:"line_count,omitempty"`
	Accounts  int `json:"accounts,omitempty"`
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
	on, err := fileImportsOn(r.Context(), db, organization(r))
	if err != nil {
		problem(w, 503, "File imports are unavailable.")
		return "", false
	}
	if !on {
		problem(w, 403, "File imports are not switched on for your firm yet.")
		return "", false
	}
	return importOwner(w, r, db)
}

// importOwner is the client a file request names (?name=), seen whole by the reader, whether or not the firm's file
// imports are on: a file already sent stays the firm's to download.
func importOwner(w http.ResponseWriter, r *http.Request, db *sql.DB) (string, bool) {
	asked := strings.TrimSpace(r.URL.Query().Get("name"))
	if asked == "" || utf8.RuneCountInString(asked) > 200 {
		problem(w, 404, "Client not found.")
		return "", false
	}
	org := organization(r)
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

// importFormat is import id of client name's format: csv or xlsx.
func importFormat(ctx context.Context, q rowReader, org, name, id string) (string, error) {
	var format string
	err := q.QueryRowContext(ctx, `SELECT format FROM books_imports WHERE id::text=$1 AND organization_id=$2 AND client_identity(organization_id,client_entity)=client_identity($2,$3)`, id, org, name).Scan(&format)
	return format, err
}

// askedChoice is why a CSV choice asked for cannot be used for a file of format, or "": either both an encoding and a
// delimiter the reader knows, for a CSV, or neither.
func askedChoice(format string, asked csvChoice) string {
	if asked.Encoding == "" && asked.Delimiter == "" {
		return ""
	}
	if !slices.Contains([]string{"utf-8", "utf-16le", "utf-16be", "windows-1252"}, asked.Encoding) || !slices.Contains([]string{"comma", "semicolon", "tab", "pipe"}, asked.Delimiter) {
		return "Choose an encoding (UTF-8, UTF-16 or Windows-1252) and a delimiter (comma, semicolon, tab or pipe) together."
	}
	if format != "csv" {
		return "Only a CSV file is read with an encoding and a delimiter."
	}
	return ""
}

// parseKey names what decides how a file of format is read, as readImportAs reads it: nothing, for a workbook; for a
// CSV, the encoding and delimiter asked for, else those of its own confirmed layout, else the reader's own reading with
// the client's last choice for what it is unsure of. Rows read under the same key are the same rows, however often
// layouts are confirmed.
func parseKey(format string, asked csvChoice, confirmed, saved *confirmedRead) string {
	switch {
	case format != "csv":
		return "workbook"
	case asked.Encoding != "" && asked.Delimiter != "":
		return "as|" + asked.Encoding + "|" + asked.Delimiter
	case confirmed != nil:
		return "as|" + confirmed.CSV.Encoding + "|" + confirmed.CSV.Delimiter
	case saved != nil && saved.CSV.Encoding != "":
		return "reader|" + saved.CSV.Encoding + "|" + saved.CSV.Delimiter
	}
	return "reader"
}

// storeImportPages keeps the file's tables as read under key, two hundred rows to a chunk, unless they are kept already,
// in place of those kept for any other way of reading it: a file keeps one copy of its rows.
func storeImportPages(ctx context.Context, db *sql.DB, org, id, key string, file bookfiles.File) error {
	type chunk struct {
		Position      int             `json:"position"`
		Table         string          `json:"table_name"`
		Hidden        bool            `json:"hidden"`
		HiddenColumns []int           `json:"hidden_columns"`
		Total         int             `json:"total"`
		Chunk         int             `json:"chunk"`
		First         int             `json:"first_row"`
		Rows          []bookfiles.Row `json:"rows"`
		Meta          map[string]any  `json:"meta"`
	}
	var kept bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM books_import_pages WHERE import_id::text=$1 AND parse=$2)`, id, key).Scan(&kept); err != nil || kept {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM books_import_pages WHERE import_id=$1::bigint AND parse<>$2`, id, key); err != nil {
		return err
	}
	// The chunks go in fifty at a time (ten thousand rows), so a large file is never written as one value.
	var batch []chunk
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		raw, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		batch = batch[:0]
		_, err = tx.ExecContext(ctx, `INSERT INTO books_import_pages(organization_id,import_id,parse,position,table_name,hidden,hidden_columns,total,chunk,first_row,rows,meta)
 SELECT $1,$2::bigint,$3,c.position,c.table_name,c.hidden,c.hidden_columns,c.total,c.chunk,c.first_row,c.rows,c.meta FROM jsonb_to_recordset($4::jsonb)
  AS c(position int,table_name text,hidden boolean,hidden_columns jsonb,total int,chunk int,first_row int,rows jsonb,meta jsonb) ON CONFLICT DO NOTHING`, org, id, key, string(raw))
		return err
	}
	meta := fileFacts(file)
	for position, t := range file.Tables {
		hidden := t.HiddenColumns
		if hidden == nil {
			hidden = []int{}
		}
		for start := 0; start == 0 || start < len(t.Rows); start += previewRows {
			rows := t.Rows[min(start, len(t.Rows)):min(start+previewRows, len(t.Rows))]
			first := 0
			if len(rows) > 0 {
				first = rows[0].Number
			}
			if batch = append(batch, chunk{position, t.Name, t.Hidden, hidden, len(t.Rows), start / previewRows, first, rows, meta}); len(batch) == 50 {
				if err = flush(); err != nil {
					return err
				}
			}
		}
	}
	if err = flush(); err != nil {
		return err
	}
	return tx.Commit()
}

// fileFacts is what a file was read as, beside its rows: as the import's reply names them.
func fileFacts(file bookfiles.File) map[string]any {
	return map[string]any{"encoding": file.Encoding, "delimiter": file.Delimiter, "unsure": file.Unsure,
		"linked_workbooks": file.LinkedWorkbooks, "recalculate_on_open": file.RecalculateOnOpen, "formulas": file.Formulas}
}

// storedImportPage is each table's rows from row number from, up to two hundred, read under key from the chunks kept
// for import id of client name in firm org, with what the file was read as, and false when none are kept.
func storedImportPage(ctx context.Context, db *sql.DB, org, name, id, key string, from int) ([]previewTable, map[string]any, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.position,p.table_name,p.hidden,p.hidden_columns,p.total,p.chunk,p.rows,p.meta FROM books_import_pages p
 JOIN books_imports i ON i.id=p.import_id AND i.organization_id=$4 AND client_identity(i.organization_id,i.client_entity)=client_identity($4,$5)
 WHERE p.import_id::text=$1 AND p.parse=$2 AND p.chunk BETWEEN COALESCE((SELECT max(q.chunk) FROM books_import_pages q WHERE q.import_id=p.import_id AND q.parse=p.parse AND q.position=p.position AND q.first_row<=$3),0)
  AND COALESCE((SELECT max(q.chunk) FROM books_import_pages q WHERE q.import_id=p.import_id AND q.parse=p.parse AND q.position=p.position AND q.first_row<=$3),0)+1
 ORDER BY p.position,p.chunk`, id, key, from, org, name)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	var tables []previewTable
	meta := map[string]any{}
	position := -1
	for rows.Next() {
		var at, total, chunk int
		var name string
		var hidden bool
		var hiddenColumns, raw, facts []byte
		if err = rows.Scan(&at, &name, &hidden, &hiddenColumns, &total, &chunk, &raw, &facts); err != nil {
			return nil, nil, false, err
		}
		json.Unmarshal(facts, &meta)
		if at != position {
			position = at
			t := previewTable{Name: name, Hidden: hidden, Rows: []bookfiles.Row{}}
			json.Unmarshal(hiddenColumns, &t.HiddenColumns)
			tables = append(tables, t)
		}
		t := &tables[len(tables)-1]
		var chunkRows []bookfiles.Row
		json.Unmarshal(raw, &chunkRows)
		for i, row := range chunkRows {
			if row.Number < from || len(t.Rows) == previewRows {
				continue
			}
			t.Rows = append(t.Rows, row)
			t.More = total - (chunk*previewRows + i + 1)
		}
	}
	return tables, meta, len(tables) > 0, rows.Err()
}

// readImportAs reads import id the way a person sees it: a CSV as asked, else as its own layout was confirmed, else as
// the client's last file was confirmed (its exports keep their format) when that reads it, else as the reader reads it.
func readImportAs(ctx context.Context, q rowReader, org, name, id string, asked csvChoice, confirmed, saved *confirmedRead) (importSummary, bookfiles.File, error) {
	if asked.Encoding != "" && asked.Delimiter != "" {
		return readImport(ctx, q, org, name, id, asked)
	}
	if confirmed != nil {
		return readImport(ctx, q, org, name, id, confirmed.CSV)
	}
	summary, file, err := readImport(ctx, q, org, name, id, csvChoice{})
	// The client's earlier choice settles only what the reader is unsure of: a file it reads with certainty keeps the
	// reader's reading, its dialect perhaps changed.
	if err == nil && saved != nil && saved.CSV.Encoding != "" && summary.Format == "csv" && len(file.Unsure) > 0 {
		// Only what the reader is unsure of takes the earlier choice; what it read with certainty stays its own.
		as := csvChoice{Encoding: file.Encoding, Delimiter: file.Delimiter}
		if slices.Contains(file.Unsure, "encoding") {
			as.Encoding = saved.CSV.Encoding
		}
		if slices.Contains(file.Unsure, "delimiter") {
			as.Delimiter = saved.CSV.Delimiter
		}
		if s, f, e := readImport(ctx, q, org, name, id, as); e == nil {
			return s, f, nil
		}
	}
	return summary, file, err
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

// importSummaryOf is import id of client name in firm org as the list shows it, without its file.
func importSummaryOf(ctx context.Context, q rowReader, org, name, id string) (importSummary, error) {
	var s importSummary
	var replaces sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id::text,period,name,format,replaces::text,actor,created_at FROM books_imports
 WHERE id::text=$1 AND organization_id=$2 AND client_identity(organization_id,client_entity)=client_identity($2,$3)`, id, org, name).
		Scan(&s.ID, &s.Period, &s.Name, &s.Format, &replaces, &s.Actor, &s.At)
	s.Replaces = replaces.String
	return s, err
}

// confirmedBrief is import id's confirmed layout with its reading's totals, problems and notes, and how many lines and
// accounts it read, the lines themselves left in the database (/client/import/lines).
func confirmedBrief(ctx context.Context, q rowReader, org, name, id string) (*confirmedRead, error) {
	var c confirmedRead
	var layout, reading []byte
	err := q.QueryRowContext(ctx, `SELECT id::text,layout,reading-'lines',reason,actor,created_at,encoding,delimiter,COALESCE(jsonb_array_length(reading->'lines'),0),
 (SELECT count(*) FROM jsonb_array_elements(COALESCE(reading->'lines','[]'::jsonb)) e WHERE e->>'kind'='posting')
 FROM books_layouts WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND import_id::text=$3 ORDER BY id DESC LIMIT 1`, org, name, id).
		Scan(&c.ID, &layout, &reading, &c.Reason, &c.Actor, &c.At, &c.CSV.Encoding, &c.CSV.Delimiter, &c.LineCount, &c.Accounts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(layout, &c.Layout)
	json.Unmarshal(reading, &c.Reading)
	c.Reading.Lines = []bookfiles.Line{}
	c.Balanced, c.Problems = c.Reading.Balanced, len(c.Reading.Problems)
	return &c, nil
}

// latestLayout is the layout confirmed last for client name (for import id when id is not ""), or nil: the layout and
// the CSV choice it was confirmed with, which decide how a file is read and proposed, without its reading
// (confirmedBrief has that, for display).
func latestLayout(ctx context.Context, q rowReader, org, name, id string) (*confirmedRead, error) {
	var c confirmedRead
	var layout []byte
	err := q.QueryRowContext(ctx, `SELECT id::text,layout,reason,actor,created_at,encoding,delimiter FROM books_layouts
 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND ($3='' OR import_id::text=$3) ORDER BY id DESC LIMIT 1`, org, name, id).
		Scan(&c.ID, &layout, &c.Reason, &c.Actor, &c.At, &c.CSV.Encoding, &c.CSV.Delimiter)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(layout, &c.Layout)
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
	// Every column read or left out has a header, blank when its cell is: a later file with a header there differs.
	headers := map[int]string{}
	for column := range l.Columns {
		headers[column] = ""
	}
	for _, column := range l.IgnoredColumns {
		headers[column] = ""
	}
	for _, r := range t.Rows {
		if r.Number != l.HeaderRow {
			continue
		}
		// The columns read and those left out on purpose: a column left out keeps its header, so a file whose header
		// there says something else is not read with the layout again unasked.
		for _, c := range r.Cells {
			if _, ok := l.Columns[c.Column]; ok || slices.Contains(l.IgnoredColumns, c.Column) {
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
	// A firm's files stay readable when its file imports are switched off: only sending and confirming stop.
	mux.HandleFunc("GET /v1/closes/client/imports", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		// The list comes in pages of a hundred, newest first, from ?offset=, and says when there are more.
		offset := 0
		if o := r.URL.Query().Get("offset"); o != "" {
			var err error
			if offset, err = strconv.Atoi(o); err != nil || offset < 0 {
				problem(w, 422, "Say where the list continues from, as a number.")
				return
			}
		}
		rows, err := db.QueryContext(r.Context(), `SELECT i.id::text,i.period,i.name,i.format,COALESCE(i.replaces::text,''),i.replaces_reason,i.actor,i.created_at,
 l.id::text,COALESCE(l.reason,''),COALESCE(l.actor,''),l.created_at,COALESCE((l.reading->>'balanced')::boolean,false),COALESCE(jsonb_array_length(l.reading->'problems'),0)
 FROM books_imports i LEFT JOIN LATERAL (SELECT * FROM books_layouts b WHERE b.import_id=i.id ORDER BY b.id DESC LIMIT 1) l ON true
 WHERE i.organization_id=$1 AND client_identity(i.organization_id,i.client_entity)=client_identity($1,$2) ORDER BY i.period DESC,i.id DESC LIMIT 101 OFFSET $3`, organization(r), name, offset)
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
			if err = rows.Scan(&s.ID, &s.Period, &s.Name, &s.Format, &s.Replaces, &s.Why, &s.Actor, &s.At, &layoutID, &c.Reason, &c.Actor, &at, &c.Balanced, &c.Problems); err != nil {
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
		more := len(imports) > 100
		if more {
			imports = imports[:100]
		}
		on, err := fileImportsOn(r.Context(), db, organization(r))
		if err != nil {
			problem(w, 503, "The client's files are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"imports": imports, "more": more, "on": on, "can_confirm": canReview(r) && on})
	})
	mux.HandleFunc("POST /v1/closes/client/imports", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		// The file's 50 MiB, and a mebibyte for the form around it.
		r.Body = http.MaxBytesReader(w, r.Body, bookfiles.MaxFile+(1<<20))
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
		// A replacement names a file of this client's, and says why: one that names no such file is refused, never kept
		// unlinked.
		replaces, why := strings.TrimSpace(r.FormValue("replaces")), strings.TrimSpace(r.FormValue("replaces_reason"))
		if replaces != "" && (why == "" || utf8.RuneCountInString(why) > 500) {
			problem(w, 422, "Say why this file replaces the earlier one (1–500 characters).")
			return
		}
		// The file it replaces is locked while this one is kept, so a deletion meanwhile either waits or is seen.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The file could not be kept.")
			return
		}
		defer tx.Rollback()
		if replaces != "" {
			var found bool
			if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM books_imports p WHERE p.id::text=$1 AND p.organization_id=$2 AND client_identity(p.organization_id,p.client_entity)=client_identity($2,$3) AND p.period=$4 FOR UPDATE)`, replaces, organization(r), name, period).Scan(&found); err != nil {
				problem(w, 503, "The file could not be kept.")
				return
			}
			if !found {
				problem(w, 422, "The file it replaces is not one of "+name+"'s for "+period+".")
				return
			}
		}
		// The same file sent again for the same month (a retry after a lost answer) is the import already kept.
		var repeat string
		switch err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM books_imports WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) AND period=$3 AND digest=$4 ORDER BY id DESC LIMIT 1`,
			organization(r), name, period, hex.EncodeToString(sum[:])).Scan(&repeat); err {
		case nil:
			jsonReply(w, 200, map[string]any{"id": repeat, "repeat": true})
			return
		case sql.ErrNoRows:
		default:
			problem(w, 503, "The file could not be kept.")
			return
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO books_imports(organization_id,client_entity,period,name,digest,content,format,replaces,replaces_reason,actor)
 SELECT $1,$2,$3,$4,$5,$6,$7,(SELECT p.id FROM books_imports p WHERE p.id::text=$8 AND p.organization_id=$1 AND client_identity(p.organization_id,p.client_entity)=client_identity($1,$2)),$10,$9
 ON CONFLICT DO NOTHING RETURNING id::text`,
			organization(r), name, period, filename, hex.EncodeToString(sum[:]), body, format, replaces, reviewerName(r), why).Scan(&id)
		// A retry that overlapped this one kept the file first: it is the import.
		if err == sql.ErrNoRows {
			if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM books_imports WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND period=$3 AND digest=$4`, organization(r), name, period, hex.EncodeToString(sum[:])).Scan(&id); err == nil {
				jsonReply(w, 200, map[string]any{"id": id, "repeat": true})
				return
			}
		}
		if err != nil {
			problem(w, 503, "The file could not be kept.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "The file could not be kept.")
			return
		}
		jsonReply(w, 201, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /v1/closes/client/import", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		org := organization(r)
		id := r.URL.Query().Get("id")
		// How the file is read and proposed is decided by its own confirmed layout, else the client's last, read without
		// their readings.
		confirmed, err := latestLayout(r.Context(), db, org, name, id)
		var saved *confirmedRead
		if err == nil && confirmed == nil {
			saved, err = latestLayout(r.Context(), db, org, name, "")
		}
		if err != nil {
			problem(w, 503, "The file's layout is unavailable.")
			return
		}
		format, err := importFormat(r.Context(), db, org, name, id)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		}
		if err != nil {
			problem(w, 503, "The file is unavailable.")
			return
		}
		asked := csvChoice{Encoding: r.URL.Query().Get("encoding"), Delimiter: r.URL.Query().Get("delimiter")}
		if refusal := askedChoice(format, asked); refusal != "" {
			problem(w, 422, refusal)
			return
		}
		key := parseKey(format, asked, confirmed, saved)
		// Rows come in pages: from the first, or from the row a person asks for (?from=), so any row can be seen and
		// marked. A later page is read from the rows kept the first time the file was read this way.
		from := 1
		if f := r.URL.Query().Get("from"); f != "" {
			if from, err = strconv.Atoi(f); err != nil || from < 1 {
				problem(w, 422, "Say which row to show from, as a number.")
				return
			}
		}
		// A later page, and a confirmed file reopened, are read from what was kept the first time the file was read
		// this way: its rows and what it was read as, with the import and its confirmed reading, without reading the
		// file again.
		if from > 1 || confirmed != nil {
			tables, facts, kept, err := storedImportPage(r.Context(), db, org, name, id, key, from)
			if err != nil {
				problem(w, 503, "The file's rows are unavailable.")
				return
			}
			if kept && from > 1 {
				jsonReply(w, 200, map[string]any{"tables": tables})
				return
			}
			if kept {
				summary, err := importSummaryOf(r.Context(), db, org, name, id)
				var brief *confirmedRead
				if err == nil {
					brief, err = confirmedBrief(r.Context(), db, org, name, id)
				}
				on, onErr := fileImportsOn(r.Context(), db, org)
				if errors.Is(err, sql.ErrNoRows) {
					problem(w, 404, "File not found.")
					return
				}
				if err != nil || onErr != nil {
					problem(w, 503, "The file is unavailable.")
					return
				}
				summary.Confirmed = brief
				out := map[string]any{"import": summary, "tables": tables, "on": on, "can_confirm": canReview(r) && on, "can_delete": canManageData(r)}
				for k, v := range facts {
					out[k] = v
				}
				jsonReply(w, 200, out)
				return
			}
		}
		summary, file, err := readImportAs(r.Context(), db, org, name, id, asked, confirmed, saved)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		}
		if err != nil {
			problem(w, 422, "The file cannot be read that way: "+err.Error()+".")
			return
		}
		// The confirmed reading comes with its totals, problems and notes; its lines, which can number thousands, come in
		// pages of their own (/client/import/lines).
		if confirmed != nil {
			if summary.Confirmed, err = confirmedBrief(r.Context(), db, org, name, id); err != nil {
				problem(w, 503, "The file's layout is unavailable.")
				return
			}
		}
		if err = storeImportPages(r.Context(), db, org, id, key, file); err != nil {
			problem(w, 503, "The file's rows could not be kept.")
			return
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
		out := fileFacts(file)
		out["import"], out["tables"] = summary, tables
		on, err := fileImportsOn(r.Context(), db, org)
		if err != nil {
			problem(w, 503, "File imports are unavailable.")
			return
		}
		out["on"], out["can_confirm"], out["can_delete"] = on, canReview(r) && on, canManageData(r)
		// A later page of rows is rows alone: the reading and the proposal came with the first.
		if from > 1 {
			delete(out, "import")
			jsonReply(w, 200, out)
			return
		}
		if confirmed == nil {
			layout, questions, reused := proposal(file, saved)
			out["proposal"] = map[string]any{"layout": layout, "questions": questions, "reused": reused}
		}
		jsonReply(w, 200, out)
	})
	// The lines of an import's confirmed reading, two hundred from ?offset=.
	mux.HandleFunc("GET /v1/closes/client/import/lines", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		offset, err := strconv.Atoi(cmp.Or(r.URL.Query().Get("offset"), "0"))
		if err != nil || offset < 0 {
			problem(w, 422, "Say where the lines continue from, as a number.")
			return
		}
		// The page is cut from the stored reading in the database: a reading's thousands of lines are not sent, or
		// decoded, for each page.
		var page []byte
		var total int
		err = db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT jsonb_agg(e ORDER BY n) FROM jsonb_array_elements(l.reading->'lines') WITH ORDINALITY t(e,n) WHERE n>$4 AND n<=$4+200),'[]'::jsonb),jsonb_array_length(l.reading->'lines')
 FROM books_layouts l JOIN books_imports i ON i.id=l.import_id WHERE i.id::text=$1 AND i.organization_id=$2 AND client_identity(i.organization_id,i.client_entity)=client_identity($2,$3) ORDER BY l.id DESC LIMIT 1`,
			r.URL.Query().Get("id"), organization(r), name, min(offset, 1<<30)).Scan(&page, &total)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "The file has no confirmed reading.")
			return
		}
		if err != nil {
			problem(w, 503, "The file's reading is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"lines": json.RawMessage(page), "total": total})
	})
	// A file is deleted by a member who manages the firm's data, with a reason: sent in error, or at the client's
	// request, whether or not its month has a close. A receipt says what it was, never what it held.
	mux.HandleFunc("POST /v1/closes/client/import/delete", func(w http.ResponseWriter, r *http.Request) {
		if !canManageData(r) {
			problem(w, 403, "Deleting a file requires clients:all and data:manage.")
			return
		}
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Reason = strings.TrimSpace(in.Reason); in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The file could not be deleted.")
			return
		}
		defer tx.Rollback()
		org, id := organization(r), r.URL.Query().Get("id")
		var period, filename, digest string
		var replaced, retained bool
		// A file of a month whose close is under legal hold, or kept until a date still to come, is kept with it.
		err = tx.QueryRowContext(r.Context(), `SELECT period,name,digest,EXISTS(SELECT 1 FROM books_imports n WHERE n.replaces=i.id),
 EXISTS(SELECT 1 FROM client_closes c WHERE c.organization_id=i.organization_id AND client_identity(c.organization_id,c.entity)=client_identity(i.organization_id,i.client_entity) AND c.period=i.period
  AND (c.legal_hold OR c.retain_until>CURRENT_DATE)) FROM books_imports i
 WHERE i.id::text=$1 AND i.organization_id=$2 AND client_identity(i.organization_id,i.client_entity)=client_identity($2,$3) FOR UPDATE`, id, org, name).Scan(&period, &filename, &digest, &replaced, &retained)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		}
		if err != nil {
			problem(w, 503, "The file could not be deleted.")
			return
		}
		if replaced {
			problem(w, 409, "A later file replaces this one: delete that one first.")
			return
		}
		if retained {
			problem(w, 409, "This month's close is under legal hold or kept until a date still to come: its files are kept with it.")
			return
		}
		for _, q := range []string{`DELETE FROM books_layouts WHERE import_id::text=$1`, `DELETE FROM books_imports WHERE id::text=$1`} {
			if _, err = tx.ExecContext(r.Context(), q, id); err != nil {
				problem(w, 503, "The file could not be deleted.")
				return
			}
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO books_import_deletions(organization_id,client_entity,period,name,digest,reason,actor) VALUES($1,$2,$3,$4,$5,$6,$7)`, org, name, period, filename, digest, in.Reason, reviewerName(r)); err != nil || tx.Commit() != nil {
			problem(w, 503, "The file could not be deleted.")
			return
		}
		jsonReply(w, 200, map[string]bool{"deleted": true})
	})
	// One confirmed layout, with what it read: each version stays readable, its exit included.
	mux.HandleFunc("GET /v1/closes/client/layout", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT to_jsonb(l) FROM books_layouts l WHERE l.id::text=$1 AND l.organization_id=$2 AND client_identity(l.organization_id,l.client_entity)=client_identity($2,$3)`,
			r.URL.Query().Get("id"), organization(r), name).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "Layout not found.")
			return
		}
		if err != nil {
			problem(w, 503, "The layout is unavailable.")
			return
		}
		jsonReply(w, 200, json.RawMessage(raw))
	})
	// The file as it was sent, byte for byte: the firm's to download at any time, its exit included.
	mux.HandleFunc("GET /v1/closes/client/import/file", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		var filename, format string
		var content []byte
		err := db.QueryRowContext(r.Context(), `SELECT name,format,content FROM books_imports WHERE id::text=$1 AND organization_id=$2 AND client_identity(organization_id,client_entity)=client_identity($2,$3)`,
			r.URL.Query().Get("id"), organization(r), name).Scan(&filename, &format, &content)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		}
		if err != nil {
			problem(w, 503, "The file is unavailable.")
			return
		}
		contentType := "text/csv"
		if format == "xlsx" {
			contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Write(content)
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
			// LinesFrom asks a preview for its lines from that one (0-based), two hundred at a time.
			LinesFrom int `json:"lines_from"`
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
		// With no choice of its own, a CSV is read as the reader shows it: as its layout was confirmed, else as the
		// client's last file was.
		ownLayout, err := latestLayout(r.Context(), db, org, name, r.URL.Query().Get("id"))
		var clientLayout *confirmedRead
		if err == nil && ownLayout == nil {
			clientLayout, err = latestLayout(r.Context(), db, org, name, "")
		}
		if err != nil {
			problem(w, 503, "The file's layout is unavailable.")
			return
		}
		format, err := importFormat(r.Context(), db, org, name, r.URL.Query().Get("id"))
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		}
		if err != nil {
			problem(w, 503, "The file is unavailable.")
			return
		}
		if refusal := askedChoice(format, in.CSV); refusal != "" {
			problem(w, 422, refusal)
			return
		}
		// A preview's further lines come from the preview kept when it was read, while it was read the same way with the
		// same layout, without reading the file again.
		sent, _ := json.Marshal(in.Layout)
		sum := sha256.Sum256([]byte(parseKey(format, in.CSV, ownLayout, clientLayout) + "\n" + string(sent)))
		digest := hex.EncodeToString(sum[:])
		if in.Action == "preview" && in.LinesFrom > 0 {
			out, kept, err := storedPreview(r.Context(), db, org, r.URL.Query().Get("id"), digest, in.LinesFrom)
			if err != nil {
				problem(w, 503, "The preview is unavailable.")
				return
			}
			if kept {
				jsonReply(w, 200, out)
				return
			}
		}
		summary, file, err := readImportAs(r.Context(), db, org, name, r.URL.Query().Get("id"), in.CSV, ownLayout, clientLayout)
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
		// A reading answers with its totals, problems and notes, and two hundred of its lines (from lines_from), with how
		// many it has and how many are accounts: a file's thousands of lines are not sent at once.
		brief := reading
		accounts := 0
		for _, l := range reading.Lines {
			if l.Kind == bookfiles.Posting {
				accounts++
			}
		}
		from := min(max(in.LinesFrom, 0), len(reading.Lines))
		brief.Lines = reading.Lines[from:min(from+200, len(reading.Lines))]
		if in.Action == "preview" {
			if err = keepPreview(r.Context(), db, org, summary.ID, digest, reading); err != nil {
				problem(w, 503, "The preview could not be kept.")
				return
			}
			jsonReply(w, 200, map[string]any{"reading": brief, "line_count": len(reading.Lines), "accounts": accounts, "lines_from": from})
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
		jsonReply(w, 200, map[string]any{"id": id, "reading": brief, "line_count": len(reading.Lines), "accounts": accounts})
	})
}

// keepPreview keeps reading as import id's latest preview, under digest, in place of any earlier one.
func keepPreview(ctx context.Context, db *sql.DB, org, id, digest string, reading bookfiles.Reading) error {
	raw, err := json.Marshal(reading)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM books_import_previews WHERE import_id=$1::bigint`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO books_import_previews(import_id,organization_id,digest,reading) VALUES($1::bigint,$2,$3,$4::jsonb) ON CONFLICT DO NOTHING`, id, org, digest, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

// storedPreview is import id's kept preview under digest, as a preview answers: its totals, problems and notes, two
// hundred of its lines from from (0-based), how many lines it has and how many are accounts; false when none is kept.
func storedPreview(ctx context.Context, db *sql.DB, org, id, digest string, from int) (map[string]any, bool, error) {
	var brief, lines []byte
	var count, accounts int
	err := db.QueryRowContext(ctx, `SELECT p.reading-'lines',COALESCE(jsonb_array_length(p.reading->'lines'),0),
 (SELECT count(*) FROM jsonb_array_elements(COALESCE(p.reading->'lines','[]'::jsonb)) e WHERE e->>'kind'='posting'),
 COALESCE((SELECT jsonb_agg(e ORDER BY n) FROM jsonb_array_elements(COALESCE(p.reading->'lines','[]'::jsonb)) WITH ORDINALITY t(e,n) WHERE n>$4 AND n<=$4+200),'[]'::jsonb)
 FROM books_import_previews p WHERE p.import_id::text=$1 AND p.organization_id=$2 AND p.digest=$3`, id, org, digest, from).Scan(&brief, &count, &accounts, &lines)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var reading bookfiles.Reading
	if err = json.Unmarshal(brief, &reading); err != nil {
		return nil, false, err
	}
	if err = json.Unmarshal(lines, &reading.Lines); err != nil {
		return nil, false, err
	}
	return map[string]any{"reading": reading, "line_count": count, "accounts": accounts, "lines_from": min(from, count)}, true, nil
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
