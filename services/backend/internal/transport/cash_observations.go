package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Current cash on files (docs/books-sources.md, Open items and cash dates; delivery step 11): a reviewer records a files
// month's cash at a recent date, as book cash, or as bank balances bridged to book cash by their outstanding items, each
// with its evidence. The newest at or after the date the month's books are as of opens its cash plan instead of the
// month-end file: the plan's open items must then be as of the same date. It is never a month-end reconciliation.

type cashAccount struct {
	Name    string `json:"name"`
	Balance string `json:"balance"`
}

type cashBridgeItem struct {
	Account     string `json:"account"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
	Evidence    string `json:"evidence"`
}

// cashObservation is close id's newest observation of cash recorded against its books version and as of from or later:
// its id, date, and its accounts as a capture's directory of bank accounts at their book balances (a bank balance with
// its bridge's items added); none without one.
func cashObservation(ctx context.Context, db *sql.DB, closeID, version, from string) (id, asOf string, directory []byte, err error) {
	var accountsRaw, bridgeRaw []byte
	switch err = db.QueryRowContext(ctx, `SELECT id::text,as_of::text,accounts,bridge FROM cash_observations WHERE close_id::text=$1 AND books_version=$2 AND as_of>=$3::date ORDER BY id DESC LIMIT 1`, closeID, version, from).Scan(&id, &asOf, &accountsRaw, &bridgeRaw); {
	case err == sql.ErrNoRows:
		return "", "", nil, nil
	case err != nil:
		return "", "", nil, err
	}
	var accounts []cashAccount
	var bridge []cashBridgeItem
	if err = json.Unmarshal(accountsRaw, &accounts); err == nil {
		err = json.Unmarshal(bridgeRaw, &bridge)
	}
	if err != nil {
		return "", "", nil, err
	}
	entries := []map[string]any{}
	for i, a := range accounts {
		balance, _ := new(big.Rat).SetString(a.Balance)
		for _, item := range bridge {
			if v, ok := new(big.Rat).SetString(item.Amount); ok && strings.EqualFold(item.Account, a.Name) {
				balance.Add(balance, v)
			}
		}
		entries = append(entries, map[string]any{"Id": fmt.Sprintf("cash:%s:%d", id, i), "Name": a.Name, "AccountType": "Bank", "CurrentBalance": json.Number(balance.FloatString(2))})
	}
	directory, _ = json.Marshal(entries)
	return id, asOf, directory, nil
}

func registerCashObservations(mux *http.ServeMux, db *sql.DB) {
	// The month's observations of cash, newest first; the bank accounts of its books, to observe; whether the reader may
	// record one.
	mux.HandleFunc("GET /v1/closes/{id}/cash-observations", func(w http.ResponseWriter, r *http.Request) {
		files, b, month, _, _, err := filesCash(r.Context(), db, r.PathValue("id"))
		var list json.RawMessage
		if err == nil {
			version := ""
			if month != nil {
				version = month.Version
			}
			err = db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'as_of',as_of,'kind',kind,'accounts',accounts,'bridge',bridge,'book_cash',book_cash::text,'note',note,'actor',actor,'created_at',created_at,'current',books_version=$2) ORDER BY id DESC),'[]') FROM cash_observations WHERE close_id::text=$1`, r.PathValue("id"), version).Scan(&list)
		}
		if err != nil {
			problem(w, 503, "The cash observations are unavailable.")
			return
		}
		banks, from := []string{}, ""
		if b != nil {
			for key, a := range b.Accounts {
				if a.Type == "Bank" && b.Balances[key] != nil {
					banks = append(banks, a.Name)
				}
			}
			from = month.AsOf
		}
		jsonReply(w, 200, map[string]any{"on": files && b != nil, "observations": list, "banks": banks, "from": from, "can_record": canReview(r) && files && b != nil})
	})
	// A reviewer records the month's cash at a date on or after its books': book balances, or bank balances and the
	// outstanding items that bridge them to book cash, saying where they come from.
	mux.HandleFunc("POST /v1/closes/{id}/cash-observations", func(w http.ResponseWriter, r *http.Request) {
		if !canReview(r) {
			problem(w, 403, "Recording cash requires workpapers:review.")
			return
		}
		var in struct {
			Kind      string           `json:"kind"`
			AsOf      string           `json:"as_of"`
			Accounts  []cashAccount    `json:"accounts"`
			Bridge    []cashBridgeItem `json:"bridge"`
			Note      string           `json:"note"`
			Confirmed bool             `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Note = strings.TrimSpace(in.Note)
		if !in.Confirmed || (in.Kind != "book" && in.Kind != "bank") || in.Note == "" || utf8.RuneCountInString(in.Note) > 2000 || len(in.Accounts) == 0 || len(in.Accounts) > 50 || len(in.Bridge) > 200 || (in.Kind == "book" && len(in.Bridge) > 0) {
			problem(w, 422, "Confirm book or bank balances for 1–50 accounts, a bridge only for bank balances, and where they come from.")
			return
		}
		if _, err := time.Parse("2006-01-02", in.AsOf); err != nil {
			problem(w, 422, "Give the date the balances are as of, as YYYY-MM-DD.")
			return
		}
		// Amounts are whole cents, so the cash recorded and the plan's agree to the cent.
		cents := func(text string) (*big.Rat, bool) {
			v, ok := new(big.Rat).SetString(strings.TrimSpace(text))
			return v, ok && new(big.Rat).Mul(v, big.NewRat(100, 1)).IsInt()
		}
		total, seen := new(big.Rat), map[string]bool{}
		for _, a := range in.Accounts {
			v, ok := cents(a.Balance)
			name := strings.ToLower(strings.TrimSpace(a.Name))
			if !ok || name == "" || seen[name] {
				problem(w, 422, "Give each account once, by name, with its balance in currency units to the cent.")
				return
			}
			seen[name] = true
			total.Add(total, v)
		}
		for _, item := range in.Bridge {
			v, ok := cents(item.Amount)
			if !ok || !seen[strings.ToLower(strings.TrimSpace(item.Account))] || strings.TrimSpace(item.Description) == "" || strings.TrimSpace(item.Evidence) == "" {
				problem(w, 422, "Give each outstanding item one of the accounts, a description, its amount (a deposit in transit positive, an uncleared check negative) and its evidence.")
				return
			}
			total.Add(total, v)
		}
		files, b, month, _, _, err := filesCash(r.Context(), db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "The cash observation could not be kept.")
			return
		}
		if !files || b == nil {
			problem(w, 409, "Cash is recorded here only for a month whose books are its files, once its file is accepted.")
			return
		}
		if in.AsOf < month.AsOf {
			problem(w, 422, "The balances are older than the month's books, which are as of "+month.AsOf+".")
			return
		}
		// Every bank account of the month's books is observed, or the plan's opening would leave one out.
		for key, a := range b.Accounts {
			if a.Type == "Bank" && b.Balances[key] != nil && !seen[strings.ToLower(strings.TrimSpace(a.Name))] {
				problem(w, 422, "Give a balance for each of the books' bank accounts: "+a.Name+" has none.")
				return
			}
		}
		accounts, _ := json.Marshal(in.Accounts)
		bridge, _ := json.Marshal(append([]cashBridgeItem{}, in.Bridge...))
		var id string
		if err = db.QueryRowContext(r.Context(), `INSERT INTO cash_observations(close_id,books_version,as_of,kind,accounts,bridge,book_cash,note,actor) VALUES($1::bigint,$2,$3::date,$4,$5,$6,$7::numeric,$8,$9) RETURNING id::text`,
			r.PathValue("id"), month.Version, in.AsOf, in.Kind, string(accounts), string(bridge), total.FloatString(2), in.Note, reviewerName(r)).Scan(&id); err != nil {
			problem(w, 503, "The cash observation could not be kept.")
			return
		}
		jsonReply(w, 201, map[string]string{"id": id, "book_cash": total.FloatString(2)})
	})
}
