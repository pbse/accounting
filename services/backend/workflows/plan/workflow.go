// Package plan is the built-in interpreter for plans: workflows composed at runtime, built by staff
// or proposed by the agent, that run only once a person approved their exact content. See
// docs/workflow-engine.md#plans-staff-built-and-agent-proposed.
package plan

import (
	"context"
	"fmt"

	"close.local/backend/internal/wf"
)

type Input struct {
	PlanID int64  `json:"plan_id"`
	Digest string `json:"digest"`
}

type Output struct {
	Outcomes map[string]string `json:"outcomes"`
}

var _ = wf.Define(wf.Spec[Input, Output]{
	Name:        "plan",
	Title:       "Approved plan",
	Description: "Runs a plan built by staff or proposed by the agent, exactly as a person approved it.",
	Triggers:    []wf.Trigger{wf.OnApproval()},
	Concurrency: func(in Input) string { return fmt.Sprintf("plan:%d", in.PlanID) },
	Run: func(ctx wf.Context, in Input) (Output, error) {
		// The approved version is saved with the run, so later edits to the plan never change it.
		p, err := wf.Step(ctx, "load", func(c context.Context) (wf.Plan, error) { return wf.LoadApprovedPlan(c, in.PlanID, in.Digest) })
		if err != nil {
			return Output{}, err
		}
		outcomes, err := wf.RunPlan(ctx, p)
		return Output{Outcomes: outcomes}, err
	},
})
