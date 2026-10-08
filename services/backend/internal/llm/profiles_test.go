package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// An organization-wide profile (subject "*") completes for every member of that organization as themselves: the scope
// on the wire carries the member's own subject, never the wildcard, so the service checks that member's access. A
// member's own profile wins over the wide one, another organization is still refused, and a caller without a subject
// cannot complete.
func TestOrganizationWideProfileCompletesForEachMemberAsThemselves(t *testing.T) {
	wide := Scope{Organization: "firm", Subject: AnyMember, AccessProfile: "firm-read"}
	own := Scope{Organization: "firm", Subject: "owner", AccessProfile: "owner-read"}
	var sent []Scope
	cfg := Config{Provider: "context_machine", ContextMachineToken: "service-token", ContextMachineModel: "codex", CodexModel: "cli-model", Profiles: []Scope{wide, own}}
	cfg.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var in CompletionRequest
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Fatal("unreadable completion")
		}
		sent = append(sent, in.Scope)
		raw, _ := json.Marshal(CompletionResponse{Version: 1, Status: "completed", Scope: in.Scope, Response: Response{ID: "run", Model: "cli-model", ReasoningEffort: "medium", AuthMode: "api_key", Text: "{}", ModelCalls: 1}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := WithClient(context.Background(), client)
	ask := func(ctx context.Context) error {
		_, err := Generate(ctx, Request{Purpose: "chat", Instructions: "Answer", Schema: &OutputSchema{Name: "answer", Definition: json.RawMessage(`{"type":"object"}`)}, MaxOutputTokens: 50})
		return err
	}
	if err := ask(WithIdentity(base, "firm", "member-a")); err != nil {
		t.Fatal("a member of the wide profile was refused:", err)
	}
	if err := ask(WithIdentity(base, "firm", "owner")); err != nil {
		t.Fatal("the owner was refused:", err)
	}
	want := []Scope{{Organization: "firm", Subject: "member-a", AccessProfile: "firm-read"}, own}
	if len(sent) != 2 || sent[0] != want[0] || sent[1] != want[1] {
		t.Fatalf("scopes on the wire: %v", sent)
	}
	for name, ctx := range map[string]context.Context{"another organization": WithIdentity(base, "other", "member-a"), "no identity": base, "the wildcard itself": WithIdentity(base, "firm", AnyMember), "an empty subject": WithIdentity(base, "firm", "")} {
		if err := ask(ctx); err == nil || len(sent) != 2 {
			t.Fatalf("%s completed: %v", name, err)
		}
	}
	if !Allows(WithIdentity(base, "firm", "member-b"), "firm") || Allows(WithIdentity(base, "other", "member-b"), "other") || !Allows(base, "firm") || Allows(base, "other") || Allows(WithIdentity(base, "firm", AnyMember), "firm") {
		t.Fatal("eligibility does not follow the wide profile")
	}
	if _, err := New(Config{Provider: "context_machine", ContextMachineToken: "service-token", ContextMachineModel: "codex", CodexModel: "cli-model", Profiles: []Scope{wide, wide}}); err == nil {
		t.Fatal("a duplicate wide profile was accepted")
	}
}
