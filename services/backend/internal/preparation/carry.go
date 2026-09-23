package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
)

type CarryForward struct {
	CloseID        string            `json:"close_id"`
	PaperID        string            `json:"paper_id"`
	Digest         string            `json:"digest"`
	ReviewID       string            `json:"review_id"`
	Period         string            `json:"period"`
	OpeningBalance string            `json:"opening_balance"`
	SourceIDs      map[string]string `json:"source_ids"`
}

func ValidateCarry(ctx context.Context, tx *sql.Tx, p Plan, targetID, entity, period string) error {
	c := p.CarryForward
	var state, digest, review, priorEntity, priorPeriod string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT w.review_state,w.digest,w.review_id,c.entity,c.period,w.paper FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id WHERE w.id::text=$1 AND w.close_id::text=$2 AND w.kind='prepaid' AND c.organization_id=(SELECT organization_id FROM client_closes WHERE id::text=$3)`, c.PaperID, c.CloseID, targetID).Scan(&state, &digest, &review, &priorEntity, &priorPeriod, &raw)
	if err != nil || state != "approved" || digest != c.Digest || review != c.ReviewID {
		return fmt.Errorf("Prior-period work or review changed. Review the predecessor before carrying forward.")
	}
	when, e := time.Parse("2006-01", priorPeriod)
	if e != nil || when.AddDate(0, 1, 0).Format("2006-01") != period || c.Period != priorPeriod || !strings.EqualFold(entity, priorEntity) {
		return fmt.Errorf("Carry-forward must stay with the same client in the immediately following month.")
	}
	var prior struct {
		prepaids.Workpaper
		Sources []Source `json:"sources"`
	}
	var input prepaids.Input
	if json.Unmarshal(raw, &prior) != nil || Strict(p.Input, &input) != nil {
		return fmt.Errorf("Invalid carry-forward input.")
	}
	expected := prior.Input
	expected.Entity = entity
	expected.ClosePeriod = period
	expected.BookedBalance = ""
	expected.BookingReference = ""
	expected.ExamplesConfirmed = false
	expected.MethodConfirmed = false
	expected.BookingConfirmed = false
	expectedSources := len(prior.Sources)
	if p.Revises != "" {
		expectedSources++
		var revised struct {
			prepaids.Workpaper
			CarryForward CarryForward `json:"carry_forward"`
		}
		if err := tx.QueryRowContext(ctx, `SELECT w.paper FROM workpapers w WHERE w.id::text=$1 AND w.close_id::text=$2 AND w.kind='prepaid' AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text)`, p.Revises, targetID).Scan(&raw); err != nil || json.Unmarshal(raw, &revised) != nil || revised.CarryForward.PaperID != c.PaperID || revised.CarryForward.ReviewID != c.ReviewID {
			return fmt.Errorf("The carried draft changed. Refresh recurring review.")
		}
		if input.BookedBalance == "" || input.BookingReference == "" || !input.BookingConfirmed || !input.MethodConfirmed || !input.ExamplesConfirmed {
			return fmt.Errorf("Confirm the current booked balance and supported method.")
		}
		expected.BookedBalance, expected.BookingReference = input.BookedBalance, input.BookingReference
		expected.BookingConfirmed, expected.MethodConfirmed, expected.ExamplesConfirmed = true, true, true
	}
	if !reflect.DeepEqual(input, expected) || c.OpeningBalance != prior.PrepaidBalance || len(prior.Sources) == 0 || expectedSources != len(p.Sources) || len(c.SourceIDs) != len(prior.Sources) {
		return fmt.Errorf("Carry-forward no longer matches the reviewed schedule.")
	}
	for i, s := range p.Sources[:len(prior.Sources)] {
		old := prior.Sources[i]
		if c.SourceIDs[s.ID] != old.ID || s.Digest != old.Digest || s.Role != old.Role {
			return fmt.Errorf("Carry-forward source lineage changed.")
		}
	}
	if p.Revises != "" && (p.Sources[len(prior.Sources)].Role != "period_ledger" || p.Sources[len(prior.Sources)].Location == "") {
		return fmt.Errorf("Current-period ledger evidence is missing.")
	}
	return nil
}
