// Package operations_alerts collects, every minute, each firm's stuck or failed work and sends new alerts to the
// operations webhook, when one is configured. It is a periodic workflow (wf.Periodic): one run a day. Platform alerts
// stay a worker loop of their own: they watch this engine, so they cannot run on it.
package operations_alerts

import (
	"context"
	"time"

	"close.local/backend/internal/wf"
)

// Sweep is one pass. The alert code (transport/alert_worker.go) sets it, so this directory need not import it.
var Sweep func(ctx context.Context) error

var _ = wf.Periodic("operations_alerts", "Operations alerts", "Every minute: collects each firm's stuck or failed work and sends new alerts to the operations webhook, when one is configured.", time.Minute, &Sweep)
