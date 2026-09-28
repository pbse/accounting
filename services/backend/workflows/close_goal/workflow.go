// Package close_goal keeps a close handed to the agent moving. About once a minute it takes the goal's next step: it
// brings new documents into preparation, records what people owe and notices sign-off, until the goal is stopped. A
// person or a monthly hand-over starts it; stopping the goal ends it. The goal itself (its history, what it found)
// stays in close_goals, where the workspace reads it.
package close_goal

import (
	"context"
	"errors"
	"time"

	"close.local/backend/internal/wf"
)

// Input names the goal the run watches.
type Input struct {
	GoalID string `json:"goal_id" jsonschema:"title=Close goal ID"`
}

// Next takes the goal's next step and reports done once the goal is stopped. The goal's own code, in transport, sets
// it: transport starts these runs, so it imports this package rather than the other way round.
var Next func(ctx context.Context, goalID string) (done bool, err error)

var _ = wf.Define(wf.Spec[Input, struct{}]{
	Name:        "close_goal",
	Title:       "Close handed to the agent",
	Description: "Takes a delegated close's next step about once a minute until the goal is stopped: new documents into preparation, what people owe, sign-off.",
	Triggers:    []wf.Trigger{wf.ManualForFirm()},
	Concurrency: func(in Input) string { return "close_goal:" + in.GoalID },
	Run: func(ctx wf.Context, in Input) (struct{}, error) {
		return struct{}{}, wf.Watch(ctx, "watch", time.Minute, func(c context.Context) (bool, error) {
			if Next == nil {
				return false, errors.New("this build does not include close goals")
			}
			return Next(c, in.GoalID)
		})
	},
})
