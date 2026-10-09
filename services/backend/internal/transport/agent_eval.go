package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/agenteval"
	"close.local/backend/internal/auth"
	"close.local/backend/internal/llm"
	"github.com/golang-jwt/jwt/v5"
)

// The agent chat's golden set runs the real loop (docs/agent-chat.md, "Evaluation and acceptance"): a recording run
// prepares each acceptance case as a close in a firm of its own and makes the expected reads as the firm's member; a
// replay run answers the loop's reads from that record and sends the conversation to the configured provider. The
// routes decide what a read returns in both; the model decides nothing a card would run.

// agentEvalPermissions is what the golden set's member may do: confirm cards, nothing firm-wide.
var agentEvalPermissions = []string{"workpapers:review"}

func agentEvalActor(ctx context.Context, firm AcceptanceFirm) context.Context {
	return auth.WithActor(ctx, &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: firm.Subject}, Organization: firm.Organization, Permissions: agentEvalPermissions})
}

// PrepareAgentEvalCase gives one acceptance case a firm of its own at the firm-wide agent level, with one member
// assigned to the case's client, and runs the case through the pipeline as the acceptance run does. It returns the
// case's close.
func PrepareAgentEvalCase(ctx context.Context, db *sql.DB, c acceptance.Case, firm AcceptanceFirm, model func(files map[string]string) (*llm.Client, error)) (string, error) {
	if firm.Organization == "" || firm.Subject == "" {
		return "", errors.New("the golden set runs inside a named firm and member")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organizations(id,name,max_model_calls_per_day,agent_chat) VALUES($1,$2,100000,'firm') ON CONFLICT (id) DO UPDATE SET agent_chat='firm'`, firm.Organization, "Agent golden set "+c.ID); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'Agent golden set','agent-eval')`, firm.Organization, firm.Subject); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,$3,true,'Agent golden set','agent-eval')`, firm.Organization, firm.Subject, c.Client); err != nil {
		return "", err
	}
	if _, err := RunAcceptanceCase(ctx, db, c, firm, model); err != nil {
		return "", err
	}
	var closeID string
	err := db.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, firm.Organization, c.Client, c.Period).Scan(&closeID)
	return closeID, err
}

// AddAgentEvalEvidence adds a text document to the close, as the firm's member uploading it would.
func AddAgentEvalEvidence(ctx context.Context, db *sql.DB, firm AcceptanceFirm, closeID string, e agenteval.Evidence) error {
	ctx = agentEvalActor(ctx, firm)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT id FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID); err != nil {
		return err
	}
	if _, _, _, refused := addEvidence(ctx, tx, closeID, e.Name, []byte(e.Text), false, "work"); refused != nil {
		return errors.New(refused.message)
	}
	return tx.Commit()
}

// RecordAgentEvalReads makes each of the question's expected reads as the firm's member, exactly as the loop would
// make them, and returns what the routes answered. A card's tool is never run: a card proposes, it does not act.
func RecordAgentEvalReads(ctx context.Context, db *sql.DB, firm AcceptanceFirm, closeID, entity, today string, q agenteval.Question) ([]agenteval.Read, error) {
	tools := agentTools(q.Scope == "close", "firm", false)
	store := savedCloses(db, strings.Repeat("a", 32))
	var reads []agenteval.Read
	for _, name := range q.Tools {
		var t mcpTool
		found := false
		for _, candidate := range tools {
			if candidate.name == name {
				t, found = candidate, true
			}
		}
		if !found {
			return nil, fmt.Errorf("question %s: %s is not a tool the agent offers %s-wide", q.ID, name, q.Scope)
		}
		if agentWrites[name] != "" {
			continue
		}
		args := map[string]any{}
		for key, value := range q.Args[name] {
			resolved, err := agentEvalValue(ctx, db, closeID, value)
			if err != nil {
				return nil, fmt.Errorf("question %s: %s: %w", q.ID, name, err)
			}
			args[key] = resolved
		}
		pinned, client := "", ""
		if q.Scope == "close" {
			pinned, client = closeID, entity
		} else if _, given := args["close_id"]; !given && strings.Contains(t.path, "{close_id}") {
			args["close_id"] = closeID
		}
		path, body, err := agentRequest(t, args, pinned, client, today)
		if err != nil {
			return nil, fmt.Errorf("question %s: %s: %w", q.ID, name, err)
		}
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = strings.NewReader(string(raw))
		}
		r := httptest.NewRequestWithContext(agentEvalActor(ctx, firm), t.method, path, reader)
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		store.ServeHTTP(rec, r)
		reads = append(reads, agenteval.Read{Tool: name, Method: t.method, Path: path, Body: body, Output: rec.Body.String(), OK: rec.Code == 200})
	}
	return reads, nil
}

// agentEvalValue resolves what the recording run created: $close, $evidence:<file> or $workpaper:<n>.
func agentEvalValue(ctx context.Context, db *sql.DB, closeID string, value any) (any, error) {
	s, ok := value.(string)
	if !ok || !strings.HasPrefix(s, "$") {
		return value, nil
	}
	var id string
	switch {
	case s == "$close":
		return closeID, nil
	case strings.HasPrefix(s, "$evidence:"):
		err := db.QueryRowContext(ctx, `SELECT id::text FROM evidence WHERE close_id::text=$1 AND name=$2 ORDER BY id LIMIT 1`, closeID, strings.TrimPrefix(s, "$evidence:")).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("%s: no such document on the close", s)
		}
		return id, nil
	case strings.HasPrefix(s, "$workpaper:"):
		n, err := strconv.Atoi(strings.TrimPrefix(s, "$workpaper:"))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%s: the draft number must be 1 or more", s)
		}
		if err = db.QueryRowContext(ctx, `SELECT id::text FROM workpapers WHERE close_id::text=$1 ORDER BY id OFFSET $2 LIMIT 1`, closeID, n-1).Scan(&id); err != nil {
			return nil, fmt.Errorf("%s: the close has no such draft", s)
		}
		return id, nil
	}
	return nil, fmt.Errorf("%s: not a value the recording run resolves", s)
}

// RunAgentEvalQuestion runs the loop for one question against the configured provider, with every read answered from
// the record and access to exactly the recorded close and client; nothing is saved.
func RunAgentEvalQuestion(ctx context.Context, db *sql.DB, q agenteval.Question, record agenteval.Record, client *llm.Client) agenteval.Observed {
	firm := AcceptanceFirm{Organization: record.Organization, Subject: record.Subject}
	ctx = agentEvalActor(llm.WithClient(ctx, client), firm)
	// The production deadline: an answer the chat could not deliver does not pass here.
	ctx, cancel := context.WithTimeout(ctx, agentDeadline)
	defer cancel()
	closeID, entity, period := "", "", ""
	if q.Scope == "close" {
		closeID, entity, period = record.CloseID, record.Entity, record.Period
	}
	read := func(ctx context.Context, method, path string, body any) (string, bool) {
		output, ok, found := record.Lookup(q.ID, method, path, body)
		if !found {
			return "", false
		}
		return output, ok
	}
	canOpen := func(id string) bool { return id == record.CloseID }
	canClient := func(name string) bool { return strings.EqualFold(strings.TrimSpace(name), record.Entity) }
	result := runAgent(ctx, db, record.Organization, closeID, "", q.Question, "Golden set firm", entity, period, record.Today, agentEvalPermissions, "firm", nil, read, canOpen, canClient, func(string, map[string]any) bool { return true })
	observed := agenteval.Observed{ID: q.ID, Kind: result.kind, Answer: result.reply.body, Citations: len(result.reply.citations), Outcome: result.outcome, Why: result.why, Rounds: result.rounds, Reads: result.calls}
	for _, step := range result.steps {
		// The reads that ran; a proposed card is scored as the card, a refused or unavailable read as none.
		switch step.Status {
		case "proposed":
		case "refused", "unavailable":
			observed.Attempted = append(observed.Attempted, step.Tool)
		default:
			observed.Tools = append(observed.Tools, step.Tool)
		}
	}
	if result.proposal != nil {
		observed.Card, observed.Args = result.proposal.Action, result.proposal.Args
	}
	return observed
}

// EnsureAgentEvalFirm keeps the recorded firm present for a replay: the loop reserves its model calls against it.
func EnsureAgentEvalFirm(ctx context.Context, db *sql.DB, record agenteval.Record) error {
	_, err := db.ExecContext(ctx, `INSERT INTO organizations(id,name,max_model_calls_per_day,agent_chat) VALUES($1,$2,100000,'firm') ON CONFLICT (id) DO NOTHING`, record.Organization, "Agent golden set "+record.Case)
	return err
}
