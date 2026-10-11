package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"slices"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// firmClients finds every client of the firm whose name the answer uses, across all its clients, with no cap, and none
// of another firm's.
func TestFirmClientsFindsTheNamesAnAnswerUses(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	firm, other := "org_Claims"+rand.Text(), "org_Other"+rand.Text()
	for _, q := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO organizations(id,name) VALUES($1,'Claims firm'),($2,'Other firm')`, []any{firm, other}},
		{`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Europe','2026-10'),($1,'Birch Dental','2026-10'),($1,'GE','2026-10'),($2,'Zenith Other','2026-10')`, []any{firm, other}},
	} {
		if _, err := db.Exec(q.query, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM client_closes WHERE organization_id IN ($1,$2)`, firm, other)
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, firm, other)
	})
	got, err := firmClients(context.Background(), db, firm, "Birch Dental and Acme Europe each grew, as did GE; Zenith Other did not.")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if len(got) != 3 || got[0] != "Acme Europe" || got[1] != "Birch Dental" || got[2] != "GE" {
		t.Fatalf("the firm's clients the answer names, and no other firm's: %v", got)
	}
	// A failed lookup is an error, never an empty list the gate would read as "no client named".
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := firmClients(ctx, db, firm, "Birch Dental"); err == nil {
		t.Fatal("a failed lookup reports its error")
	}
}
