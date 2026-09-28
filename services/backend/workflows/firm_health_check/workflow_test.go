package firm_health_check_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"close.local/backend/internal/wf"
	"close.local/backend/workflows/firm_health_check"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestHealthCheckCountsOnlyItsFirmsProblems(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mine, theirs := "org_health"+rand.Text(), "org_healthOther"+rand.Text()
	closes := map[string]string{}
	for _, firm := range []string{mine, theirs} {
		must(t, exec(db, `INSERT INTO organizations(id,name) VALUES($1,'Health fixture')`, firm))
		var closeID string
		must(t, db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Client','2026-09') RETURNING id::text`, firm).Scan(&closeID))
		closes[firm] = closeID
		must(t, exec(db, `INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','failed','fixture')`, closeID, rand.Text()))
	}
	must(t, exec(db, `INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by,state_changed_at) VALUES($1,$2,'[]','queued','fixture',now()-interval '2 hours')`, closes[mine], rand.Text()))
	defer func() {
		for _, firm := range []string{mine, theirs} {
			db.Exec(`DELETE FROM workflow_steps WHERE organization_id=$1`, firm)
			db.Exec(`DELETE FROM workflow_runs WHERE organization_id=$1`, firm)
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id::text=$1`, closes[firm])
			db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, closes[firm])
			db.Exec(`DELETE FROM organizations WHERE id=$1`, firm)
		}
	}()

	id, _, err := wf.Start(ctx, db, wf.StartRequest{Workflow: "firm_health_check", Organization: mine, IdempotencyKey: "test", StartedBy: "test"})
	must(t, err)
	engine := &wf.Engine{DB: db, OnlyOrganization: mine}
	if ran, err := engine.Tick(ctx); err != nil || !ran {
		t.Fatalf("tick: %v %v", ran, err)
	}
	var state string
	var output []byte
	must(t, db.QueryRow(`SELECT state,output FROM workflow_runs WHERE id=$1`, id).Scan(&state, &output))
	var report firm_health_check.Report
	if err = json.Unmarshal(output, &report); err != nil || state != "completed" {
		t.Fatalf("%s %s %v", state, output, err)
	}
	if report.FailedPreparation != 1 || report.StuckPreparation != 1 {
		t.Fatalf("the report must count this firm's problems only: %+v", report)
	}
}

func exec(db *sql.DB, query string, args ...any) error { _, err := db.Exec(query, args...); return err }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
