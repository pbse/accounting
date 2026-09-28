// Package xero_refresh captures, every ten minutes, the month-end Trial Balance for due closes with an active Xero
// connection, and gives older captures their search rows. It is a periodic workflow (wf.Periodic): one run a day.
package xero_refresh

import (
	"context"
	"time"

	"close.local/backend/internal/wf"
)

// Sweep is one pass. The Xero code (transport/xero.go) sets it, so this directory need not import it.
var Sweep func(ctx context.Context) error

var _ = wf.Periodic("xero_refresh", "Xero captures", "Every ten minutes: captures the month-end Trial Balance for due closes with an active Xero connection, and gives older captures their search rows.", 10*time.Minute, &Sweep)
