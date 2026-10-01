package transport

import (
	"close.local/backend/internal/llm"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"close.local/backend/internal/preparation"
)

// ponytail: one replay per person per process.
var replaying sync.Map

type replayItem struct {
	JobID     string   `json:"job_id"`
	Source    string   `json:"source_name"`
	Confirmed string   `json:"confirmed_kind"`
	Replayed  string   `json:"replay_kind,omitempty"`
	Kept      bool     `json:"kept"`
	Changed   []string `json:"changed_fields"`
	Error     string   `json:"error,omitempty"`
	plan      confirmedPlan
}

// runReplay reads up to three of a close's confirmed documents again with the current model, counted against the firm's daily limit, and compares each with what was confirmed.
func runReplay(ctx context.Context, db *sql.DB, org, closeID string) ([]*replayItem, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.job_id::text,e.name,j.plans FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN evidence e ON e.id=i.source_id WHERE j.close_id::text=$1 AND i.confirmed_by IS NOT NULL AND i.confirmed_by<>'agent' AND i.proposal->>'engine'='openai' ORDER BY i.confirmed_at DESC,i.job_id DESC LIMIT 3`, closeID)
	if err != nil {
		return nil, err
	}
	items := []*replayItem{}
	for rows.Next() {
		var it replayItem
		var rawPlans []byte
		if err = rows.Scan(&it.JobID, &it.Source, &rawPlans); err != nil {
			break
		}
		var plans []confirmedPlan
		if json.Unmarshal(rawPlans, &plans) == nil && len(plans) == 1 && plans[0].Kind != "" {
			it.plan = plans[0]
		} else {
			it.Error = "The confirmation is not one readable plan; not replayed."
		}
		it.Confirmed, it.Changed = it.plan.Kind, []string{}
		items = append(items, &it)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	calls := 0
	for _, it := range items {
		if it.Error == "" {
			calls++
		}
	}
	if err != nil || calls == 0 {
		return items, err
	}
	if err = preparation.ReserveModelCalls(ctx, db, org, calls); err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	for _, it := range items {
		if it.Error != "" {
			continue
		}
		wg.Go(func() {
			fresh, err := preparation.Replay(ctx, db, closeID, it.JobID)
			if err != nil {
				it.Error = err.Error()
				return
			}
			var reading readProposal
			raw, _ := json.Marshal(fresh)
			json.Unmarshal(raw, &reading)
			expected := []string{}
			for _, f := range reading.Facts {
				if strings.TrimSpace(factText(it.plan.Input[f.Field])) != "" {
					expected = append(expected, f.Field)
				}
			}
			it.Replayed = reading.Kind
			it.Kept, it.Changed, _ = compareProposal(reading, it.plan, expected)
		})
	}
	wg.Wait()
	return items, nil
}

// modelBudget reserves n model calls for the request's firm, answering 429 once today's limit is spent.
func modelBudget(w http.ResponseWriter, r *http.Request, db *sql.DB, n int) bool {
	err := preparation.ReserveModelCalls(r.Context(), db, organization(r), n)
	if errors.Is(err, preparation.ErrModelLimit) {
		problem(w, 429, err.Error())
	} else if err != nil {
		problem(w, 503, "The firm's model limit could not be checked.")
	}
	return err == nil
}

func saveReplay(ctx context.Context, db *sql.DB, org, closeID, model, actor string, items []*replayItem) error {
	raw, _ := json.Marshal(items)
	_, err := db.ExecContext(ctx, `INSERT INTO replay_runs(organization_id,close_id,model,actor,items,reasoning_effort) VALUES($1,$2::bigint,$3,$4,$5,$6)`, org, closeID, model, actor, string(raw), llm.ReasoningEffort(ctx, "preparation"))
	return err
}

// replay runs and saves a replay of the close's confirmed readings, or returns the latest saved one.
func replay(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			var run struct {
				Model           string          `json:"model"`
				ReasoningEffort string          `json:"reasoning_effort"`
				Actor           string          `json:"actor"`
				Items           json.RawMessage `json:"items"`
				CreatedAt       time.Time       `json:"created_at"`
			}
			err := db.QueryRowContext(r.Context(), `SELECT model,actor,items,created_at,reasoning_effort FROM replay_runs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, r.PathValue("id")).Scan(&run.Model, &run.Actor, &run.Items, &run.CreatedAt, &run.ReasoningEffort)
			if err == sql.ErrNoRows {
				jsonReply(w, 200, map[string]any{"run": nil})
				return
			}
			if err != nil {
				problem(w, 503, "Saved replays are unavailable.")
				return
			}
			jsonReply(w, 200, map[string]any{"run": run})
			return
		}
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		model := llm.Model(r.Context(), "preparation")
		if model == "" {
			problem(w, 503, "Configure the preparation model to replay model readings.")
			return
		}
		if _, running := replaying.LoadOrStore(actor, true); running {
			problem(w, 429, "Your previous replay is still running.")
			return
		}
		defer replaying.Delete(actor)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(95 * time.Second))
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		items, err := runReplay(ctx, db, organization(r), r.PathValue("id"))
		if errors.Is(err, preparation.ErrModelLimit) {
			problem(w, 429, err.Error())
			return
		}
		if err == nil && len(items) > 0 {
			err = saveReplay(ctx, db, organization(r), r.PathValue("id"), model, actor, items)
		}
		if err != nil {
			problem(w, 503, "Confirmed readings could not be replayed or saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"model": model, "reasoning_effort": llm.ReasoningEffort(ctx, "preparation"), "items": items})
	}
}

// sweepReplays replays, for each firm not yet checked on the current model and reasoning effort, the close with its newest confirmed reading. A run counts once it scores a reading.
func sweepReplays(ctx context.Context, db *sql.DB) error {
	model := llm.Model(ctx, "preparation")
	if model == "" {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT o.id,j.close_id::text FROM organizations o CROSS JOIN LATERAL (SELECT j.close_id FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN client_closes c ON c.id=j.close_id WHERE c.organization_id=o.id AND i.confirmed_by IS NOT NULL AND i.confirmed_by<>'agent' AND i.proposal->>'engine'='openai' ORDER BY i.confirmed_at DESC LIMIT 1) j
 WHERE o.enabled AND NOT EXISTS(SELECT 1 FROM replay_runs r WHERE r.organization_id=o.id AND r.model=$1 AND r.reasoning_effort=$2) ORDER BY o.id LIMIT 20`, model, llm.ReasoningEffort(ctx, "preparation"))
	if err != nil {
		return err
	}
	type due struct{ org, close string }
	firms := []due{}
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.org, &d.close); err != nil {
			break
		}
		firms = append(firms, d)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range firms {
		run, cancel := context.WithTimeout(ctx, 90*time.Second)
		items, err := runReplay(run, db, d.org, d.close)
		scored := false
		for _, it := range items {
			scored = scored || it.Error == ""
		}
		if err == nil && scored {
			err = saveReplay(run, db, d.org, d.close, model, "scheduled-replay", items)
		}
		cancel()
		if err != nil && !errors.Is(err, preparation.ErrModelLimit) {
			return err
		}
	}
	return nil
}
