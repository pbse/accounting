// Package preparation runs bounded, deterministic preparation from immutable evidence.
package preparation

import (
	"bytes"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/payroll"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
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
	// Unrelated is the other invoices in the close a straight-through plan left unused, proven again before its draft is saved.
	Unrelated []string `json:"unrelated,omitempty"`
	// Imported is one line of a prior accountant's schedule read on the client's opening month: prepared as a schedule
	// only, and only while it gives the balance the schedule states at the month-end.
	Imported *Imported `json:"imported,omitempty"`
	// Procedure is the enabled procedure version a straight-through draft was prepared under; the worker refuses the
	// plan if that version is no longer the client's current, enabled one with the same accounts.
	Procedure string `json:"procedure,omitempty"`
	// Chart is the chart-of-accounts snapshot a straight-through draft's booking was checked against.
	Chart string `json:"chart,omitempty"`
	// BilledTo is the name the invoice bills, accepted as the client or a confirmed alias; the worker checks it again.
	BilledTo string          `json:"billed_to,omitempty"`
	Kind     string          `json:"kind"`
	Input    json.RawMessage `json:"input"`
	Sources  []Source        `json:"sources"`
}

// Imported names a schedule line by the first version of its schedule's evidence and its label, so a corrected schedule
// revises the same draft, and keeps the balance the schedule states.
type Imported struct {
	RootSourceID string `json:"root_source_id"`
	Label        string `json:"label"`
	Remaining    string `json:"remaining"`
}

// WorkKind is the kind of work a plan prepares: a prepaid plan with a schedule prepares deferred revenue or depreciation.
func WorkKind(kind, schedule string) string {
	if kind == "prepaid" && schedule != "" {
		return schedule
	}
	return kind
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
		if p.CarryForward != nil && p.Revises == "" || p.Imported != nil {
			prepare = prepaids.ScheduleOnly
		}
		if p.Imported != nil && (in.DisposalPeriod != "" || in.CreditAmount != "" || len(in.Credits) > 0 || in.CancellationMethod != "" || in.Method == prepaids.DecliningMethod || in.Convention != "") {
			return nil, fmt.Errorf("An imported schedule line is a straight schedule without credits, cancellations, disposals or depreciation conventions")
		}
		paper, err := prepare(in)
		if err != nil {
			return nil, err
		}
		if p.Imported != nil {
			// A register states an asset's net book value: what is left to depreciate plus its salvage value.
			calculated, e := prepaids.Amount(paper.PrepaidBalance)
			if salvage, err := prepaids.Amount(in.SalvageValue); in.Schedule == prepaids.Depreciation && e == nil && err == nil {
				calculated += salvage
			}
			stated, err := prepaids.Amount(p.Imported.Remaining)
			if e != nil || err != nil || stated != calculated {
				return nil, fmt.Errorf("%s: the schedule states %s at the month-end, but the calculation gives %s. Check the dates, amount and method", p.Imported.Label, p.Imported.Remaining, prepaids.Money(calculated))
			}
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
	case "payroll":
		var in payroll.Input
		if err := Strict(p.Input, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Entity) != entity || in.ClosePeriod != period || p.CarryForward != nil || p.RecurringAccrual != nil {
			return nil, fmt.Errorf("Client and period must match this close")
		}
		paper, err := payroll.Prepare(in)
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
		prepare := loans.Prepare
		if p.Imported != nil {
			prepare = loans.ScheduleOnly
		}
		paper, err := prepare(in)
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
	if p.Imported != nil {
		object["imported"], _ = json.Marshal(p.Imported)
	}
	// A draft prepared without field confirmation says so, so a later replacement can revise it without a person.
	if p.Procedure != "" {
		object["straight_through"], _ = json.Marshal(true)
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
	err = tx.QueryRowContext(ctx, `SELECT id::text,close_id::text,plans FROM preparation_jobs WHERE state='queued' AND close_id IN (SELECT c.id FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE o.enabled AND ($1::bigint[] IS NULL OR c.id=ANY($1::bigint[])) AND ($1::bigint[] IS NOT NULL OR NOT EXISTS(SELECT 1 FROM demonstration_seeds d WHERE d.organization_id=c.organization_id AND d.finished_at IS NULL AND c.id=ANY(d.close_ids)))) ORDER BY preparation_jobs.id FOR UPDATE SKIP LOCKED LIMIT 1`, CloseScope(ctx)).Scan(&id, &closeID, &raw)
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
	var opening bool
	if err = tx.QueryRowContext(ctx, `SELECT entity,period,takeover FROM client_closes WHERE id=$1 FOR UPDATE`, closeID).Scan(&entity, &period, &opening); err != nil {
		return false, err
	}
	bodies := make([][]byte, len(plans))
	failure, changed := "", false
	for _, p := range plans {
		if p.Imported != nil && !opening {
			failure = "This close is no longer the client's opening month, so its prior schedule is not imported. Read the schedule on the opening month."
		}
	}
	var proposalRaw []byte
	intakeErr := tx.QueryRowContext(ctx, `SELECT proposal FROM preparation_intakes WHERE job_id=$1`, id).Scan(&proposalRaw)
	if intakeErr != nil && intakeErr != sql.ErrNoRows {
		return false, intakeErr
	}
	var proposal Proposal
	if intakeErr == nil {
		if json.Unmarshal(proposalRaw, &proposal) != nil {
			return false, fmt.Errorf("Invalid intake proposal")
		}
		if llm.IsModelEngine(proposal.Engine) {
			if _, e := CurrentProposalSources(ctx, tx, closeID, id, proposal); e != nil {
				failure, changed = e.Error(), errors.Is(e, ErrInventoryChanged)
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
		// A straight-through reconciliation stands only while its procedure version is the client's current, enabled one and
		// nothing arrived in the close for a person to weigh; its rows and balances are the plan's own, checked by digest below.
		if p.Procedure != "" && p.Kind == "reconciliation" {
			current, e := CurrentProcedure(ctx, tx, closeID, "reconciliation")
			if e != nil && e != sql.ErrNoRows {
				return false, e
			}
			if current.ID != p.Procedure || !current.Enabled {
				failure = "The reconciliation procedure this draft was prepared under (v" + p.Procedure + ") is no longer the client's current enabled one; a person confirms the facts."
				break
			}
			if other, e := OtherEvidence(ctx, tx, closeID, id); e != nil {
				return false, e
			} else if other {
				failure = "Evidence arrived in the close that a person must weigh before this draft is prepared."
				break
			}
			// Each document the plan left unused must still be another matter, as the other jobs now read them.
			if len(p.Unrelated) > 0 {
				var in struct {
					Account string `json:"account"`
				}
				json.Unmarshal(p.Input, &in)
				still, e := ReconciliationOtherMatter(ctx, tx, closeID, id, in.Account)
				if e != nil {
					return false, e
				}
				if slices.ContainsFunc(p.Unrelated, func(s string) bool { return !still[s] }) {
					failure, changed = "Another document in the close may now bear on this account; reading the close again.", true
					break
				}
			}
			// Another job may have drafted this account since this one was queued; the close is locked, so one wins.
			if drafted, e := ReconciliationDrafted(ctx, tx, closeID, p); e != nil {
				return false, e
			} else if drafted != "" {
				failure = strings.ToUpper(drafted[:1]) + drafted[1:] + "."
				break
			}
		} else if p.Procedure != "" {
			current, e := CurrentProcedure(ctx, tx, closeID, p.Kind)
			if e != nil && e != sql.ErrNoRows {
				return false, e
			}
			if e := ProcedurePinned(p, current); e != nil {
				failure = e.Error()
				break
			}
			aliases, e := ClientAliases(ctx, tx, closeID)
			if e != nil {
				return false, e
			}
			if e := BilledToClient(p.BilledTo, entity, aliases); e != nil {
				failure = e.Error()
				break
			}
			// The draft this plan revises must still be unrevised; a revision saved meanwhile makes this one a person's to place.
			if p.Revises != "" {
				var revised bool
				if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id::text=$1 AND newer.paper->>'revises'=$2)`, closeID, p.Revises).Scan(&revised); e != nil {
					return false, e
				}
				if revised {
					failure = "The draft this recalculation revises has been revised since; a person confirms the facts."
					break
				}
			}
			// A reply or answer for another job, or unread evidence, arriving since the plan was queued is a person's to weigh.
			if other, e := OtherEvidence(ctx, tx, closeID, id); e != nil {
				return false, e
			} else if other {
				failure = "Evidence arrived in the close that a person must weigh before this draft is prepared."
				break
			}
			// Each other invoice this plan left unused must still be one, as its own job now reads it.
			if len(p.Unrelated) > 0 {
				still, e := UnrelatedEvidence(ctx, tx, closeID, id, proposal)
				if e != nil {
					return false, e
				}
				if slices.ContainsFunc(p.Unrelated, func(s string) bool { return !still[s] }) {
					failure, changed = "Another document in the close is no longer read as a separate invoice; reading the close again.", true
					break
				}
			}
			// The capture the plan cites must still be the close's current one; a capture queued or failed since then is a question for a person.
			ledger, e := CloseLedger(ctx, tx, closeID)
			if e != nil {
				return false, e
			}
			if ledger == nil || ledger.Chart != p.Chart || !slices.ContainsFunc(p.Sources, func(s Source) bool {
				return s.Role == "period_ledger" && s.ID == ledger.Source.ID && s.Digest == ledger.Source.Digest
			}) {
				failure = "The QuickBooks capture or chart of accounts this draft was prepared from is no longer the close's current one; a person confirms the facts."
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
		// A payroll draft carries the checks its reviewer confirms beside: duplicates, an entry already booked, and the
		// clearing account's debits against the provider's.
		if p.Kind == "payroll" {
			if bodies[i], err = CheckPayroll(ctx, tx, closeID, period, p.Revises, bodies[i]); err != nil {
				return false, err
			}
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
	// A straight-through job whose close gained or changed evidence since it was read is read again, not handed to a person.
	if changed && slices.ContainsFunc(plans, func(p Plan) bool { return p.Procedure != "" }) {
		if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='waiting_evidence',error='Evidence changed before the draft was prepared; reading the close again.',finished_at=NULL WHERE id=$1`, id); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL,confirmed_by=NULL,confirmed_at=NULL WHERE job_id=$1 AND confirmed_by='agent'`, id)
		}
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	state := "completed"
	if failure != "" {
		state = "failed"
		// A straight-through plan the worker refuses prepared nothing; the agent's confirmation is withdrawn with it.
		if slices.ContainsFunc(plans, func(p Plan) bool { return p.Procedure != "" }) {
			if _, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET confirmed_by=NULL,confirmed_at=NULL,agent_checkpoint=jsonb_set(COALESCE(agent_checkpoint,'{}'::jsonb),'{straight_through}',to_jsonb($2::text)) WHERE job_id=$1 AND confirmed_by='agent'`, id, "Waiting for a person: "+failure); err != nil {
				return false, err
			}
		}
	}
	encoded, _ := json.Marshal(ids)
	if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state=$2,workpaper_ids=$3,error=$4,finished_at=now() WHERE id=$1`, id, state, string(encoded), failure); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Run reads intake and prepares queued work. PDFs are extracted in a loop of their own (documents.Run), so a slow page
// reading never holds up this one.
func Run(ctx context.Context, db *sql.DB) {
	for ctx.Err() == nil {
		intake, intakeErr := ProcessIntake(ctx, db)
		worked, err := ProcessOne(ctx, db)
		if intakeErr != nil && ctx.Err() == nil {
			log.Print("Intake worker could not persist proposals; will retry")
		}
		if err != nil && ctx.Err() == nil {
			log.Print("Preparation worker could not process queued work; will retry")
		}
		if (worked || intake) && err == nil && intakeErr == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
