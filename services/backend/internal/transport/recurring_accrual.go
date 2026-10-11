package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

type recurringAccrualItem struct {
	ID          string          `json:"id"`
	Digest      string          `json:"digest"`
	ReviewID    string          `json:"review_id"`
	Amount      string          `json:"amount"`
	Rule        string          `json:"rule"`
	AccountID   string          `json:"account_id,omitempty"`
	AccountName string          `json:"account_name,omitempty"`
	Location    string          `json:"location,omitempty"`
	Cells       json.RawMessage `json:"cells,omitempty"`
	Blocker     string          `json:"blocker,omitempty"`
	// Defaults for the person's one confirmation: the account's credit balance in this capture's Trial Balance (only
	// when the balance sits on its credit side), last month's reversal choice, and this month's document when exactly
	// one the agent read as an accrual shows the rule's amount.
	BookedBalance     string `json:"booked_balance,omitempty"`
	ReverseNextMonth  bool   `json:"reverse_next_month"`
	SuggestedEvidence string `json:"suggested_evidence,omitempty"`
	// suggestedCurrency is the currency the suggested document itself states ("" when its reading names none).
	suggestedCurrency string
	Paper             accruals.Workpaper `json:"-"`
}

func recurringAccrualPreview(ctx context.Context, tx *sql.Tx, closeID, org string) (recurringSnapshot, []recurringAccrualItem, string, string, error) {
	var snapshot recurringSnapshot
	var entity, period string
	err := tx.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id::text=$1 AND organization_id=$2`, closeID, org).Scan(&entity, &period)
	if err != nil {
		return snapshot, nil, "", "", err
	}
	when, err := time.Parse("2006-01", period)
	if err != nil {
		return snapshot, nil, "", "", err
	}
	previous := when.AddDate(0, -1, 0).Format("2006-01")
	var raw []byte
	var payload struct {
		Accounts json.RawMessage            `json:"accounts"`
		Reports  map[string]json.RawMessage `json:"reports"`
		SourceID string                     `json:"source_id"`
	}
	err = tx.QueryRowContext(ctx, `SELECT s.id::text,s.payload,s.captured_at::text,COALESCE(e.digest,''),COALESCE(x.state='active' AND e.superseded_by IS NULL AND e.extraction_state='ready',false) FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND s.payload->>'period'=$2 AND `+sourceAllows("s.close_id", "quickbooks")+` ORDER BY s.id DESC LIMIT 1`, closeID, period).Scan(&snapshot.ID, &raw, &snapshot.CapturedAt, &snapshot.SourceDigest, &snapshot.Active)
	if err != nil && err != sql.ErrNoRows {
		return snapshot, nil, "", "", err
	}
	if err == nil {
		if json.Unmarshal(raw, &payload) != nil {
			return snapshot, nil, "", "", fmt.Errorf("Invalid period snapshot")
		}
		snapshot.Period, snapshot.SourceID = period, payload.SourceID
	}
	balances := map[string]*big.Rat{}
	if snapshot.Active {
		if _, list, e := quickbooks.TrialBalance(payload.Accounts, payload.Reports["TrialBalance"]); e == nil {
			for _, b := range list {
				balances[b.ID] = b.Net
			}
		}
	}
	docs, err := accrualDocuments(ctx, tx, closeID, payload.SourceID)
	if err != nil {
		return snapshot, nil, "", "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.id::text,w.digest,w.review_id,w.paper,c.id::text FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1 AND lower(c.entity)=lower($2) AND c.period=$3 AND w.kind='accrual' AND w.review_state='approved' AND w.paper->'input'->>'method'=$4 AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text) ORDER BY w.id DESC LIMIT 101`, org, entity, previous, accruals.RecurringMethod)
	if err != nil {
		return snapshot, nil, "", "", err
	}
	defer rows.Close()
	items := []recurringAccrualItem{}
	priorClose := ""
	for rows.Next() {
		var item recurringAccrualItem
		if err = rows.Scan(&item.ID, &item.Digest, &item.ReviewID, &raw, &priorClose); err != nil {
			return snapshot, nil, "", "", err
		}
		if json.Unmarshal(raw, &item.Paper) != nil {
			return snapshot, nil, "", "", fmt.Errorf("Invalid recurring accrual")
		}
		item.Amount, item.Rule = item.Paper.Input.Amount, item.Paper.Input.AccountantRuleReference
		if snapshot.ID == "" {
			item.Blocker = "Capture current QuickBooks period reports first."
		} else if !snapshot.Active {
			item.Blocker = "Refresh the current QuickBooks period evidence."
		} else {
			var accounts []struct {
				ID                string `json:"Id"`
				Name, AccountType string
			}
			if json.Unmarshal(payload.Accounts, &accounts) != nil {
				return snapshot, nil, "", "", fmt.Errorf("Invalid account directory")
			}
			for _, account := range accounts {
				if account.AccountType == "Other Current Liability" && strings.EqualFold(strings.TrimSpace(account.Name), strings.TrimSpace(item.Paper.Input.LiabilityAccount)) {
					if item.AccountID != "" {
						item.Blocker = "Multiple liability accounts match this name."
						break
					}
					item.AccountID, item.AccountName = account.ID, account.Name
				}
			}
			if item.AccountID == "" && item.Blocker == "" {
				item.Blocker = "No exact current-liability account matches the prior rule."
			}
			if item.Blocker == "" {
				_, item.Location, item.Cells, err = quickbooks.TrialBalanceLiability(payload.Accounts, payload.Reports["TrialBalance"], item.AccountID, item.Paper.Input.Currency)
				if err != nil {
					item.Blocker = err.Error()
				} else if net, ok := balances[item.AccountID]; ok && net.Sign() <= 0 {
					item.BookedBalance = money(new(big.Rat).Neg(net))
				}
			}
		}
		item.ReverseNextMonth = item.Paper.Input.ReverseNextMonth
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return snapshot, nil, "", "", err
	}
	suggestEvidence(items, docs)
	if len(items) > 100 {
		return snapshot, nil, "", "", fmt.Errorf("Recurring accrual preview exceeds 100 papers")
	}
	return snapshot, items, entity, previous, nil
}

// accrualDocument is a current, readable document of the close the agent read as an accrual, with the one amount it
// shows (cited from the document itself) and the currency it states, if any.
type accrualDocument struct {
	id, currency string
	amount       int64
}

// accrualDocuments lists those documents; the capture's own evidence, and documents with no amount or several, are not
// among them.
func accrualDocuments(ctx context.Context, tx *sql.Tx, closeID, capture string) ([]accrualDocument, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id::text,i.proposal FROM evidence e JOIN preparation_intakes i ON i.source_id=e.id
 WHERE e.close_id::text=$1 AND e.id::text<>$2 AND e.superseded_by IS NULL AND e.extraction_state='ready' AND i.proposal->>'kind'='accrual'`, closeID, capture)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parse := func(v string) (int64, error) {
		return prepaids.Amount(strings.ReplaceAll(strings.TrimSpace(v), ",", ""))
	}
	var out []accrualDocument
	for rows.Next() {
		var id string
		var raw []byte
		var p preparation.Proposal
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		doc, amounts, unclear := accrualDocument{id: id}, 0, false
		for _, f := range p.Facts {
			switch f.Field {
			case "amount":
				amounts++
				value, e := parse(f.Value)
				if e == nil && slices.ContainsFunc(f.Candidates, func(c preparation.Candidate) bool {
					v, e := parse(c.Value)
					return c.SourceID == id && e == nil && v == value
				}) {
					doc.amount = value
				}
			case "currency":
				// Candidates with no resolved value are a currency the document states but the reading left in conflict.
				doc.currency, unclear = strings.ToUpper(strings.TrimSpace(f.Value)), strings.TrimSpace(f.Value) == "" && len(f.Candidates) > 0
			}
		}
		if amounts == 1 && doc.amount > 0 && !unclear {
			out = append(out, doc)
		}
	}
	return out, rows.Err()
}

// suggestEvidence names this month's document for each rule when exactly one document shows the rule's amount in its
// currency and no other rule has that amount, so one document is never offered for two rules.
func suggestEvidence(items []recurringAccrualItem, docs []accrualDocument) {
	rules := map[int64]int{}
	for _, item := range items {
		if amount, e := prepaids.Amount(item.Amount); e == nil {
			rules[amount]++
		}
	}
	for i := range items {
		amount, e := prepaids.Amount(items[i].Amount)
		if e != nil || rules[amount] != 1 {
			continue
		}
		matches := slices.DeleteFunc(slices.Clone(docs), func(d accrualDocument) bool {
			return d.amount != amount || (d.currency != "" && d.currency != items[i].Paper.Input.Currency)
		})
		if len(matches) == 1 {
			items[i].SuggestedEvidence, items[i].suggestedCurrency = matches[0].id, matches[0].currency
		}
	}
}

func registerRecurringAccrual(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/recurring-accruals"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Recurring accrual preview unavailable.")
			return
		}
		defer tx.Rollback()
		snapshot, items, _, _, err := recurringAccrualPreview(r.Context(), tx, r.PathValue("id"), organization(r))
		if err != nil {
			problem(w, 503, "Could not compare reviewed accruals.")
			return
		}
		jsonReply(w, 200, map[string]any{"snapshot": snapshot, "items": items})
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			PaperID          string `json:"paper_id"`
			Digest           string `json:"digest"`
			ReviewID         string `json:"review_id"`
			SnapshotID       string `json:"snapshot_id"`
			EvidenceID       string `json:"evidence_id"`
			BookedBalance    string `json:"booked_balance"`
			ReverseNextMonth bool   `json:"reverse_next_month"`
			Confirmed        bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		balance, err := prepaids.Amount(in.BookedBalance)
		if !in.Confirmed || err != nil {
			problem(w, 422, "Confirm a fresh booked liability, source, method, examples and reversal choice.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Recurring accrual unavailable.")
			return
		}
		defer tx.Rollback()
		var entity, period, decision string
		err = tx.QueryRowContext(r.Context(), `SELECT entity,period FROM client_closes WHERE id::text=$1 AND organization_id=$2 FOR UPDATE`, r.PathValue("id"), organization(r)).Scan(&entity, &period)
		if err != nil {
			problem(w, 404, "Close unavailable.")
			return
		}
		err = tx.QueryRowContext(r.Context(), `SELECT decision FROM close_signoffs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, r.PathValue("id")).Scan(&decision)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Sign-off unavailable.")
			return
		}
		if decision == "signed_off" {
			problem(w, 409, "Reopen this close before preparing a new accrual.")
			return
		}
		snapshot, items, _, previous, err := recurringAccrualPreview(r.Context(), tx, r.PathValue("id"), organization(r))
		if err != nil {
			problem(w, 503, "Current comparison unavailable.")
			return
		}
		var selected *recurringAccrualItem
		for i := range items {
			if items[i].ID == in.PaperID && items[i].Digest == in.Digest && items[i].ReviewID == in.ReviewID {
				selected = &items[i]
				break
			}
		}
		if selected == nil || selected.Blocker != "" || snapshot.ID != in.SnapshotID || !snapshot.Active {
			problem(w, 409, "Rule, review or period evidence changed. Refresh the comparison.")
			return
		}
		// A person's choice for this month replaces the sweep's still-queued one for the same rule, so it is not stranded
		// behind it.
		if _, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='cancelled',error='Replaced by a person''s choice for this month' WHERE close_id::text=$1 AND created_by='scheduled-recurring-accrual' AND state='queued' AND plans->0->'recurring_accrual'->>'paper_id'=$2`, r.PathValue("id"), selected.ID); err != nil {
			problem(w, 503, "Recurring accrual unavailable.")
			return
		}
		job, status, message := queueRecurringAccrual(r.Context(), tx, r.PathValue("id"), entity, period, actor, snapshot, previous, selected, in.EvidenceID, prepaids.Money(balance), in.ReverseNextMonth, "")
		if status != 0 {
			problem(w, status, message)
			return
		}
		if err = tx.Commit(); err != nil {
			jobProblem(w, err, "Could not queue recurring accrual.")
			return
		}
		jsonReply(w, 202, map[string]string{"job_id": job})
	})
}

// queueRecurringAccrual queues one month of a reviewed recurring accrual rule from this month's service document and
// booked balance, for a person (or the firm's standing approval) to decide; it changes nothing it does not queue.
// note, when set, is added to the source reference (the sweep marks its own reading for confirmation).
func queueRecurringAccrual(ctx context.Context, tx *sql.Tx, closeID, entity, period, actor string, snapshot recurringSnapshot, previous string, selected *recurringAccrualItem, evidenceID, bookedBalance string, reverse bool, note string) (string, int, string) {
	var sourceName, sourceDigest string
	err := tx.QueryRowContext(ctx, `SELECT name,digest FROM evidence WHERE id::text=$1 AND close_id::text=$2 AND superseded_by IS NULL AND extraction_state='ready'`, evidenceID, closeID).Scan(&sourceName, &sourceDigest)
	if err != nil || evidenceID == snapshot.SourceID {
		return "", 422, "Choose fresh current-month service evidence, separate from the ledger report."
	}
	when, _ := time.Parse("2006-01", period)
	input := selected.Paper.Input
	input.ClosePeriod = period
	input.ServiceStart = when.Format("2006-01-02")
	input.ServiceEnd = when.AddDate(0, 1, -1).Format("2006-01-02")
	input.SourceReference = fmt.Sprintf("%s (#%s); prior rule draft #%s%s", sourceName, evidenceID, selected.ID, note)
	input.BookedBalance = bookedBalance
	input.BookingReference = fmt.Sprintf("QuickBooks snapshot #%s, Trial Balance account #%s, %s", snapshot.ID, selected.AccountID, selected.Location)
	input.ReverseNextMonth = reverse
	input.MethodConfirmed, input.BookingConfirmed, input.ExamplesConfirmed = true, true, true
	if len(input.SourceReference) > 200 || len(input.BookingReference) > 200 {
		return "", 422, "Source or ledger reference is too long."
	}
	raw, _ := json.Marshal(input)
	plan := preparation.Plan{Kind: "accrual", Input: raw, Sources: []preparation.Source{{ID: evidenceID, Digest: sourceDigest, Name: sourceName, Role: "current_service"}, {ID: snapshot.SourceID, Digest: snapshot.SourceDigest, Name: "QuickBooks " + period + " period reports", Role: "period_ledger", Location: selected.Location}}, RecurringAccrual: &preparation.RecurringAccrual{CloseID: "", PaperID: selected.ID, Digest: selected.Digest, ReviewID: selected.ReviewID, Period: previous}}
	err = tx.QueryRowContext(ctx, `SELECT close_id::text FROM workpapers WHERE id::text=$1`, selected.ID).Scan(&plan.RecurringAccrual.CloseID)
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id::text FROM workpapers WHERE close_id::text=$1 AND kind='accrual' AND paper->'recurring_accrual'->>'paper_id'=$2 AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=workpapers.close_id AND newer.paper->>'revises'=workpapers.id::text) ORDER BY workpapers.id DESC LIMIT 1),'')`, closeID, selected.ID).Scan(&plan.Revises)
	}
	if err != nil || preparation.ValidateRecurringAccrual(ctx, tx, plan, closeID, entity, period) != nil {
		return "", 409, "Reviewed predecessor changed."
	}
	if _, err = preparation.Calculate(plan, entity, period); err != nil {
		return "", 422, err.Error()
	}
	plans, _ := json.Marshal([]preparation.Plan{plan})
	jobDigest := preparation.Digest([]byte("recurring-accrual:" + selected.ID + ":" + selected.ReviewID + ":" + snapshot.ID + ":" + evidenceID + ":" + input.BookedBalance + ":" + fmt.Sprint(reverse)))
	var job string
	// The same choice made again re-queues its job if it was cancelled (a person confirming what the sweep queued), as
	// that person's, with the plan just validated.
	if err = tx.QueryRowContext(ctx, `INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,digest) DO UPDATE SET digest=preparation_jobs.digest,
 state=CASE WHEN preparation_jobs.state='cancelled' THEN 'queued' ELSE preparation_jobs.state END,error=CASE WHEN preparation_jobs.state='cancelled' THEN '' ELSE preparation_jobs.error END,
 plans=CASE WHEN preparation_jobs.state='cancelled' THEN EXCLUDED.plans ELSE preparation_jobs.plans END,created_by=CASE WHEN preparation_jobs.state='cancelled' THEN EXCLUDED.created_by ELSE preparation_jobs.created_by END RETURNING id::text`, closeID, jobDigest, string(plans), actor).Scan(&job); err != nil {
		return "", 503, "Could not queue recurring accrual."
	}
	return job, 0, ""
}

// sweepRecurringAccruals queues, for up to 20 open closes after the cursor, each reviewed recurring accrual whose month
// the books and documents already give in full: no blocker, a booked balance read from this capture's Trial Balance and
// exactly one current document showing the rule's amount. A rule already tried against this capture, by a person or a
// sweep, is left alone; nothing is approved here.
func sweepRecurringAccruals(ctx context.Context, db *sql.DB, cursor string) (string, int, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id::text FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id>$1::bigint AND o.enabled
 AND NOT `+seedInProgress("c")+`
 AND COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')<>'signed_off'
 AND EXISTS(SELECT 1 FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload->>'period'=c.period AND `+sourceAllows("c.id", "quickbooks")+`)
 ORDER BY c.id LIMIT 20`, cursor)
	if err != nil {
		return cursor, 0, err
	}
	closes := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		closes = append(closes, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return cursor, 0, err
	}
	next := "0"
	if len(closes) == 20 {
		next = closes[19]
	}
	queued := 0
	for _, id := range closes {
		n, err := queueRecurringAccruals(ctx, db, id)
		if err != nil {
			return next, queued, err
		}
		queued += n
	}
	return next, queued, nil
}

func queueRecurringAccruals(ctx context.Context, db *sql.DB, closeID string) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var org, entity, period, decision string
	if err = tx.QueryRowContext(ctx, `SELECT c.organization_id,c.entity,c.period,COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'') FROM client_closes c WHERE c.id::text=$1 FOR UPDATE`, closeID).Scan(&org, &entity, &period, &decision); err != nil || decision == "signed_off" {
		return 0, err
	}
	snapshot, items, _, previous, err := recurringAccrualPreview(ctx, tx, closeID, org)
	if err != nil || !snapshot.Active {
		return 0, err
	}
	queued := 0
	for i := range items {
		item := &items[i]
		// The document must itself state the rule's currency: an unstated one is an ambiguity a person settles.
		if item.Blocker != "" || item.BookedBalance == "" || item.SuggestedEvidence == "" || item.suggestedCurrency != item.Paper.Input.Currency {
			continue
		}
		var tried bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM preparation_jobs WHERE close_id::text=$1 AND plans->0->'recurring_accrual'->>'paper_id'=$2 AND plans->0->'sources' @> jsonb_build_array(jsonb_build_object('id',$3::text)))`, closeID, item.ID, snapshot.SourceID).Scan(&tried); err != nil {
			return 0, err
		}
		if tried {
			continue
		}
		if _, err = tx.ExecContext(ctx, `SAVEPOINT accrual`); err != nil {
			return 0, err
		}
		if _, status, _ := queueRecurringAccrual(ctx, tx, closeID, entity, period, "scheduled-recurring-accrual", snapshot, previous, item, item.SuggestedEvidence, item.BookedBalance, item.ReverseNextMonth, "; agent-read, confirm before approving"); status != 0 {
			if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT accrual`); err != nil {
				return 0, err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT accrual`); err != nil {
			return 0, err
		}
		queued++
	}
	return queued, tx.Commit()
}
