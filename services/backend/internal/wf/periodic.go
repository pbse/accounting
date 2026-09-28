package wf

import (
	"context"
	"errors"
	"log"
	"time"
)

// Periodic defines a platform workflow that calls *fn about every interval, all day: a run scheduled at midnight UTC
// watches until the next midnight, so a periodic job is one run and one step a day, and its latest look and error
// show in the admin panel. Staff can also start it by hand, for the platform only, for instance after cancelling the
// day's run; the concurrency key keeps it to one run at a time. A pass may take up to an hour. fn is a variable that the code the job belongs to sets, so the
// workflow's directory need not import it; while it is unset the job records that and does nothing. fn must be safe to
// repeat and to run late. Call it from a package-level variable in the workflow's directory, like Define.
func Periodic(name, title, description string, every time.Duration, fn *func(context.Context) error) Definition {
	return Define(Spec[struct{}, struct{}]{
		Name: name, Title: title, Description: description,
		Triggers:    []Trigger{Schedule("0 0 * * *"), ManualForPlatform()},
		Concurrency: func(struct{}) string { return "periodic:" + name },
		Run: func(ctx Context, _ struct{}) (struct{}, error) {
			end := Now(ctx).Truncate(24 * time.Hour).Add(24 * time.Hour)
			return struct{}{}, Watch(ctx, "run", every, func(c context.Context) (bool, error) {
				if x := executionOf(c); x != nil && !x.engine.now().Before(end) {
					return true, nil // tomorrow's run takes over
				}
				if *fn == nil {
					return false, errors.New("this build does not include " + name)
				}
				err := (*fn)(c)
				if err != nil && c.Err() == nil {
					log.Printf("Periodic %s: %v", name, err)
				}
				return false, err
			}, Timeout(time.Hour))
		},
	})
}
