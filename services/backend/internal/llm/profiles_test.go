package llm

import (
	"context"
	"testing"
)

func TestInferenceEligibilityHasNoOwnerAllowlist(t *testing.T) {
	c, err := New(Config{Provider: "context_machine", ContextMachineToken: "private", ContextMachineModel: "codex", CodexModel: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	base := WithClient(context.Background(), c)
	for _, org := range []string{"firm-a", "firm-b"} {
		if !Allows(base, org) || !Allows(WithIdentity(base, org, "any-user"), org) {
			t.Fatal("inference restricted to owner", org)
		}
	}
	if Allows(WithIdentity(base, "firm-a", "any-user"), "firm-b") {
		t.Fatal("application tenant budget mismatch accepted")
	}
	if EvaluationProfiles(base) != nil {
		t.Fatal("background selection still uses an inference allowlist")
	}
	if !RequiresClientGrant(base) {
		t.Fatal("background client grant check disabled")
	}
}
