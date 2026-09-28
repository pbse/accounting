// Package model_replays replays, every hour, for each firm not yet checked on the current model, the close with its
// newest confirmed reading. It is a periodic workflow (wf.Periodic): one run a day.
package model_replays

import (
	"context"
	"time"

	"close.local/backend/internal/wf"
)

// Sweep is one pass. The replay code (transport/replay.go) sets it, so this directory need not import it.
var Sweep func(ctx context.Context) error

var _ = wf.Periodic("model_replays", "Model replays", "Every hour: replays, for each firm not yet checked on the current model, the close with its newest confirmed reading.", time.Hour, &Sweep)
