package machine

import (
	"strings"
	"testing"

	"close.local/backend/internal/llm"
)

// The service accepts a completion from any member named under an organization-wide profile, with the member's own
// subject on the wire, and still refuses the wildcard itself, an empty subject, another organization and another access
// profile. A wide profile needs API-key agents: a personal login serves its owner only.
func TestServiceAcceptsMembersOfAnOrganizationWideProfile(t *testing.T) {
	wide := llm.Scope{Organization: "firm", Subject: llm.AnyMember, AccessProfile: "firm-read"}
	own := llm.Scope{Organization: "firm", Subject: "owner", AccessProfile: "owner-read"}
	s := &server{cfg: Config{Profiles: []llm.Scope{wide, own}}}
	for scope, want := range map[llm.Scope]bool{
		{Organization: "firm", Subject: "member-a", AccessProfile: "firm-read"}: true,
		own:  true,
		wide: false,
		{Organization: "firm", Subject: "", AccessProfile: "firm-read"}:          false,
		{Organization: "other", Subject: "member-a", AccessProfile: "firm-read"}: false,
		{Organization: "firm", Subject: "member-a", AccessProfile: "owner-read"}: false,
	} {
		if s.allows(scope) != want {
			t.Errorf("%v allowed=%v", scope, !want)
		}
	}
	base := Config{Token: strings.Repeat("x", 32), DatabaseURL: "postgres://invalid/unused", CodexModel: "fixed", Profiles: []llm.Scope{wide}}
	keyed := base
	keyed.OpenAIKey = "key"
	if _, err := New(keyed); err != nil {
		t.Fatal("API-key agents refused a wide profile:", err)
	}
	personal := base
	personal.CodexToken = "token"
	if _, err := New(personal); err == nil {
		t.Fatal("a personal login accepted an organization-wide profile")
	}
}
