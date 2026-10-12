package transport

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

// What a person can say about an open invoice or bill. Whether it is still open always comes from a capture; a
// write-off is an accounting correction prepared in the close, never a status here.
var openItemStatuses = map[string][]string{
	"Invoice": {"open", "following_up", "promised", "disputed", "on_hold"},
	"Bill":    {"open", "following_up", "handed_to_bill_pay", "disputed", "on_hold"},
}

type openItemAction struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Mine       bool      `json:"mine"`
	Status     string    `json:"status"`
	ExpectedOn string    `json:"expected_on"`
	Note       string    `json:"note"`
	Actor      string    `json:"actor"`
	CreatedAt  time.Time `json:"created_at"`
}

type openItem struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Label    string          `json:"label"`
	Party    string          `json:"party"`
	Currency string          `json:"currency"`
	Date     string          `json:"date"`
	Due      string          `json:"due"`
	Days     *int            `json:"days_past_due"`
	Balance  string          `json:"balance"`
	Problem  string          `json:"problem,omitempty"`
	Action   *openItemAction `json:"action"`
	// For a decided document the latest capture no longer lists: settled when that capture shows it, else why not.
	Settled bool   `json:"settled,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func openItemLabel(line quickbooks.AgingLine, party string) string {
	number := line.Number
	if number == "" {
		number = "#" + line.ID
	}
	label := []rune(line.Kind + " " + number + " · " + party)
	return string(label[:min(len(label), 300)])
}

// latestOpenItems lists the latest capture's open invoices (receivables) and bills (payables), unreadable ones included.
func latestOpenItems(r *http.Request, db *sql.DB) (receivables, payables []openItem, meta map[string]any, err error) {
	parties := [2][]quickbooks.AgingParty{}
	parties[0], parties[1], meta, err = latestAging(r, db)
	if err != nil {
		return nil, nil, nil, err
	}
	lists := [2][]openItem{{}, {}}
	for side, list := range parties {
		for _, party := range list {
			for _, line := range party.Lines {
				if line.Kind == "Invoice" || line.Kind == "Bill" {
					lists[side] = append(lists[side], openItem{Kind: line.Kind, ID: line.ID, Label: openItemLabel(line, party.Name), Party: party.Name, Currency: party.Currency, Date: line.Date, Due: line.Due, Days: line.Days, Balance: line.Balance, Problem: line.Problem})
				}
			}
		}
	}
	return lists[0], lists[1], meta, nil
}

// openItemDecisions is each decided document's newest decision for a client's ledger company, by "kind|id".
func openItemDecisions(r *http.Request, db *sql.DB, org, entity, company string) (map[string]openItem, error) {
	rows, err := db.QueryContext(r.Context(), `SELECT DISTINCT ON (kind,item_id) kind,item_id,label,COALESCE(document_date::text,''),id::text,owner,status,COALESCE(expected_on::text,''),note,actor,created_at FROM open_item_actions
 WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND company=$3 ORDER BY kind,item_id,open_item_actions.id DESC`, org, entity, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	decided := map[string]openItem{}
	for rows.Next() {
		item, a := openItem{}, openItemAction{}
		if err := rows.Scan(&item.Kind, &item.ID, &item.Label, &item.Date, &a.ID, &a.Owner, &a.Status, &a.ExpectedOn, &a.Note, &a.Actor, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Mine = a.Owner != "" && a.Owner == subject(r)
		item.Action = &a
		decided[item.Kind+"|"+item.ID] = item
	}
	return decided, rows.Err()
}

func registerOpenItems(mux *http.ServeMux, db *sql.DB) {
	client := func(r *http.Request) (org, entity string, err error) {
		err = db.QueryRowContext(r.Context(), `SELECT organization_id,entity FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&org, &entity)
		return
	}
	mux.HandleFunc("GET /v1/closes/{id}/open-items", func(w http.ResponseWriter, r *http.Request) {
		receivables, payables, meta, err := latestOpenItems(r, db)
		var wait waiting
		if errors.As(err, &wait) {
			jsonReply(w, 200, map[string]any{"state": "waiting", "reason": string(wait)})
			return
		}
		org, entity, clientErr := client(r)
		if err != nil || clientErr != nil {
			problem(w, 503, "Open items are unavailable.")
			return
		}
		decided, err := openItemDecisions(r, db, org, entity, meta["company"].(string))
		if err != nil {
			problem(w, 503, "Open item decisions are unavailable.")
			return
		}
		for _, list := range [][]openItem{receivables, payables} {
			for i, item := range list {
				key := item.Kind + "|" + item.ID
				if d, ok := decided[key]; ok {
					list[i].Action = d.Action
					delete(decided, key)
				}
			}
		}
		// A decided document the latest capture no longer lists is settled only when that capture read every open item,
		// was taken after the decision and covers the document's date; otherwise the capture cannot say.
		captured, _ := time.Parse(time.RFC3339, meta["captured_at"].(string))
		settled := []openItem{}
		for _, item := range decided {
			// Files of open items are true each at its own date and complete on their own: a document's are its side's.
			at, complete := captured, meta["complete"].(bool)
			if observed, ok := meta["observed"].(map[string]string); ok && observed[item.Kind] != "" {
				at, _ = time.Parse(time.RFC3339, observed[item.Kind])
				complete = meta["complete_by_kind"].(map[string]bool)[item.Kind]
			}
			switch {
			case !complete:
				item.Reason = "The latest capture did not read every open item, so it cannot say whether this one is paid."
			case !item.Action.CreatedAt.Before(at):
				item.Reason = "Decided after the latest capture; the next capture will say whether it is still open."
			case item.Date == "" || item.Date > meta["month_end"].(string):
				item.Reason = "Dated after this close's month-end, which the capture covers; open a later close to see it."
			default:
				item.Settled = true
			}
			settled = append(settled, item)
		}
		slices.SortFunc(settled, func(a, b openItem) int { return b.Action.CreatedAt.Compare(a.Action.CreatedAt) })
		meta["settled_total"] = len(settled)
		meta["state"], meta["receivables"], meta["payables"], meta["settled"] = "ready", receivables, payables, settled[:min(20, len(settled))]
		jsonReply(w, 200, meta)
	})
	mux.HandleFunc("POST /v1/closes/{id}/open-items", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Kind       string `json:"kind"`
			ItemID     string `json:"item_id"`
			CaptureID  string `json:"capture_id"`
			Status     string `json:"status"`
			ExpectedOn string `json:"expected_on"`
			Note       string `json:"note"`
			Assign     string `json:"assign"`
			PreviousID string `json:"previous_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Note = strings.TrimSpace(in.Note)
		expects := in.Status == "promised" || in.Status == "handed_to_bill_pay"
		date, dateErr := time.Parse("2006-01-02", in.ExpectedOn)
		switch {
		case !slices.Contains(openItemStatuses[in.Kind], in.Status):
			problem(w, 422, "Choose a status this kind of document can have. A write-off is prepared as a correction in the close.")
			return
		case expects && (dateErr != nil || date.Year() < 2000 || date.Year() > 2100):
			problem(w, 422, "Give the date the customer promised to pay, or the date the bill-pay tool will pay.")
			return
		case !expects && in.ExpectedOn != "":
			problem(w, 422, "Only a promise or a bill-pay handoff carries a date.")
			return
		case in.Status == "disputed" && in.Note == "":
			problem(w, 422, "Say what is disputed.")
			return
		case len(in.Note) > 2000 || (in.Assign != "" && in.Assign != "take" && in.Assign != "release"):
			problem(w, 422, "Keep the note under 2,000 characters and assign as take or release.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Open item decisions are unavailable.")
			return
		}
		defer tx.Rollback()
		// Completing a capture locks the close row (a Xero snapshot's insert waits on it through its foreign key), so with
		// it held the capture read next stays the latest until this decision commits.
		if _, err = tx.ExecContext(r.Context(), `SELECT 1 FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")); err != nil {
			problem(w, 503, "Open item decisions are unavailable.")
			return
		}
		receivables, payables, meta, err := latestOpenItems(r, db)
		var wait waiting
		if errors.As(err, &wait) {
			problem(w, 409, string(wait))
			return
		}
		org, entity, clientErr := client(r)
		if err != nil || clientErr != nil {
			problem(w, 503, "Open items are unavailable.")
			return
		}
		// A decision is made against the capture the person saw; a newer capture or another ledger company needs a fresh look.
		if in.CaptureID != meta["capture_id"] {
			problem(w, 409, "A newer capture has arrived. Refresh before deciding.")
			return
		}
		all := append(slices.Clone(receivables), payables...)
		i := slices.IndexFunc(all, func(item openItem) bool { return item.Kind == in.Kind && item.ID == in.ItemID })
		if i < 0 {
			problem(w, 409, "That document is not open in the latest capture. Refresh before deciding.")
			return
		}
		item := all[i]
		if _, err := time.Parse("2006-01-02", item.Date); err != nil {
			item.Date = ""
		}
		// The same document's decisions from another month's close are serialized too.
		if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtext($1))`, strings.Join([]string{"open-item", org, strings.ToLower(entity), meta["company"].(string), in.Kind, in.ItemID}, "|")); err != nil {
			problem(w, 503, "Open item decisions are unavailable.")
			return
		}
		var previous, owner string
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,owner FROM open_item_actions WHERE organization_id=$1 AND lower(client_entity)=lower($2) AND company=$3 AND kind=$4 AND item_id=$5 ORDER BY open_item_actions.id DESC LIMIT 1`, org, entity, meta["company"], in.Kind, in.ItemID).Scan(&previous, &owner)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Open item decisions are unavailable.")
			return
		}
		if previous != in.PreviousID {
			problem(w, 409, "Someone changed this document's follow-up. Refresh before deciding.")
			return
		}
		switch in.Assign {
		case "take":
			owner = who
		case "release":
			owner = ""
		}
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO open_item_actions(organization_id,client_entity,source,company,kind,item_id,label,document_date,capture_id,owner,status,expected_on,note,actor) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::date,$9,$10,$11,NULLIF($12,'')::date,$13,$14) RETURNING id::text`,
			org, entity, meta["source"], meta["company"], in.Kind, in.ItemID, item.Label, item.Date, in.CaptureID, owner, in.Status, in.ExpectedOn, in.Note, who).Scan(&id); err != nil || tx.Commit() != nil {
			problem(w, 503, "The decision could not be saved. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
