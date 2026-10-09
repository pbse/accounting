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
	body, truncated, total := capResult(raw, toolCap{array: "rows", limit: 2})
	var out struct {
		Rows      []map[string]string `json:"rows"`
		Truncated bool                `json:"truncated"`
		Total     int                 `json:"total"`
		State     string              `json:"state"`
	}
	if err := json.Unmarshal(body, &out); err != nil || !truncated || total != 3 || len(out.Rows) != 2 || !out.Truncated || out.Total != 3 || out.State != "ready" || out.Rows[1]["account"] != "AR" {
		t.Fatalf("capped result: %s truncated=%v total=%d err=%v", body, truncated, total, err)
	}
	same, truncated, _ := capResult(raw, toolCap{array: "rows", limit: 3})
	if truncated || string(same) != string(raw) {
		t.Fatalf("a result within its cap changed: %s", same)
	}
	same, truncated, _ = capResult(raw, toolCap{})
	if truncated || string(same) != string(raw) {
		t.Fatalf("a result without a cap changed: %s", same)
	}
	list, truncated, total := capResult([]byte(`[{"id":"1"},{"id":"2"},{"id":"3"}]`), toolCap{array: "items", limit: 2})
	if err := json.Unmarshal(list, &out); err != nil || !truncated || total != 3 || !out.Truncated || out.Total != 3 || !strings.Contains(string(list), `"items":[{"id":"1"},{"id":"2"}]`) {
		t.Fatalf("a bare array is capped as items: %s truncated=%v total=%d err=%v", list, truncated, total, err)
	}
	if same, truncated, _ := capResult([]byte(`[{"id":"1"}]`), toolCap{array: "items", limit: 2}); truncated || string(same) != `[{"id":"1"}]` {
		t.Fatalf("a short bare array changed: %s", same)
	}
	text := []byte(`"a document's text"`)
	if same, _, _ := capResult(text, toolCap{array: "rows", limit: 1}); string(same) != string(text) {
		t.Fatalf("a text result changed: %s", same)
	}
}

// Inside a close the procedures listing is cut to this client's versions and the firm-wide ones.
func TestProceduresAreScopedToTheConversationsClient(t *testing.T) {
	var value any
	json.Unmarshal([]byte(`{"versions":[{"id":"1","client_entity":"Alpha","method":"a"},{"id":"2","client_entity":"","method":"b"},{"id":"3","client_entity":"Beta","method":"c"}]}`), &value)
	out, _ := json.Marshal(scopeProcedures(value, "alpha "))
	if strings.Contains(string(out), "Beta") || !strings.Contains(string(out), `"id":"1"`) || !strings.Contains(string(out), `"id":"2"`) {
		t.Fatalf("another client's procedures reached the model: %s", out)
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
	var pack, flux mcpTool
	for _, tool := range mcpTools {
		switch tool.name {
		case "management_pack":
			pack = tool
		case "explain_changes":
			flux = tool
		}
	}
	if _, _, _, closes, known := modelResult(pack, []byte(`{"prior":{"close_id":"40","period":"2026-08"}}`), "41"); !known || !slices.Equal(closes, []string{"41", "40"}) {
		t.Fatalf("a comparison read names the prior close too: %v %v", closes, known)
	}
	if _, _, _, closes, known := modelResult(flux, []byte(`{"state":"ready","last_close_id":40,"changes":[]}`), "41"); !known || !slices.Equal(closes, []string{"41", "40"}) {
		t.Fatalf("changes from last month name the prior close too: %v %v", closes, known)
	}
	var budget mcpTool
	for _, tool := range mcpTools {
		if tool.name == "budget_comparison" {
			budget = tool
		}
	}
	if _, _, _, closes, known := modelResult(budget, []byte(`{"month":{}}`), "41"); known || !slices.Equal(closes, []string{"41"}) {
		t.Fatalf("a budget comparison draws on months it cannot name, so its lineage is not known: %v %v", closes, known)
	}
	if _, _, _, closes, known := modelResult(pack, []byte(`{"state":"waiting"}`), "41"); !known || !slices.Equal(closes, []string{"41"}) {
		t.Fatalf("a comparison read without a prior close names its own: %v %v", closes, known)
	}
	var listing mcpTool
	for _, tool := range mcpTools {
		if tool.name == "list_closes" {
			listing = tool
		}
	}
	long := `{"closes":[` + strings.Repeat(`{"id":"1"},`, 60) + `{"id":"2"}],"has_more":false}`
	if body, truncated, _, closes, known := modelResult(listing, []byte(long), ""); !truncated || known || closes != nil || !strings.Contains(string(body), `"truncated":true`) {
		t.Fatalf("a read cut by its own cap has no known lineage: truncated=%v known=%v %v", truncated, known, closes)
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
