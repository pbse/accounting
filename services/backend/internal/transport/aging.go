package transport

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

// agingNote says what the balances are, so no one reads them as a month-end ledger.
const agingNote = "Balances at the capture time, aged against the close's month-end. A document paid after month-end but before the capture is not shown, and a partly paid one shows its later balance. Payments and credits are listed apart from the aging: what they settle is not known. In QuickBooks, unused payments and credits older than a year before month-end are not read."

// waiting is why there is nothing to age yet.
type waiting string

func (w waiting) Error() string { return string(w) }

// latestAging reads the open items of the close's latest completed transaction capture on its reading connection, or
// of its newer Xero snapshot.
func latestAging(r *http.Request, db *sql.DB) (receivables, payables []quickbooks.AgingParty, meta map[string]any, err error) {
	books, err := latestXeroBooks(r.Context(), db, r.PathValue("id"))
	if err != nil {
		return nil, nil, nil, err
	}
	if books != nil {
		if books.Unread != "" {
			return nil, nil, nil, waiting(books.Unread)
		}
		monthEnd, _ := time.Parse("2006-01-02", books.MonthEnd)
		receivables, payables, unreadable := quickbooks.Aging(books.Open, monthEnd, books.Home)
		for _, party := range append(slices.Clone(receivables), payables...) {
			unreadable += party.Unresolved
		}
		return receivables, payables, map[string]any{"captured_at": books.CapturedAt, "month_end": books.MonthEnd, "complete": unreadable == 0, "completeness": "", "note": agingNote, "buckets": quickbooks.AgingBuckets}, nil
	}
	var raw []byte
	var captured, completeness, period, home string
	err = db.QueryRowContext(r.Context(), `SELECT COALESCE(s.payload->'open_items','{}'::jsonb),COALESCE(s.payload->>'finished_at',''),COALESCE(s.payload->>'completeness',''),c.period,COALESCE((SELECT home_currency FROM quickbooks_connections WHERE id=s.connection_id),'') FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id
 WHERE s.close_id::text=$1 AND s.period=c.period AND s.state IN ('completed','unchanged') AND s.payload ? 'open_items' AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+` ORDER BY s.id DESC LIMIT 1`, r.PathValue("id")).Scan(&raw, &captured, &completeness, &period, &home)
	if err == sql.ErrNoRows {
		return nil, nil, nil, waiting("Capture this month's QuickBooks transactions or Xero books first; the aging uses the open items that capture reads.")
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var open map[string][]json.RawMessage
	json.Unmarshal(raw, &open)
	month, _ := time.Parse("2006-01", period)
	monthEnd := month.AddDate(0, 1, -1)
	receivables, payables, unreadable := quickbooks.Aging(open, monthEnd, home)
	// A type the capture could not read, or a record or balance the aging could not read, makes every total incomplete.
	for _, party := range append(slices.Clone(receivables), payables...) {
		unreadable += party.Unresolved
	}
	complete := !strings.Contains(completeness, "Open items were not fully read") && unreadable == 0
	return receivables, payables, map[string]any{"captured_at": captured, "month_end": monthEnd.Format("2006-01-02"), "complete": complete, "completeness": completeness, "note": agingNote, "buckets": quickbooks.AgingBuckets}, nil
}

func registerAging(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/aging", func(w http.ResponseWriter, r *http.Request) {
		receivables, payables, meta, err := latestAging(r, db)
		var wait waiting
		if errors.As(err, &wait) {
			jsonReply(w, 200, map[string]any{"state": "waiting", "reason": string(wait)})
			return
		}
		if err != nil {
			problem(w, 503, "Aging unavailable.")
			return
		}
		meta["state"], meta["receivables"], meta["payables"] = "ready", receivables, payables
		jsonReply(w, 200, meta)
	})
	// A statement is a download for the firm to use through its own channel; nothing is sent or changed.
	mux.HandleFunc("GET /v1/closes/{id}/aging/statement", func(w http.ResponseWriter, r *http.Request) {
		receivables, _, meta, err := latestAging(r, db)
		var wait waiting
		if errors.As(err, &wait) {
			problem(w, 404, string(wait))
			return
		}
		if err != nil {
			problem(w, 503, "Statement unavailable.")
			return
		}
		var entity string
		if err = db.QueryRowContext(r.Context(), `SELECT entity FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&entity); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		customer, currency := r.URL.Query().Get("customer"), r.URL.Query().Get("currency")
		for _, party := range receivables {
			if customer == "" || party.ID != customer || party.Currency != currency {
				continue
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="statement.csv"`)
			out := csv.NewWriter(w)
			basis := "Balances at capture " + meta["captured_at"].(string) + ", aged against month-end " + meta["month_end"].(string)
			if !meta["complete"].(bool) {
				basis += ". Incomplete: some open items could not be read, so this statement may be missing documents"
			}
			out.Write([]string{"Statement for", csvText(party.Name), "from", csvText(entity)})
			out.Write([]string{"Basis", basis})
			out.Write([]string{"Currency", csvText(party.Currency)})
			out.Write([]string{"Document", "Number", "Date", "Due", "Days past due", "Open balance", "Note"})
			for _, line := range party.Lines {
				days := ""
				if line.Days != nil {
					days = strconv.Itoa(*line.Days)
				}
				number := line.Number
				if number == "" {
					number = line.ID
				}
				out.Write([]string{line.Kind, csvText(number), csvText(line.Date), csvText(line.Due), days, line.Balance, line.Problem})
			}
			out.Write([]string{"Open invoices", "", "", "", "", party.Open})
			out.Write([]string{"Unused payments and credits (not applied to any invoice here)", "", "", "", "", party.Credits})
			out.Flush()
			return
		}
		problem(w, 404, "No open items for that customer and currency in the latest capture.")
	})
}
