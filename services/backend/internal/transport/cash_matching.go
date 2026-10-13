package transport

import (
	"context"
	"database/sql"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

// cashBill is an open bill as the cash plan reads it: counted when it has an agreed or due date from the plan's
// first day on (Reason says why not otherwise).
type cashBill struct {
	Key, Label, Vendor, Due, On string
	Balance                     *big.Rat
	Counted                     bool
	Reason                      string
}

// cashMatch is a person's standing decision on how one bill relates to one date of an approved schedule.
type cashMatch struct {
	ID         string `json:"id"`
	Obligation string `json:"obligation_id"`
	On         string `json:"on"`
	Document   string `json:"document"`
	Label      string `json:"label"`
	Relation   string `json:"relation"`
	Amount     string `json:"amount"`
	Paid       bool   `json:"paid"`
	// DocumentDate is the bill's own date, when known.
	DocumentDate string    `json:"document_date"`
	DecidedBy    string    `json:"decided_by"`
	DecidedAt    time.Time `json:"decided_at"`
}

// cashMatched is one schedule date with the bills decided against it, and what the plan counts for the date.
type cashMatched struct {
	Obligation string            `json:"obligation_id"`
	Name       string            `json:"obligation"`
	On         string            `json:"on"`
	Amount     string            `json:"amount"`
	Counted    string            `json:"counted"`
	Bills      []cashMatchedBill `json:"bills"`
	Note       string            `json:"note"`
}

// cashMatchedBill is one decision on a schedule date: State is "counted" (on On), "paid" (no longer open) or why the
// bill is left out of the weeks.
type cashMatchedBill struct {
	ID       string `json:"id"`
	Document string `json:"document"`
	Label    string `json:"label"`
	Relation string `json:"relation"`
	Amount   string `json:"amount"`
	State    string `json:"state"`
	On       string `json:"on,omitempty"`
	// Confirm asks a person whether a bill that left the open items was paid; Unknown says the capture cannot tell
	// whether it is still open.
	Confirm bool `json:"confirm,omitempty"`
	Unknown bool `json:"unknown,omitempty"`
}

// cashQuestion is a schedule date with open bills from its vendor that week that nobody has decided about. Until a
// person says how they relate, the plan counts both, so the payment may be counted twice.
type cashQuestion struct {
	Obligation string          `json:"obligation_id"`
	Name       string          `json:"obligation"`
	On         string          `json:"on"`
	Amount     string          `json:"amount"`
	Vendor     string          `json:"vendor"`
	Bills      []cashCandidate `json:"bills"`
	Effect     string          `json:"effect"`
}

type cashCandidate struct {
	Document string `json:"document"`
	Label    string `json:"label"`
	Amount   string `json:"amount"`
	On       string `json:"on"`
	Due      string `json:"due"`
}

// cashRelations are what a bill can be to a schedule's date.
var cashRelations = []string{"replaces", "part", "separate", "duplicate"}

// settleOccurrence is what the plan counts for one date of an approved outgoing schedule, from the decisions made about
// bills against it, never from a vendor and a week alone:
//   - a bill that replaces the date's payment, counted or paid, leaves nothing of the estimate;
//   - bills that invoice part of it leave the rest (the schedule's amount less what they invoice, as decided);
//   - a bill that is a separate payment, or that repeats a payment already counted, leaves the estimate whole;
//   - a deciding bill left out of the weeks (disputed, on hold, undated, past due) does not stand in for the payment,
//     so the estimate counts for it.
//
// Open bills from the schedule's vendor in the date's week that are not decided for this date, and pay or repeat no
// other date, are the question: both count until a person decides.
//
// A bill that left the open items was paid, voided or credited; no capture says which, so it settles the date only once
// a person confirms it was paid. Until then the estimate counts for it. Its absence says anything only when covers
// holds for the decision: a capture that read every open item, taken after the decision, of a month that includes the
// bill's date.
func settleOccurrence(o cashObligation, on string, sameWeek func(a, b string) bool, bills map[string]cashBill, matches []cashMatch, covers func(cashMatch) bool) (residual *big.Rat, matched *cashMatched, question *cashQuestion) {
	amount, _ := new(big.Rat).SetString(o.Amount)
	residual = new(big.Rat).Set(amount)
	decided, spoken := map[string]bool{}, map[string]bool{}
	invoiced := new(big.Rat)
	replaced := false
	var standing []string
	entry := cashMatched{Obligation: o.ID, Name: o.Name, On: on, Amount: amount.FloatString(2), Bills: []cashMatchedBill{}}
	for _, m := range matches {
		if m.Relation != "separate" {
			spoken[m.Document] = true
		}
		if m.Obligation != o.ID || m.On != on {
			continue
		}
		decided[m.Document] = true
		b, open := bills[m.Document]
		row := cashMatchedBill{ID: m.ID, Document: m.Document, Label: m.Label, Relation: m.Relation, Amount: m.Amount}
		pays := m.Relation == "replaces" || m.Relation == "part"
		switch {
		case !open && m.Paid:
			row.State = "paid"
		case !open && !pays:
			row.State = "No longer open."
		case !open && !covers(m):
			row.State, row.Unknown = "Not in this capture, which cannot say whether it is still open (it did not read every open item, was taken before the decision, or does not cover the bill's date), so it is not taken as paid.", true
		case !open:
			row.State, row.Confirm = "No longer open: paid, voided or credited. The capture cannot say which, so the estimate counts until someone confirms it was paid.", true
		case b.Counted:
			row.State, row.On = "counted", b.On
		default:
			row.State = b.Reason
		}
		paying := !open && m.Paid || open && b.Counted
		switch m.Relation {
		case "replaces":
			replaced = replaced || paying
		case "part":
			if paying {
				if v, ok := new(big.Rat).SetString(m.Amount); ok {
					invoiced.Add(invoiced, v)
				}
			}
		}
		if (m.Relation == "replaces" || m.Relation == "part") && !paying {
			standing = append(standing, m.Label)
		}
		entry.Bills = append(entry.Bills, row)
	}
	residual.Sub(residual, invoiced)
	if replaced || residual.Sign() < 0 {
		residual.SetInt64(0)
	}
	if len(entry.Bills) > 0 {
		entry.Counted = residual.FloatString(2)
		notes := []string{}
		switch {
		case replaced:
			notes = append(notes, "A bill pays it, so the schedule's estimate is not counted.")
		case invoiced.Sign() > 0 && residual.Sign() > 0:
			notes = append(notes, "Bills invoice "+groupedAmount(invoiced)+" of it; the remaining "+groupedAmount(residual)+" still counts.")
		case invoiced.Sign() > 0:
			notes = append(notes, "Bills invoice all of it, so the schedule's estimate is not counted.")
			if over := new(big.Rat).Sub(invoiced, amount); over.Sign() > 0 {
				notes = append(notes, "They invoice "+groupedAmount(over)+" more than the schedule.")
			}
		}
		if len(standing) > 0 {
			notes = append(notes, strings.Join(standing, ", ")+" is left out of the weeks, so the estimate counts for it.")
		}
		entry.Note = strings.Join(notes, " ")
		matched = &entry
	}
	if o.Vendor == "" {
		return residual, matched, nil
	}
	keys := make([]string, 0, len(bills))
	for k := range bills {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var candidates []cashCandidate
	for _, k := range keys {
		b := bills[k]
		if !b.Counted || b.Balance == nil || b.Balance.Sign() <= 0 || decided[k] || spoken[k] || !strings.EqualFold(strings.TrimSpace(b.Vendor), strings.TrimSpace(o.Vendor)) {
			continue
		}
		if !sameWeek(b.Due, on) && !sameWeek(b.On, on) {
			continue
		}
		candidates = append(candidates, cashCandidate{Document: k, Label: b.Label, Amount: b.Balance.FloatString(2), On: b.On, Due: b.Due})
	}
	if len(candidates) == 0 {
		return residual, matched, nil
	}
	which := "its bill"
	if len(candidates) > 1 {
		which = "its bills"
	}
	effect := "Until someone decides, " + which + " and the schedule's " + groupedAmount(residual) + " both count, so the payment may be counted twice. If a bill pays all of it, that week's payments fall by " + groupedAmount(residual) + "."
	// A date already paid by a decided bill still asks about another bill that week: it counts as a payment of its own
	// until someone says it is separate or the same payment entered twice.
	if residual.Sign() == 0 {
		effect = "Another bill already pays this date. Until someone decides, " + which + " also count, so the payment may be counted twice; if a bill repeats the payment, that week's payments fall by its amount."
	}
	question = &cashQuestion{Obligation: o.ID, Name: o.Name, On: on, Amount: residual.FloatString(2), Vendor: o.Vendor, Bills: candidates, Effect: effect}
	return residual, matched, question
}

// cashMatchesOf are the client's standing decisions on bills in one ledger.
func cashMatchesOf(ctx context.Context, db *sql.DB, org, entity, company string) ([]cashMatch, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text,obligation_id::text,occurrence_on::text,document,label,relation,amount::text,paid,COALESCE(document_date::text,''),decided_by,decided_at FROM cash_obligation_matches
 WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND company=$3 AND retired_at IS NULL ORDER BY cash_obligation_matches.id`, org, entity, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cashMatch{}
	for rows.Next() {
		var m cashMatch
		if err := rows.Scan(&m.ID, &m.Obligation, &m.On, &m.Document, &m.Label, &m.Relation, &m.Amount, &m.Paid, &m.DocumentDate, &m.DecidedBy, &m.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// cashGap is something the plan cannot yet settle: a source it lacks ("source") or a decision a person has not made
// ("decision").
type cashGap struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Amount string `json:"amount,omitempty"`
}

// cashReadiness is "complete" with nothing unresolved, "incomplete" while a source is missing or an amount cannot be
// placed, and otherwise "draft" while a decision is open.
func cashReadiness(gaps []cashGap) map[string]any {
	state := "complete"
	for _, g := range gaps {
		if g.Kind == "source" {
			state = "incomplete"
			break
		}
		state = "draft"
	}
	if gaps == nil {
		gaps = []cashGap{}
	}
	return map[string]any{"state": state, "unresolved": gaps}
}

// registerCashMatches records how a bill relates to one date of an approved schedule. Each decision is a person's
// (workpapers:review); a later one for the same bill and date retires the earlier, naming it, so two people never
// overwrite each other unseen.
func registerCashMatches(mux *http.ServeMux, db *sql.DB, client func(*http.Request) (string, string, error)) {
	mux.HandleFunc("POST /v1/closes/{id}/cash-matches", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action     string `json:"action"`
			Obligation string `json:"obligation_id"`
			On         string `json:"on"`
			Document   string `json:"document"`
			Relation   string `json:"relation"`
			Amount     string `json:"amount"`
			Previous   string `json:"previous_id"`
			CaptureID  string `json:"capture_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, entity, err := client(r)
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if !slices.Contains([]string{"decide", "clear", "paid"}, in.Action) || in.Action == "decide" && !slices.Contains(cashRelations, in.Relation) || !strings.HasPrefix(in.Document, "bill:") {
			problem(w, 422, "Decide how a bill relates to a schedule's date (replaces, part, separate or duplicate), confirm a bill that left the open items was paid, or clear a decision.")
			return
		}
		occurrence, err := time.Parse("2006-01-02", in.On)
		if err != nil {
			problem(w, 422, "Name the schedule's date.")
			return
		}
		// The open items are read before the transaction (no pooled connection is held while reading them); under the
		// close's lock the capture is then checked to still be the latest.
		_, payables, meta, err := latestOpenItems(r, db)
		if err != nil {
			problem(w, 409, "The open items cannot be read, so the bill cannot be matched.")
			return
		}
		if fmtString(meta["capture_id"]) != in.CaptureID {
			problem(w, 409, "The books were captured again since you looked. Refresh before deciding.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		defer tx.Rollback()
		// The client's home currency: a schedule in another no longer counts, so decisions against it have lapsed.
		var home string
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT COALESCE(x.home_currency,'') FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("$1::bigint")+`),'')`, r.PathValue("id")).Scan(&home); err != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		// Decisions for a close are made one at a time.
		if _, err = tx.ExecContext(r.Context(), `SELECT 1 FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")); err != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		// The capture the person saw must still be the latest: a newer one may show a different balance, or the bill
		// open again.
		var latest string
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT s.id::text FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id WHERE s.close_id::text=$1 AND s.period=c.period AND s.state IN ('completed','unchanged') AND s.payload ? 'open_items' AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+` AND `+sourceAllows("c.id", "quickbooks")+` ORDER BY s.id DESC LIMIT 1),'')`, r.PathValue("id")).Scan(&latest); err != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		if latest != in.CaptureID {
			problem(w, 409, "The books were captured again since you looked. Refresh before deciding.")
			return
		}
		// A newer Xero snapshot may have become the month's books since the open items were read: the same choice the
		// open items make, checked under the lock.
		if books, e := latestXeroBooks(r.Context(), tx, r.PathValue("id")); e != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		} else if books != nil {
			problem(w, 409, "The month's books are now read from Xero. Refresh before deciding.")
			return
		}
		company, _ := meta["company"].(string)
		var bill *openItem
		for i := range payables {
			if payables[i].Kind == "Bill" && "bill:"+payables[i].ID == in.Document {
				bill = &payables[i]
			}
		}
		var standing, standingLabel, standingAmount, standingRelation, standingDate string
		var standingAt time.Time
		var standingPaid bool
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,label,amount::text,relation,COALESCE(document_date::text,''),decided_at,paid FROM cash_obligation_matches WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND company=$3 AND document=$4 AND obligation_id::text=$5 AND occurrence_on=$6::date AND retired_at IS NULL`,
			org, entity, company, in.Document, in.Obligation, in.On).Scan(&standing, &standingLabel, &standingAmount, &standingRelation, &standingDate, &standingAt, &standingPaid)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		if standing != in.Previous {
			problem(w, 409, "This bill's decision for that date changed since you looked. Refresh before deciding.")
			return
		}
		// Confirming payment is only for a bill decided to pay the date (in full or in part) that has left the open items.
		// Only a capture that says the bill left the open items allows confirming it was paid.
		if in.Action == "paid" && (standing == "" || bill != nil || !captureCovers(meta, standingAt, standingDate) || standingRelation != "replaces" && standingRelation != "part") {
			problem(w, 409, "Only a bill decided to pay the date, and missing from a capture that read every open item, was taken after the decision and covers the bill's date, can be confirmed paid.")
			return
		}
		if standing != "" {
			if _, err = tx.ExecContext(r.Context(), `UPDATE cash_obligation_matches SET retired_by=$2,retired_at=now() WHERE id::text=$1 AND retired_at IS NULL`, standing, who); err != nil {
				problem(w, 503, "The decision could not be saved.")
				return
			}
		}
		if in.Action == "paid" {
			var id string
			if err = tx.QueryRowContext(r.Context(), `INSERT INTO cash_obligation_matches(organization_id,client_entity,obligation_id,occurrence_on,company,document,label,document_date,relation,amount,paid,decided_by) SELECT organization_id,client_entity,obligation_id,occurrence_on,company,document,label,document_date,relation,amount,true,$2 FROM cash_obligation_matches WHERE id::text=$1 RETURNING id::text`, standing, who).Scan(&id); err != nil || tx.Commit() != nil {
				problem(w, 503, "The decision could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]any{"saved": true, "id": id})
			return
		}
		if in.Action == "clear" {
			if standing == "" {
				problem(w, 409, "There is no decision on this bill for that date.")
				return
			}
			// A bill confirmed paid settled the date; its decision can be changed, never dropped, unless the schedule's date
			// no longer applies. A bill merely missing from a capture proves nothing, so its decision can be cleared.
			if standingPaid && (standingRelation == "replaces" || standingRelation == "part") {
				holds, err := scheduleHasDate(r.Context(), tx, org, entity, home, in.Obligation, in.On)
				if err != nil {
					problem(w, 503, "The decision could not be saved.")
					return
				}
				if holds {
					problem(w, 409, "That bill is no longer open, so its decision settles the date. Change what the bill was instead of clearing it.")
					return
				}
			}
			if tx.Commit() != nil {
				problem(w, 503, "The decision could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]any{"saved": true})
			return
		}
		// A bill no longer open (paid) can still be re-decided against a date it was decided for, from that decision; a
		// confirmed payment carries into another paying relation, since correcting the relation does not unpay it.
		paid := bill == nil && standingPaid && (in.Relation == "replaces" || in.Relation == "part")
		if bill == nil && standing != "" {
			bill = &openItem{Label: standingLabel, Balance: standingAmount, Date: standingDate}
		}
		if bill == nil || bill.Balance == "" {
			problem(w, 409, "That bill is not among the latest open items, or its balance cannot be read.")
			return
		}
		// The date is one of an approved, standing outgoing schedule of this client, in the bill's currency.
		var first, every, until, currency, amount string
		err = tx.QueryRowContext(r.Context(), `SELECT first_on::text,every,COALESCE(until_on::text,''),currency,amount::text FROM cash_obligations WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND direction='out' AND approved_at IS NOT NULL AND retired_at IS NULL`,
			in.Obligation, org, entity).Scan(&first, &every, &until, &currency, &amount)
		if err == sql.ErrNoRows {
			problem(w, 409, "That schedule is not an approved, standing payment of this client.")
			return
		}
		if err != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		start, _ := time.Parse("2006-01-02", first)
		var last *time.Time
		if t, err := time.Parse("2006-01-02", until); err == nil {
			last = &t
		}
		if dates := occurrences(start, every, last, occurrence, occurrence.AddDate(0, 0, 1)); len(dates) != 1 || bill.Currency != "" && bill.Currency != currency {
			problem(w, 409, "The schedule has no payment on that date in the bill's currency.")
			return
		}
		// Deciding a bill retires its decisions for schedule dates that no longer apply, so a renewed schedule's date can
		// take the bill.
		type earlier struct{ id, obligation, on string }
		var others []earlier
		rows, err := tx.QueryContext(r.Context(), `SELECT id::text,obligation_id::text,occurrence_on::text FROM cash_obligation_matches WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND company=$3 AND document=$4 AND retired_at IS NULL`, org, entity, company, in.Document)
		if err != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		for rows.Next() {
			var e earlier
			if rows.Scan(&e.id, &e.obligation, &e.on) == nil {
				others = append(others, e)
			}
		}
		rows.Close()
		for _, e := range others {
			holds, err := scheduleHasDate(r.Context(), tx, org, entity, home, e.obligation, e.on)
			if err != nil {
				problem(w, 503, "The decision could not be saved.")
				return
			}
			if !holds {
				if _, err := tx.ExecContext(r.Context(), `UPDATE cash_obligation_matches SET retired_by=$2,retired_at=now() WHERE id::text=$1 AND retired_at IS NULL`, e.id, who); err != nil {
					problem(w, 503, "The decision could not be saved.")
					return
				}
			}
		}
		// What the bill invoices of the schedule: as stated for a part, else its balance now.
		invoiced := bill.Balance
		if in.Relation == "part" {
			v, ok := new(big.Rat).SetString(in.Amount)
			whole, _ := new(big.Rat).SetString(amount)
			if !ok || !centsText.MatchString(in.Amount) || v.Sign() <= 0 || v.Cmp(whole) > 0 {
				problem(w, 422, "Say how much of the schedule's "+whole.FloatString(2)+" the bill invoices, in cents.")
				return
			}
			invoiced = in.Amount
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO cash_obligation_matches(organization_id,client_entity,obligation_id,occurrence_on,company,document,label,document_date,relation,amount,paid,decided_by) VALUES($1,$2,$3::bigint,$4::date,$5,$6,$7,NULLIF($8,'')::date,$9,$10::numeric,$11,$12) RETURNING id::text`,
			org, entity, in.Obligation, in.On, company, in.Document, bill.Label, bill.Date, in.Relation, invoiced, paid, who).Scan(&id)
		if err != nil {
			if strings.Contains(err.Error(), "cash_obligation_matches_paid_once") {
				problem(w, 409, "That bill already pays or repeats another schedule's date. Change that decision first.")
				return
			}
			problem(w, 503, "The decision could not be saved.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "The decision could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"saved": true, "id": id})
	})
}

// groupedAmount writes an exact amount to the cent with thousands separators (10,000.00, -1,250.50), for text a person
// reads.
func groupedAmount(v *big.Rat) string {
	text := v.FloatString(2)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	whole, cents, _ := strings.Cut(text, ".")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return sign + whole + "." + cents
}

// rowQuerier is a database or transaction that reads one row.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// scheduleHasDate says whether an approved, standing outgoing schedule of the client still pays on that date, so a
// decision made against the date still holds.
func scheduleHasDate(ctx context.Context, q rowQuerier, org, entity, home, obligation, on string) (bool, error) {
	var first, every, until string
	err := q.QueryRowContext(ctx, `SELECT first_on::text,every,COALESCE(until_on::text,'') FROM cash_obligations WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND direction='out' AND approved_at IS NOT NULL AND retired_at IS NULL AND currency=$4`,
		obligation, org, entity, home).Scan(&first, &every, &until)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	start, err1 := time.Parse("2006-01-02", first)
	day, err2 := time.Parse("2006-01-02", on)
	if err1 != nil || err2 != nil {
		return false, nil
	}
	var last *time.Time
	if t, err := time.Parse("2006-01-02", until); err == nil {
		last = &t
	}
	return len(occurrences(start, every, last, day, day.AddDate(0, 0, 1))) == 1, nil
}

// captureCovers says whether an open-items capture can say a decided bill left the open items: it read every open item,
// was taken after the decision, and covers a month that includes the bill's date.
func captureCovers(meta map[string]any, decidedAt time.Time, documentDate string) bool {
	captured, err := time.Parse(time.RFC3339, fmtString(meta["captured_at"]))
	monthEnd := fmtString(meta["month_end"])
	return err == nil && meta["complete"] == true && decidedAt.Before(captured) && documentDate != "" && documentDate <= monthEnd
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}
