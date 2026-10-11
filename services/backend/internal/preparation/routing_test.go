package preparation

import (
	"close.local/backend/internal/llm"
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestApplicationIdentityCannotSpendAnotherFirmBudget(t *testing.T) {
	c, err := llm.New(llm.Config{Provider: "context_machine", ContextMachineToken: "private", ContextMachineModel: "codex", CodexModel: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	var db *sql.DB
	ctx := llm.WithIdentity(llm.WithClient(context.Background(), c), "firm-a", "user")
	if err := ReserveModelCalls(ctx, db, "firm-b", 3); !errors.Is(err, llm.ErrUnavailable) {
		t.Fatal(err)
	}
	if !llm.Allows(llm.WithIdentity(ctx, "firm-b", "other"), "firm-b") {
		t.Fatal("different firm requires owner profile")
	}
}
