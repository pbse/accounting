package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

type recurringAccrualItem struct {
	ID          string             `json:"id"`
	Digest      string             `json:"digest"`
	ReviewID    string             `json:"review_id"`
	Amount      string             `json:"amount"`
	Rule        string             `json:"rule"`
	AccountID   string             `json:"account_id,omitempty"`
	AccountName string             `json:"account_name,omitempty"`
	Location    string             `json:"location,omitempty"`
	Cells       json.RawMessage    `json:"cells,omitempty"`
	Blocker     string             `json:"blocker,omitempty"`
	Paper       accruals.Workpaper `json:"-"`
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
	err = tx.QueryRowContext(ctx, `SELECT s.id::text,s.payload,s.captured_at::text,COALESCE(e.digest,''),COALESCE(x.state='active' AND e.superseded_by IS NULL AND e.extraction_state='ready',false) FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND s.payload->>'period'=$2 ORDER BY s.id DESC LIMIT 1`, closeID, period).Scan(&snapshot.ID, &raw, &snapshot.CapturedAt, &snapshot.SourceDigest, &snapshot.Active)
	if err != nil && err != sql.ErrNoRows {
		return snapshot, nil, "", "", err
	}
	if err == nil {
		if json.Unmarshal(raw, &payload) != nil {
			return snapshot, nil, "", "", fmt.Errorf("Invalid period snapshot")
		}
		snapshot.Period, snapshot.SourceID = period, payload.SourceID
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
				}
			}
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return snapshot, nil, "", "", err
	}
	if len(items) > 100 {
		return snapshot, nil, "", "", fmt.Errorf("Recurring accrual preview exceeds 100 papers")
	}
	return snapshot, items, entity, previous, nil
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
		var sourceName, sourceDigest string
		err = tx.QueryRowContext(r.Context(), `SELECT name,digest FROM evidence WHERE id::text=$1 AND close_id::text=$2 AND superseded_by IS NULL AND extraction_state='ready'`, in.EvidenceID, r.PathValue("id")).Scan(&sourceName, &sourceDigest)
		if err != nil || in.EvidenceID == snapshot.SourceID {
			problem(w, 422, "Choose fresh current-month service evidence, separate from the ledger report.")
			return
		}
		when, _ := time.Parse("2006-01", period)
		input := selected.Paper.Input
		input.ClosePeriod = period
		input.ServiceStart = when.Format("2006-01-02")
		input.ServiceEnd = when.AddDate(0, 1, -1).Format("2006-01-02")
		input.SourceReference = fmt.Sprintf("%s (#%s); prior rule draft #%s", sourceName, in.EvidenceID, selected.ID)
		input.BookedBalance = prepaids.Money(balance)
		input.BookingReference = fmt.Sprintf("QuickBooks snapshot #%s, Trial Balance account #%s, %s", snapshot.ID, selected.AccountID, selected.Location)
		input.ReverseNextMonth = in.ReverseNextMonth
		input.MethodConfirmed, input.BookingConfirmed, input.ExamplesConfirmed = true, true, true
		if len(input.SourceReference) > 200 || len(input.BookingReference) > 200 {
			problem(w, 422, "Source or ledger reference is too long.")
			return
		}
		raw, _ := json.Marshal(input)
		plan := preparation.Plan{Kind: "accrual", Input: raw, Sources: []preparation.Source{{ID: in.EvidenceID, Digest: sourceDigest, Name: sourceName, Role: "current_service"}, {ID: snapshot.SourceID, Digest: snapshot.SourceDigest, Name: "QuickBooks " + period + " period reports", Role: "period_ledger", Location: selected.Location}}, RecurringAccrual: &preparation.RecurringAccrual{CloseID: "", PaperID: selected.ID, Digest: selected.Digest, ReviewID: selected.ReviewID, Period: previous}}
		err = tx.QueryRowContext(r.Context(), `SELECT close_id::text FROM workpapers WHERE id::text=$1`, selected.ID).Scan(&plan.RecurringAccrual.CloseID)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM workpapers WHERE close_id::text=$1 AND kind='accrual' AND paper->'recurring_accrual'->>'paper_id'=$2 AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=workpapers.close_id AND newer.paper->>'revises'=workpapers.id::text) ORDER BY id DESC LIMIT 1),'')`, r.PathValue("id"), selected.ID).Scan(&plan.Revises)
		}
		if err != nil || preparation.ValidateRecurringAccrual(r.Context(), tx, plan, r.PathValue("id"), entity, period) != nil {
			problem(w, 409, "Reviewed predecessor changed.")
			return
		}
		if _, err = preparation.Calculate(plan, entity, period); err != nil {
			problem(w, 422, err.Error())
			return
		}
		plans, _ := json.Marshal([]preparation.Plan{plan})
		jobDigest := preparation.Digest([]byte("recurring-accrual:" + selected.ID + ":" + selected.ReviewID + ":" + snapshot.ID + ":" + in.EvidenceID + ":" + input.BookedBalance + ":" + fmt.Sprint(in.ReverseNextMonth)))
		var job string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,digest) DO UPDATE SET digest=preparation_jobs.digest RETURNING id::text`, r.PathValue("id"), jobDigest, string(plans), actor).Scan(&job)
		if err != nil || tx.Commit() != nil {
			jobProblem(w, err, "Could not queue recurring accrual.")
			return
		}
		jsonReply(w, 202, map[string]string{"job_id": job})
	})
}
