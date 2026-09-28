package wf

import (
	"context"
	"sync/atomic"
)

// OnlyOrg limits an engine to one firm's runs, so tests sharing a database cannot claim each other's.
func OnlyOrg(e *Engine, org string) { e.OnlyOrganization = org }

// CrashOnce makes the next arrival at point ("before-call" or "after-call") of step key behave like
// the worker dying there: nothing more is written for that execution.
func CrashOnce(e *Engine, point, key string) {
	var fired atomic.Bool
	e.hook = func(p, k string) error {
		if p == point && k == key && fired.CompareAndSwap(false, true) {
			return errCrashed
		}
		return nil
	}
}

// Hook runs fn at every step boundary.
func Hook(e *Engine, fn func(point, key string)) {
	e.hook = func(p, k string) error { fn(p, k); return nil }
}

// Sweep runs the engine's periodic check for events that arrived while runs were parking.
func Sweep(ctx context.Context, e *Engine) error { return e.sweep(ctx) }

// Triggers runs one pass of schedule and event triggers.
func Triggers(ctx context.Context, e *Engine) { e.triggers(ctx) }
