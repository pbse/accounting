package transport

import (
	"net/http"

	"close.local/backend/internal/documents"
)

// The helpers the agent loop shares with the chat route. The written-answer and investigation paths they served are
// replaced by the agent loop (agent.go); see docs/agent-chat.md.

// streamWriter is a streamed reply: its status went out with the first line, so a later one is not sent.
type streamWriter struct{ http.ResponseWriter }

func (s *streamWriter) WriteHeader(int) {}

// redactValues removes sensitive details from every string in a decoded JSON value.
func redactValues(v any) any {
	switch t := v.(type) {
	case string:
		clean, _ := documents.Redact(t)
		return clean
	case []any:
		for i := range t {
			t[i] = redactValues(t[i])
		}
	case map[string]any:
		for k := range t {
			t[k] = redactValues(t[k])
		}
	}
	return v
}

// asked is the question as the model sees it: with card, bank and sign-in details removed.
func asked(question string) string {
	redacted, _ := documents.Redact(question)
	return redacted
}
