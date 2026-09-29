package transport

import (
	"context"
	"net/http"
	"testing"

	"close.local/backend/internal/llm"
)

// Tests that change deployment settings rebuild the dependency explicitly to
// simulate a restart. Production configuration is frozen at startup.
func modelContext(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	client, err := llm.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return llm.WithClient(ctx, client)
}

func modelHandler(t *testing.T, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(modelContext(t, r.Context())))
	})
}
