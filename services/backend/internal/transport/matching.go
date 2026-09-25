package transport

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

const matchingNote = "Leads, not proof: a document and a captured bill or purchase pair only when theirs is the only document and the only transaction with that currency and exact amount. Documents are the prepaid invoices uploaded to this close; transactions are the bills and purchases dated in the close month. Nothing is linked or changed in the ledger."

type matchItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Number   string `json:"number,omitempty"`
	Date     string `json:"date,omitempty"`
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	cents    int64
}

type matchGroup struct {
	Currency     string      `json:"currency"`
	Amount       string      `json:"amount"`
	Documents    []matchItem `json:"documents"`
	Transactions []matchItem `json:"transactions"`
}

// match pairs by currency and exact cents only; any key with more than one item on a side is left to a person.
func match(docs, txns []matchItem) (pairs, ambiguous []matchGroup, docsLeft, txnsLeft []matchItem) {
	pairs, ambiguous, docsLeft, txnsLeft = []matchGroup{}, []matchGroup{}, []matchItem{}, []matchItem{}
	groups := map[string]*matchGroup{}
	var keys []string
	add := func(item matchItem, doc bool) {
		key := item.Currency + " " + item.Amount
		g := groups[key]
		if g == nil {
			g = &matchGroup{Currency: item.Currency, Amount: item.Amount, Documents: []matchItem{}, Transactions: []matchItem{}}
			groups[key] = g
			keys = append(keys, key)
		}
		if doc {
			g.Documents = append(g.Documents, item)
		} else {
			g.Transactions = append(g.Transactions, item)
		}
	}
	for _, d := range docs {
		add(d, true)
	}
	for _, t := range txns {
		add(t, false)
	}
	for _, key := range keys {
		g := groups[key]
		switch {
		case len(g.Documents) == 1 && len(g.Transactions) == 1:
			pairs = append(pairs, *g)
		case len(g.Documents) > 0 && len(g.Transactions) > 0:
			ambiguous = append(ambiguous, *g)
		default:
			docsLeft = append(docsLeft, g.Documents...)
			txnsLeft = append(txnsLeft, g.Transactions...)
		}
	}
	slices.SortStableFunc(txnsLeft, func(a, b matchItem) int { return cmp.Compare(b.cents, a.cents) })
	return pairs, ambiguous, docsLeft, txnsLeft
}

// matchAmount reads a plain nonnegative decimal as exact cents; anything else, grouped digits included, is unreadable.
func matchAmount(item *matchItem, raw string) bool {
	cents, err := prepaids.Amount(strings.TrimSpace(raw))
	item.cents, item.Amount = cents, prepaids.Money(cents)
	return err == nil
}

func registerMatching(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/matching", func(w http.ResponseWriter, r *http.Request) {
		books, err := latestXeroBooks(r.Context(), db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Matching unavailable.")
			return
		}
		var rows []quickbooks.TransactionRow
		var home, captured string
		if books != nil {
			if books.Unread != "" {
				jsonReply(w, 200, map[string]any{"state": "waiting", "reason": books.Unread})
				return
			}
			rows, home, captured = books.Rows, books.Home, books.CapturedAt
		} else {
			c, err := latestCapture(r.Context(), db, r.PathValue("id"))
			if err == sql.ErrNoRows {
				jsonReply(w, 200, map[string]any{"state": "waiting", "reason": "Capture this month's QuickBooks transactions or Xero books first; matching reads the bills and purchases that capture holds."})
				return
			}
			if err != nil {
				problem(w, 503, "Matching unavailable.")
				return
			}
			if rows, err = quickbooks.CapturedRows(r.Context(), db, c.RowsSync); err != nil {
				problem(w, 503, "Matching unavailable.")
				return
			}
			home, captured = c.HomeCurrency, c.Finished
		}
		var txns []matchItem
		unreadableTxns := 0
		for _, row := range rows {
			if row.Kind != "Bill" && row.Kind != "Purchase" {
				continue
			}
			var record struct {
				DocNumber string
				VendorRef struct{ Name string }
				EntityRef struct{ Name string }
			}
			json.Unmarshal(row.Record, &record)
			item := matchItem{ID: row.ProviderID, Kind: row.Kind, Name: record.VendorRef.Name + record.EntityRef.Name, Number: record.DocNumber, Date: row.Date, Currency: row.Currency}
			if item.Currency == "" {
				item.Currency = home
			}
			if !matchAmount(&item, row.Amount) {
				unreadableTxns++
				continue
			}
			txns = append(txns, item)
		}
		// Only an uploaded invoice read as prepaid work has a total, and only when that total was quoted from the invoice itself.
		docRows, err := db.QueryContext(r.Context(), `SELECT e.id::text,e.name,i.proposal FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN evidence e ON e.id=i.source_id AND e.close_id=j.close_id
 WHERE j.close_id::text=$1 AND e.superseded_by IS NULL AND i.proposal->>'kind'='prepaid' ORDER BY e.id`, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Matching unavailable.")
			return
		}
		defer docRows.Close()
		docs, unreadableDocs := []matchItem{}, []matchItem{}
		for docRows.Next() {
			item := matchItem{Kind: "document", Currency: home}
			var raw []byte
			if err = docRows.Scan(&item.ID, &item.Name, &raw); err != nil {
				break
			}
			var p preparation.Proposal
			json.Unmarshal(raw, &p)
			total := ""
			for _, f := range p.Facts {
				quoted := slices.ContainsFunc(f.Candidates, func(c preparation.Candidate) bool { return c.SourceID == item.ID })
				switch {
				case f.Field == "amount" && quoted:
					total = f.Value
				case f.Field == "currency" && f.Value != "":
					item.Currency = strings.ToUpper(strings.TrimSpace(f.Value))
				}
			}
			if total == "" || !matchAmount(&item, total) {
				item.Amount = ""
				unreadableDocs = append(unreadableDocs, item)
				continue
			}
			docs = append(docs, item)
		}
		if err != nil || docRows.Err() != nil {
			problem(w, 503, "Matching unavailable.")
			return
		}
		pairs, ambiguous, docsLeft, txnsLeft := match(docs, txns)
		total := len(txnsLeft)
		if total > 200 {
			txnsLeft = txnsLeft[:200]
		}
		jsonReply(w, 200, map[string]any{
			"state": "ready", "captured_at": captured, "note": matchingNote,
			"pairs": pairs, "ambiguous": ambiguous,
			"documents_not_in_capture": docsLeft, "documents_without_total": unreadableDocs,
			"transactions_without_document": txnsLeft, "transactions_without_document_total": total, "has_more": total > len(txnsLeft),
			"unreadable_transactions": unreadableTxns,
		})
	})
}
