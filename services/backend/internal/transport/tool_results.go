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
}

// A read's lineage says where its result names the closes it drew on, so a conversation can record every close an
// answer touched without trusting the model to say. The forms are a path to an array of close ids ("items[].close_id"),
// optionally followed by "|field": a top-level field that, when true or a number beyond the listed rows, means the
// result counted or capped across more closes than it lists, so its lineage is not known. A close-scoped read (its
// path holds {close_id}) needs no declaration: its lineage is the pinned close. lineageNone marks a read that cannot
// name its closes.
const lineageNone = "none"

// capResult keeps a result's first rows. It returns the result unchanged when it has no cap, is not an object or its
// array is within the limit; otherwise the array is cut and "truncated": true and "total" are added.
func capResult(raw []byte, c toolCap) ([]byte, bool, int) {
	if c.array == "" || c.limit < 1 {
		return raw, false, 0
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return raw, false, 0
	}
	var rows []json.RawMessage
	if json.Unmarshal(object[c.array], &rows) != nil || len(rows) <= c.limit {
		return raw, false, len(rows)
	}
	cut, _ := json.Marshal(rows[:c.limit])
	object[c.array] = cut
	object["truncated"], _ = json.Marshal(true)
	object["total"], _ = json.Marshal(len(rows))
	out, err := json.Marshal(object)
	if err != nil {
		return raw, false, len(rows)
	}
	return out, true, len(rows)
}

// lineageOf collects the closes a result names. pinned is the conversation's close for a close-scoped read. The second
// result is false when the read's closes are not known: it declared no lineage it can keep, or it counted or capped
// across more closes than it lists.
func lineageOf(raw []byte, lineage, pinned string) ([]string, bool) {
	if pinned != "" {
		return []string{pinned}, true
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

// modelResult prepares a read's body for the model: its rows capped, and the closes it names collected from the body
// rather than from anything the model sent.
func modelResult(t mcpTool, raw []byte, pinned string) (body []byte, truncated bool, total int, closes []string, known bool) {
	if !strings.Contains(t.path, "{close_id}") {
		pinned = ""
	}
	closes, known = lineageOf(raw, t.lineage, pinned)
	body, truncated, total = capResult(raw, t.cap)
	return body, truncated, total, closes, known
}
