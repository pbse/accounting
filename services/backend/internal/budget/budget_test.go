package budget

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestGeminiReadingsShareTheLimitAndAreCountedApart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_" + rand.Text()
	if _, err = db.Exec(`INSERT INTO organizations(id,name,max_model_calls_per_day) VALUES($1,'Budget firm',3)`, org); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM model_calls WHERE organization_id=$1`, org)
	ctx := context.Background()
	if err = ReserveGeminiCalls(ctx, db, org, 2); err != nil {
		t.Fatal(err)
	}
	if err = ReserveModelCalls(ctx, db, org, 1); err != nil {
		t.Fatal(err)
	}
	var calls, gemini int
	if err = db.QueryRow(`SELECT calls,gemini_calls FROM model_calls WHERE organization_id=$1`, org).Scan(&calls, &gemini); err != nil || calls != 3 || gemini != 2 {
		t.Fatalf("one limit, with Gemini's readings counted apart: %d %d %v", calls, gemini, err)
	}
	if err = ReserveGeminiCalls(ctx, db, org, 1); !errors.Is(err, ErrModelLimit) {
		t.Fatalf("a Gemini reading over the firm's limit is refused: %v", err)
	}
}
