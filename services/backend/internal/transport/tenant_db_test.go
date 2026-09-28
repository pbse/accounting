package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"close.local/backend/internal/auth"
)

func TestRowLevelSecurityIsolatesFirms(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	// Test roles are superusers, which bypass row-level security; the runtime role must not.
	_, err = admin.Exec(`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='close_rls_probe') THEN CREATE ROLE close_rls_probe LOGIN PASSWORD 'probe-only' NOSUPERUSER NOBYPASSRLS; END IF; END $$;
		GRANT USAGE ON SCHEMA public TO close_rls_probe; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO close_rls_probe; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO close_rls_probe`)
	must(err)
	a, b := "org_RlsA"+rand.Text(), "org_RlsB"+rand.Text()
	closes, papers := []string{}, []string{}
	for _, org := range []string{a, b} {
		var closeID, paperID string
		_, err = admin.Exec(`INSERT INTO organizations(id,name) VALUES($1,'RLS fixture')`, org)
		must(err)
		must(admin.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Same client','2026-09') RETURNING id::text`, org).Scan(&closeID))
		must(admin.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','rls','{}') RETURNING id::text`, closeID).Scan(&paperID))
		_, err = admin.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'rls','approved','Synthetic review','fixture')`, paperID)
		must(err)
		closes, papers = append(closes, closeID), append(papers, paperID)
	}
	defer func() {
		for i := range closes {
			admin.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id=$1`, papers[i])
			admin.Exec(`DELETE FROM workpapers WHERE id=$1`, papers[i])
			admin.Exec(`DELETE FROM client_closes WHERE id=$1`, closes[i])
		}
		admin.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, a, b)
	}()

	probeURL, err := url.Parse(dsn)
	must(err)
	probeURL.User = url.UserPassword("close_rls_probe", "probe-only")
	probe, err := OpenTenantDB(probeURL.String(), "")
	must(err)
	defer probe.Close()
	count := func(ctx context.Context, query string, args ...any) int {
		t.Helper()
		var n int
		must(probe.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	firmA := auth.WithActor(context.Background(), &auth.Claims{Organization: a})
	for query, args := range map[string][]any{
		`SELECT count(*) FROM client_closes WHERE id::text IN ($1,$2)`:               {closes[0], closes[1]},
		`SELECT count(*) FROM workpapers WHERE id::text IN ($1,$2)`:                  {papers[0], papers[1]},
		`SELECT count(*) FROM workpaper_reviews WHERE workpaper_id::text IN ($1,$2)`: {papers[0], papers[1]},
		`SELECT count(*) FROM workpaper_review_state WHERE id::text IN ($1,$2)`:      {papers[0], papers[1]},
		`SELECT count(*) FROM organizations WHERE id IN ($1,$2)`:                     {a, b},
	} {
		if n := count(firmA, query, args...); n != 1 {
			t.Fatalf("firm A must see only its own row, got %d: %s", n, query)
		}
	}
	if n := count(context.Background(), `SELECT count(*) FROM client_closes WHERE id::text IN ($1,$2)`, closes[0], closes[1]); n != 0 {
		t.Fatalf("without a firm or a system mark nothing is visible, got %d", n)
	}
	var seen int
	system(func(w http.ResponseWriter, r *http.Request) {
		seen = count(r.Context(), `SELECT count(*) FROM client_closes WHERE id::text IN ($1,$2)`, closes[0], closes[1])
	})(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if seen != 2 {
		t.Fatalf("system work sees every firm, got %d", seen)
	}
	if _, err = probe.ExecContext(firmA, `INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Planted','2026-10')`, b); err == nil {
		t.Fatal("firm A must not write a row into firm B")
	}
	// A rollback undoes a setting changed inside the transaction; the next statement must set it again.
	firmB := auth.WithActor(context.Background(), &auth.Claims{Organization: b})
	conn, err := probe.Conn(firmA)
	must(err)
	tx, err := conn.BeginTx(firmA, nil)
	must(err)
	var n int
	must(tx.QueryRowContext(firmB, `SELECT count(*) FROM client_closes WHERE id::text=$1`, closes[1]).Scan(&n))
	must(tx.Rollback())
	must(conn.QueryRowContext(firmB, `SELECT count(*) FROM client_closes WHERE id::text=$1`, closes[1]).Scan(&n))
	conn.Close()
	if n != 1 {
		t.Fatal("after a rollback firm B must still see its own close")
	}
	if _, err = probe.PrepareContext(firmA, `SELECT 1`); err == nil {
		t.Fatal("a prepared statement could later run as another firm")
	}
	var enforced bool
	must(probe.QueryRow(rowSecurityEnforced).Scan(&enforced))
	if !enforced {
		t.Fatal("every table and view must apply row-level security for the runtime role")
	}
	must(admin.QueryRow(rowSecurityEnforced).Scan(&enforced))
	if enforced {
		t.Fatal("a superuser role must not count as enforced")
	}
}

func TestOpenTenantDBRefusesPooler(t *testing.T) {
	if _, err := OpenTenantDB("postgres://close:x@ep-dark-frog-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require", ""); err == nil {
		t.Fatal("a transaction-pooled URL was accepted")
	}
	if _, err := OpenTenantDB("postgres://close:x@ep-dark-frog.us-east-2.aws.neon.tech,ep-dark-frog-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require", ""); err == nil {
		t.Fatal("a pooled fallback host was accepted")
	}
	db, err := OpenTenantDB("postgres://close:x@ep-dark-frog.us-east-2.aws.neon.tech,ep-dark-toad.us-east-2.aws.neon.tech/neondb?sslmode=require", "")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
}
