package transport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/bookfiles"
	"close.local/backend/internal/quickbooks"
)

// Open items by file (docs/books-sources.md, Open items and cash dates; delivery step 11): a month whose books are its
// files reads its open invoices and bills from an export the client keeps, one file per side, true at its as-of date.
// A reviewer sends it with how its columns read (proposed from their headings); the newest file of each side is the
// month's open items, given to the aging as a ledger's capture would give them, so open items, collections and their
// decisions follow. A decision is kept by document (its party and number), so it carries over to the next export.

// filesAging is the aging of close id's newest open-items file of each side.
func filesAging(ctx context.Context, db *sql.DB, closeID string) (receivables, payables []quickbooks.AgingParty, meta map[string]any, err error) {
	var company, period string
	if err = db.QueryRowContext(ctx, `SELECT 'files:'||client_identity(organization_id,entity),period FROM client_closes WHERE id::text=$1`, closeID).Scan(&company, &period); err != nil {
		return nil, nil, nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT ON (side) id::text,side,as_of::text,items,jsonb_array_length(problems) FROM open_item_files WHERE close_id::text=$1 ORDER BY side,id DESC`, closeID)
	if err != nil {
		return nil, nil, nil, err
	}
	open := map[string][]json.RawMessage{}
	var ids, sides []string
	asOf, problems, observed, sideProblems := "", 0, map[string]string{}, map[string]int{}
	sideAsOf, sideOpen, sideCurrencies := map[string]string{}, map[string]*big.Rat{}, map[string][]string{}
	for rows.Next() {
		var id, side, at string
		var raw []byte
		var n int
		if err = rows.Scan(&id, &side, &at, &raw, &n); err != nil {
			break
		}
		var items []bookfiles.OpenItem
		if err = json.Unmarshal(raw, &items); err != nil {
			break
		}
		ids, sides, problems = append(ids, id), append(sides, side), problems+n
		if asOf == "" || at < asOf {
			asOf = at
		}
		observed[map[string]string{"receivable": "Invoice", "payable": "Bill"}[side]] = at + "T23:59:59Z"
		sideProblems[side], sideAsOf[side], sideOpen[side] = n, at, new(big.Rat)
		for _, item := range items {
			if v, ok := new(big.Rat).SetString(item.Open); ok {
				sideOpen[side].Add(sideOpen[side], v)
			}
			if c := strings.ToUpper(strings.TrimSpace(item.Currency)); c != "" && !slices.Contains(sideCurrencies[side], c) {
				sideCurrencies[side] = append(sideCurrencies[side], c)
			}
		}
		for _, item := range items {
			kind, credit := map[string][2]string{"receivable": {"Invoice", "CreditMemo"}, "payable": {"Bill", "VendorCredit"}}[side], false
			balance, _ := new(big.Rat).SetString(item.Open)
			if balance != nil && balance.Sign() < 0 {
				credit = true
				balance.Neg(balance)
			}
			party := map[string]string{"value": strings.ToLower(strings.Join(strings.Fields(item.Party), " ")), "name": item.Party}
			doc := map[string]any{"Id": item.Key(side), "DocNumber": item.Document, "TxnDate": item.Date, "DueDate": item.Due, "CurrencyRef": map[string]string{"value": item.Currency}}
			amount := item.Open
			if balance != nil {
				amount = balance.FloatString(2)
			}
			switch {
			case side == "receivable" && credit:
				doc["RemainingCredit"], doc["CustomerRef"] = amount, party
			case side == "receivable":
				doc["Balance"], doc["CustomerRef"] = amount, party
			default:
				doc["Balance"], doc["VendorRef"] = amount, party
			}
			name := kind[0]
			if credit {
				name = kind[1]
			}
			raw, _ := json.Marshal(doc)
			open[name] = append(open[name], raw)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	if len(ids) == 0 {
		return nil, nil, nil, waiting("Send this month's open receivables and payables as files first; the aging uses the documents they hold.")
	}
	home := ""
	b, books, err := fileMonthBooks(ctx, db, closeID)
	if err != nil {
		return nil, nil, nil, err
	}
	if b != nil {
		home = b.Currency
	}
	month, _ := time.Parse("2006-01", period)
	monthEnd := month.AddDate(0, 1, -1)
	receivables, payables, unreadable := quickbooks.Aging(open, monthEnd, home)
	// Each side is complete on its own: its file sent and every row and document of it read, so a document of that side
	// missing from it is settled whatever the other side's file says.
	completeBy := map[string]bool{}
	for kind, parties := range map[string][]quickbooks.AgingParty{"Invoice": receivables, "Bill": payables} {
		side := map[string]string{"Invoice": "receivable", "Bill": "payable"}[kind]
		_, sent := observed[kind]
		unresolved := 0
		for _, p := range parties {
			unresolved += p.Unresolved
		}
		completeBy[kind] = sent && sideProblems[side] == 0 && unresolved == 0
	}
	// Accrual books: a side's open items as of the month's end reconcile to its control accounts in the month's books
	// (receivables, debit; payables, credit); a difference holds that side's open items, not the close. Cash-basis books
	// carry no receivables or payables: the list is the client's operational one, outside the books.
	held, outside := []string{}, ""
	if b != nil && books.Basis == "accrual" {
		for _, side := range []string{"receivable", "payable"} {
			if sideAsOf[side] != monthEnd.Format("2006-01-02") {
				continue
			}
			kind, control, sign := map[string]string{"receivable": "Accounts Receivable", "payable": "Accounts Payable"}[side], new(big.Rat), int64(1)
			if side == "payable" {
				sign = -1
			}
			for key, a := range b.Accounts {
				if a.Type == kind && b.Balances[key] != nil {
					control.Add(control, new(big.Rat).Mul(b.Balances[key], big.NewRat(sign, 1)))
				}
			}
			// Amounts in another currency than the books' cannot be added to theirs: the side is held, not compared.
			if foreign := slices.DeleteFunc(slices.Clone(sideCurrencies[side]), func(c string) bool { return strings.EqualFold(c, home) }); len(foreign) > 0 {
				held = append(held, fmt.Sprintf("The open %ss include documents in %s, which cannot be reconciled to the books' %s accounts in %s.", side, strings.Join(foreign, ", "), strings.ToLower(kind), home))
				completeBy[map[string]string{"receivable": "Invoice", "payable": "Bill"}[side]] = false
			} else if control.Cmp(sideOpen[side]) != 0 {
				difference := new(big.Rat).Sub(control, sideOpen[side])
				held = append(held, fmt.Sprintf("The open %ss total %s, but the books' %s accounts hold %s at the month's end, a difference of %s: reconcile them before relying on these %ss.",
					side, sideOpen[side].FloatString(2), strings.ToLower(kind), control.FloatString(2), difference.FloatString(2), side))
				completeBy[map[string]string{"receivable": "Invoice", "payable": "Bill"}[side]] = false
			}
		}
	} else if b != nil {
		outside = "The books are on a " + strings.ReplaceAll(books.Basis, "_", " ") + " basis, which carries no receivables or payables: these open items are the client's operational list, outside the books."
	}
	note := filesAgingNote
	if outside != "" {
		note = outside + " " + note
	}
	completeness := ""
	if len(sides) < 2 {
		completeness = "Only the open " + sides[0] + "s were sent for this month as a file."
	}
	if problems > 0 {
		completeness = strings.TrimSpace(completeness + " Some rows of the files could not be read.")
	}
	if len(held) > 0 {
		completeness = strings.TrimSpace(completeness + " " + strings.Join(held, " "))
	}
	// The files' open items were true at the end of their earliest as-of date, as a capture's are at its time.
	return receivables, payables, map[string]any{"source": "files", "company": company, "capture_id": "files:" + strings.Join(ids, "+"), "captured_at": asOf + "T23:59:59Z", "month_end": monthEnd.Format("2006-01-02"),
		"complete": unreadable == 0 && problems == 0 && len(sides) == 2 && len(held) == 0, "completeness": completeness, "note": note, "buckets": quickbooks.AgingBuckets, "observed": observed, "complete_by_kind": completeBy, "outside": outside}, nil
}

const filesAgingNote = "Balances as of each file's as-of date, aged against the close's month-end. A document paid after month-end but before that date is not shown."

func registerOpenItemFiles(mux *http.ServeMux, db *sql.DB) {
	// The month's open-items files, newest first, and whether the reader may send one.
	mux.HandleFunc("GET /v1/closes/{id}/open-item-files", func(w http.ResponseWriter, r *http.Request) {
		var on bool
		var files json.RawMessage
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT o.file_imports FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id::text=$1),false),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id::text,'side',side,'as_of',as_of,'name',name,'documents',jsonb_array_length(items),'problems',problems,'actor',actor,'created_at',created_at) ORDER BY id DESC) FROM open_item_files WHERE close_id::text=$1),'[]')`, r.PathValue("id")).Scan(&on, &files)
		var source string
		if err == nil {
			source, err = closeBooksSource(r.Context(), db, r.PathValue("id"))
		}
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "The open-items files are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"files": files, "on": on && source == "files", "can_send": canReview(r) && on && source == "files"})
	})
	// A reviewer sends a side's open items: without a layout, the reading proposed for it; with one, its reading, and,
	// confirmed, the file kept as the month's open items of that side.
	mux.HandleFunc("POST /v1/closes/{id}/open-item-files", func(w http.ResponseWriter, r *http.Request) {
		if !canReview(r) {
			problem(w, 403, "Sending open items requires workpapers:review.")
			return
		}
		allowUpload(w)
		r.Body = http.MaxBytesReader(w, r.Body, bookfiles.MaxFile+(1<<20))
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			problem(w, 413, "Send one file of 50 MiB or less.")
			return
		}
		side, asOf := r.FormValue("side"), r.FormValue("as_of")
		if side != "receivable" && side != "payable" {
			problem(w, 422, "Say whether the file holds open receivables or payables.")
			return
		}
		if _, err := time.Parse("2006-01-02", asOf); err != nil {
			problem(w, 422, "Give the date the file's open items were true at, as YYYY-MM-DD.")
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
		name := strings.TrimSpace(header.Filename)
		if name == "" || utf8.RuneCountInString(name) > 255 {
			name = "open items"
		}
		read, err := bookfiles.Read(name, body)
		if err != nil {
			problem(w, 422, "The file cannot be read: "+err.Error()+".")
			return
		}
		// Without a layout, one is proposed: at the headings a person chose (a table and its row), else where the headings
		// name a number and an amount; a file with neither still opens, for a person to choose its headings.
		var layout bookfiles.OpenItemsLayout
		proposed := r.FormValue("layout") == ""
		if proposed {
			var ok bool
			if table, row := r.FormValue("table"), r.FormValue("header_row"); table != "" {
				n, _ := strconv.Atoi(row)
				layout, ok = bookfiles.ProposeOpenItemsAt(read, table, n)
			} else {
				layout, ok = bookfiles.ProposeOpenItems(read)
			}
			if !ok && len(read.Tables) > 0 {
				layout = bookfiles.OpenItemsLayout{Table: read.Tables[0].Name, Columns: map[int]bookfiles.Role{}, Style: bookfiles.NumberStyle{Scale: 1}}
			}
		} else if err = json.Unmarshal([]byte(r.FormValue("layout")), &layout); err != nil || !slices.Contains([]int64{0, 1, 1000, 1000000}, layout.Style.Scale) {
			problem(w, 422, "The layout cannot be read: amounts are in units, thousands (1000) or millions (1000000).")
			return
		} else {
			// The amounts' style is read from the column the layout gives the open amount, which a person may have changed.
			layout = bookfiles.RestyleOpenItems(read, layout)
		}
		items, problems := bookfiles.ReadOpenItems(read, layout)
		// The headings read by, and each table's first rows, any of which a person may say holds the headings.
		var headings []bookfiles.Cell
		rows := []map[string]any{}
		for _, t := range read.Tables {
			for i, row := range t.Rows {
				if t.Name == layout.Table && row.Number == layout.HeaderRow {
					headings = row.Cells
				}
				if i < 30 {
					var texts []string
					for _, c := range row.Cells {
						if v := strings.TrimSpace(c.Value); v != "" {
							texts = append(texts, v)
						}
					}
					if text := strings.Join(texts, " · "); text != "" {
						rows = append(rows, map[string]any{"table": t.Name, "number": row.Number, "text": text[:min(len(text), 120)]})
					}
				}
			}
		}
		total := new(big.Rat)
		for _, item := range items {
			if v, ok := new(big.Rat).SetString(item.Open); ok {
				total.Add(total, v)
			}
		}
		preview := append([]bookfiles.OpenItem{}, items...)
		if len(preview) > 20 {
			preview = preview[:20]
		}
		// A layout the file cannot be read by gives no documents at all, which is not none open: it cannot be kept.
		out := map[string]any{"layout": layout, "headings": headings, "roles": bookfiles.Roles, "documents": len(items), "total": total.FloatString(2), "preview": preview, "problems": problems, "usable": items != nil, "rows": rows}
		if proposed || r.FormValue("confirmed") != "true" {
			jsonReply(w, 200, out)
			return
		}
		if items == nil {
			problem(w, 422, strings.Join(problems, " "))
			return
		}
		// Each document is named by its party and number (or, unnumbered, its dates and amount): two the name cannot tell
		// apart would share every decision, so the file is not kept until they are told apart.
		seen := map[string]int{}
		for _, item := range items {
			if row, twice := seen[item.Key(side)]; twice {
				problem(w, 422, fmt.Sprintf("Rows %d and %d are one document twice, or two documents nothing in the file tells apart: export the documents' numbers, or remove one.", row, item.Row))
				return
			}
			seen[item.Key(side)] = item.Row
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The open items could not be kept.")
			return
		}
		defer tx.Rollback()
		var on bool
		if err = tx.QueryRowContext(r.Context(), `SELECT o.file_imports FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id::text=$1 FOR UPDATE OF c`, r.PathValue("id")).Scan(&on); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		source, err := closeBooksSource(r.Context(), tx, r.PathValue("id"))
		if err != nil || !on || source != "files" {
			problem(w, 409, "Open items are sent as files only for a month whose books are its files, in a firm with file imports on.")
			return
		}
		sum := sha256.Sum256(body)
		rawLayout, _ := json.Marshal(layout)
		rawItems, _ := json.Marshal(items)
		rawProblems, _ := json.Marshal(problems)
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO open_item_files(close_id,side,as_of,name,digest,content,layout,items,problems,actor) VALUES($1::bigint,$2,$3::date,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`,
			r.PathValue("id"), side, asOf, name, hex.EncodeToString(sum[:]), body, string(rawLayout), string(rawItems), string(rawProblems), reviewerName(r)).Scan(&id); err != nil {
			problem(w, 503, "The open items could not be kept.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "The open items could not be kept.")
			return
		}
		out["id"] = id
		jsonReply(w, 201, out)
	})
}
