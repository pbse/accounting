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

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ReserveModelCalls counts n model calls against the firm's daily limit, or none of them.
func ReserveModelCalls(ctx context.Context, q queryer, org string, n int) error {
	return reserve(ctx, q, org, n, 0)
}

// ReserveGeminiCalls counts n Gemini page readings against the same limit, and apart as Gemini's, so the security
// view attributes them to Google rather than to the firm's other model provider.
func ReserveGeminiCalls(ctx context.Context, q queryer, org string, n int) error {
	return reserve(ctx, q, org, n, n)
}

func reserve(ctx context.Context, q queryer, org string, n, gemini int) error {
	err := q.QueryRowContext(ctx, `INSERT INTO model_calls AS m(organization_id,day,calls,gemini_calls) SELECT id,(now() AT TIME ZONE 'UTC')::date,$2,$3 FROM organizations WHERE id=$1 AND $2<=max_model_calls_per_day
 ON CONFLICT(organization_id,day) DO UPDATE SET calls=m.calls+$2,gemini_calls=m.gemini_calls+$3 WHERE m.calls+$2<=(SELECT max_model_calls_per_day FROM organizations WHERE id=m.organization_id) RETURNING calls`, org, n, gemini).Scan(new(int))
	if err == sql.ErrNoRows {
		return ErrModelLimit
	}
	return err
}
