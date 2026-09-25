package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"close.local/backend/internal/preparation"
)

// ponytail: one replay per person per process; a firm-wide model spend limit belongs with the other model-calling routes.
var replaying sync.Map

// replay reads up to three confirmed documents again with the current model and compares each with what was confirmed.
func replay(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		model := os.Getenv("OPENAI_MODEL")
		if os.Getenv("OPENAI_API_KEY") == "" || model == "" {
			problem(w, 503, "Set OPENAI_API_KEY and OPENAI_MODEL to replay model readings.")
			return
		}
		type item struct {
			JobID     string   `json:"job_id"`
			Source    string   `json:"source_name"`
			Confirmed string   `json:"confirmed_kind"`
			Replayed  string   `json:"replay_kind,omitempty"`
			Kept      bool     `json:"kept"`
			Changed   []string `json:"changed_fields"`
			Error     string   `json:"error,omitempty"`
			proposal  readProposal
			plan      confirmedPlan
		}
		rows, err := db.QueryContext(r.Context(), `SELECT i.job_id::text,e.name,i.proposal,j.plans FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN evidence e ON e.id=i.source_id WHERE j.close_id::text=$1 AND i.confirmed_by IS NOT NULL AND i.proposal->>'engine'='openai' ORDER BY i.confirmed_at DESC,i.job_id DESC LIMIT 3`, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Confirmed readings are unavailable.")
			return
		}
		items := []*item{}
		for rows.Next() {
			var it item
			var rawProposal, rawPlans []byte
			if err = rows.Scan(&it.JobID, &it.Source, &rawProposal, &rawPlans); err != nil {
				break
			}
			var plans []confirmedPlan
			json.Unmarshal(rawProposal, &it.proposal)
			if json.Unmarshal(rawPlans, &plans) == nil && len(plans) == 1 {
				it.plan = plans[0]
			}
			it.Confirmed, it.Changed = it.plan.Kind, []string{}
			items = append(items, &it)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Confirmed readings are unavailable.")
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
		client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		var wg sync.WaitGroup
		for _, it := range items {
			wg.Go(func() {
				fresh, err := preparation.Replay(ctx, db, client, r.PathValue("id"), it.JobID)
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
		jsonReply(w, 200, map[string]any{"model": model, "items": items})
	}
}
