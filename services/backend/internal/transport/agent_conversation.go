package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"close.local/backend/internal/llm"
)

// closesLiteral is a PostgreSQL array literal of close ids, for a bigint[] parameter.
func closesLiteral(ids []string) string {
	return "{" + strings.Join(ids, ",") + "}"
}

// pruneConversation deletes every turn pair whose reads touched a close the member can no longer open, or that no
// longer exists, before the conversation is shown or replayed; the question goes with its answer. A pair's closes are
// written on both messages, so one statement finds both.
func pruneConversation(ctx context.Context, db *sql.DB, org, author, closeID string, firmWide bool) error {
	_, err := db.ExecContext(ctx, `DELETE FROM chat_messages m WHERE m.organization_id=$1 AND m.author=$2 AND m.close_id IS NOT DISTINCT FROM NULLIF($3,'')::bigint AND EXISTS(SELECT 1 FROM unnest(m.closes) AS touched(id) WHERE NOT EXISTS(SELECT 1 FROM client_closes c WHERE c.id=touched.id AND c.organization_id=$1 AND ($4 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$2 AND lower(g.client_entity)=lower(c.entity) AND g.granted))))`, org, author, closeID, firmWide)
	return err
}

// agentHistory is the conversation the model sees: the last 20 turns as plain text, each member message redacted as
// it is sent. Only a kept answer and its question are replayable; a firm-wide pair whose reads touched no close is
// not, since nothing verified its client scope; a close conversation is already bound to its close.
func agentHistory(ctx context.Context, db *sql.DB, org, author, closeID string) ([]llm.Message, error) {
	rows, err := db.QueryContext(ctx, `SELECT role,body,array_to_json(closes)::text FROM chat_messages WHERE organization_id=$1 AND author=$2 AND close_id IS NOT DISTINCT FROM NULLIF($3,'')::bigint AND replayable AND (close_id IS NOT NULL OR created_at>now()-interval '30 days') ORDER BY id DESC LIMIT 40`, org, author, closeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type turn struct {
		role, body string
		closes     []int64
	}
	turns := []turn{}
	for rows.Next() {
		var t turn
		var closes string
		if err = rows.Scan(&t.role, &t.body, &closes); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(closes), &t.closes)
		turns = append(turns, t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// Newest first: an answer is kept with the question before it; anything else is skipped. The window is bounded
	// by characters as well as turns, oldest first out, and no single message is replayed past 2,000 characters.
	const budget = 24000
	spent := 0
	out := []llm.Message{}
	for i := 0; i+1 < len(turns); i++ {
		answer, question := turns[i], turns[i+1]
		if answer.role != "assistant" || question.role != "user" {
			continue
		}
		i++
		if closeID == "" && len(answer.closes) == 0 {
			continue
		}
		body := answer.body
		if cut := strings.Index(body, "\n"+agentNote); cut > 0 {
			body = body[:cut]
		}
		asked := asked(question.body)
		if len(asked) > 2000 {
			asked = asked[:2000] + "…"
		}
		if len(body) > 2000 {
			body = body[:2000] + "…"
		}
		if spent+len(asked)+len(body) > budget {
			break
		}
		spent += len(asked) + len(body)
		out = append([]llm.Message{{Role: "user", Content: asked}, {Role: "assistant", Content: body}}, out...)
		if len(out) >= 40 {
			break
		}
	}
	return out, nil
}
