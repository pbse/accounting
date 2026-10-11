package transport

import (
	"log"
	"time"

	"close.local/backend/internal/llm"
)

func agentModelFailure(err error, phase string, rounds, calls int, attempt string, began time.Time) string {
	code, status := llm.FailureCode(err)
	// Never log err: provider errors may contain prompts, URLs or credentials.
	log.Printf("agent_model_failure attempt=%s phase=%s code=%s status=%d elapsed_ms=%d rounds=%d tool_calls=%d", attempt, phase, code, status, time.Since(began).Milliseconds(), rounds, calls)
	switch code {
	case "timeout":
		if phase == "planning" {
			return "The agent ran out of time while reading. Nothing was saved; try a more focused question or try again."
		}
		return "The agent ran out of time while writing its answer. Nothing was saved; try a more focused question or try again."
	case "canceled":
		return "The conversation was interrupted. Nothing was saved; try again."
	case "busy":
		return "The AI service is busy or has reached its usage limit. Nothing was saved; try again shortly."
	case "incomplete", "limit":
		return "The AI service returned an incomplete response. Nothing was saved; try again."
	}
	switch status {
	case 401:
		return "The AI service could not authenticate. Nothing was saved; its sign-in needs attention."
	case 403:
		return "The AI service denied this request. Nothing was saved; its access configuration needs attention."
	case 400, 422:
		return "The AI service rejected this request. Nothing was saved; please contact support."
	}
	return "The AI service is unavailable. Nothing was saved; try again shortly."
}
