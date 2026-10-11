package machine

import (
	"close.local/backend/internal/llm"
	"strings"
	"testing"
)

func TestProfilesAreOptionalForInferenceAndRestrictedForPersonalResearch(t *testing.T) {
	base := Config{Token: strings.Repeat("x", 32), DatabaseURL: "postgres://invalid/unused", CodexModel: "fixed", CodexToken: "token"}
	if _, err := New(base); err != nil {
		t.Fatal("profile-free subscription inference refused", err)
	}
	base.Profiles = []llm.Scope{{Organization: "firm", Subject: llm.AnyMember, AccessProfile: "firm-read"}}
	if _, err := New(base); err == nil {
		t.Fatal("personal research accepted organization-wide access")
	}
	base.CodexToken = ""
	base.OpenAIKey = "key"
	if _, err := New(base); err != nil {
		t.Fatal("API research profile refused", err)
	}
}
