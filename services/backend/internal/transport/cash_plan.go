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
	"unicode/utf8"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

var centsText = regexp.MustCompile(`^[0-9]{1,16}(\.[0-9]{1,2})?$`)

// cashItem is one dated receipt (positive) or disbursement (negative) in the plan's currency, with where it came from.
type cashItem struct {
	// Key names an open invoice or bill ("invoice:3") so a scenario can move it; Ref is the record that dates or adds
	// it: a recorded decision ("decision:12") or an approved schedule ("obligation:5").
	Key    string `json:"key,omitempty"`
	Ref    string `json:"ref,omitempty"`
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

// cashChange is one change a scenario makes: move an open item to another date, or add a receipt or payment once or on
// a schedule.
type cashChange struct {
	Kind      string `json:"kind"`
	Item      string `json:"item,omitempty"`
	On        string `json:"on,omitempty"`
	Label     string `json:"label,omitempty"`
	Direction string `json:"direction,omitempty"`
	Amount    string `json:"amount,omitempty"`
	FirstOn   string `json:"first_on,omitempty"`
	Every     string `json:"every,omitempty"`
	UntilOn   string `json:"until_on,omitempty"`
}

var itemKey = regexp.MustCompile(`^(invoice|bill):[^\s]{1,100}$`)

func (c cashChange) valid() bool {
	date := func(v string) bool {
		t, err := time.Parse("2006-01-02", v)
		return err == nil && t.Year() >= 2000 && t.Year() <= 2100
	}
	switch c.Kind {
	case "move":
		return itemKey.MatchString(c.Item) && date(c.On)
	case "add":
		label := strings.TrimSpace(c.Label)
		return label != "" && len(label) <= 200 && (c.Direction == "in" || c.Direction == "out") && centsText.MatchString(c.Amount) && c.Amount != "0" && strings.Trim(c.Amount, "0.") != "" &&
			date(c.FirstOn) && slices.Contains([]string{"once", "week", "two_weeks", "month"}, c.Every) && (c.UntilOn == "" || (date(c.UntilOn) && c.UntilOn >= c.FirstOn))
	}
	return false
}

// applyScenario is the plan's items with a scenario's changes: moved open items keep their amount on the new date, and
// added receipts or payments fall on each of their dates in the horizon. A move whose item is not in the plan (paid,
// disputed or undated since) is returned as missing.
func applyScenario(items []cashItem, name string, changes []cashChange, start, end time.Time) (out []cashItem, missing []string) {
	out, missing = slices.Clone(items), []string{}
	for _, c := range changes {
		switch c.Kind {
		case "move":
			i := slices.IndexFunc(out, func(item cashItem) bool { return item.Key == c.Item })
			if i < 0 {
				missing = append(missing, c.Item)
				continue
			}
			out[i].On, out[i].Basis = c.On, "scenario: "+name
		case "add":
			first, _ := time.Parse("2006-01-02", c.FirstOn)
			var until *time.Time
			if t, err := time.Parse("2006-01-02", c.UntilOn); err == nil {
				until = &t
			}
			for _, on := range occurrences(first, c.Every, until, start, end) {
				value, _ := new(big.Rat).SetString(c.Amount)
				if c.Direction == "out" {
					value.Neg(value)
				}
				out = append(out, cashItem{On: on.Format("2006-01-02"), Amount: value.FloatString(2), Label: strings.TrimSpace(c.Label), Source: "scenario", Basis: "scenario: " + name, value: value})
			}
		}
	}
	return out, missing
}

// visibleScenario is SQL: a client's executive (the executive placeholder) sees and removes only their own scenarios
// (the who placeholder), so a firm's what-ifs (a draft salary, say) stay with the firm; anyone else sees every scenario.
func visibleScenario(executive, who string) string {
	return `(NOT ` + executive + `::bool OR created_by=` + who + `)`
}

// bookCash is a capture's book cash: its home-currency bank accounts' balances at the capture; accounts in another
// currency, and home-currency ones whose balance cannot be read, are named apart. Not ok when the accounts or the home
// currency cannot be read.
func bookCash(accounts []byte, home string) (total *big.Rat, banks []map[string]string, foreign, unreadable []string, ok bool) {
	var directory []struct {
		ID             string `json:"Id"`
		Name           string
		AccountType    string
		CurrentBalance json.Number
		CurrencyRef    struct{ Value string }
	}
	if json.Unmarshal(accounts, &directory) != nil || home == "" {
		return nil, nil, nil, nil, false
	}
	total, banks, foreign, unreadable = new(big.Rat), []map[string]string{}, []string{}, []string{}
	for _, a := range directory {
		if a.AccountType != "Bank" {
			continue
		}
		balance, ok := new(big.Rat).SetString(string(a.CurrentBalance))
		switch {
		case a.CurrencyRef.Value != "" && a.CurrencyRef.Value != home:
			foreign = append(foreign, a.Name)
		case !ok:
			unreadable = append(unreadable, a.Name)
		default:
			total.Add(total, balance)
			banks = append(banks, map[string]string{"account_id": a.ID, "name": a.Name, "balance": balance.FloatString(2)})
		}
	}
	return total, banks, foreign, unreadable, true
}

// forecastOn is a plan's end-of-day book cash on a day within its weeks: the week's opening plus that week's items
// through the day. Not ok outside the 13 weeks.
func forecastOn(weeks []cashWeek, day string) (string, bool) {
	for _, week := range weeks {
		if day < week.Start || day > week.End {
			continue
		}
		balance, _ := new(big.Rat).SetString(week.Opening)
		for _, item := range week.Items {
			if item.On <= day {
				value, _ := new(big.Rat).SetString(item.Amount)
				balance.Add(balance, value)
			}
		}
		return balance.FloatString(2), true
	}
	return "", false
}

// cashPlanCalculation names how the plan is laid out; a review keeps it with the plan.
const cashPlanCalculation = "cash_plan_v1"

// lowestWeek is the week with the lowest end-of-day balance in the plan.
func lowestWeek(weeks []cashWeek) cashWeek {
	lowest, low := weeks[0], new(big.Rat)
	low.SetString(lowest.Lowest)
	for _, week := range weeks {
		if l, _ := new(big.Rat).SetString(week.Lowest); l.Cmp(low) < 0 {
			lowest, low = week, l
		}
	}
	return lowest
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
	// plan is the close's 13-week plan, with a scenario's changes when one is named; a plan that cannot be laid out yet
	// is {state: waiting, reason}. Its digest covers every figure and date shown, not when it was read.
	plan := func(r *http.Request, scenarioID string) (map[string]any, error) {
		ctx := r.Context()
		wait := func(reason string) (map[string]any, error) {
			return map[string]any{"state": "waiting", "reason": reason}, nil
		}
		org, entity, err := client(r)
		if err != nil {
			return nil, tieRefusal{404, "Close not found."}
		}
		// Opening book cash: the ledger's bank accounts in the close's newest QuickBooks capture, as of that capture.
		var snapshotID, home, company string
		var asOf time.Time
		var stale bool
		var accounts []byte
		err = db.QueryRowContext(ctx, `SELECT s.id::text,s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active',COALESCE(s.payload->'accounts','[]'::jsonb),COALESCE(x.home_currency,''),'quickbooks:'||x.environment||':'||x.realm FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id
 LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND `+sourceAllows("s.close_id", "quickbooks")+` AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, r.PathValue("id")).Scan(&snapshotID, &asOf, &stale, &accounts, &home, &company)
		if err == sql.ErrNoRows {
			return wait("Capture this client's QuickBooks books first: the plan opens from the ledger's bank balances. A Xero client's cash plan is not built yet.")
		}
		if err != nil {
			return nil, tieRefusal{503, "The cash plan is unavailable."}
		}
		opening, banks, foreignBanks, unreadableBanks, ok := bookCash(accounts, home)
		foreignBanks = append(foreignBanks, unreadableBanks...)
		if !ok {
			return wait("The capture's accounts or the client's home currency could not be read.")
		}
		receivables, payables, meta, err := latestOpenItems(r, db)
		var waitErr waiting
		if errors.As(err, &waitErr) {
			return wait(string(waitErr))
		}
		if err != nil {
			return nil, tieRefusal{503, "The cash plan is unavailable."}
		}
		if meta["company"] != company {
			return wait("The latest open items come from other books than the bank balances; the plan needs both from the same QuickBooks company. Capture this month's QuickBooks transactions.")
		}
		decided, err := openItemDecisions(r, db, org, entity, company)
		if err != nil {
			return nil, tieRefusal{503, "The cash plan is unavailable."}
		}
		asOf = asOf.UTC()
		start := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)
		day := func(on time.Time) int { return int(on.Sub(start) / (24 * time.Hour)) }
		end := start.AddDate(0, 0, 13*7)
		// Counted open bills by vendor and due week, before any bill-pay date moves them: an obligation's payment that week is
		// the bill. A bill left out of the plan (disputed, on hold, undated) is not the payment, so the obligation counts.
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
				on, basis, reason := cashSchedule(item)
				if reason != "" {
					unscheduled = append(unscheduled, map[string]string{"label": item.Label, "amount": item.Balance, "reason": reason})
					continue
				}
				if due, err := time.Parse("2006-01-02", item.Due); err == nil && side == 1 && !due.Before(start) {
					billWeeks[strings.ToLower(strings.TrimSpace(item.Party))+"|"+strconv.Itoa(day(due)/7)] = true
				}
				value, _ := new(big.Rat).SetString(item.Balance)
				if side == 1 {
					value.Neg(value)
				}
				ref := ""
				if item.Action != nil && basis != "due date" {
					ref = "decision:" + item.Action.ID
				}
				items = append(items, cashItem{Key: strings.ToLower(item.Kind) + ":" + item.ID, Ref: ref, On: on, Amount: value.FloatString(2), Label: item.Label, Source: strings.ToLower(item.Kind), Basis: basis, value: value})
			}
		}
		obligations, err := clientObligations(r, db, org, entity)
		if err != nil {
			return nil, tieRefusal{503, "The cash plan is unavailable."}
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
				item := cashItem{Ref: "obligation:" + o.ID, On: on.Format("2006-01-02"), Amount: value.FloatString(2), Label: o.Name, Source: o.Category, Basis: "approved " + strings.ReplaceAll(o.Every, "_", " ") + " schedule", value: value}
				if o.Direction == "out" && o.Vendor != "" && billWeeks[strings.ToLower(o.Vendor)+"|"+strconv.Itoa(day(on)/7)] {
					covered = append(covered, map[string]string{"obligation": o.Name, "on": item.On, "vendor": o.Vendor})
					continue
				}
				items = append(items, item)
			}
		}
		// A scenario changes only this view: the base plan's lowest point is reported beside it.
		var scenario map[string]any
		if id := scenarioID; id != "" {
			var name, books, currency string
			var raw []byte
			if err := db.QueryRowContext(ctx, `SELECT name,changes,company,currency FROM cash_scenarios WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND retired_at IS NULL AND `+visibleScenario("$4", "$5"), id, org, entity, executive(r), reviewerName(r)).Scan(&name, &raw, &books, &currency); err != nil {
				return nil, tieRefusal{404, "Scenario not found."}
			}
			// A scenario names open items and amounts of the books it was made for; other books or a new home currency
			// could give the same key to another document.
			if books != company || currency != home {
				return nil, tieRefusal{409, "This scenario was made for other books or another currency, so it is not applied. Try the change again on these books."}
			}
			var changes []cashChange
			json.Unmarshal(raw, &changes)
			base, _ := planWeeks(start, opening, items)
			baseLowest := lowestWeek(base)
			var missing []string
			items, missing = applyScenario(items, name, changes, start, end)
			scenario = map[string]any{"id": id, "name": name, "changes": changes, "missing": missing, "base_lowest": map[string]string{"amount": baseLowest.Lowest, "on": baseLowest.LowestOn}}
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
		lowest := lowestWeek(weeks)
		openItemsAt, _ := time.Parse(time.RFC3339, meta["captured_at"].(string))
		apart := asOf.Sub(openItemsAt)
		// An executive sees approved schedules only; drafts (their amounts, vendors and notes) stay with the firm.
		var draftSchedules any = pending
		if executive(r) {
			draftSchedules = []cashObligation{}
		}
		out := map[string]any{"state": "ready", "entity": entity, "currency": home, "start": start.Format("2006-01-02"), "unapproved_schedules": len(pending),
			"opening":    map[string]any{"amount": opening.FloatString(2), "as_of": asOf, "snapshot_id": snapshotID, "stale": stale, "accounts": banks, "left_out": foreignBanks},
			"open_items": map[string]any{"captured_at": meta["captured_at"], "capture_id": meta["capture_id"], "complete": meta["complete"], "hours_apart": int(apart.Abs().Hours()), "through": meta["month_end"], "missing_later": first > meta["month_end"].(string)},
			"weeks":      weeks, "lowest": map[string]string{"amount": lowest.Lowest, "on": lowest.LowestOn},
			"past_due": before, "after_horizon": after, "unscheduled": unscheduled, "other_currency": otherCurrency, "covered": covered, "pending_obligations": draftSchedules, "scenario": scenario,
			"note": "Book cash is the ledger's bank balances at the capture, not bank-available cash. Receipts and payments are expectations from due dates, promises, bill-pay handoffs and approved schedules; nothing is paid, collected or written to the ledger. Sales not yet invoiced are not in the plan unless an approved receipt schedule adds them, so later weeks show only what is already known."}
		// The digest binds the sources (the capture and the open items read) and the calculation, not only the totals.
		figures, _ := json.Marshal([]any{cashPlanCalculation, entity, home, company, snapshotID, meta["capture_id"], out["start"], opening.FloatString(2), banks, weeks, before, after, unscheduled, covered, otherCurrency, scenario})
		out["company"], out["calculation"], out["digest"] = company, cashPlanCalculation, preparation.Digest(figures)
		return out, nil
	}
	mux.HandleFunc("GET /v1/closes/{id}/cash-plan", func(w http.ResponseWriter, r *http.Request) {
		out, err := plan(r, r.URL.Query().Get("scenario"))
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil {
			problem(w, 503, "The cash plan is unavailable.")
			return
		}
		jsonReply(w, 200, out)
	})
	// A person reviews the plan they see: its figures must be unchanged since, its capture current and its open items
	// complete. The review keeps the whole plan and the assumptions they accepted.
	mux.HandleFunc("POST /v1/closes/{id}/cash-plan/reviews", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Scenario    string `json:"scenario"`
			Digest      string `json:"digest"`
			Assumptions string `json:"assumptions"`
			Confirmed   bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Assumptions = strings.TrimSpace(in.Assumptions); !in.Confirmed || in.Digest == "" || utf8.RuneCountInString(in.Assumptions) < 5 || utf8.RuneCountInString(in.Assumptions) > 4000 {
			problem(w, 422, "Confirm the plan you reviewed and state the assumptions you accept (5–4,000 characters).")
			return
		}
		out, err := plan(r, in.Scenario)
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil {
			problem(w, 503, "The cash plan is unavailable.")
			return
		}
		switch {
		case out["state"] != "ready":
			problem(w, 409, "The plan is not ready: "+out["reason"].(string))
			return
		case out["digest"] != in.Digest:
			problem(w, 409, "The plan changed since you looked at it. Review it again.")
			return
		case out["opening"].(map[string]any)["stale"] == true || out["open_items"].(map[string]any)["complete"] != true:
			problem(w, 409, "Only a plan from a current capture with every open item read can be reviewed.")
			return
		}
		// Reviews of a close are made one at a time; the newest review of this view decides whether this one is new, so a
		// plan that returns to an earlier state can be reviewed again.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The review could not be saved.")
			return
		}
		defer tx.Rollback()
		var org, newest string
		if err = tx.QueryRowContext(r.Context(), `SELECT organization_id FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&org); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT digest FROM cash_plan_reviews WHERE close_id::text=$1 AND scenario_id IS NOT DISTINCT FROM NULLIF($2,'')::bigint ORDER BY id DESC LIMIT 1),'')`, r.PathValue("id"), in.Scenario).Scan(&newest); err != nil {
			problem(w, 503, "The review could not be saved.")
			return
		}
		if newest == in.Digest {
			problem(w, 409, "This plan is already reviewed; nothing in it has changed since.")
			return
		}
		frozen, _ := json.Marshal(out)
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO cash_plan_reviews(organization_id,close_id,scenario_id,company,currency,plan,digest,assumptions,reviewed_by) VALUES($1,$2,NULLIF($3,'')::bigint,$4,$5,$6,$7,$8,$9) RETURNING id::text`,
			org, r.PathValue("id"), in.Scenario, out["company"], out["currency"], frozen, in.Digest, in.Assumptions, who).Scan(&id); err != nil || tx.Commit() != nil {
			problem(w, 503, "The review could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"id": id})
	})
	// Reviewed plans, newest first: the newest for each scenario (or the plan as it stands) is current until its figures
	// change, older ones are superseded. Each is compared with the book cash of later captures of the same books, on the
	// capture's day. A client's executive sees reviews of the plan as it stands only.
	mux.HandleFunc("GET /v1/closes/{id}/cash-plan/reviews", func(w http.ResponseWriter, r *http.Request) {
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT v.id::text,COALESCE(v.scenario_id::text,''),COALESCE(s.name,''),v.company,v.currency,v.plan,v.digest,v.assumptions,v.reviewed_by,v.reviewed_at
 FROM cash_plan_reviews v LEFT JOIN cash_scenarios s ON s.id=v.scenario_id WHERE v.close_id::text=$1 AND (NOT $2::bool OR v.scenario_id IS NULL) ORDER BY v.id DESC LIMIT 50`, r.PathValue("id"), executive(r))
		if err != nil {
			problem(w, 503, "Reviews are unavailable.")
			return
		}
		// A capture's book cash is measured at its time; the plan's figure is the planned balance at the end of that day.
		type comparison struct {
			CapturedAt time.Time `json:"captured_at"`
			On         string    `json:"on"`
			Forecast   string    `json:"forecast"`
			Actual     string    `json:"actual,omitempty"`
			Difference string    `json:"difference,omitempty"`
			Unreadable []string  `json:"unreadable,omitempty"`
		}
		type review struct {
			ID          string       `json:"id"`
			Scenario    string       `json:"scenario"`
			Name        string       `json:"scenario_name"`
			Assumptions string       `json:"assumptions"`
			ReviewedBy  string       `json:"reviewed_by"`
			ReviewedAt  time.Time    `json:"reviewed_at"`
			Start       string       `json:"start"`
			Lowest      any          `json:"lowest"`
			State       string       `json:"state"`
			Actuals     []comparison `json:"actuals"`
			Currency    string       `json:"currency"`
			Calculation string       `json:"calculation"`
			company     string
			digest      string
			asOf        time.Time
			weeks       []cashWeek
		}
		reviews := []review{}
		for rows.Next() {
			var v review
			var raw []byte
			if err = rows.Scan(&v.ID, &v.Scenario, &v.Name, &v.company, &v.Currency, &raw, &v.digest, &v.Assumptions, &v.ReviewedBy, &v.ReviewedAt); err != nil {
				break
			}
			var frozen struct {
				Start       string
				Calculation string
				Lowest      any
				Opening     struct {
					AsOf time.Time `json:"as_of"`
				}
				Weeks []cashWeek
			}
			json.Unmarshal(raw, &frozen)
			v.Start, v.Calculation, v.Lowest, v.asOf, v.weeks, v.Actuals = frozen.Start, frozen.Calculation, frozen.Lowest, frozen.Opening.AsOf, frozen.Weeks, []comparison{}
			reviews = append(reviews, v)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Reviews are unavailable.")
			return
		}
		seen := map[string]bool{}
		for i := range reviews {
			v := &reviews[i]
			if seen[v.Scenario] {
				v.State = "superseded"
			} else {
				seen[v.Scenario] = true
				// The newest review of each view is current while the plan still shows the same figures.
				live, e := plan(r, v.Scenario)
				switch {
				case e != nil:
					v.State = "unverified"
				case live["state"] != "ready" || live["opening"].(map[string]any)["stale"] == true || live["open_items"].(map[string]any)["complete"] != true:
					// The plan can no longer be laid out from current, complete books, so it cannot be confirmed as reviewed.
					v.State = "inputs_changed"
				case live["digest"] == v.digest:
					v.State = "current"
				default:
					v.State = "inputs_changed"
				}
			}
			start, _ := time.Parse("2006-01-02", v.Start)
			later, e := db.QueryContext(r.Context(), `SELECT s.captured_at,COALESCE(s.payload->'accounts','[]'::jsonb) FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id JOIN client_closes c ON c.id=s.close_id
 WHERE c.organization_id=$1 AND lower(c.entity)=lower($2) AND s.captured_at>$3 AND s.captured_at<$4 AND `+sourceAllows("c.id", "quickbooks")+` AND 'quickbooks:'||x.environment||':'||x.realm=$5 AND COALESCE(x.home_currency,'')=$6 ORDER BY s.captured_at LIMIT 30`, org, entity, v.asOf, start.AddDate(0, 0, 13*7), v.company, v.Currency)
			if e != nil {
				problem(w, 503, "Reviews are unavailable.")
				return
			}
			for later.Next() {
				var at time.Time
				var accounts []byte
				if e = later.Scan(&at, &accounts); e != nil {
					break
				}
				on := at.UTC().Format("2006-01-02")
				forecast, inPlan := forecastOn(v.weeks, on)
				actual, _, _, unreadable, ok := bookCash(accounts, v.Currency)
				if !inPlan || !ok {
					continue
				}
				// A home-currency balance that cannot be read is not zero: no actual is stated for that capture.
				if len(unreadable) > 0 {
					v.Actuals = append(v.Actuals, comparison{CapturedAt: at, On: on, Forecast: forecast, Unreadable: unreadable})
					continue
				}
				expected, _ := new(big.Rat).SetString(forecast)
				v.Actuals = append(v.Actuals, comparison{CapturedAt: at, On: on, Forecast: forecast, Actual: actual.FloatString(2), Difference: new(big.Rat).Sub(actual, expected).FloatString(2)})
			}
			if e == nil {
				e = later.Err()
			}
			later.Close()
			if e != nil {
				problem(w, 503, "Reviews are unavailable.")
				return
			}
		}
		jsonReply(w, 200, map[string]any{"reviews": reviews})
	})
	mux.HandleFunc("GET /v1/closes/{id}/cash-scenarios", func(w http.ResponseWriter, r *http.Request) {
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var raw []byte
		if err = db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'name',name,'changes',changes,'created_by',created_by,'created_at',created_at) ORDER BY id DESC),'[]') FROM cash_scenarios WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND retired_at IS NULL AND `+visibleScenario("$3", "$4"), org, entity, executive(r), reviewerName(r)).Scan(&raw); err != nil {
			problem(w, 503, "Scenarios are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"scenarios": json.RawMessage(raw)})
	})
	// Anyone who can open the plan can try a what-if; a scenario changes no plan, open item or ledger.
	mux.HandleFunc("POST /v1/closes/{id}/cash-scenarios", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action  string       `json:"action"`
			ID      string       `json:"id"`
			Name    string       `json:"name"`
			Changes []cashChange `json:"changes"`
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
			in.Name = strings.TrimSpace(in.Name)
			if in.Name == "" || len(in.Name) > 200 || len(in.Changes) < 1 || len(in.Changes) > 20 || slices.ContainsFunc(in.Changes, func(c cashChange) bool { return !c.valid() }) {
				problem(w, 422, "Name the scenario and give 1–20 changes: move an open invoice or bill to a date, or add a receipt or payment with a label, an amount in cents, a first date and how often.")
				return
			}
			raw, _ := json.Marshal(in.Changes)
			// The scenario is kept with the books and home currency it was made for.
			var company, home string
			if err = db.QueryRowContext(r.Context(), `SELECT 'quickbooks:'||x.environment||':'||x.realm,COALESCE(x.home_currency,'') FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("$1::bigint"), r.PathValue("id")).Scan(&company, &home); err != nil && err != sql.ErrNoRows {
				problem(w, 503, "The scenario could not be saved.")
				return
			}
			if home == "" {
				problem(w, 409, "Connect the client's QuickBooks books first: a scenario is made for one set of books.")
				return
			}
			var id string
			if err = db.QueryRowContext(r.Context(), `INSERT INTO cash_scenarios(organization_id,client_entity,company,currency,name,changes,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, org, entity, company, home, in.Name, raw, reviewerName(r)).Scan(&id); err != nil {
				problem(w, 503, "The scenario could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]any{"saved": true, "id": id})
			return
		case "retire":
			result, err = db.ExecContext(r.Context(), `UPDATE cash_scenarios SET retired_by=$5,retired_at=now() WHERE id::text=$3 AND organization_id=$1 AND lower(client_entity)=lower($2) AND retired_at IS NULL AND `+visibleScenario("$4", "$5"), org, entity, in.ID, executive(r), reviewerName(r))
		default:
			problem(w, 422, "Add or retire a scenario.")
			return
		}
		if err != nil {
			problem(w, 503, "The scenario could not be saved.")
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			problem(w, 409, "That scenario was already retired. Refresh the list.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
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
