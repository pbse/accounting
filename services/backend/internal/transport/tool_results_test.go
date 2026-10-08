package transport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// A read sent to the model keeps its first rows and says how many there were, instead of being dropped for its size;
// a result without a cap, or within it, is sent as it is.
func TestModelReadsAreCappedNotDropped(t *testing.T) {
	raw := []byte(`{"state":"ready","rows":[{"account":"Cash"},{"account":"AR"},{"account":"AP"}]}`)
	body, truncated, total := capResult(raw, toolCap{"rows", 2})
	var out struct {
		Rows      []map[string]string `json:"rows"`
		Truncated bool                `json:"truncated"`
		Total     int                 `json:"total"`
		State     string              `json:"state"`
	}
	if err := json.Unmarshal(body, &out); err != nil || !truncated || total != 3 || len(out.Rows) != 2 || !out.Truncated || out.Total != 3 || out.State != "ready" || out.Rows[1]["account"] != "AR" {
		t.Fatalf("capped result: %s truncated=%v total=%d err=%v", body, truncated, total, err)
	}
	same, truncated, _ := capResult(raw, toolCap{"rows", 3})
	if truncated || string(same) != string(raw) {
		t.Fatalf("a result within its cap changed: %s", same)
	}
	same, truncated, _ = capResult(raw, toolCap{})
	if truncated || string(same) != string(raw) {
		t.Fatalf("a result without a cap changed: %s", same)
	}
	text := []byte(`"a document's text"`)
	if same, _, _ := capResult(text, toolCap{"rows", 1}); string(same) != string(text) {
		t.Fatalf("a text result changed: %s", same)
	}
}

// The closes an answer touched come from the read's own body: a close-scoped read names the conversation's close
// whatever the model sent; a firm-wide read names each close in its rows once; a read that counted or capped across
// more closes than it lists, or that declared no lineage, has no known closes.
func TestLineageComesFromTheReadNotTheModel(t *testing.T) {
	var pinned mcpTool
	for _, tool := range mcpTools {
		if tool.name == "tie_out" {
			pinned = tool
		}
	}
	if _, _, _, closes, known := modelResult(pinned, []byte(`{"rows":[]}`), "41"); !known || !slices.Equal(closes, []string{"41"}) {
		t.Fatalf("pinned close: %v %v", closes, known)
	}
	work := []byte(`{"items":[{"id":"1","close_id":"7"},{"id":"2","close_id":"7"},{"id":"3","close_id":9}],"has_more":false,"counts":{"active":3}}`)
	if closes, known := lineageOf(work, "items[].close_id|has_more", ""); !known || !slices.Equal(closes, []string{"7", "9"}) {
		t.Fatalf("listed closes: %v %v", closes, known)
	}
	if closes, known := lineageOf([]byte(`{"items":[{"close_id":"7"}],"has_more":true}`), "items[].close_id|has_more", ""); known || closes != nil {
		t.Fatalf("a capped list claimed its closes: %v %v", closes, known)
	}
	if _, known := lineageOf([]byte(`{"goals":[{"close_id":"7"}],"more":false}`), "goals[].close_id|more", ""); !known {
		t.Fatal("a complete list has known closes")
	}
	if _, known := lineageOf([]byte(`{"items":[{"close_id":"7"}],"count":4}`), "items[].close_id|count", ""); known {
		t.Fatal("a list counting beyond its rows claimed its closes")
	}
	if _, known := lineageOf([]byte(`{"items":[{"workpaper_id":"3"}]}`), "items[].close_id", ""); known {
		t.Fatal("a row without a close id claimed a lineage")
	}
	if _, known := lineageOf(work, lineageNone, ""); known {
		t.Fatal("a read without lineage claimed closes")
	}
	// Every registry read declares its lineage, or is close-scoped, so no read reaches the model unclassified.
	for _, tool := range mcpTools {
		if tool.readOnly && !strings.Contains(tool.path, "{close_id}") && tool.lineage == "" {
			t.Errorf("%s declares no lineage", tool.name)
		}
	}
}
