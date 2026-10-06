package acceptance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"close.local/backend/internal/llm"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Recorded is a model client that answers each reading with the case's recorded answer for the document being read, so a
// recorded run replays everything around the model, not the model itself. ids maps each document's evidence ID to its
// file. A reading with no recorded answer is refused, which the pipeline records as a failed reading.
func (c Case) Recorded(ids map[string]string) (*llm.Client, error) {
	if err := c.RecordedComplete(); err != nil {
		return nil, err
	}
	answers := map[string]string{}
	for _, d := range c.Documents {
		if d.Model == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.Dir, d.Model))
		if err != nil {
			return nil, err
		}
		if !json.Valid(raw) {
			return nil, fmt.Errorf("case %s: recorded answer %s is not JSON", c.ID, d.Model)
		}
		answers[d.File] = string(raw)
	}
	reply := func(status int, body any) *http.Response {
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}
	}
	model := roundTrip(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Input []struct {
				Content string `json:"content"`
			} `json:"input"`
		}
		json.Unmarshal(raw, &request)
		var given struct {
			Primary string `json:"primary_source_id"`
		}
		for _, item := range request.Input {
			json.Unmarshal([]byte(item.Content), &given)
		}
		answer, ok := answers[ids[given.Primary]]
		if !ok {
			return reply(400, map[string]any{"error": map[string]string{"message": "no recorded answer for this reading"}}), nil
		}
		answer = strings.ReplaceAll(answer, `"$primary"`, strconv.Quote(given.Primary))
		for id, file := range ids {
			answer = strings.ReplaceAll(answer, `"$source:`+file+`"`, strconv.Quote(id))
		}
		return reply(200, map[string]any{"id": "recorded", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": answer}}}}}), nil
	})
	return llm.New(llm.Config{APIKey: "recorded-answers", Model: "recorded", HTTP: &http.Client{Transport: model}})
}

// RecordedComplete refuses a case that a recorded run cannot evaluate: every document the app keeps (each whose bytes do
// not repeat an earlier one's) needs its recorded answer, or its reading would fail and nothing would be scored.
func (c Case) RecordedComplete() error {
	var problems []error
	seen := map[string]bool{}
	for _, d := range c.Documents {
		body, err := os.ReadFile(filepath.Join(c.Dir, d.File))
		if err != nil {
			return err
		}
		if seen[string(body)] {
			continue
		}
		seen[string(body)] = true
		if d.Model == "" {
			problems = append(problems, fmt.Errorf("case %s: document %s has no recorded answer", c.ID, d.File))
		}
	}
	return errors.Join(problems...)
}
