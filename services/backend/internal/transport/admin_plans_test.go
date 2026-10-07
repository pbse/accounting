package transport

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	_ "close.local/backend/activities/plan_signal"
	"close.local/backend/internal/wf"
	_ "close.local/backend/workflows/plan"
)

var _ = wf.DefineActivity(wf.ActivitySpec[struct{}, struct{}]{
	Name: "test_admin_guarded", Title: "Guarded", Description: "Needs a firm permission to approve.", Permission: "closes:signoff", Effect: wf.Internal,
	Run: func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil },
})

func TestStaffReviewApproveAndCopyPlans(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("l", 32)
	ctx := context.Background()
	firm := "org_Plans" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Plan firm')`, firm); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`UPDATE workflow_plans SET run_id=NULL WHERE organization_id=$1`, `DELETE FROM workflow_steps WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`,
			`DELETE FROM workflow_plans WHERE organization_id=$1`} {
			db.Exec(q, firm)
		}
	})
	create := func(d wf.PlanDraft) int64 {
		t.Helper()
		id, err := wf.CreatePlan(ctx, db, d)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	signal := func(title string) json.RawMessage {
		return json.RawMessage(`{"version":1,"title":"` + title + `","nodes":[{"id":"tell","activity":"plan_signal","input":{"name":"acme_secret_ping"}}]}`)
	}
	draft := create(wf.PlanDraft{Organization: firm, Plan: signal("Chase Acme Secret"), Source: "staff", CreatedBy: "staff:builder"})
	proposal := create(wf.PlanDraft{Organization: firm, Plan: signal("Agent idea"), Source: "agent", CreatedBy: "agent:planner", Agent: "planner", Rationale: "Acme Secret has not replied"})
	guarded := create(wf.PlanDraft{Organization: firm, Plan: json.RawMessage(`{"version":1,"title":"Sign off","nodes":[{"id":"sign","activity":"test_admin_guarded","input":{}}]}`), Source: "staff", CreatedBy: "staff:builder"})
	template := create(wf.PlanDraft{Plan: signal("Template Secret"), Source: "staff", CreatedBy: "staff:builder", IsTemplate: true})
	t.Cleanup(func() {
		// Copies point at their template, so they go first.
		db.Exec(`DELETE FROM workflow_plans WHERE template_id=$1`, template)
		db.Exec(`DELETE FROM workflow_plans WHERE id=$1`, template)
	})
	h := apiWithStore(db, token)
	path := func(id int64, rest string) string { return "/v1/admin/plans/" + strconv.FormatInt(id, 10) + rest }
	digest := func(id int64) string {
		var d string
		if err := db.QueryRow(`SELECT digest FROM workflow_plans WHERE id=$1`, id).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}

	// The list shows what plans are, not what they say, until revealed.
	listed := mustString(adminJSON(t, h, token, "GET", "/v1/admin/plans?firm="+firm, "", 200))
	if strings.Contains(listed, "Acme") || !strings.Contains(listed, `"source":"agent"`) || !strings.Contains(listed, `"nodes":1`) {
		t.Fatalf("plans list: %s", listed)
	}
	if revealed := mustString(adminJSON(t, h, token, "GET", "/v1/admin/plans?firm="+firm+"&reveal=1", "", 200, "X-Access-Reason", "reviewing the queue")); !strings.Contains(revealed, "Chase Acme Secret") || !strings.Contains(revealed, "Acme Secret has not replied") {
		t.Fatalf("revealed list: %s", revealed)
	}
	var listReveals int
	if db.QueryRow(`SELECT count(*) FROM platform_audit WHERE organization_id=$1 AND action='content.reveal' AND target_type='plans'`, firm).Scan(&listReveals); listReveals != 1 {
		t.Fatalf("a revealed list is in the firm's audit: %d", listReveals)
	}
	if templates := mustString(adminJSON(t, h, token, "GET", "/v1/admin/plans?templates=1", "", 200)); !strings.Contains(templates, `"id":`+strconv.FormatInt(template, 10)+`,`) {
		t.Fatalf("templates: %s", templates)
	}

	// One plan: drawn without its inputs, approvable by staff, and fully shown once revealed.
	detail := adminJSON(t, h, token, "GET", path(draft, ""), "", 200)
	if text := mustString(detail); strings.Contains(text, "acme_secret") || !strings.Contains(text, `"activity":"plan_signal"`) || detail["approvable"] != true || !strings.Contains(text, digest(draft)) {
		t.Fatalf("masked plan: %s", text)
	}
	if text := mustString(adminJSON(t, h, token, "GET", path(draft, "?reveal=1"), "", 200, "X-Access-Reason", "reviewing before approval")); !strings.Contains(text, "acme_secret_ping") || !strings.Contains(text, `"problems":[]`) {
		t.Fatalf("revealed plan: %s", text)
	}
	// A plan whose activity needs a firm permission waits for the firm; why shows once revealed.
	if g := adminJSON(t, h, token, "GET", path(guarded, ""), "", 200); g["approvable"] != false || mustString(g["problems"]) != `["••••"]` {
		t.Fatalf("guarded plan: %v", g)
	}
	if why := mustString(adminJSON(t, h, token, "GET", path(guarded, "?reveal=1"), "", 200, "X-Access-Reason", "why can't staff approve")["problems"]); !strings.Contains(why, "closes:signoff") {
		t.Fatalf("guarded problems: %s", why)
	}
	adminJSON(t, h, token, "POST", path(guarded, "/approve"), `{"digest":"`+digest(guarded)+`","reason":"Trying to approve for the firm"}`, 422)

	// Propose, reject, approve exactly what was reviewed.
	adminJSON(t, h, token, "POST", path(draft, "/propose"), `{"reason":"Ready for a second look"}`, 200)
	adminJSON(t, h, token, "POST", path(draft, "/propose"), `{"reason":"Ready for a second look"}`, 409)
	adminJSON(t, h, token, "POST", path(proposal, "/reject"), `{"reason":"The client already replied"}`, 200)
	adminJSON(t, h, token, "POST", path(draft, "/approve"), `{"digest":"`+strings.Repeat("0", 64)+`","reason":"Reviewed with the firm"}`, 409)
	run := adminJSON(t, h, token, "POST", path(draft, "/approve"), `{"digest":"`+digest(draft)+`","reason":"Reviewed with the firm"}`, 200)["run_id"].(float64)
	var state string
	var linked int64
	if err := db.QueryRow(`SELECT p.state,r.plan_id FROM workflow_plans p JOIN workflow_runs r ON r.id=p.run_id WHERE p.id=$1`, draft).Scan(&state, &linked); err != nil || state != "approved" || linked != draft || int64(run) == 0 {
		t.Fatalf("approved %s, run linked to %d: %v", state, linked, err)
	}

	// A template is copied into a new draft for a firm.
	copied := int64(adminJSON(t, h, token, "POST", path(template, "/instantiate"), `{"firm":"`+firm+`","reason":"The firm asked for the usual chase"}`, 200)["plan_id"].(float64))
	var from int64
	if err := db.QueryRow(`SELECT template_id FROM workflow_plans WHERE id=$1 AND organization_id=$2 AND state='draft'`, copied, firm).Scan(&from); err != nil || from != template {
		t.Fatalf("copy of %d: %d %v", template, from, err)
	}
	adminJSON(t, h, token, "POST", path(template, "/instantiate"), `{"firm":"org_Missing","reason":"The firm asked for the usual chase"}`, 404)
	// A template is copied, never proposed or rejected.
	adminJSON(t, h, token, "POST", path(template, "/propose"), `{"reason":"Proposing a template"}`, 409)
	// Approving needs that the approver revealed, and so read, this exact version.
	unread := create(wf.PlanDraft{Organization: firm, Plan: signal("Never opened"), Source: "staff", CreatedBy: "staff:builder"})
	adminJSON(t, h, token, "POST", path(unread, "/approve"), `{"digest":"`+digest(unread)+`","reason":"Approving without reading it"}`, 409)

	var actions string
	if err := db.QueryRow(`SELECT string_agg(action||':'||result,',' ORDER BY id) FROM platform_audit WHERE organization_id=$1 AND action LIKE 'plan.%'`, firm).Scan(&actions); err != nil ||
		actions != "plan.approve:refused,plan.propose:ok,plan.propose:refused,plan.reject:ok,plan.approve:refused,plan.approve:ok,plan.instantiate:ok,plan.approve:refused" {
		t.Fatalf("plan audit %q %v", actions, err)
	}
}

func TestStaffBuildPlansAndTemplates(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("b", 32)
	firm := "org_Build" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Builder firm')`, firm); err != nil {
		t.Fatal(err)
	}
	var shared int64
	t.Cleanup(func() {
		db.Exec(`DELETE FROM workflow_plans WHERE organization_id=$1`, firm)
		db.Exec(`DELETE FROM workflow_plans WHERE id=$1`, shared)
	})
	h := apiWithStore(db, token)
	body := func(fields string) string { return `{` + fields + `}` }
	good := `{"version":1,"title":"Remind","nodes":[{"id":"tell","activity":"plan_signal","input":{"name":"remind"}}]}`
	guarded := `{"version":1,"title":"Sign off","nodes":[{"id":"sign","activity":"test_admin_guarded","input":{}}]}`
	broken := `{"version":1,"title":"Broken","nodes":[{"id":"late","after":["missing"],"activity":"plan_signal","input":{"name":"x"}}]}`

	// Live validation: what is wrong, in the author's words; or the digest and whether staff could approve it.
	if v := adminJSON(t, h, token, "POST", "/v1/admin/plans/validate", body(`"plan":`+broken), 200); !strings.Contains(mustString(v["problems"]), "missing") || v["digest"] != "" {
		t.Fatalf("broken plan: %v", v)
	}
	if v := adminJSON(t, h, token, "POST", "/v1/admin/plans/validate", body(`"plan":`+good), 200); mustString(v["problems"]) != "[]" || len(v["digest"].(string)) != 64 || v["staff_approvable"] != true {
		t.Fatalf("good plan: %v", v)
	}
	if v := adminJSON(t, h, token, "POST", "/v1/admin/plans/validate", body(`"plan":`+guarded), 200); v["staff_approvable"] != false {
		t.Fatalf("a plan needing a firm permission: %v", v)
	}

	// Saving: a plan for a firm, or a template; a broken plan is refused with its problems.
	adminJSON(t, h, token, "POST", "/v1/admin/plans", body(`"plan":`+good+`,"reason":"Weekly reminder for the firm"`), 422)
	adminJSON(t, h, token, "POST", "/v1/admin/plans", body(`"firm":"org_Missing","plan":`+good+`,"reason":"Weekly reminder for the firm"`), 404)
	if refused := adminJSON(t, h, token, "POST", "/v1/admin/plans", body(`"firm":"`+firm+`","plan":`+broken+`,"reason":"Weekly reminder for the firm"`), 422); !strings.Contains(refused["message"].(string), "missing") {
		t.Fatalf("refused save: %v", refused)
	}
	draft := int64(adminJSON(t, h, token, "POST", "/v1/admin/plans", body(`"firm":"`+firm+`","plan":`+good+`,"reason":"Weekly reminder for the firm"`), 200)["plan_id"].(float64))
	shared = int64(adminJSON(t, h, token, "POST", "/v1/admin/plans", body(`"template":true,"plan":`+good+`,"reason":"A reminder any firm can use"`), 200)["plan_id"].(float64))
	var state, source, by string
	var template, orgless bool
	if err := db.QueryRow(`SELECT state,source,created_by,is_template,organization_id IS NULL FROM workflow_plans WHERE id=$1`, shared).Scan(&state, &source, &by, &template, &orgless); err != nil || state != "draft" || source != "staff" || by != "staff:local-development" || !template || !orgless {
		t.Fatalf("shared template: %s %s %s %v %v %v", state, source, by, template, orgless, err)
	}

	// Editing a draft replaces it; an agent's proposal and an approved plan are not edited.
	path := func(id int64) string { return "/v1/admin/plans/" + strconv.FormatInt(id, 10) }
	var before, after string
	db.QueryRow(`SELECT digest FROM workflow_plans WHERE id=$1`, draft).Scan(&before)
	adminJSON(t, h, token, "POST", path(draft), body(`"plan":`+strings.Replace(good, "Remind", "Remind again", 1)+`,"reason":"Clearer title for the firm"`), 200)
	db.QueryRow(`SELECT digest FROM workflow_plans WHERE id=$1`, draft).Scan(&after)
	if before == after {
		t.Fatal("the edit must change the plan")
	}
	adminJSON(t, h, token, "POST", path(draft), body(`"plan":`+broken+`,"reason":"Clearer title for the firm"`), 422)
	proposal, err := wf.CreatePlan(context.Background(), db, wf.PlanDraft{Organization: firm, Plan: json.RawMessage(good), Source: "agent", CreatedBy: "agent:planner", Agent: "planner", Rationale: "Reminders help"})
	if err != nil {
		t.Fatal(err)
	}
	adminJSON(t, h, token, "POST", path(proposal), body(`"plan":`+good+`,"reason":"Rewriting the agent's idea"`), 409)
	member, err := wf.CreatePlan(context.Background(), db, wf.PlanDraft{Organization: firm, Plan: json.RawMessage(good), Source: "member", CreatedBy: "user_member"})
	if err != nil {
		t.Fatal(err)
	}
	adminJSON(t, h, token, "POST", path(member), body(`"plan":`+good+`,"reason":"Rewriting the member's plan"`), 409)

	// A large integer in an input stays exact through saving and the builder's revealed read.
	exact := int64(adminJSON(t, h, token, "POST", "/v1/admin/plans", body(`"firm":"`+firm+`","plan":{"version":1,"title":"Exact","nodes":[{"id":"tell","activity":"plan_signal","input":{"name":"n","payload":{"n":9007199254740993}}}]},"reason":"Checking large numbers survive"`), 200)["plan_id"].(float64))
	if inputs := mustString(adminJSON(t, h, token, "GET", path(exact)+"?reveal=1", "", 200, "X-Access-Reason", "editing the plan")["inputs"]); !strings.Contains(inputs, "9007199254740993") {
		t.Fatalf("inputs as stored: %s", inputs)
	}
	if masked := adminJSON(t, h, token, "GET", path(exact), "", 200)["inputs"]; masked != nil {
		t.Fatalf("inputs are content: %v", masked)
	}

	var created string
	if err := db.QueryRow(`SELECT string_agg(action||':'||result||':'||COALESCE(request->>'plan_id','-'),',' ORDER BY id) FROM platform_audit WHERE organization_id=$1 AND action IN ('plan.create','plan.update')`, firm).Scan(&created); err != nil ||
		created != "plan.create:refused:-,plan.create:ok:"+strconv.FormatInt(draft, 10)+",plan.update:ok:-,plan.update:refused:-,plan.update:refused:-,plan.update:refused:-,plan.create:ok:"+strconv.FormatInt(exact, 10) {
		t.Fatalf("builder audit %q %v", created, err)
	}
}
