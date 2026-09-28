// Package schedule_comparisons prepares, every minute, each carried schedule's month-end comparison from the captured
// Trial Balance, for a person to approve. It is a periodic workflow (wf.Periodic): one run a day.
package schedule_comparisons

import (
	"context"
	"time"

	"close.local/backend/internal/wf"
)

// Sweep is one pass. The comparison code (transport/comparisons.go) sets it, so this directory need not import it.
var Sweep func(ctx context.Context) error

var _ = wf.Periodic("schedule_comparisons", "Schedule comparisons", "Every minute: prepares each carried schedule's month-end comparison from the captured Trial Balance, for a person to approve.", time.Minute, &Sweep)
