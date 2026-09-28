package wf_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/wf"
)

type number struct {
	N int `json:"n"`
}

var double = wf.DefineActivity(wf.ActivitySpec[number, number]{
	Name: "test_double", Title: "Double", Description: "Doubles n.", Effect: wf.Read,
	Run: func(_ context.Context, in number) (number, error) { return number{N: in.N * 2}, nil },
})

var _ = wf.DefineActivity(wf.ActivitySpec[ref, string]{
	Name: "test_send", Title: "Send outside", Description: "Sends something to a provider.", Effect: wf.External, Permission: "requests:send",
	Run: func(_ context.Context, in ref) (string, error) { count(in.ID + "/send"); return "sent " + in.ID, nil },
})

var _ = wf.DefineActivity(wf.ActivitySpec[number, number]{
	Name: "test_fail", Title: "Fail", Description: "Always fails.", Effect: wf.Read,
	Run: func(context.Context, number) (number, error) {
		return number{}, wf.Permanent(errors.New("broken on purpose"))
	},
})

var _ = wf.Define(wf.Spec[number, number]{
	Name: "test_call", Title: "Call an activity",
	Run: func(ctx wf.Context, in number) (number, error) { return wf.Call(ctx, "double", double, in) },
})

var _ = wf.Define(wf.Spec[wf.Plan, map[string]string]{
	Name: "test_run_plan", Title: "Run an inline plan",
	Run: func(ctx wf.Context, p wf.Plan) (map[string]string, error) { return wf.RunPlan(ctx, p) },
})

func TestSchemaDescribesInputs(t *testing.T) {
	type sample struct {
		Name    string             `json:"name" description:"Who"`
		Count   int                `json:"count,omitempty"`
		At      time.Time          `json:"at"`
		Tags    []string           `json:"tags"`
		Secret  string             `json:"secret" wf:"sensitive"`
		Meta    map[string]float64 `json:"meta,omitempty"`
		Ignored string             `json:"-"`
		hidden  int
	}
	_ = sample{}.hidden
	s := wf.Schema(reflect.TypeFor[sample]())
	properties := s["properties"].(map[string]any)
	if !slices.Equal(s["required"].([]string), []string{"name", "at", "tags", "secret"}) {
		t.Fatalf("required %v", s["required"])
	}
	check := func(name, key string, want any) {
		t.Helper()
		if got := properties[name].(map[string]any)[key]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s.%s = %v, want %v", name, key, got, want)
		}
	}
	check("name", "description", "Who")
	check("count", "type", "integer")
	check("at", "format", "date-time")
	check("tags", "type", "array")
	check("secret", "sensitive", true)
	check("meta", "type", "object")
	if _, ok := properties["Ignored"]; ok || len(properties) != 6 {
		t.Fatalf("properties %v", properties)
	}
}

func TestCallRunsActivitiesAndTheCatalogDescribesThem(t *testing.T) {
	h := setup(t)
	id, _, err := wf.Start(h.ctx, h.db, wf.StartRequest{Workflow: "test_call", Organization: h.org, Input: number{N: 21}, IdempotencyKey: "call", StartedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h.drain()
	if v := h.expect(id, "completed"); v.output != `{"n": 42}` {
		t.Fatalf("output %s", v.output)
	}
	var send wf.ActivityInfo
	for _, a := range wf.Activities() {
		if a.Name == "test_send" {
			send = a
		}
	}
	if send.Effect != wf.External || send.Permission != "requests:send" || send.Input["type"] != "object" {
		t.Fatalf("catalog entry %+v", send)
	}
}

func plan(t *testing.T, raw string) wf.Plan {
	t.Helper()
	p, err := wf.ParsePlan(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanProblemsNameEveryRuleBroken(t *testing.T) {
	for want, raw := range map[string]string{
		"must follow an approval node":    `{"version":1,"title":"x","nodes":[{"id":"send","activity":"test_send","input":{"id":"a"}}]}`,
		"is not an earlier node":          `{"version":1,"title":"x","nodes":[{"id":"a","after":["b"],"activity":"test_double"},{"id":"b","activity":"test_double"}]}`,
		"when must name a node":           `{"version":1,"title":"x","nodes":[{"id":"a","activity":"test_double"},{"id":"b","when":"a.completed","activity":"test_double"}]}`,
		"cannot end":                      `{"version":1,"title":"x","nodes":[{"id":"a","activity":"test_double"},{"id":"b","after":["a"],"when":"a.approved","activity":"test_double"}]}`,
		"must name a node this one waits": `{"version":1,"title":"x","nodes":[{"id":"a","activity":"test_double"},{"id":"b","activity":"test_double","input":{"n":"{{a.n}}"}}]}`,
		"does not exist":                  `{"version":1,"title":"x","nodes":[{"id":"a","activity":"test_nothing"}]}`,
		"timeout":                         `{"version":1,"title":"x","nodes":[{"id":"a","wait":{"event":"x","timeout":"forever"}}]}`,
		"exactly one of":                  `{"version":1,"title":"x","nodes":[{"id":"a","activity":"test_double","workflow":"test_call"}]}`,
		"unknown field":                   `{"version":1,"title":"x","nodes":[{"id":"a","activity":"test_double","input":{"m":1}}]}`,
		"cannot run inside a plan":        `{"version":1,"title":"x","nodes":[{"id":"a","workflow":"plan"}]}`,
	} {
		if problems := strings.Join(plan(t, raw).Problems(nil), "; "); !strings.Contains(problems, want) {
			t.Errorf("want %q in problems for %s, got %q", want, raw, problems)
		}
	}
	gated := plan(t, `{"version":1,"title":"x","nodes":[{"id":"ok","approval":{"permission":"requests:send","timeout":"24h"}},{"id":"send","after":["ok"],"activity":"test_send","input":{"id":"a"}}]}`)
	if problems := gated.Problems(nil); len(problems) != 0 {
		t.Fatalf("a gated send is valid: %v", problems)
	}
	if problems := strings.Join(gated.Problems([]string{"workpapers:review"}), ";"); !strings.Contains(problems, "needs the requests:send permission") {
		t.Fatalf("an approver without the activity's permission: %q", problems)
	}
	if _, err := wf.ParsePlan(json.RawMessage(`{"version":1,"title":"x","nodes":[],"extra":1}`)); !errors.Is(err, wf.ErrPlanInvalid) {
		t.Fatalf("unknown plan fields: %v", err)
	}
}

const gatedSend = `{"version":1,"title":"Double, approve, send","nodes":[
 {"id":"first","activity":"test_double","input":{"n":21}},
 {"id":"second","after":["first"],"activity":"test_double","input":{"n":"{{first.n}}"}},
 {"id":"ok","after":["second"],"approval":{"permission":"requests:send","timeout":"24h"}},
 {"id":"send","after":["ok"],"activity":"test_send","input":{"id":"%ID%{{second.n}}"}},
 {"id":"declined","after":["ok"],"when":"ok.rejected","activity":"test_double","input":{"n":0}}]}`

func (h *harness) runPlan(raw string) int64 {
	h.t.Helper()
	p := plan(h.t, raw)
	if problems := p.Problems(nil); len(problems) > 0 {
		h.t.Fatalf("plan problems: %v", problems)
	}
	id, _, err := wf.Start(h.ctx, h.db, wf.StartRequest{Workflow: "test_run_plan", Organization: h.org, Input: p, IdempotencyKey: rand.Text(), StartedBy: "test"})
	if err != nil {
		h.t.Fatal(err)
	}
	h.drain()
	return id
}

func TestPlansPassOutputsAndFollowApprovals(t *testing.T) {
	h := setup(t)
	prefix := "P" + rand.Text()[:6]
	approved := h.runPlan(strings.ReplaceAll(gatedSend, "%ID%", prefix))
	if v := h.expect(approved, "waiting"); v.steps["node:second"] != "completed" || v.steps["node:ok"] != "waiting" {
		t.Fatalf("steps %v", v.steps)
	}
	if err := wf.Approve(h.ctx, h.db, approved, "node:ok", wf.Decision{Approved: true, By: "user_b"}, []string{"requests:send"}); err != nil {
		t.Fatal(err)
	}
	h.drain()
	v := h.expect(approved, "completed")
	if !strings.Contains(v.output, `"send": "completed"`) || !strings.Contains(v.output, `"declined": "skipped"`) || counted(prefix+"84/send") != 1 {
		t.Fatalf("output %s; the send must get the second node's output (84)", v.output)
	}

	rejected := h.runPlan(strings.ReplaceAll(gatedSend, "%ID%", "R"+rand.Text()[:6]))
	if err := wf.Approve(h.ctx, h.db, rejected, "node:ok", wf.Decision{Approved: false, By: "user_b"}, []string{"requests:send"}); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if v := h.expect(rejected, "completed"); !strings.Contains(v.output, `"send": "skipped"`) || !strings.Contains(v.output, `"declined": "completed"`) {
		t.Fatalf("output %s", v.output)
	}
}

func TestPlanFailuresFailTheRunUnlessHandled(t *testing.T) {
	h := setup(t)
	unhandled := h.runPlan(`{"version":1,"title":"Fails","nodes":[{"id":"broken","activity":"test_fail"}]}`)
	if v := h.expect(unhandled, "failed"); !strings.Contains(v.err, "plan nodes failed: broken") {
		t.Fatalf("error %q", v.err)
	}
	handled := h.runPlan(`{"version":1,"title":"Recovers","nodes":[{"id":"broken","activity":"test_fail"},{"id":"fallback","after":["broken"],"when":"broken.failed","activity":"test_double","input":{"n":1}}]}`)
	if v := h.expect(handled, "completed"); !strings.Contains(v.output, `"fallback": "completed"`) {
		t.Fatalf("output %s", v.output)
	}
}

func TestPlanEventWaitsUseEarlierOutputs(t *testing.T) {
	h := setup(t)
	id := h.runPlan(`{"version":1,"title":"Waits","nodes":[{"id":"first","activity":"test_double","input":{"n":21}},
 {"id":"answer","after":["first"],"wait":{"event":"plan.answer:{{first.n}}","timeout":"1h"}},
 {"id":"late","after":["first"],"when":"first.completed","wait":{"event":"plan.never","timeout":"1h"}}]}`)
	h.emit("plan.answer:42", "yes")
	h.drain()
	h.clock.Add(2 * time.Hour)
	h.drain()
	if v := h.expect(id, "completed"); !strings.Contains(v.output, `"answer": "received"`) || !strings.Contains(v.output, `"late": "timed_out"`) {
		t.Fatalf("output %s", v.output)
	}
}

func TestPlanInputsKeepLargeIntegersExact(t *testing.T) {
	h := setup(t)
	id := h.runPlan(`{"version":1,"title":"Big","nodes":[{"id":"a","activity":"test_double","input":{"n":9007199254740993}}]}`)
	h.expect(id, "completed")
	var output string
	h.db.QueryRow(`SELECT output::text FROM workflow_steps WHERE run_id=$1 AND key='node:a'`, id).Scan(&output)
	if !strings.Contains(output, "18014398509481986") {
		t.Fatalf("the number changed on its way through the plan: %s", output)
	}
}

func TestExternalInputThatDoesNotFitFailsWithoutAnUnknownEffect(t *testing.T) {
	h := setup(t)
	id := h.runPlan(`{"version":1,"title":"Wrong type","nodes":[{"id":"first","activity":"test_double","input":{"n":1}},
 {"id":"ok","after":["first"],"approval":{"permission":"requests:send","timeout":"24h"}},
 {"id":"send","after":["ok"],"activity":"test_send","input":{"id":"{{first.n}}"}}]}`)
	if err := wf.Approve(h.ctx, h.db, id, "node:ok", wf.Decision{Approved: true, By: "user_b"}, []string{"requests:send"}); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if v := h.expect(id, "failed"); v.steps["node:send"] != "failed" {
		t.Fatalf("input that never reached the provider must fail plainly, not become unknown: %v", v.steps)
	}
}
