// Package standing_approvals approves, every minute, each later month's carried schedule that an active standing
// approval covers, under the granter's name. It is a periodic workflow (wf.Periodic): one run a day.
package standing_approvals

import (
	"context"
	"time"

	"close.local/backend/internal/wf"
)

// Sweep is one pass. The standing approval code (transport/standing.go) sets it, so this directory need not import it.
var Sweep func(ctx context.Context) error

var _ = wf.Periodic("standing_approvals", "Standing approvals", "Every minute: approves each later month's carried schedule that an active standing approval covers, under the granter's name.", time.Minute, &Sweep)
