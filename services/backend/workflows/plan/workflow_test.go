package plan_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "close.local/backend/activities/close_summary"
	_ "close.local/backend/activities/plan_signal"
	"close.local/backend/internal/wf"
	_ "close.local/backend/workflows/plan"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func setup(t *testing.T) (context.Context, *sql.DB, string, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	org := "org_plan" + rand.Text()
	var closeID string
	must(t, exec(db, `INSERT INTO organizations(id,name) VALUES($1,'Plan fixture')`, org))
	must(t, db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme','2026-09') RETURNING id::text`, org).Scan(&closeID))
	t.Cleanup(func() {
		for _, q := range []string{`UPDATE workflow_plans SET run_id=NULL WHERE organization_id=$1`, `DELETE FROM workflow_steps WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`,
			`DELETE FROM workflow_plans WHERE organization_id=$1`, `DELETE FROM workflow_events WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, err := db.Exec(q, org); err != nil {
				t.Error(err)
			}
		}
		db.Close()
	})
	return context.Background(), db, org, closeID
}

func look(closeID string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"version":1,"title":"Look and tell","nodes":[
 {"id":"look","activity":"close_summary","input":{"close_id":%q}},
 {"id":"tell","after":["look"],"activity":"plan_signal","input":{"name":"looked","payload":{"entity":"{{look.entity}}"}}}]}`, closeID))
}

func TestStaffPlanRunsOnlyAsApproved(t *testing.T) {
	ctx, db, org, closeID := setup(t)
	id, err := wf.CreatePlan(ctx, db, wf.PlanDraft{Organization: org, Plan: look(closeID), Source: "staff", CreatedBy: "staff:ops@example.com"})
	must(t, err)
	var digest, state string
	must(t, db.QueryRow(`SELECT digest,state FROM workflow_plans WHERE id=$1`, id).Scan(&digest, &state))
	if state != "draft" {
		t.Fatalf("a staff plan starts as a draft, got %s", state)
	}
	if _, err = wf.ApprovePlan(ctx, db, id, strings.Repeat("0", 64), "staff:lead@example.com", nil); !errors.Is(err, wf.ErrNotAllowed) {
		t.Fatalf("approving a version nobody reviewed: %v", err)
	}
	runID, err := wf.ApprovePlan(ctx, db, id, digest, "staff:lead@example.com", []string{})
	must(t, err)
	var linked int64
	must(t, db.QueryRow(`SELECT plan_id FROM workflow_runs WHERE id=$1`, runID).Scan(&linked))
	if linked != id {
		t.Fatalf("run %d is not linked to plan %d", runID, id)
	}
	if err = wf.UpdatePlan(ctx, db, id, look(closeID), "staff:ops@example.com"); !errors.Is(err, wf.ErrNotAllowed) {
		t.Fatalf("an approved plan cannot be edited: %v", err)
	}
	engine := &wf.Engine{DB: db, OnlyOrganization: org}
	for i := 0; i < 5; i++ {
		if ran, err := engine.Tick(ctx); err != nil || !ran {
			break
		}
	}
	var runState, output, payload string
	must(t, db.QueryRow(`SELECT state,COALESCE(output::text,error) FROM workflow_runs WHERE id=$1`, runID).Scan(&runState, &output))
	if runState != "completed" || !strings.Contains(output, `"tell": "completed"`) {
		t.Fatalf("%s %s", runState, output)
	}
	must(t, db.QueryRow(`SELECT payload::text FROM workflow_events WHERE organization_id=$1 AND key='plan.looked'`, org).Scan(&payload))
	if !strings.Contains(payload, "Acme") {
		t.Fatalf("the signal carries the summary's output: %s", payload)
	}
}

func TestAgentPlansAreOnlyProposals(t *testing.T) {
	ctx, db, org, closeID := setup(t)
	if _, err := wf.CreatePlan(ctx, db, wf.PlanDraft{Organization: org, Plan: look(closeID), Source: "agent", CreatedBy: "agent:claude", Agent: "claude"}); err == nil {
		t.Fatal("an agent proposal must say why")
	}
	id, err := wf.CreatePlan(ctx, db, wf.PlanDraft{Organization: org, Plan: look(closeID), Source: "agent", CreatedBy: "agent:claude", Agent: "claude", Rationale: "Check Acme before the reminder"})
	must(t, err)
	var state string
	must(t, db.QueryRow(`SELECT state FROM workflow_plans WHERE id=$1`, id).Scan(&state))
	if state != "proposed" {
		t.Fatalf("an agent plan is created as a proposal, got %s", state)
	}
	if err = wf.UpdatePlan(ctx, db, id, look(closeID), "staff:ops@example.com"); !errors.Is(err, wf.ErrNotAllowed) {
		t.Fatalf("an agent's proposal is copied, not edited: %v", err)
	}
	if _, err = db.Exec(`UPDATE workflow_plans SET state='draft' WHERE id=$1`, id); err == nil {
		t.Fatal("the database must refuse an agent plan in draft")
	}
	for i := 0; i < 18; i++ {
		_, err = wf.CreatePlan(ctx, db, wf.PlanDraft{Organization: org, Plan: look(closeID), Source: "agent", CreatedBy: "agent:claude", Rationale: "again"})
		must(t, err)
	}
	// Five proposals at once when one place is left: exactly one gets it.
	results := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			_, err := wf.CreatePlan(ctx, db, wf.PlanDraft{Organization: org, Plan: look(closeID), Source: "agent", CreatedBy: "agent:claude", Rationale: "racing"})
			results <- err
		}()
	}
	accepted := 0
	for i := 0; i < 5; i++ {
		if err := <-results; err == nil {
			accepted++
		} else if !errors.Is(err, wf.ErrNotAllowed) {
			t.Fatal(err)
		}
	}
	var total int
	must(t, db.QueryRow(`SELECT count(*) FROM workflow_plans WHERE organization_id=$1 AND source='agent'`, org).Scan(&total))
	if accepted != 1 || total != 20 {
		t.Fatalf("a firm takes at most 20 agent proposals a day, also concurrently: accepted %d, total %d", accepted, total)
	}
}

func TestTemplatesAreCopiedNeverRun(t *testing.T) {
	ctx, db, org, closeID := setup(t)
	template, err := wf.CreatePlan(ctx, db, wf.PlanDraft{Organization: org, Plan: look(closeID), Source: "staff", CreatedBy: "staff:ops@example.com", IsTemplate: true})
	must(t, err)
	var digest string
	must(t, db.QueryRow(`SELECT digest FROM workflow_plans WHERE id=$1`, template).Scan(&digest))
	if _, err = wf.ApprovePlan(ctx, db, template, digest, "staff:lead@example.com", []string{}); !errors.Is(err, wf.ErrNotAllowed) {
		t.Fatalf("a template never runs itself: %v", err)
	}
	copyID, err := wf.InstantiateTemplate(ctx, db, template, org, "staff:ops@example.com")
	must(t, err)
	var state string
	var from int64
	must(t, db.QueryRow(`SELECT state,template_id FROM workflow_plans WHERE id=$1`, copyID).Scan(&state, &from))
	if state != "draft" || from != template {
		t.Fatalf("copy %s from %d", state, from)
	}
	if _, err = wf.CreatePlan(ctx, db, wf.PlanDraft{Organization: org, Plan: json.RawMessage(`{"version":1,"title":"x","nodes":[{"id":"a","activity":"close_summary","input":{"close_id":1}}]}`), Source: "staff", CreatedBy: "staff:ops@example.com"}); !errors.Is(err, wf.ErrPlanInvalid) {
		t.Fatalf("an invalid plan is refused when saved: %v", err)
	}
}

func exec(db *sql.DB, query string, args ...any) error { _, err := db.Exec(query, args...); return err }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
