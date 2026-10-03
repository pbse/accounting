package transport

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

var centsText = regexp.MustCompile(`^[0-9]{1,16}(\.[0-9]{1,2})?$`)

// cashItem is one dated receipt (positive) or disbursement (negative) in the plan's currency, with where it came from.
type cashItem struct {
	On     string `json:"on"`
	Amount string `json:"amount"`
	Label  string `json:"label"`
	Source string `json:"source"`
	// Basis says why it falls on that date: due date, promised date, bill-pay date or an obligation's schedule.
	Basis string `json:"basis"`
	value *big.Rat
}

type cashWeek struct {
	Start         string     `json:"start"`
	End           string     `json:"end"`
	Opening       string     `json:"opening"`
	Receipts      string     `json:"receipts"`
	Disbursements string     `json:"disbursements"`
	Closing       string     `json:"closing"`
	Lowest        string     `json:"lowest"`
	LowestOn      string     `json:"lowest_on"`
	Items         []cashItem `json:"items"`
}

// planWeeks lays dated items over 13 weeks from start: each week's opening is the last week's closing, and its lowest
// balance is the lowest end-of-day balance in the week, so a shortfall midweek shows even when the week ends positive.
// Items dated before start or after the 13 weeks are returned apart, never placed.
func planWeeks(start time.Time, opening *big.Rat, items []cashItem) (weeks []cashWeek, outside []cashItem) {
	end := start.AddDate(0, 0, 13*7)
	byDay := map[string][]cashItem{}
	for _, item := range items {
		on, err := time.Parse("2006-01-02", item.On)
		if err != nil || on.Before(start) || !on.Before(end) {
			outside = append(outside, item)
			continue
		}
		byDay[item.On] = append(byDay[item.On], item)
	}
	balance := new(big.Rat).Set(opening)
	for w := range 13 {
		week := cashWeek{Start: start.AddDate(0, 0, 7*w).Format("2006-01-02"), End: start.AddDate(0, 0, 7*w+6).Format("2006-01-02"), Opening: balance.FloatString(2), Items: []cashItem{}}
		receipts, disbursements := new(big.Rat), new(big.Rat)
		var lowest *big.Rat
		for d := range 7 {
			day := start.AddDate(0, 0, 7*w+d).Format("2006-01-02")
			for _, item := range byDay[day] {
				balance.Add(balance, item.value)
				if item.value.Sign() > 0 {
					receipts.Add(receipts, item.value)
				} else {
					disbursements.Sub(disbursements, item.value)
				}
				week.Items = append(week.Items, item)
			}
			if lowest == nil || balance.Cmp(lowest) < 0 {
				lowest, week.LowestOn = new(big.Rat).Set(balance), day
			}
		}
		week.Receipts, week.Disbursements, week.Closing, week.Lowest = receipts.FloatString(2), disbursements.FloatString(2), balance.FloatString(2), lowest.FloatString(2)
		weeks = append(weeks, week)
	}
	slices.SortStableFunc(outside, func(a, b cashItem) int { return strings.Compare(a.On, b.On) })
	return weeks, outside
}

// occurrences are an obligation's dates from start (exclusive of earlier ones, which the opening balance already
// reflects) through until or end (exclusive): once, every week, every two weeks, or monthly on the same day, the
// month's last day when it has fewer.
func occurrences(first time.Time, every string, until *time.Time, start, end time.Time) []time.Time {
	step := 0
	switch every {
	case "week":
		step = 7
	case "two_weeks":
		step = 14
	}
	out := []time.Time{}
	for i := 0; ; i++ {
		on := first.AddDate(0, 0, step*i)
		if every == "month" {
			month := time.Date(first.Year(), first.Month()+time.Month(i), 1, 0, 0, 0, 0, time.UTC)
			on = month.AddDate(0, 0, min(first.Day(), month.AddDate(0, 1, -1).Day())-1)
		}
		if !on.Before(end) || (until != nil && on.After(*until)) || (every == "once" && i > 0) {
			return out
		}
		if !on.Before(start) {
			out = append(out, on)
		}
	}
}

type cashObligation struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Category     string     `json:"category"`
	Direction    string     `json:"direction"`
	Amount       string     `json:"amount"`
	Currency     string     `json:"currency"`
	FirstOn      string     `json:"first_on"`
	Every        string     `json:"every"`
	UntilOn      string     `json:"until_on"`
	Vendor       string     `json:"vendor"`
	Basis        string     `json:"basis"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ApprovedBy   string     `json:"approved_by"`
	ApprovedAt   *time.Time `json:"approved_at"`
	RetiredBy    string     `json:"retired_by"`
	RetiredAt    *time.Time `json:"retired_at"`
	RetireReason string     `json:"retire_reason"`
}

func clientObligations(r *http.Request, db *sql.DB, org, entity string) ([]cashObligation, error) {
	rows, err := db.QueryContext(r.Context(), `SELECT id::text,name,category,direction,amount::text,currency,first_on::text,every,COALESCE(until_on::text,''),vendor,basis,created_by,created_at,COALESCE(approved_by,''),approved_at,COALESCE(retired_by,''),retired_at,COALESCE(retire_reason,'')
 FROM cash_obligations WHERE organization_id=$1 AND lower(client_entity)=lower($2) ORDER BY (retired_at IS NULL) DESC,id DESC`, org, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cashObligation{}
	for rows.Next() {
		var o cashObligation
		if err := rows.Scan(&o.ID, &o.Name, &o.Category, &o.Direction, &o.Amount, &o.Currency, &o.FirstOn, &o.Every, &o.UntilOn, &o.Vendor, &o.Basis, &o.CreatedBy, &o.CreatedAt, &o.ApprovedBy, &o.ApprovedAt, &o.RetiredBy, &o.RetiredAt, &o.RetireReason); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// cashSchedule is when an open invoice or bill is expected to move cash, from people's decisions about it: a promise or
// a bill-pay handoff gives its date, otherwise the due date; a dispute, a hold, an unreadable balance or a missing due
// date leaves it unscheduled with the reason.
func cashSchedule(item openItem) (on, basis, unscheduled string) {
	a := item.Action
	switch {
	case item.Balance == "":
		return "", "", "Its balance could not be read."
	case a != nil && a.Status == "promised":
		return a.ExpectedOn, "promised date", ""
	case a != nil && a.Status == "handed_to_bill_pay":
		return a.ExpectedOn, "bill-pay date", ""
	case a != nil && (a.Status == "disputed" || a.Status == "on_hold"):
		return "", "", "It is " + strings.ReplaceAll(a.Status, "_", " ") + ", so no date is assumed."
	case item.Due == "":
		return "", "", "It has no due date and no agreed date."
	}
	return item.Due, "due date", ""
}

func registerCashPlan(mux *http.ServeMux, db *sql.DB) {
	client := func(r *http.Request) (org, entity string, err error) {
		err = db.QueryRowContext(r.Context(), `SELECT organization_id,entity FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&org, &entity)
		return
	}
	mux.HandleFunc("GET /v1/closes/{id}/cash-plan", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		wait := func(reason string) { jsonReply(w, 200, map[string]any{"state": "waiting", "reason": reason}) }
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		// Opening book cash: the ledger's bank accounts in the close's newest QuickBooks capture, as of that capture.
		var snapshotID, home, company string
		var asOf time.Time
		var stale bool
		var accounts []byte
		err = db.QueryRowContext(ctx, `SELECT s.id::text,s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active',COALESCE(s.payload->'accounts','[]'::jsonb),COALESCE(x.home_currency,''),'quickbooks:'||x.environment||':'||x.realm FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id
 LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, r.PathValue("id")).Scan(&snapshotID, &asOf, &stale, &accounts, &home, &company)
		if err == sql.ErrNoRows {
			wait("Capture this client's QuickBooks books first: the plan opens from the ledger's bank balances. A Xero client's cash plan is not built yet.")
			return
		}
		if err != nil {
			problem(w, 503, "The cash plan is unavailable.")
			return
		}
		var directory []struct {
			ID             string `json:"Id"`
			Name           string
			AccountType    string
			CurrentBalance json.Number
			CurrencyRef    struct{ Value string }
		}
		if json.Unmarshal(accounts, &directory) != nil || home == "" {
			wait("The capture's accounts or the client's home currency could not be read.")
			return
		}
		opening, banks, foreignBanks := new(big.Rat), []map[string]string{}, []string{}
		for _, a := range directory {
			if a.AccountType != "Bank" {
				continue
			}
			balance, ok := new(big.Rat).SetString(string(a.CurrentBalance))
			if !ok || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != home) {
				foreignBanks = append(foreignBanks, a.Name)
				continue
			}
			opening.Add(opening, balance)
			banks = append(banks, map[string]string{"account_id": a.ID, "name": a.Name, "balance": balance.FloatString(2)})
		}
		receivables, payables, meta, err := latestOpenItems(r, db)
		var waitErr waiting
		if errors.As(err, &waitErr) {
			wait(string(waitErr))
			return
		}
		if err != nil {
			problem(w, 503, "The cash plan is unavailable.")
			return
		}
		if meta["company"] != company {
			wait("The latest open items come from other books than the bank balances; the plan needs both from the same QuickBooks company. Capture this month's QuickBooks transactions.")
			return
		}
		decided, err := openItemDecisions(r, db, org, entity, company)
		if err != nil {
			problem(w, 503, "The cash plan is unavailable.")
			return
		}
		asOf = asOf.UTC()
		start := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)
		day := func(on time.Time) int { return int(on.Sub(start) / (24 * time.Hour)) }
		end := start.AddDate(0, 0, 13*7)
		// Open bills by vendor and due week, before any bill-pay date moves them: an obligation's payment that week is the bill.
		billWeeks := map[string]bool{}
		items, unscheduled, otherCurrency := []cashItem{}, []map[string]string{}, []string{}
		for side, list := range [][]openItem{receivables, payables} {
			for _, item := range list {
				if d, ok := decided[item.Kind+"|"+item.ID]; ok {
					item.Action = d.Action
				}
				if item.Currency != home {
					otherCurrency = append(otherCurrency, item.Label)
					continue
				}
				if due, err := time.Parse("2006-01-02", item.Due); err == nil && side == 1 && !due.Before(start) {
					billWeeks[strings.ToLower(strings.TrimSpace(item.Party))+"|"+strconv.Itoa(day(due)/7)] = true
				}
				on, basis, reason := cashSchedule(item)
				if reason != "" {
					unscheduled = append(unscheduled, map[string]string{"label": item.Label, "amount": item.Balance, "reason": reason})
					continue
				}
				value, _ := new(big.Rat).SetString(item.Balance)
				if side == 1 {
					value.Neg(value)
				}
				items = append(items, cashItem{On: on, Amount: value.FloatString(2), Label: item.Label, Source: strings.ToLower(item.Kind), Basis: basis, value: value})
			}
		}
		obligations, err := clientObligations(r, db, org, entity)
		if err != nil {
			problem(w, 503, "The cash plan is unavailable.")
			return
		}
		// An approved obligation counts on each of its dates, except where an open bill from its named vendor already
		// falls in the same week: that bill is the payment, so the estimate is not counted twice.
		pending, covered := []cashObligation{}, []map[string]string{}
		for _, o := range obligations {
			if o.RetiredAt != nil {
				continue
			}
			if o.ApprovedAt == nil {
				pending = append(pending, o)
				continue
			}
			if o.Currency != home {
				otherCurrency = append(otherCurrency, o.Name)
				continue
			}
			first, _ := time.Parse("2006-01-02", o.FirstOn)
			var until *time.Time
			if t, err := time.Parse("2006-01-02", o.UntilOn); err == nil {
				until = &t
			}
			for _, on := range occurrences(first, o.Every, until, start, end) {
				value, _ := new(big.Rat).SetString(o.Amount)
				if o.Direction == "out" {
					value.Neg(value)
				}
				item := cashItem{On: on.Format("2006-01-02"), Amount: value.FloatString(2), Label: o.Name, Source: o.Category, Basis: "approved " + strings.ReplaceAll(o.Every, "_", " ") + " schedule", value: value}
				if o.Direction == "out" && o.Vendor != "" && billWeeks[strings.ToLower(o.Vendor)+"|"+strconv.Itoa(day(on)/7)] {
					covered = append(covered, map[string]string{"obligation": o.Name, "on": item.On, "vendor": o.Vendor})
					continue
				}
				items = append(items, item)
			}
		}
		weeks, outside := planWeeks(start, opening, items)
		before, after, first := []cashItem{}, []cashItem{}, start.Format("2006-01-02")
		for _, item := range outside {
			if item.On < first {
				before = append(before, item)
			} else {
				after = append(after, item)
			}
		}
		lowest, low := weeks[0], new(big.Rat)
		low.SetString(lowest.Lowest)
		for _, week := range weeks {
			if l, _ := new(big.Rat).SetString(week.Lowest); l.Cmp(low) < 0 {
				lowest, low = week, l
			}
		}
		openItemsAt, _ := time.Parse(time.RFC3339, meta["captured_at"].(string))
		apart := asOf.Sub(openItemsAt)
		// An executive sees approved schedules only; drafts (their amounts, vendors and notes) stay with the firm.
		var draftSchedules any = pending
		if executive(r) {
			draftSchedules = []cashObligation{}
		}
		jsonReply(w, 200, map[string]any{"state": "ready", "entity": entity, "currency": home, "start": start.Format("2006-01-02"), "unapproved_schedules": len(pending),
			"opening":    map[string]any{"amount": opening.FloatString(2), "as_of": asOf, "snapshot_id": snapshotID, "stale": stale, "accounts": banks, "left_out": foreignBanks},
			"open_items": map[string]any{"captured_at": meta["captured_at"], "capture_id": meta["capture_id"], "complete": meta["complete"], "hours_apart": int(apart.Abs().Hours()), "through": meta["month_end"], "missing_later": first > meta["month_end"].(string)},
			"weeks":      weeks, "lowest": map[string]string{"amount": lowest.Lowest, "on": lowest.LowestOn},
			"past_due": before, "after_horizon": after, "unscheduled": unscheduled, "other_currency": otherCurrency, "covered": covered, "pending_obligations": draftSchedules,
			"note": "Book cash is the ledger's bank balances at the capture, not bank-available cash. Receipts and payments are expectations from due dates, promises, bill-pay handoffs and approved schedules; nothing is paid, collected or written to the ledger. Sales not yet invoiced are not in the plan unless an approved receipt schedule adds them, so later weeks show only what is already known."})
	})
	mux.HandleFunc("GET /v1/closes/{id}/cash-obligations", func(w http.ResponseWriter, r *http.Request) {
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		list, err := clientObligations(r, db, org, entity)
		if err != nil {
			problem(w, 503, "Cash obligations are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"obligations": list})
	})
	mux.HandleFunc("POST /v1/closes/{id}/cash-obligations", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action    string `json:"action"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			Category  string `json:"category"`
			Direction string `json:"direction"`
			Amount    string `json:"amount"`
			FirstOn   string `json:"first_on"`
			Every     string `json:"every"`
			UntilOn   string `json:"until_on"`
			Vendor    string `json:"vendor"`
			Basis     string `json:"basis"`
			Reason    string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var result sql.Result
		switch in.Action {
		case "add":
			in.Name, in.Vendor, in.Basis = strings.TrimSpace(in.Name), strings.TrimSpace(in.Vendor), strings.TrimSpace(in.Basis)
			amount, ok := new(big.Rat).SetString(in.Amount)
			first, firstErr := time.Parse("2006-01-02", in.FirstOn)
			_, untilErr := time.Parse("2006-01-02", in.UntilOn)
			if in.Name == "" || len(in.Name) > 200 || !slices.Contains([]string{"payroll", "tax", "debt", "rent", "other"}, in.Category) || (in.Direction != "in" && in.Direction != "out") ||
				!ok || amount.Sign() <= 0 || !centsText.MatchString(in.Amount) ||
				firstErr != nil || first.Year() < 2000 || first.Year() > 2100 || !slices.Contains([]string{"once", "week", "two_weeks", "month"}, in.Every) ||
				(in.UntilOn != "" && (untilErr != nil || in.UntilOn < in.FirstOn)) || len(in.Vendor) > 200 || len(in.Basis) < 5 || len(in.Basis) > 2000 {
				problem(w, 422, "Give a name, a category, in or out, a positive amount in cents, a first date, how often and, optionally, an end date and the vendor whose bills pay it, with where the schedule comes from (5–2,000 characters).")
				return
			}
			// A schedule is in the client's home currency when it is added, kept with it so a later change of books cannot reread it.
			var home string
			if err = db.QueryRowContext(r.Context(), `SELECT COALESCE(x.home_currency,'') FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("$1::bigint"), r.PathValue("id")).Scan(&home); err != nil && err != sql.ErrNoRows {
				problem(w, 503, "The obligation could not be saved.")
				return
			}
			if home == "" {
				problem(w, 409, "Connect the client's QuickBooks books first, so the schedule's currency is known.")
				return
			}
			result, err = db.ExecContext(r.Context(), `INSERT INTO cash_obligations(organization_id,client_entity,name,category,direction,amount,currency,first_on,every,until_on,vendor,basis,created_by) VALUES($1,$2,$3,$4,$5,$6::numeric,$7,$8::date,$9,NULLIF($10,'')::date,$11,$12,$13)`,
				org, entity, in.Name, in.Category, in.Direction, in.Amount, home, in.FirstOn, in.Every, in.UntilOn, in.Vendor, in.Basis, who)
		case "approve":
			result, err = db.ExecContext(r.Context(), `UPDATE cash_obligations SET approved_by=$4,approved_at=now() WHERE id::text=$3 AND organization_id=$1 AND lower(client_entity)=lower($2) AND approved_at IS NULL AND retired_at IS NULL`, org, entity, in.ID, who)
		case "retire":
			if in.Reason = strings.TrimSpace(in.Reason); in.Reason == "" || len(in.Reason) > 2000 {
				problem(w, 422, "Say why the obligation no longer applies.")
				return
			}
			result, err = db.ExecContext(r.Context(), `UPDATE cash_obligations SET retired_by=$4,retired_at=now(),retire_reason=$5 WHERE id::text=$3 AND organization_id=$1 AND lower(client_entity)=lower($2) AND retired_at IS NULL`, org, entity, in.ID, who, in.Reason)
		default:
			problem(w, 422, "Add, approve or retire an obligation.")
			return
		}
		if err != nil {
			problem(w, 503, "The obligation could not be saved.")
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			problem(w, 409, "That obligation was already approved or retired. Refresh before deciding.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
	})
}
