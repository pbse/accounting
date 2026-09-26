package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
)

type RecurringAccrual struct {
	CloseID  string `json:"close_id"`
	PaperID  string `json:"paper_id"`
	Digest   string `json:"digest"`
	ReviewID string `json:"review_id"`
	Period   string `json:"period"`
}

func ValidateRecurringAccrual(ctx context.Context, tx *sql.Tx, p Plan, targetID, entity, period string) error {
	lineage := p.RecurringAccrual
	if lineage == nil || p.Kind != "accrual" {
		return fmt.Errorf("Recurring accrual lineage is invalid")
	}
	var state, digest, review, priorEntity, priorPeriod string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT w.review_state,w.digest,w.review_id,c.entity,c.period,w.paper FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id WHERE w.id::text=$1 AND w.close_id::text=$2 AND w.kind='accrual' AND c.organization_id=(SELECT organization_id FROM client_closes WHERE id::text=$3) AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text)`, lineage.PaperID, lineage.CloseID, targetID).Scan(&state, &digest, &review, &priorEntity, &priorPeriod, &raw)
	if err != nil || state != "approved" || digest != lineage.Digest || review != lineage.ReviewID {
		return fmt.Errorf("Prior recurring accrual or review changed")
	}
	when, err := time.Parse("2006-01", priorPeriod)
	if err != nil || when.AddDate(0, 1, 0).Format("2006-01") != period || lineage.Period != priorPeriod || !strings.EqualFold(entity, priorEntity) {
		return fmt.Errorf("Recurring accrual must use the immediately prior close for the same client")
	}
	var prior accruals.Workpaper
	var next accruals.Input
	if json.Unmarshal(raw, &prior) != nil || Strict(p.Input, &next) != nil || prior.Input.Method != accruals.RecurringMethod || next.Method != accruals.RecurringMethod || prior.Input.AccountantRuleReference != next.AccountantRuleReference || prior.Input.Amount != next.Amount || prior.Input.ExpenseAccount != next.ExpenseAccount || prior.Input.LiabilityAccount != next.LiabilityAccount || prior.Input.EstimationBasis != next.EstimationBasis {
		return fmt.Errorf("Recurring method changed; enter a new reviewed rule")
	}
	if len(p.Sources) != 2 || p.Sources[0].Role != "current_service" || p.Sources[1].Role != "period_ledger" || p.Sources[0].ID == p.Sources[1].ID || p.Sources[1].Location == "" {
		return fmt.Errorf("Current incurred-service and period-ledger evidence are required")
	}
	var latestLedger string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT CASE WHEN x.state='active' THEN s.payload->>'source_id' ELSE '' END FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id WHERE s.close_id::text=$1 AND s.payload->>'period'=$2 ORDER BY s.id DESC LIMIT 1),'')`, targetID, period).Scan(&latestLedger)
	if err != nil || latestLedger != p.Sources[1].ID {
		return fmt.Errorf("Current period ledger capture changed; refresh the recurring accrual")
	}
	var latest string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id::text FROM workpapers WHERE close_id::text=$1 AND kind='accrual' AND paper->'recurring_accrual'->>'paper_id'=$2 AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=workpapers.close_id AND newer.paper->>'revises'=workpapers.id::text) ORDER BY workpapers.id DESC LIMIT 1),'')`, targetID, lineage.PaperID).Scan(&latest)
	if err != nil || p.Revises != latest {
		return fmt.Errorf("Current recurring accrual changed; refresh the comparison")
	}
	return nil
}
