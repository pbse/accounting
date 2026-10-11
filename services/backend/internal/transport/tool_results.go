package transport

import (
	"encoding/json"
	"strconv"
	"strings"
)

// toolCap names a read's main array and how many of its rows a model is sent. A capped result keeps its first rows
// and says so, instead of being dropped whole for its size.
type toolCap struct {
	array string
	limit int
	// arrays are further top-level arrays cut to the same limit (an overview lists closes and their reports).
	arrays []string
}

// A read's lineage says where its result names the closes it drew on, so a conversation can record every close an
// answer touched without trusting the model to say. The forms are a path to an array of close ids ("items[].close_id"),
// optionally followed by "|field": a top-level field that, when true or a number beyond the listed rows, means the
// result counted or capped across more closes than it lists, so its lineage is not known. A close-scoped read (its
// path holds {close_id}) needs no declaration: its lineage is the pinned close; one that also draws on another close
// names where ("+prior.close_id", "+last_close_id"), and that close joins the pinned one. lineageNone marks a read
// that cannot name its closes.
const lineageNone = "none"

// capResult keeps a result's first rows. It returns the result unchanged when it has no cap, is not an object or its
// array is within the limit; otherwise the array is cut and "truncated": true and "total" are added.
func capResult(raw []byte, c toolCap) ([]byte, bool, int) {
	if c.array == "" || c.limit < 1 {
		return raw, false, 0
	}
	var object map[string]json.RawMessage
	var rows []json.RawMessage
	// A bare array (the evidence list) is the "items" the MCP handler also presents it as.
	if c.array == "items" && json.Unmarshal(raw, &rows) == nil {
		if len(rows) <= c.limit {
			return raw, false, len(rows)
		}
		object = map[string]json.RawMessage{"items": raw}
	} else if json.Unmarshal(raw, &object) != nil || object == nil {
		return raw, false, 0
	}
	if json.Unmarshal(object[c.array], &rows) != nil {
		return raw, false, 0
	}
	total, truncated := len(rows), false
	for _, name := range append([]string{c.array}, c.arrays...) {
		var items []json.RawMessage
		if json.Unmarshal(object[name], &items) != nil || len(items) <= c.limit {
			continue
		}
		cut, _ := json.Marshal(items[:c.limit])
		object[name] = cut
		truncated = true
	}
	if !truncated {
		return raw, false, total
	}
	object["truncated"], _ = json.Marshal(true)
	object["total"], _ = json.Marshal(total)
	out, err := json.Marshal(object)
	if err != nil {
		return raw, false, total
	}
	return out, true, total
}

// projectCashPlan is the 13-week cash plan as the model is sent it: every week's totals, with each week's dated items
// and every other list of items (past due, after the horizon, unscheduled) cut to eight and the rest counted
// (more_<list>), so a client with many open invoices and bills still fits the read.
func projectCashPlan(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	cut := func(o map[string]any, key string) {
		if items, ok := o[key].([]any); ok && len(items) > 8 {
			o[key] = items[:8]
			o["more_"+key] = len(items) - 8
		}
	}
	for key, v := range object {
		if key == "weeks" {
			if weeks, ok := v.([]any); ok {
				for _, week := range weeks {
					if w, ok := week.(map[string]any); ok {
						cut(w, "items")
					}
				}
			}
			continue
		}
		cut(object, key)
	}
	return object
}

// projectPublications is a close's published reports as the model is sent them: each report's number, version, who
// published it and when, its state and how many acknowledged it, with its commentary and reason cut to a line, so a
// long listing stays within the read limit and every report's number is seen.
func projectPublications(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	rows, ok := object["publications"].([]any)
	if !ok {
		return value
	}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		p, ok := row.(map[string]any)
		if !ok {
			continue
		}
		compact := map[string]any{}
		for _, key := range []string{"id", "version", "published_by", "published_at", "state", "supersedes"} {
			if v, ok := p[key]; ok {
				compact[key] = v
			}
		}
		for _, key := range []string{"commentary", "reason"} {
			if text, ok := p[key].(string); ok {
				if len(text) > 200 {
					text = text[:200] + "…"
				}
				compact[key] = text
			}
		}
		if acks, ok := p["acknowledgments"].([]any); ok {
			compact["acknowledged_by"] = len(acks)
		}
		out = append(out, compact)
	}
	object["publications"] = out
	return object
}

// projectPublication is the part of a published report the model is sent: by default its headline (the publication,
// the pack's entity, month, currency, basis and status, the Profit and Loss measures, working capital and the cash-flow
// statement's standing), or one section in full, so a large frozen pack is read a section at a time rather than dropped whole.
func projectPublication(value any, section string) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	pack, _ := object["pack"].(map[string]any)
	publication, _ := object["publication"].(map[string]any)
	switch section {
	case "profit_and_loss", "balance_sheet", "working_capital", "cash_flow":
		// A section keeps what its figures are of (client, month, currency, basis), so they stay bound to them.
		out := map[string]any{"publication": publication, "section": section, section: pack[section]}
		for _, key := range []string{"entity", "period", "currency", "basis", "status"} {
			if v, ok := pack[key]; ok {
				out[key] = v
			}
		}
		return out
	case "commentary":
		return map[string]any{"publication": publication, "section": section, "commentary": publication["commentary"]}
	}
	summary := map[string]any{}
	for _, key := range []string{"entity", "period", "currency", "basis", "as_of", "status", "overall", "note", "working_capital"} {
		if v, ok := pack[key]; ok {
			summary[key] = v
		}
	}
	if pl, ok := pack["profit_and_loss"].(map[string]any); ok {
		summary["profit_and_loss"] = map[string]any{"measures": pl["measures"]}
	}
	// The cash-flow statement's standing (reconciled or not, and its disclosure) is part of the headline; its lines are a section.
	if flow, ok := pack["cash_flow"].(map[string]any); ok {
		standing := map[string]any{}
		for _, key := range []string{"reconciled", "disclosure", "state"} {
			if v, ok := flow[key]; ok {
				standing[key] = v
			}
		}
		summary["cash_flow"] = standing
	}
	summary["sections"] = []string{"profit_and_loss", "balance_sheet", "working_capital", "cash_flow", "commentary"}
	return map[string]any{"publication": publication, "section": "summary", "pack": summary}
}

// lineageOf collects the closes a result names. pinned is the conversation's close for a close-scoped read. The second
// result is false when the read's closes are not known: it declared no lineage it can keep, or it counted or capped
// across more closes than it lists.
func lineageOf(raw []byte, lineage, pinned string) ([]string, bool) {
	if pinned != "" {
		// A close-scoped read that draws on closes it cannot name (a budget comparison's prior months and year to date)
		// is pinned but its lineage is not known, so its answer is shown once.
		if lineage == lineageNone {
			return []string{pinned}, false
		}
		closes := []string{pinned}
		if extra, ok := strings.CutPrefix(lineage, "+"); ok {
			if id := closeAt(raw, extra); id != "" && id != pinned {
				closes = append(closes, id)
			}
		}
		return closes, true
	}
	if lineage == "" || lineage == lineageNone {
		return nil, false
	}
	path, more, _ := strings.Cut(lineage, "|")
	array, field, ok := strings.Cut(path, "[].")
	if !ok {
		return nil, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, false
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(object[array], &rows) != nil {
		return nil, false
	}
	if more != "" {
		var flag bool
		var count float64
		if json.Unmarshal(object[more], &flag) == nil && flag {
			return nil, false
		}
		if json.Unmarshal(object[more], &count) == nil && count > float64(len(rows)) {
			return nil, false
		}
	}
	closes := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		var id string
		var number float64
		if json.Unmarshal(row[field], &id) != nil {
			if json.Unmarshal(row[field], &number) != nil {
				return nil, false
			}
			id = strconv.FormatInt(int64(number), 10)
		}
		if id == "" {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			closes = append(closes, id)
		}
	}
	return closes, true
}

// closeAt reads a close id at a dotted object path ("prior.close_id"), as a string or a number; "" when absent.
func closeAt(raw []byte, path string) string {
	current := json.RawMessage(raw)
	for _, key := range strings.Split(path, ".") {
		var object map[string]json.RawMessage
		if json.Unmarshal(current, &object) != nil || object[key] == nil {
			return ""
		}
		current = object[key]
	}
	var id string
	var number float64
	if json.Unmarshal(current, &id) == nil {
		return id
	}
	if json.Unmarshal(current, &number) == nil && number > 0 {
		return strconv.FormatInt(int64(number), 10)
	}
	return ""
}

// scopeProcedures keeps, of a procedures listing, the versions for one client and those for all clients: inside a
// close the model is told this client's treatments, never another client's instructions or accounts.
func scopeProcedures(value any, entity string) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	versions, ok := object["versions"].([]any)
	if !ok {
		return value
	}
	kept := []any{}
	for _, v := range versions {
		row, isRow := v.(map[string]any)
		if !isRow {
			continue
		}
		client, _ := row["client_entity"].(string)
		if client == "" || strings.EqualFold(strings.TrimSpace(client), strings.TrimSpace(entity)) {
			kept = append(kept, row)
		}
	}
	object["versions"] = kept
	return object
}

// modelResult prepares a read's body for the model: its rows capped, and the closes it names collected from the body
// rather than from anything the model sent. A firm-wide read cut by its cap lists fewer closes than it drew on, so its
// lineage is not known, exactly as a read the route itself capped.
func modelResult(t mcpTool, raw []byte, pinned string) (body []byte, truncated bool, total int, closes []string, known bool) {
	if !strings.Contains(t.path, "{close_id}") {
		pinned = ""
	}
	closes, known = lineageOf(raw, t.lineage, pinned)
	body, truncated, total = capResult(raw, t.cap)
	if truncated && pinned == "" {
		closes, known = nil, false
	}
	return body, truncated, total, closes, known
}
