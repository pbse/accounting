package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

type recurringItem struct {
	ID          string          `json:"id"`
	Digest      string          `json:"digest"`
	Scheduled   string          `json:"scheduled_balance"`
	AccountID   string          `json:"account_id,omitempty"`
	AccountName string          `json:"account_name,omitempty"`
	Location    string          `json:"location,omitempty"`
	Cells       json.RawMessage `json:"cells,omitempty"`
	Columns     []string        `json:"columns,omitempty"`
	Blocker     string          `json:"blocker,omitempty"`
	JobID       string          `json:"job_id,omitempty"`
	Paper       struct {
		prepaids.Workpaper
		CarryForward *preparation.CarryForward `json:"carry_forward"`
		Sources      []preparation.Source      `json:"sources"`
	} `json:"-"`
}

type recurringSnapshot struct {
	ID           string `json:"id"`
	Period       string `json:"period"`
	CapturedAt   string `json:"captured_at"`
	SourceID     string `json:"source_id"`
	SourceDigest string `json:"-"`
	Active       bool   `json:"-"`
}

func recurringPreview(ctx context.Context, tx *sql.Tx, closeID string) (recurringSnapshot, []recurringItem, error) {
	var snapshot recurringSnapshot
	var period string
	if err := tx.QueryRowContext(ctx, `SELECT period FROM client_closes WHERE id::text=$1`, closeID).Scan(&period); err != nil {
		return snapshot, nil, err
	}
	var raw []byte
	var payload struct {
		Period   string                     `json:"period"`
		SourceID string                     `json:"source_id"`
		Accounts json.RawMessage            `json:"accounts"`
		Reports  map[string]json.RawMessage `json:"reports"`
	}
	err := tx.QueryRowContext(ctx, `SELECT s.payload,s.id::text,s.captured_at::text,COALESCE(e.digest,''),COALESCE(x.state='active' AND e.superseded_by IS NULL AND e.extraction_state='ready',false) FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id::text=$1 AND s.payload->>'period'=$2 ORDER BY s.id DESC LIMIT 1`, closeID, period).Scan(&raw, &snapshot.ID, &snapshot.CapturedAt, &snapshot.SourceDigest, &snapshot.Active)
	if err != nil && err != sql.ErrNoRows {
		return snapshot, nil, err
	}
	if err == nil {
		if json.Unmarshal(raw, &payload) != nil {
			return snapshot, nil, fmt.Errorf("Invalid period snapshot")
		}
		snapshot.Period, snapshot.SourceID = payload.Period, payload.SourceID
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.id::text,w.digest,w.paper,COALESCE((SELECT j.id::text FROM preparation_jobs j WHERE j.close_id=w.close_id AND j.state='queued' AND j.plans->0->>'revises'=w.id::text ORDER BY j.id DESC LIMIT 1),'') FROM workpapers w WHERE w.close_id::text=$1 AND w.kind='prepaid' AND w.paper ? 'carry_forward' AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text) ORDER BY w.id DESC LIMIT 101`, closeID)
	if err != nil {
		return snapshot, nil, err
	}
	items := []recurringItem{}
	for rows.Next() {
		var item recurringItem
		if err = rows.Scan(&item.ID, &item.Digest, &raw, &item.JobID); err != nil {
			rows.Close()
			return snapshot, nil, err
		}
		if err = json.Unmarshal(raw, &item.Paper); err != nil {
			rows.Close()
			return snapshot, nil, err
		}
		item.Scheduled = item.Paper.PrepaidBalance
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snapshot, nil, err
	}
	if len(items) > 100 {
		return snapshot, nil, fmt.Errorf("Recurring work exceeds the 100-paper preview limit")
	}
	for i := range items {
		item := &items[i]
		switch {
		case item.JobID != "":
			item.Blocker = "Comparison job #" + item.JobID + " is queued. Wait for its draft before changing the booked balance."
		case snapshot.ID == "":
			item.Blocker = "Capture this month's QuickBooks period reports first."
		case !snapshot.Active:
			item.Blocker = "Reconnect or refresh the current QuickBooks evidence."
		default:
			if item.Paper.CarryForward == nil {
				item.Blocker = "Carried schedule lineage is missing."
				break
			}
			var previousState, previousDigest, previousReview string
			if err = tx.QueryRowContext(ctx, `SELECT review_state,digest,review_id FROM workpaper_review_state WHERE id::text=$1`, item.Paper.CarryForward.PaperID).Scan(&previousState, &previousDigest, &previousReview); err != nil {
				return snapshot, nil, err
			}
			if previousState != "approved" || previousDigest != item.Paper.CarryForward.Digest || previousReview != item.Paper.CarryForward.ReviewID {
				item.Blocker = "The reviewed predecessor changed. Refresh the carried schedule first."
				break
			}
			var accounts []struct {
				ID                   string `json:"Id"`
				Name, AccountSubType string
			}
			if json.Unmarshal(payload.Accounts, &accounts) != nil {
				return snapshot, nil, fmt.Errorf("Invalid account directory")
			}
			for _, a := range accounts {
				if a.AccountSubType == "PrepaidExpenses" && strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(item.Paper.Input.PrepaidAccount)) {
					if item.AccountID != "" {
						item.Blocker = "Multiple prepaid accounts match this name; resolve the mapping."
						break
					}
					item.AccountID, item.AccountName = a.ID, a.Name
				}
			}
			if item.AccountID == "" && item.Blocker == "" {
				item.Blocker = "No exact prepaid account name matches the carried schedule. Resolve the mapping."
			}
			if item.Blocker == "" {
				_, item.Location, item.Cells, err = quickbooks.TrialBalanceAccount(payload.Accounts, payload.Reports["TrialBalance"], item.AccountID)
				if err != nil {
					item.Blocker = err.Error()
				} else {
					var report struct {
						Columns struct{ Column []struct{ ColTitle string } }
					}
					if json.Unmarshal(payload.Reports["TrialBalance"], &report) != nil {
						item.Blocker = "Trial Balance columns are invalid."
					} else {
						for _, c := range report.Columns.Column {
							item.Columns = append(item.Columns, c.ColTitle)
						}
					}
				}
			}
		}
	}
	return snapshot, items, nil
}

func registerRecurring(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/recurring", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Recurring review unavailable.")
			return
		}
		defer tx.Rollback()
		snapshot, items, err := recurringPreview(r.Context(), tx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Could not inspect recurring work.")
			return
		}
		jsonReply(w, 200, map[string]any{"snapshot": snapshot, "items": items})
	})
	mux.HandleFunc("POST /v1/closes/{id}/recurring", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			PaperID       string `json:"paper_id"`
			Digest        string `json:"digest"`
			SnapshotID    string `json:"snapshot_id"`
			BookedBalance string `json:"booked_balance"`
			Confirmed     bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the account, cutoff, booked balance and supported method.")
			return
		}
		balance, err := prepaids.Amount(in.BookedBalance)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Recurring review unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, entity, period string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity,period FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID, &entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var decision string
		err = tx.QueryRowContext(r.Context(), `SELECT decision FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&decision)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Could not inspect sign-off.")
			return
		}
		if decision == "signed_off" {
			problem(w, 409, "Reopen this close before refreshing recurring work.")
			return
		}
		snapshot, items, err := recurringPreview(r.Context(), tx, closeID)
		if err != nil {
			problem(w, 503, "Could not inspect current evidence.")
			return
		}
		if snapshot.ID == "" || snapshot.ID != in.SnapshotID || !snapshot.Active {
			problem(w, 409, "Period evidence changed. Refresh the comparison.")
			return
		}
		var selected *recurringItem
		for i := range items {
			if items[i].ID == in.PaperID && items[i].Digest == in.Digest {
				selected = &items[i]
				break
			}
		}
		if selected == nil || selected.Paper.CarryForward == nil {
			problem(w, 409, "Carried work or account evidence changed. Refresh the comparison.")
			return
		}
		if selected.JobID != "" {
			var currentDigest string
			if err = tx.QueryRowContext(r.Context(), `SELECT digest FROM preparation_jobs WHERE id::text=$1 AND close_id::text=$2`, selected.JobID, closeID).Scan(&currentDigest); err != nil {
				problem(w, 503, "Could not inspect queued comparison.")
				return
			}
			wantDigest := preparation.Digest([]byte("recurring:" + selected.ID + ":" + snapshot.ID + ":" + prepaids.Money(balance)))
			if currentDigest != wantDigest {
				problem(w, 409, "A different booked-balance comparison is already queued. Wait for it to finish, then refresh.")
				return
			}
			jsonReply(w, 202, map[string]string{"job_id": selected.JobID})
			return
		}
		if selected.Blocker != "" {
			problem(w, 409, "Carried work or account evidence changed. Refresh the comparison.")
			return
		}
		input := selected.Paper.Input
		input.BookedBalance = prepaids.Money(balance)
		input.BookingReference = fmt.Sprintf("QuickBooks snapshot #%s, Trial Balance account #%s, %s", snapshot.ID, selected.AccountID, selected.Location)
		input.MethodConfirmed, input.BookingConfirmed, input.ExamplesConfirmed = true, true, true
		if len(input.BookingReference) > 200 {
			problem(w, 422, "Ledger reference is too long.")
			return
		}
		base := len(selected.Paper.CarryForward.SourceIDs)
		if len(selected.Paper.Sources) < base {
			problem(w, 409, "Carried source lineage changed.")
			return
		}
		sources := append([]preparation.Source{}, selected.Paper.Sources[:base]...)
		sources = append(sources, preparation.Source{ID: snapshot.SourceID, Digest: snapshot.SourceDigest, Name: "QuickBooks " + period + " period reports", Role: "period_ledger", Location: selected.Location})
		raw, _ := json.Marshal(input)
		plan := preparation.Plan{Kind: "prepaid", Input: raw, Sources: sources, CarryForward: selected.Paper.CarryForward, Revises: selected.ID}
		if err = preparation.ValidateCarry(r.Context(), tx, plan, closeID, entity, period); err != nil {
			problem(w, 409, err.Error())
			return
		}
		if _, err = preparation.Calculate(plan, entity, period); err != nil {
			problem(w, 422, err.Error())
			return
		}
		plans, _ := json.Marshal([]preparation.Plan{plan})
		var job string
		jobDigest := preparation.Digest([]byte("recurring:" + selected.ID + ":" + snapshot.ID + ":" + input.BookedBalance))
		err = tx.QueryRowContext(r.Context(), `INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,digest) DO UPDATE SET state=CASE WHEN preparation_jobs.state IN ('failed','cancelled') THEN 'queued' ELSE preparation_jobs.state END,error=CASE WHEN preparation_jobs.state IN ('failed','cancelled') THEN '' ELSE preparation_jobs.error END,finished_at=CASE WHEN preparation_jobs.state IN ('failed','cancelled') THEN NULL ELSE preparation_jobs.finished_at END RETURNING id::text`, closeID, jobDigest, string(plans), actor).Scan(&job)
		if err != nil {
			jobProblem(w, err, "Could not queue recurring preparation.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Recurring preparation could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 202, map[string]string{"job_id": job})
	})
}
