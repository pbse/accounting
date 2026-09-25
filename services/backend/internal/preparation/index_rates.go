package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"close.local/backend/internal/loans"
)

// IndexMismatch is a loan rate from an index that is not the published value on record.
type IndexMismatch string

func (e IndexMismatch) Error() string { return string(e) }

// CheckIndexRates refuses a loan workpaper whose rate from an index differs from the published value stored when a
// preparer looked it up, so the rate rests on the published figure, not a typed one.
func CheckIndexRates(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, kind string, workpaper []byte) error {
	var paper struct {
		Input struct {
			RateChanges []loans.RateChange `json:"rate_changes"`
		} `json:"input"`
	}
	if kind != "loan" || json.Unmarshal(workpaper, &paper) != nil {
		return nil
	}
	for _, c := range paper.Input.RateChanges {
		if c.Index == "" {
			continue
		}
		var matches bool
		err := q.QueryRowContext(ctx, `SELECT percent=$3::numeric FROM index_rates WHERE name=$1 AND effective_date=$2::date`, c.Index, c.IndexDate, c.IndexRate).Scan(&matches)
		if err == sql.ErrNoRows || (err == nil && !matches) {
			return IndexMismatch(fmt.Sprintf("Look up %s for %s again: %s%% is not the published value on record.", c.Index, c.IndexDate, c.IndexRate))
		}
		if err != nil {
			return err
		}
	}
	return nil
}
