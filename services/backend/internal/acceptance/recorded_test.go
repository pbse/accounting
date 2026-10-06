package acceptance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/llm"
)

// A recorded run answers each request by its purpose: the evidence selection a large close asks for first, and the
// reading itself, with the evidence IDs the documents were given.
func TestRecordedAnswersFollowTheRequestsPurpose(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"a.txt": "Invoice", "b.txt": "Contract", "a.model.json": `{"kind":"prepaid","sources":[{"id":"$primary"}]}`,
		"a.selection.json": `{"selected_ids":["$primary","$source:b.txt"]}`, "b.model.json": `{"kind":"none"}`} {
		if err := writeFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	c := Case{ID: "rec", Dir: dir, Documents: []Document{{File: "a.txt", Model: "a.model.json", Selection: "a.selection.json"}, {File: "b.txt", Model: "b.model.json"}}}
	client, err := c.Recorded(map[string]string{"7": "a.txt", "8": "b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := llm.WithClient(context.Background(), client)
	ask := func(purpose, primary string) (string, error) {
		data, _ := json.Marshal(map[string]string{"primary_source_id": primary})
		result, err := llm.Generate(ctx, llm.Request{Purpose: purpose, Instructions: "Answer.", Data: json.RawMessage(data), MaxResponseBytes: 65536, MaxOutputTokens: 100,
			Schema: &llm.OutputSchema{Name: purpose, Definition: json.RawMessage(`{"type":"object"}`)}})
		return result.Text, err
	}
	if got, err := ask("evidence_selection", "7"); err != nil || got != `{"selected_ids":["7","8"]}` {
		t.Fatalf("selection: %s %v", got, err)
	}
	if got, err := ask("preparation", "7"); err != nil || got != `{"kind":"prepaid","sources":[{"id":"7"}]}` {
		t.Fatalf("reading: %s %v", got, err)
	}
	if _, err := ask("evidence_selection", "8"); err == nil {
		t.Fatal("b.txt has no recorded selection; the request fails rather than taking its reading")
	}
	if got, err := ask("preparation", "8"); err != nil || !strings.Contains(got, `"none"`) {
		t.Fatalf("b.txt's reading: %s %v", got, err)
	}
}
