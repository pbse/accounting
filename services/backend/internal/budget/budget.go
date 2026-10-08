// Package budget holds a firm's daily model-call limit, shared by every caller of a model: preparation, document
// reading and the rest count against the same limit.
package budget

import (
	"context"
	"database/sql"
	"errors"
)

// ErrModelLimit refuses model calls once the firm has used its calls for the UTC day.
var ErrModelLimit = errors.New("The firm's daily model call limit is reached; it resets at midnight UTC.")

// ReserveModelCalls counts n model calls against the firm's daily limit, or none of them.
func ReserveModelCalls(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, org string, n int) error {
	err := q.QueryRowContext(ctx, `INSERT INTO model_calls AS m(organization_id,day,calls) SELECT id,(now() AT TIME ZONE 'UTC')::date,$2 FROM organizations WHERE id=$1 AND $2<=max_model_calls_per_day
 ON CONFLICT(organization_id,day) DO UPDATE SET calls=m.calls+$2 WHERE m.calls+$2<=(SELECT max_model_calls_per_day FROM organizations WHERE id=m.organization_id) RETURNING calls`, org, n).Scan(new(int))
	if err == sql.ErrNoRows {
		return ErrModelLimit
	}
	return err
}
