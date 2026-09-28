package transport

import (
	"context"
	"errors"
	"sync"

	"close.local/backend/internal/wf"
	"close.local/backend/workflows/chat_expiry"
	"close.local/backend/workflows/model_replays"
	"close.local/backend/workflows/operations_alerts"
	"close.local/backend/workflows/schedule_comparisons"
	"close.local/backend/workflows/standing_approvals"
	"close.local/backend/workflows/stripe_reports"
	"close.local/backend/workflows/xero_refresh"
)

// The worker's periodic sweeps run as periodic workflows (wf.Periodic, one directory each under workflows/): one run a
// day on the engine, looking every interval, with the latest look and its error in the admin panel. Each pass is set
// here, beside the code it sweeps. Queues that drain back to back (preparation, email delivery, forwarded email,
// QuickBooks sync) and the platform alerts, which watch the engine itself, stay worker loops.

// sweepCursors are where the standing approval and comparison sweeps left off, so refused items cannot starve later
// ones. They live in the worker running the day's run; another worker starts again from the beginning.
var sweepCursors = struct {
	sync.Mutex
	standing, comparisons string
}{standing: "0", comparisons: "0"}

// cursorSweep runs a sweep from the cursor at *at and keeps where it stopped.
func cursorSweep(ctx context.Context, at *string, sweep func(context.Context, string) (string, int, error)) error {
	sweepCursors.Lock()
	cursor := *at
	sweepCursors.Unlock()
	next, _, err := sweep(ctx, cursor)
	sweepCursors.Lock()
	*at = next
	sweepCursors.Unlock()
	return err
}

func init() {
	standing_approvals.Sweep = func(ctx context.Context) error {
		return cursorSweep(ctx, &sweepCursors.standing, func(ctx context.Context, cursor string) (string, int, error) {
			return sweepStandingApprovals(ctx, wf.DB(ctx), cursor)
		})
	}
	schedule_comparisons.Sweep = func(ctx context.Context) error {
		return cursorSweep(ctx, &sweepCursors.comparisons, func(ctx context.Context, cursor string) (string, int, error) {
			return sweepComparisons(ctx, wf.DB(ctx), cursor)
		})
	}
	stripe_reports.Sweep = func(ctx context.Context) error { return sweepStripeReports(ctx, wf.DB(ctx)) }
	operations_alerts.Sweep = func(ctx context.Context) error {
		endpoint, token, ok := alertWebhook()
		if !ok {
			return nil
		}
		db := wf.DB(ctx)
		return errors.Join(collectOperationsAlerts(ctx, db), deliverOperationsAlerts(ctx, db, endpoint, token))
	}
	model_replays.Sweep = func(ctx context.Context) error { return sweepReplays(ctx, wf.DB(ctx)) }
	xero_refresh.Sweep = func(ctx context.Context) error {
		backfillXeroRows(ctx, wf.DB(ctx))
		return sweepXeroRefresh(ctx, wf.DB(ctx))
	}
	chat_expiry.Sweep = func(ctx context.Context) error { return expireChat(ctx, wf.DB(ctx)) }
}
