package transport

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/wf"
	"close.local/backend/workflows/chat_expiry"
	"close.local/backend/workflows/model_replays"
	"close.local/backend/workflows/operations_alerts"
	"close.local/backend/workflows/schedule_comparisons"
	"close.local/backend/workflows/standing_approvals"
	"close.local/backend/workflows/stripe_reports"
	"close.local/backend/workflows/xero_refresh"
)

// Every periodic workflow has its pass, and a day's run does it on the engine: here, removing expired assistant
// messages, as the worker loop used to.
func TestPeriodicSweepsRunOnTheEngine(t *testing.T) {
	for name, pass := range map[string]func(context.Context) error{"standing_approvals": standing_approvals.Sweep, "schedule_comparisons": schedule_comparisons.Sweep, "stripe_reports": stripe_reports.Sweep,
		"operations_alerts": operations_alerts.Sweep, "model_replays": model_replays.Sweep, "xero_refresh": xero_refresh.Sweep, "chat_expiry": chat_expiry.Sweep} {
		if pass == nil {
			t.Errorf("%s has no pass", name)
		}
	}
	db := adminDB(t)
	org := "org_Periodic" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Periodic fixture')`, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	})
	if _, err := db.Exec(`INSERT INTO chat_messages(organization_id,author,role,body,created_at) VALUES($1,'user_a','user','old',now()-interval '31 days'),($1,'user_a','user','recent',now())`, org); err != nil {
		t.Fatal(err)
	}
	// A run for the fixture firm, so an engine limited to it runs it; the day's real run is a platform run.
	id, _, err := wf.Start(t.Context(), db, wf.StartRequest{Workflow: "chat_expiry", Organization: org, IdempotencyKey: "periodic-test", StartedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// Staff start a periodic job only for the platform: one for a firm could run beside the platform's own.
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("z", 32)
	if message := adminJSON(t, apiWithStore(db, token), token, "POST", "/v1/admin/workflows/runs", `{"workflow":"chat_expiry","organization":"`+org+`","input":{},"idempotency_key":"by-hand","reason":"Clearing old messages now"}`, 422)["message"].(string); !strings.Contains(message, "across the platform") {
		t.Fatalf("a periodic job names no firm: %s", message)
	}
	engine := &wf.Engine{DB: db, OnlyOrganization: org, Now: func() time.Time { return time.Now() }}
	if ran, err := engine.Tick(t.Context()); err != nil || !ran {
		t.Fatalf("the day's run looks: %v %v", ran, err)
	}
	var left, state string
	if err = db.QueryRow(`SELECT string_agg(body,','),(SELECT state FROM workflow_runs WHERE id=$2) FROM chat_messages WHERE organization_id=$1`, org, id).Scan(&left, &state); err != nil || left != "recent" || state != "waiting" {
		t.Fatalf("the look removed the expired message and the run waits for its next: %q %s %v", left, state, err)
	}
}
