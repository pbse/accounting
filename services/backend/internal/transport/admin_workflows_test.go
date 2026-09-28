package transport

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/wf"
)

type adminFlowInput struct {
	ID     string `json:"id"`
	Secret string `json:"secret" wf:"sensitive"`
}

var _ = wf.Define(wf.Spec[struct{}, struct{}]{
	Name: "test_admin_approved", Title: "Starts on approval", Triggers: []wf.Trigger{wf.OnApproval()},
	Run: func(wf.Context, struct{}) (struct{}, error) { return struct{}{}, nil },
})

var _ = wf.Define(wf.Spec[adminFlowInput, map[string]string]{
	Name: "test_admin_flow", Title: "Admin flow", Triggers: []wf.Trigger{wf.ManualForFirm()},
	Run: func(ctx wf.Context, in adminFlowInput) (map[string]string, error) {
		note, err := wf.Step(ctx, "note", func(context.Context) (string, error) { return "the client said " + in.Secret, nil })
		if err != nil {
			return nil, err
		}
		switch in.ID {
		case "fail":
			_, err = wf.Step(ctx, "boom", func(context.Context) (int, error) { return 0, wf.Permanent(errors.New("boom")) })
			return nil, err
		case "approval":
			if _, err = wf.WaitForApproval(ctx, "sign", wf.Approval{Permission: "closes:signoff", Subject: in.Secret, Prompt: "Approve " + in.Secret, Timeout: time.Hour}); err != nil {
				return nil, err
			}
		case "effect":
			if _, err = wf.Effect(ctx, "post", func(context.Context) (string, error) { return "", errors.New("provider timed out") }); err != nil {
				return nil, err
			}
		}
		reply, err := wf.WaitForEvent[map[string]string](ctx, "reply", "admin.reply:"+in.ID, time.Hour)
		if err != nil {
			return nil, err
		}
		return map[string]string{"note": note, "reply": reply["text"]}, nil
	},
})

func adminJSON(t *testing.T, h http.Handler, token, method, path, body string, want int, header ...string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestStaffRunWorkflowsForAFirmAndEveryActionIsAudited(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("d", 32)
	firm := "org_Flows" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Flow firm')`, firm); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM workflow_steps WHERE organization_id=$1`, firm)
		db.Exec(`DELETE FROM workflow_runs WHERE organization_id=$1`, firm)
		db.Exec(`DELETE FROM workflow_events WHERE organization_id=$1`, firm)
		db.Exec(`DELETE FROM workflow_plans WHERE organization_id=$1`, firm)
	})
	h := apiWithStore(db, token)
	engine := &wf.Engine{DB: db, OnlyOrganization: firm}
	drain := func() {
		for i := 0; i < 20; i++ {
			if ran, err := engine.Tick(context.Background()); err != nil || !ran {
				return
			}
		}
	}
	start := func(id string) float64 {
		body := `{"workflow":"test_admin_flow","organization":"` + firm + `","input":{"id":"` + id + `","secret":"SSN 123-45-6789"},"reason":"support ticket 7"}`
		return adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs", body, 200)["id"].(float64)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs", `{"workflow":"test_admin_flow","organization":"`+firm+`","input":{"id":"x","secret":"y"}}`, 422)
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs", `{"workflow":"test_admin_flow","organization":"`+firm+`","input":{"id":"x","extra":1},"reason":"r"}`, 422)
	// A firm's workflow needs a firm, and one that starts on approval cannot be started by hand.
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs", `{"workflow":"test_admin_flow","input":{"id":"x"},"reason":"r"}`, 422)
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs", `{"workflow":"test_admin_approved","organization":"`+firm+`","input":{},"reason":"r"}`, 422)

	// The catalog marks the sensitive field.
	definitions := adminJSON(t, h, token, "GET", "/v1/admin/workflows/definitions", "", 200)
	if !strings.Contains(mustString(definitions), `"secret":{"sensitive":true`) {
		t.Fatalf("the catalog must mark sensitive inputs: %s", mustString(definitions))
	}

	// A started run is listed and masked; revealing needs a reason and is audited.
	id := start("main")
	drain()
	runPath := "/v1/admin/workflows/runs/" + jsonNumber(id)
	detail := adminJSON(t, h, token, "GET", runPath, "", 200)
	text := mustString(detail)
	if strings.Contains(text, "123-45-6789") || !strings.Contains(text, `"secret":"••••"`) || !strings.Contains(text, `"hidden":true`) {
		t.Fatalf("client content must be masked by default: %s", text)
	}
	adminJSON(t, h, token, "GET", runPath+"?reveal=1", "", 422)
	revealed := adminJSON(t, h, token, "GET", runPath+"?reveal=1", "", 200, "X-Access-Reason", "the firm asked us to check the reply")
	if !strings.Contains(mustString(revealed), "123-45-6789") {
		t.Fatalf("a reveal with a reason shows the content: %s", mustString(revealed))
	}
	listed := adminJSON(t, h, token, "GET", "/v1/admin/workflows/runs?firm="+firm+"&state=waiting", "", 200)
	if runs := listed["runs"].([]any); len(runs) != 1 {
		t.Fatalf("runs %v", runs)
	}

	// An event sent by staff wakes the run; engine events cannot be sent.
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/events", `{"organization":"`+firm+`","key":"admin.reply:main","payload":{"text":"received"},"reason":"the client replied by phone"}`, 200)
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/events", `{"organization":"`+firm+`","key":"wf.approval:1:x","payload":{},"reason":"forge"}`, 422)
	drain()
	done := adminJSON(t, h, token, "GET", runPath, "", 200)
	if state := done["run"].(map[string]any)["state"]; state != "completed" {
		t.Fatalf("the event must complete the run: %v", state)
	}
	// Output the workflow derived from the secret is masked too, though no output field is tagged.
	if text := mustString(done); strings.Contains(text, "123-45-6789") {
		t.Fatalf("derived client content must be masked: %s", text)
	}

	// An approval shows who may decide and what they decided, not its subject, prompt or note.
	approval := start("approval")
	drain()
	if err := wf.Approve(context.Background(), db, int64(approval), "sign", wf.Decision{Approved: true, By: "user_1", Note: "checked SSN 123-45-6789"}, []string{"closes:signoff"}); err != nil {
		t.Fatal(err)
	}
	drain()
	if text := mustString(adminJSON(t, h, token, "GET", "/v1/admin/workflows/runs/"+jsonNumber(approval), "", 200)); strings.Contains(text, "123-45-6789") || !strings.Contains(text, `"permission":"closes:signoff"`) || !strings.Contains(text, `"approved":true`) {
		t.Fatalf("approval text must be masked: %s", text)
	}

	// A plan run's nodes come without their inputs unless revealed.
	var plan, planRun int64
	if err := db.QueryRow(`INSERT INTO workflow_plans(organization_id,title,plan,digest,source,created_by) VALUES($1,'Chase',
 '{"version":1,"title":"Chase","nodes":[{"id":"tell","activity":"plan_signal","input":{"name":"n","payload":{"ssn":"123-45-6789"}}},{"id":"ok","after":["tell"],"approval":{"permission":"closes:signoff","subject":"SSN 123-45-6789"}},{"id":"w","after":["ok"],"wait":{"event":"reply:123-45-6789","timeout":"1h"}}]}',
 'd','staff','staff:test') RETURNING id`, firm).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO workflow_runs(organization_id,workflow,idempotency_key,started_by,input,plan_id,state,finished_at) VALUES($1,'plan',$2,'test','{}',$3,'completed',now()) RETURNING id`, firm, rand.Text(), plan).Scan(&planRun); err != nil {
		t.Fatal(err)
	}
	planPath := "/v1/admin/workflows/runs/" + jsonNumber(float64(planRun))
	if text := mustString(adminJSON(t, h, token, "GET", planPath, "", 200)["plan"]); strings.Contains(text, "123-45-6789") || strings.Contains(text, "Chase") || !strings.Contains(text, `"activity":"plan_signal"`) {
		t.Fatalf("plan text must be masked: %s", text)
	}
	if text := mustString(adminJSON(t, h, token, "GET", planPath+"?reveal=1", "", 200, "X-Access-Reason", "checking the plan")["plan"]); !strings.Contains(text, "123-45-6789") {
		t.Fatalf("a reveal shows the plan's inputs: %s", text)
	}

	// Cancel needs a reason.
	cancelled := start("cancelled")
	drain()
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs/"+jsonNumber(cancelled)+"/cancel", `{}`, 422)
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs/"+jsonNumber(cancelled)+"/cancel", `{"reason":"the firm withdrew the request"}`, 200)

	// An unknown effect is found in its view and resolved; a failed step is retried.
	uncertain := start("effect")
	failed := start("fail")
	drain()
	unknown := adminJSON(t, h, token, "GET", "/v1/admin/workflows/runs?firm="+firm+"&view=unknown_effects", "", 200)["runs"].([]any)
	if len(unknown) != 1 {
		t.Fatalf("unknown effects view %v", unknown)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs/"+jsonNumber(uncertain)+"/resolve", `{"key":"post","applied":true,"output":"\"REF-9\"","reason":"found REF-9 at the provider"}`, 200)
	// Errors are free text, so they are masked until revealed.
	failedPath := "/v1/admin/workflows/runs/" + jsonNumber(failed)
	if text := mustString(adminJSON(t, h, token, "GET", failedPath, "", 200)); strings.Contains(text, `"error":"boom"`) {
		t.Fatalf("errors must be masked: %s", text)
	}
	if text := mustString(adminJSON(t, h, token, "GET", failedPath+"?reveal=1", "", 200, "X-Access-Reason", "why did it fail")); !strings.Contains(text, `"error":"boom"`) {
		t.Fatalf("a reveal shows the error: %s", text)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs/"+jsonNumber(failed)+"/retry", `{"key":"boom","reason":"the provider recovered"}`, 200)
	// A completed step cannot be retried; the refusal is audited too.
	adminJSON(t, h, token, "POST", "/v1/admin/workflows/runs/"+jsonNumber(failed)+"/retry", `{"key":"note","reason":"again"}`, 409)

	// A run woken long ago but never claimed is stuck.
	var woken int64
	if err := db.QueryRow(`INSERT INTO workflow_runs(organization_id,workflow,idempotency_key,started_by,input,state,wake_at,updated_at) VALUES($1,'test_admin_flow',$2,'test','{"id":"x"}','waiting','-infinity',now()-interval '1 hour') RETURNING id`, firm, rand.Text()).Scan(&woken); err != nil {
		t.Fatal(err)
	}
	if stuck := mustString(adminJSON(t, h, token, "GET", "/v1/admin/workflows/runs?firm="+firm+"&view=stuck", "", 200)["runs"]); !strings.Contains(stuck, `"id":`+jsonNumber(float64(woken))+`,`) {
		t.Fatalf("a woken, unclaimed run must be stuck: %s", stuck)
	}

	var actions []string
	rows, err := db.Query(`SELECT action||' '||result FROM platform_audit WHERE organization_id=$1 ORDER BY id`, firm)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		rows.Scan(&a)
		actions = append(actions, a)
	}
	for _, want := range []string{"workflow.start ok", "content.reveal ok", "workflow.event ok", "workflow.cancel ok", "workflow.resolve ok", "workflow.retry ok", "workflow.retry refused"} {
		if !strings.Contains(strings.Join(actions, "|"), want) {
			t.Fatalf("audit %v lacks %q", actions, want)
		}
	}
}

func TestReadOnlyStaffCannotActOrReveal(t *testing.T) {
	db := adminDB(t)
	sign := signer(t)
	staffOrg := "org_Staff" + rand.Text()
	t.Setenv("PLATFORM_ORGANIZATION_ID", staffOrg)
	h := apiWithStore(db, strings.Repeat("x", 32))
	adminJSON(t, h, sign(staffOrg, "platform:read"), "POST", "/v1/admin/workflows/runs", `{"workflow":"test_admin_flow","reason":"r"}`, 403)
	adminJSON(t, h, sign(staffOrg, "platform:read"), "POST", "/v1/admin/workflows/runs/1/cancel", `{"reason":"r"}`, 403)
	var run int64
	if err := db.QueryRow(`INSERT INTO workflow_runs(workflow,idempotency_key,started_by,input) VALUES('test_admin_flow',$1,'test','{"id":"p","secret":"s"}') RETURNING id`, rand.Text()).Scan(&run); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM workflow_runs WHERE id=$1`, run)
	// A run with no step yet has an empty list of steps, not null.
	if steps := adminJSON(t, h, sign(staffOrg, "platform:read"), "GET", "/v1/admin/workflows/runs/"+jsonNumber(float64(run)), "", 200)["steps"]; steps == nil {
		t.Fatal("a new run's steps must be an empty list")
	}
	adminJSON(t, h, sign(staffOrg, "platform:read", "platform:operate"), "GET", "/v1/admin/workflows/runs/"+jsonNumber(float64(run))+"?reveal=1", "", 403, "X-Access-Reason", "curious")
}

func mustString(v any) string { raw, _ := json.Marshal(v); return string(raw) }

func jsonNumber(f float64) string { raw, _ := json.Marshal(int64(f)); return string(raw) }
