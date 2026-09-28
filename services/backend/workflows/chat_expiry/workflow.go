// Package chat_expiry removes, every hour, firm-wide assistant messages older than 30 days. It is a periodic workflow
// (wf.Periodic): one run a day.
package chat_expiry

import (
	"context"
	"time"

	"close.local/backend/internal/wf"
)

// Sweep is one pass. The assistant code (transport/chat.go) sets it, so this directory need not import it.
var Sweep func(ctx context.Context) error

var _ = wf.Periodic("chat_expiry", "Assistant message expiry", "Every hour: removes firm-wide assistant messages older than 30 days.", time.Hour, &Sweep)
