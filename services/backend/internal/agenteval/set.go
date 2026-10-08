// Package agenteval holds the agent chat's golden set (docs/agent-chat.md, "Evaluation and acceptance"): questions over
// the acceptance corpus with the tools, facts and card each expects, off-scope prompts that must be refused, the tool
// results recorded for replay, and the scoring of a run.
package agenteval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Question is one golden question: the acceptance case it is asked about (the case is a close in a firm of its own),
// the scope it is asked in, the tools the agent is expected to call (a set, in any order), the facts its answer must
// contain, and the card it must propose. An off-scope prompt must be refused with no card instead.
type Question struct {
	ID       string   `json:"id"`
	Case     string   `json:"case"`
	Scope    string   `json:"scope"` // firm or close
	Question string   `json:"question"`
	Tools    []string `json:"tools,omitempty"`
	// Args are the arguments each expected tool is recorded with. A value may name what the recording run created:
	// $close, $evidence:<file name> or $workpaper:<n> (the close's nth draft, oldest first).
	Args     map[string]map[string]any `json:"args,omitempty"`
	Facts    []string                  `json:"facts,omitempty"`
	Card     string                    `json:"card,omitempty"`
	OffScope bool                      `json:"off_scope,omitempty"`
	// Evidence is a text document added to the case's close before the question is asked, for prompts whose
	// instruction text sits inside a document. It is added after the case's other questions were recorded.
	Evidence *Evidence `json:"evidence,omitempty"`
}

// Evidence is a text document added to a close.
type Evidence struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// Set is the golden set.
type Set struct {
	Questions []Question `json:"questions"`
	Dir       string     `json:"-"`
}

// Read is one recorded tool result: the request the loop makes for it and what the route answered.
type Read struct {
	Tool   string `json:"tool"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
	Output string `json:"output"`
	OK     bool   `json:"ok"`
}

// Record is what one case's firm answered, kept for replay: the firm and member it was recorded under, the case's
// close, the day it was recorded, and every read.
type Record struct {
	Case         string `json:"case"`
	Organization string `json:"organization"`
	Subject      string `json:"subject"`
	CloseID      string `json:"close_id"`
	Entity       string `json:"entity"`
	Period       string `json:"period"`
	Today        string `json:"today"`
	Reads        []Read `json:"reads"`
}

// Load reads golden.json and every recorded case under reads/.
func Load(dir string) (Set, map[string]Record, error) {
	var set Set
	raw, err := os.ReadFile(filepath.Join(dir, "golden.json"))
	if err != nil {
		return set, nil, err
	}
	if err = json.Unmarshal(raw, &set); err != nil {
		return set, nil, fmt.Errorf("golden.json: %w", err)
	}
	set.Dir = dir
	if err = set.Check(); err != nil {
		return set, nil, err
	}
	records := map[string]Record{}
	files, _ := filepath.Glob(filepath.Join(dir, "reads", "*.json"))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return set, nil, err
		}
		var r Record
		if err = json.Unmarshal(raw, &r); err != nil {
			return set, nil, fmt.Errorf("%s: %w", file, err)
		}
		records[r.Case] = r
	}
	return set, records, nil
}

// Check refuses a set that could not be scored: a repeated id, an unknown scope, a question without a case, an
// off-scope prompt that expects facts or a card, or a card the agent does not propose.
func (s Set) Check() error {
	seen := map[string]bool{}
	cards := map[string]bool{"prepare": true, "recheck": true, "stop": true, "request": true, "delegate": true, "approval": true, "portfolio": true, "open_close": true}
	var errs []error
	for _, q := range s.Questions {
		switch {
		case q.ID == "" || seen[q.ID]:
			errs = append(errs, fmt.Errorf("question %q: the id is missing or repeated", q.ID))
		case q.Case == "" || strings.TrimSpace(q.Question) == "":
			errs = append(errs, fmt.Errorf("question %s: needs a case and a question", q.ID))
		case q.Scope != "firm" && q.Scope != "close":
			errs = append(errs, fmt.Errorf("question %s: scope must be firm or close", q.ID))
		case q.OffScope && (len(q.Facts) > 0 || q.Card != ""):
			errs = append(errs, fmt.Errorf("question %s: an off-scope prompt expects no facts and no card", q.ID))
		case q.Card != "" && !cards[q.Card]:
			errs = append(errs, fmt.Errorf("question %s: %q is not a card the agent proposes", q.ID, q.Card))
		case q.Card == "open_close" && q.Scope != "firm", q.Card == "portfolio" && q.Scope != "firm":
			errs = append(errs, fmt.Errorf("question %s: the %s card is proposed firm-wide only", q.ID, q.Card))
		}
		seen[q.ID] = true
	}
	return errors.Join(errs...)
}

// Cases lists the acceptance cases the set asks about, in order of first use.
func (s Set) Cases() []string {
	var out []string
	seen := map[string]bool{}
	for _, q := range s.Questions {
		if !seen[q.Case] {
			seen[q.Case] = true
			out = append(out, q.Case)
		}
	}
	return out
}

// Key is how a read is matched on replay: its method, path and body, exactly as the loop makes the request.
func Key(method, path string, body any) string {
	raw, _ := json.Marshal(body)
	if body == nil {
		raw = []byte("null")
	}
	return method + " " + path + " " + string(raw)
}

// Lookup answers a read from the record: the output and whether the route answered 200, and whether the read was
// recorded at all.
func (r Record) Lookup(method, path string, body any) (string, bool, bool) {
	want := Key(method, path, body)
	for _, read := range r.Reads {
		if Key(read.Method, read.Path, read.Body) == want {
			return read.Output, read.OK, true
		}
	}
	return "", false, false
}

// Add keeps a read once; a repeated request keeps its first answer, so a case's reads are stable across its questions.
func (r *Record) Add(read Read) {
	if _, _, found := r.Lookup(read.Method, read.Path, read.Body); found {
		return
	}
	r.Reads = append(r.Reads, read)
}

// Save writes the record under reads/<case>.json, reads sorted by their key so a re-recording diffs cleanly.
func (r Record) Save(dir string) error {
	sort.SliceStable(r.Reads, func(i, j int) bool {
		return Key(r.Reads[i].Method, r.Reads[i].Path, r.Reads[i].Body) < Key(r.Reads[j].Method, r.Reads[j].Path, r.Reads[j].Body)
	})
	raw, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(dir, "reads"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "reads", r.Case+".json"), append(raw, '\n'), 0o644)
}
