package preparation

import (
	"close.local/backend/internal/llm"
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestForeignContextMachineIdentityCannotSpendFirmBudget(t *testing.T) {
	scope := llm.Scope{Organization: "own-firm", Subject: "owner", AccessProfile: "own"}
	c, err := llm.New(llm.Config{Provider: "context_machine", ContextMachineToken: "private", ContextMachineModel: "codex", CodexModel: "fixed", Profiles: []llm.Scope{scope}})
	if err != nil {
		t.Fatal(err)
	}
	var db *sql.DB // Any attempted budget write panics, proving rejection happens before SQL.
	ctx := llm.WithClient(context.Background(), c)
	for _, identity := range [][2]string{{"other-firm", "owner"}, {"own-firm", "other-user"}} {
		if err := ReserveModelCalls(llm.WithIdentity(ctx, identity[0], identity[1]), db, identity[0], 3); !errors.Is(err, llm.ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if llm.Allows(ctx, "other-firm") || !llm.Allows(ctx, "own-firm") {
		t.Fatal("scheduled eligibility ignores configured organizations")
	}
}
