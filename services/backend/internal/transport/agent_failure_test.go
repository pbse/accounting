package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/llm"
)

func TestAgentFailureMessagesAndLogsDoNotExposeModelContent(t *testing.T) {
	oldOutput, oldFlags := log.Writer(), log.Flags()
	defer func() { log.SetOutput(oldOutput); log.SetFlags(oldFlags) }()
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetFlags(0)
	for _, tc := range []struct {
		err         error
		phase, want string
	}{{context.DeadlineExceeded, "planning", "while reading"}, {context.DeadlineExceeded, "answer", "while writing"}, {&llm.ServiceError{Status: 504}, "answer", "while writing"}, {&llm.ServiceError{Status: 401}, "planning", "authenticate"}, {&llm.ServiceError{Status: 403}, "planning", "access configuration"}, {&llm.ServiceError{Status: 429}, "planning", "usage limit"}, {&llm.ServiceError{Status: 400}, "answer", "rejected"}, {llm.ErrIncomplete, "answer", "incomplete"}, {errors.New("SECRET_PROMPT_AND_TOKEN"), "answer", "unavailable"}} {
		message := agentModelFailure(fmt.Errorf("SECRET_PROMPT_AND_TOKEN: %w", tc.err), tc.phase, 2, 3, "test-attempt", time.Now())
		if !strings.Contains(message, tc.want) || !strings.Contains(message, "Nothing was saved") || strings.Contains(message, "SECRET") {
			t.Fatal(message)
		}
	}
	if strings.Contains(output.String(), "SECRET") || !strings.Contains(output.String(), "phase=answer code=timeout status=504") {
		t.Fatal(output.String())
	}
}
func TestAgentLoopKeepsTimeForFinalAnswerAndClassifiesFailures(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_ChatFailure" + rand.Text()
	if _, err = db.Exec(`INSERT INTO organizations(id,name,max_model_calls_per_day,agent_chat) VALUES($1,'Synthetic failure test',100,'firm')`, org); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM model_calls WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	for _, failAt := range []int{1, 2, 0} {
		calls := 0
		client, err := llm.New(llm.Config{APIKey: "fake", Model: "fake", HTTP: &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			deadline, _ := r.Context().Deadline()
			left := time.Until(deadline)
			planning := calls == 1
			if planning && left < 30*time.Second || !planning && left < 55*time.Second {
				t.Errorf("insufficient request time at call %d: %s", calls, left)
			}
			status, body := 200, `{"id":"r","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Clarification needed."}]}]}`
			if calls == failAt {
				status, body = 504, "SECRET_PROVIDER_BODY"
			} else if calls == 2 {
				body = `{"id":"r","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"kind\":\"help\",\"answer\":\"Which client and month should I use?\",\"support\":[]}"}]}]}`
			}

			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(llm.WithClient(context.Background(), client), agentDeadline)
		result := runAgent(ctx, db, org, "", "", "What is blocking this month's close?", "Synthetic firm", "", "", "2026-10-11", []string{"workpapers:read"}, "firm", nil, func(context.Context, string, string, any) (string, bool) {
			t.Error("clarification ran a read")
			return "", false
		}, func(id string) bool { return false }, func(string) bool { return false }, func(string, map[string]any) bool { return true })
		cancel()
		if failAt != 0 {
			if calls != failAt || result.outcome != "failed" || result.proposal != nil || result.reply.body != "" || !strings.Contains(result.why, "ran out of time") {
				t.Fatalf("failure at %d calls=%d result=%+v", failAt, calls, result)
			}
		} else if calls != 2 || result.outcome != "kept" || result.kind != "help" || result.reply.body != "Which client and month should I use?" {
			t.Fatalf("clarification calls=%d result=%+v", calls, result)
		}
	}
}

func TestAgentClarificationQuestionsKeepFactGuards(t *testing.T) {
	for _, text := range []string{"Which client and month should I use?", "What period should I read?", "Which close do you mean?"} {
		if _, why := agentSupport("help", text, nil, nil, nil); why != "" {
			t.Fatalf("clarification rejected: %s: %s", text, why)
		}
	}
	for _, tc := range []struct {
		text  string
		names []string
	}{
		{"Which client is blocked?", nil},
		{"Which client with overdue invoices should I read?", nil},
		{"Which client has cash of 100?", nil},
		{"Which client, Alpha Ltd, should I read?", []string{"Alpha Ltd"}},
	} {
		if _, why := agentSupport("help", tc.text, nil, nil, tc.names); why == "" {
			t.Fatalf("clarification bypassed fact guards: %s", tc.text)
		}
	}
}
