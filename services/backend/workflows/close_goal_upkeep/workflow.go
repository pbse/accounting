// Package close_goal_upkeep runs every hour. It hands each client's newly ended month to the agent under its monthly
// hand-over, and makes sure every open close goal has a close_goal run watching it: goals handed over before goals ran
// on the engine, and any whose run was cancelled while the goal stayed open.
package close_goal_upkeep

import (
	"context"
	"errors"
	"time"

	"close.local/backend/internal/wf"
)

// Report is what one pass did.
type Report struct {
	MonthsHandedOver int `json:"months_handed_over"`
	GoalsWatched     int `json:"goals_watched"`
}

// HandOverMonths and WatchGoals are set by transport, where the goals' code lives (see close_goal.Next).
var (
	HandOverMonths func(ctx context.Context) (int, error)
	WatchGoals     func(ctx context.Context) (int, error)
)

var _ = wf.Define(wf.Spec[struct{}, Report]{
	Name:        "close_goal_upkeep",
	Title:       "Monthly hand-overs and close goal upkeep",
	Description: "Every hour: hands each client's newly ended month to the agent under its monthly hand-over, and starts a run for any open close goal that has none.",
	Triggers:    []wf.Trigger{wf.Schedule("0 * * * *"), wf.ManualForPlatform()},
	Concurrency: func(struct{}) string { return "close_goal_upkeep" },
	Run: func(ctx wf.Context, _ struct{}) (Report, error) {
		if HandOverMonths == nil || WatchGoals == nil {
			return Report{}, wf.Permanent(errors.New("this build does not include close goals"))
		}
		months, err := wf.Step(ctx, "hand-over-months", HandOverMonths, wf.Timeout(30*time.Minute))
		if err != nil {
			return Report{}, err
		}
		goals, err := wf.Step(ctx, "watch-goals", WatchGoals, wf.Timeout(30*time.Minute))
		return Report{MonthsHandedOver: months, GoalsWatched: goals}, err
	},
})
