// Package preparation runs bounded, deterministic preparation from immutable evidence.
package preparation

import (
	"bytes"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/reconciliation"
)

type Source struct {
	ID       string `json:"id"`
	Digest   string `json:"digest"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Location string `json:"location"`
}
type Plan struct {
	CarryForward     *CarryForward     `json:"carry_forward,omitempty"`
	RecurringAccrual *RecurringAccrual `json:"recurring_accrual,omitempty"`
	Revises          string            `json:"revises,omitempty"`
	Kind             string            `json:"kind"`
	Input            json.RawMessage   `json:"input"`
	Sources          []Source          `json:"sources"`
}

func Digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func Strict(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("Invalid preparation input: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("Send one input object")
	}
	return nil
}

// Calculate uses existing domain validation and preserves the exact evidence revision.
func Calculate(p Plan, entity, period string) ([]byte, error) {
	var value any
	switch p.Kind {
	case "prepaid":
		var in prepaids.Input
		if err := Strict(p.Input, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Entity) != entity || in.ClosePeriod != period {
			return nil, fmt.Errorf("Client and period must match this close")
		}
		prepare := prepaids.Prepare
		if p.CarryForward != nil && p.Revises == "" {
			prepare = prepaids.ScheduleOnly
		}
		paper, err := prepare(in)
		if err != nil {
			return nil, err
		}
		value = paper
	case "accrual":
		var in accruals.Input
		if err := Strict(p.Input, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Entity) != entity || in.ClosePeriod != period || p.CarryForward != nil || (p.RecurringAccrual != nil && in.Method != accruals.RecurringMethod) {
			return nil, fmt.Errorf("Accrual must match this close and cannot inherit carry-forward approval")
		}
		paper, err := accruals.Prepare(in)
		if err != nil {
			return nil, err
		}
		value = paper
	case "reconciliation":
		var in reconciliation.Input
		if err := Strict(p.Input, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Entity) != entity || in.ClosePeriod != period {
			return nil, fmt.Errorf("Client and period must match this close")
		}
		paper, err := reconciliation.Prepare(in)
		if err != nil {
			return nil, err
		}
		value = paper
	case "owner":
		var in owners.Input
		if err := Strict(p.Input, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Entity) != entity || in.ClosePeriod != period || p.CarryForward != nil || p.RecurringAccrual != nil {
			return nil, fmt.Errorf("Client and period must match this close")
		}
		paper, err := owners.Prepare(in)
		if err != nil {
			return nil, err
		}
		value = paper
	case "loan":
		var in loans.Input
		if err := Strict(p.Input, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Entity) != entity || in.ClosePeriod != period || p.CarryForward != nil || p.RecurringAccrual != nil {
			return nil, fmt.Errorf("Client and period must match this close")
		}
		paper, err := loans.Prepare(in)
		if err != nil {
			return nil, err
		}
		value = paper
	case "settlement":
		var in reconciliation.SettlementInput
		if err := Strict(p.Input, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Entity) != entity || in.ClosePeriod != period || p.CarryForward != nil || p.RecurringAccrual != nil {
			return nil, fmt.Errorf("Client and period must match this close")
		}
		paper, err := reconciliation.PrepareSettlement(in)
		if err != nil {
			return nil, err
		}
		value = paper
	default:
		return nil, fmt.Errorf("Unsupported preparation kind")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if p.CarryForward != nil {
		object["carry_forward"], _ = json.Marshal(p.CarryForward)
	}
	if p.RecurringAccrual != nil {
		object["recurring_accrual"], _ = json.Marshal(p.RecurringAccrual)
	}
	if p.Revises != "" {
		object["revises"], _ = json.Marshal(p.Revises)
	}
	object["sources"], err = json.Marshal(p.Sources)
	if err != nil {
		return nil, err
	}
	return json.Marshal(object)
}

// ProcessOne holds a database lock only during local calculation. A crash rolls
// back both results and completion, leaving the job queued for the next worker.
// ponytail: at most 20 small plans per transaction; use checkpoints before adding slow external tools.
func ProcessOne(ctx context.Context, db *sql.DB) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id, closeID string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,close_id::text,plans FROM preparation_jobs WHERE state='queued' AND close_id IN (SELECT c.id FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE o.enabled) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &closeID, &raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var plans []Plan
	if err = json.Unmarshal(raw, &plans); err != nil {
		return false, err
	}
	var entity, period string
	if err = tx.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id=$1 FOR UPDATE`, closeID).Scan(&entity, &period); err != nil {
		return false, err
	}
	bodies := make([][]byte, len(plans))
	failure := ""
	var proposalRaw []byte
	intakeErr := tx.QueryRowContext(ctx, `SELECT proposal FROM preparation_intakes WHERE job_id=$1`, id).Scan(&proposalRaw)
	if intakeErr != nil && intakeErr != sql.ErrNoRows {
		return false, intakeErr
	}
	if intakeErr == nil {
		var proposal Proposal
		if json.Unmarshal(proposalRaw, &proposal) != nil {
			return false, fmt.Errorf("Invalid intake proposal")
		}
		if proposal.Engine == "openai" {
			if _, e := CurrentProposalSources(ctx, tx, closeID, id, proposal); e != nil {
				failure = e.Error()
			}
		}
	}
	for i, p := range plans {
		if failure != "" {
			break
		}
		if p.CarryForward != nil {
			if e := ValidateCarry(ctx, tx, p, closeID, entity, period); e != nil {
				failure = e.Error()
				break
			}
		}
		if p.RecurringAccrual != nil {
			if e := ValidateRecurringAccrual(ctx, tx, p, closeID, entity, period); e != nil {
				failure = e.Error()
				break
			}
		}
		if p.Revises != "" {
			var active bool
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_postings WHERE workpaper_id::text=$1 AND state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM vendor_credit_postings WHERE workpaper_id::text=$1 AND state IN ('executing','unknown'))`, p.Revises).Scan(&active)
			if err != nil {
				return false, err
			}
			if active {
				failure = "Resolve the outstanding sandbox execution before recalculating."
				break
			}
		}
		for _, s := range p.Sources {
			var digest string
			err = tx.QueryRowContext(ctx, `SELECT digest FROM evidence WHERE id::text=$1 AND close_id=$2 AND superseded_by IS NULL`, s.ID, closeID).Scan(&digest)
			if err != nil && err != sql.ErrNoRows {
				return false, err
			}
			if err == sql.ErrNoRows || digest != s.Digest {
				failure = "Source evidence is missing or changed. Save new instructions before preparing."
				break
			}
		}
		if failure != "" {
			break
		}
		bodies[i], err = Calculate(p, entity, period)
		if err != nil {
			failure = err.Error()
			break
		}
		if err = CheckProcedureMethod(ctx, tx, closeID, p.Kind, bodies[i]); err != nil {
			failure = err.Error()
			break
		}
		// A rate from an index is checked again here, in case the published value was revised after queueing.
		if err = CheckIndexRates(ctx, tx, p.Kind, bodies[i]); err != nil {
			failure = err.Error()
			break
		}
	}
	ids := []string{}
	if failure == "" {
		for i, body := range bodies {
			var paperID string
			err = tx.QueryRowContext(ctx, `INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,kind,digest) DO UPDATE SET digest=workpapers.digest RETURNING id::text`, closeID, plans[i].Kind, Digest(body), string(body)).Scan(&paperID)
			if err != nil {
				return false, err
			}
			if plans[i].Revises != "" {
				if _, err = tx.ExecContext(ctx, `UPDATE sandbox_postings SET state='preview',approved_by=NULL,approval_expires_at=NULL WHERE workpaper_id::text=$1 AND state='approved'`, plans[i].Revises); err != nil {
					return false, err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE vendor_credit_postings SET state='preview',approved_by=NULL,approval_expires_at=NULL WHERE workpaper_id::text=$1 AND state='approved'`, plans[i].Revises); err != nil {
					return false, err
				}
			}
			ids = append(ids, paperID)
		}
	}
	state := "completed"
	if failure != "" {
		state = "failed"
	}
	encoded, _ := json.Marshal(ids)
	if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state=$2,workpaper_ids=$3,error=$4,finished_at=now() WHERE id=$1`, id, state, string(encoded), failure); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func Run(ctx context.Context, db *sql.DB) {
	for ctx.Err() == nil {
		extracted, extractErr := documents.ProcessOne(ctx, db)
		intake, intakeErr := ProcessIntake(ctx, db)
		worked, err := ProcessOne(ctx, db)
		if intakeErr != nil && ctx.Err() == nil {
			log.Print("Intake worker could not persist proposals; will retry")
		}
		if extractErr != nil && ctx.Err() == nil {
			log.Print("Document worker could not persist extraction; will retry")
		}
		if err != nil && ctx.Err() == nil {
			log.Print("Preparation worker could not process queued work; will retry")
		}
		if (worked || extracted || intake) && err == nil && extractErr == nil && intakeErr == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
