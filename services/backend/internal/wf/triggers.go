package wf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// triggers starts the runs that schedules and event triggers call for. Every start is idempotent
// (a schedule slot or an event ID is the run's idempotency key), so any number of workers may
// evaluate triggers at once without starting anything twice.
func (e *Engine) triggers(ctx context.Context) {
	now := e.now()
	for _, w := range registered() {
		for _, t := range w.Triggers {
			var err error
			switch t.Kind {
			case "schedule", "schedule_each_firm":
				err = e.schedule(ctx, w, t, now)
			case "event":
				err = e.onEvents(ctx, w, t)
			}
			if err != nil && ctx.Err() == nil {
				log.Printf("Workflow %s trigger %s %q could not be evaluated: %v", w.Name, t.Kind, t.Value, err)
			}
		}
	}
}

func registered() []*workflow {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*workflow, 0, len(registry))
	for _, w := range registry {
		out = append(out, w)
	}
	return out
}

// latestSlot is the latest slot of s in the day up to now, or zero when none falls in it.
func latestSlot(s cron.Schedule, now time.Time) time.Time {
	var last time.Time
	for t, n := s.Next(now.Add(-24*time.Hour)), 0; !t.IsZero() && !t.After(now) && n < 1500; t, n = s.Next(t), n+1 {
		last = t
	}
	return last
}

func (e *Engine) schedule(ctx context.Context, w *workflow, t Trigger, now time.Time) error {
	s, err := cronParser.Parse(t.Value)
	if err != nil {
		return err
	}
	slot := latestSlot(s, now)
	if slot.IsZero() {
		return nil
	}
	firms := []string{""}
	if t.Kind == "schedule_each_firm" {
		if firms, err = e.enabledFirms(ctx); err != nil {
			return err
		}
	} else if e.OnlyOrganization != "" {
		return nil
	}
	for _, firm := range firms {
		_, _, err = start(ctx, e.DB, StartRequest{Workflow: w.Name, Organization: firm, Input: json.RawMessage("{}"), IdempotencyKey: "schedule@" + slot.Format(time.RFC3339), StartedBy: "schedule"}, 0, "")
		if err != nil && !errors.Is(err, ErrBusy) {
			// A slot still busy with the previous run is tried again on later passes within the day.
			return fmt.Errorf("slot %s for %q: %w", slot.Format(time.RFC3339), firm, err)
		}
	}
	return nil
}

func (e *Engine) enabledFirms(ctx context.Context) ([]string, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT id FROM organizations WHERE enabled AND ($1='' OR id=$1) ORDER BY id`, e.OnlyOrganization)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var firms []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		firms = append(firms, id)
	}
	return firms, rows.Err()
}

// onEvents starts a run for each matching event of the last three days that has none yet. The
// engine's own events (wf.) never start runs.
func (e *Engine) onEvents(ctx context.Context, w *workflow, t Trigger) error {
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSuffix(t.Value, "*"))
	if strings.HasSuffix(t.Value, "*") {
		like += "%"
	}
	// Page by event ID from where the last pass stopped, wrapping to the start at the end, so events
	// held back by a busy concurrency key never keep later ones from being reached.
	size, pages := e.eventPage, e.eventPages
	if size <= 0 {
		size, pages = 100, 20
	}
	cursor := w.Name + "\x00" + t.Value
	e.cursorMu.Lock()
	after := e.cursors[cursor]
	e.cursorMu.Unlock()
	for page := 0; ; page++ {
		n, last, err := e.onEventsPage(ctx, w, like, after, size)
		if err != nil {
			return err
		}
		if n < size {
			after = 0 // reached the end: start from the oldest next time
			break
		}
		after = last
		if page+1 >= pages {
			break
		}
	}
	e.cursorMu.Lock()
	if e.cursors == nil {
		e.cursors = map[string]int64{}
	}
	e.cursors[cursor] = after
	e.cursorMu.Unlock()
	return nil
}

func (e *Engine) onEventsPage(ctx context.Context, w *workflow, like string, after int64, size int) (int, int64, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT e.id,COALESCE(e.organization_id,''),e.payload FROM workflow_events e LEFT JOIN organizations o ON o.id=e.organization_id
 WHERE e.id>$4 AND e.created_at>now()-interval '3 days' AND e.key LIKE $1 ESCAPE '\' AND e.key NOT LIKE 'wf.%' AND (e.organization_id IS NULL OR o.enabled) AND ($3='' OR e.organization_id=$3)
 AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.workflow=$2 AND coalesce(r.organization_id,'')=coalesce(e.organization_id,'') AND r.idempotency_key='event:'||e.id)
 ORDER BY e.id LIMIT $5`, like, w.Name, e.OnlyOrganization, after, size)
	if err != nil {
		return 0, 0, err
	}
	type event struct {
		id      int64
		firm    string
		payload []byte
	}
	var events []event
	for rows.Next() {
		var ev event
		if err = rows.Scan(&ev.id, &ev.firm, &ev.payload); err != nil {
			rows.Close()
			return 0, 0, err
		}
		events = append(events, ev)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, 0, err
	}
	var last int64
	for _, ev := range events {
		last = ev.id
		key := fmt.Sprintf("event:%d", ev.id)
		_, _, err = start(ctx, e.DB, StartRequest{Workflow: w.Name, Organization: ev.firm, Input: json.RawMessage(ev.payload), IdempotencyKey: key, StartedBy: key}, 0, "")
		switch {
		case errors.Is(err, ErrInvalidInput):
			// Record it once as a failed run, so it is visible and never retried.
			_, err = e.DB.ExecContext(ctx, `INSERT INTO workflow_runs(organization_id,workflow,code_version,idempotency_key,input,started_by,state,error,finished_at)
 VALUES(NULLIF($1,''),$2,$3,$4,$5,$4,'failed',$6,now()) ON CONFLICT DO NOTHING`, ev.firm, w.Name, CodeVersion, key, string(ev.payload), truncate(err.Error()))
		case errors.Is(err, ErrBusy):
			err = nil // tried again on a later pass
		}
		if err != nil {
			return 0, 0, err
		}
	}
	return len(events), last, nil
}
