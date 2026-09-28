// Package stripe_reports moves requested Stripe report runs forward every minute and saves each finished report as
// evidence. It is a periodic workflow (wf.Periodic): one run a day.
package stripe_reports

import (
	"context"
	"time"

	"close.local/backend/internal/wf"
)

// Sweep is one pass. The Stripe code (transport/stripe.go) sets it, so this directory need not import it.
var Sweep func(ctx context.Context) error

var _ = wf.Periodic("stripe_reports", "Stripe reports", "Every minute: moves requested Stripe report runs forward and saves each finished report as evidence.", time.Minute, &Sweep)
