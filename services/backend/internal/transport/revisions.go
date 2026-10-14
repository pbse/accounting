package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"close.local/backend/internal/llm"
	"close.local/backend/internal/preparation"
)

// reviseReturned carries a reviewer's return of a draft into a revision, as part of the close's goal: the oldest return
// not yet taken up, one per look. The note, the draft's sources and the close's other documents go to the model; the
// changes it can show by a quote are recalculated as a revision that comes back to the reviewer, and anything else
// becomes one question. Either outcome is recorded once against the return, so a return is never revised twice; another
// return, with the answer, is taken up as a new note. Nothing is posted or approved here.
func reviseReturned(ctx context.Context, db *sql.DB, goalID string, roles map[string]reviewer) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var g goalRow
	var entity, period string
	err = tx.QueryRowContext(ctx, `SELECT g.id::text,c.id::text,c.organization_id,g.author,c.entity,c.period FROM close_goals g JOIN client_closes c ON c.id=g.close_id WHERE g.id=$1 AND g.state<>'stopped'`, goalID).Scan(&g.id, &g.close, &g.org, &g.author, &entity, &period)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var paperID, kind, reviewID, note, reviewer string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT w.id::text,w.kind,w.paper,x.id::text,x.note,x.actor FROM workpaper_review_state w
 CROSS JOIN LATERAL (SELECT id,note,actor FROM workpaper_reviews WHERE workpaper_id=w.id ORDER BY id DESC LIMIT 1) x
 WHERE w.close_id=$1 AND w.review_state='changes_requested'
 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)
 AND NOT EXISTS(SELECT 1 FROM preparation_jobs j WHERE j.close_id=w.close_id AND j.state='queued' AND j.plans @> jsonb_build_array(jsonb_build_object('revises',w.id::text)))
 AND NOT EXISTS(SELECT 1 FROM close_goals cg CROSS JOIN jsonb_array_elements(cg.events) ev WHERE cg.close_id=w.close_id AND ev->>'revision'=x.id::text)
 AND NOT EXISTS(SELECT 1 FROM close_signoffs s WHERE s.close_id=w.close_id AND s.id=(SELECT max(id) FROM close_signoffs WHERE close_id=w.close_id) AND s.decision='signed_off')
 ORDER BY x.id LIMIT 1`, g.close).Scan(&paperID, &kind, &raw, &reviewID, &note, &reviewer)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var paper struct {
		Input   json.RawMessage      `json:"input"`
		Sources []preparation.Source `json:"sources"`
		Carry   json.RawMessage      `json:"carry_forward"`
		Import  json.RawMessage      `json:"imported"`
		Accrual json.RawMessage      `json:"recurring_accrual"`
	}
	if err = json.Unmarshal(raw, &paper); err != nil {
		return err
	}
	returned := fmt.Sprintf("%s returned draft #%s (“%s”).", reviewer, paperID, note)
	record := func(tx *sql.Tx, text string) error {
		_, err := tx.ExecContext(ctx, `UPDATE close_goals SET events=events||jsonb_build_array(jsonb_build_object('actor','agent','text',$2::text,'at',now(),'revision',$3::text)) WHERE id=$1`, g.id, returned+" "+text, reviewID)
		if err == nil {
			err = tx.Commit()
		}
		return err
	}
	// A draft that continues an approved schedule, or a kind whose facts are not a form's, is revised on its page.
	if !preparation.Revisable(kind) || len(paper.Carry) > 0 && string(paper.Carry) != "null" || len(paper.Import) > 0 && string(paper.Import) != "null" || len(paper.Accrual) > 0 && string(paper.Accrual) != "null" {
		return record(tx, "I can't revise this kind of draft from a note, so it waits for a person to revise it on its page.")
	}
	ctx = llm.WithIdentity(ctx, g.org, g.author)
	allowed, err := goalAuthorMayUseModel(ctx, tx, g, entity, roles)
	if err != nil {
		return err
	}
	if llm.Model(ctx, "preparation") == "" || !llm.Allows(ctx, g.org) || !allowed {
		return record(tx, "No preparation model is configured or authorized for this client, so the draft waits for a person to revise it.")
	}
	// The draft's own sources as they were cited, and the close's other current documents, up to eight.
	ids := []string{}
	for _, s := range paper.Sources {
		ids = append(ids, s.ID)
	}
	rows, err := tx.QueryContext(ctx, `(SELECT e.id::text,e.name,e.digest,e.content,true FROM evidence e WHERE e.close_id=$1 AND e.purpose='work' AND e.superseded_by IS NULL AND e.extraction_state='ready' AND e.id::text=ANY($2::text[]))
 UNION ALL (SELECT e.id::text,e.name,e.digest,e.content,false FROM evidence e WHERE e.close_id=$1 AND e.purpose='work' AND e.superseded_by IS NULL AND e.extraction_state='ready' AND NOT e.id::text=ANY($2::text[])
 AND e.procedure_id IS NULL AND NOT EXISTS(SELECT 1 FROM work_replies r WHERE r.source_id=e.id) AND NOT EXISTS(SELECT 1 FROM preparation_answers a WHERE a.source_id=e.id) ORDER BY e.id DESC LIMIT 8)`, g.close, ids)
	if err != nil {
		return err
	}
	own, others := []preparation.IntakeSource{}, []preparation.IntakeSource{}
	for rows.Next() {
		var s preparation.IntakeSource
		var cited bool
		if err = rows.Scan(&s.ID, &s.Name, &s.Digest, &s.Content, &cited); err != nil {
			rows.Close()
			return err
		}
		if cited {
			own = append(own, s)
		} else {
			others = append(others, s)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, s := range paper.Sources {
		if !hasSource(own, s) {
			return record(tx, "A source it cites changed or is no longer current, so it waits for a person to prepare it again.")
		}
	}
	if err = preparation.ReserveModelCalls(ctx, tx, g.org, 1); errors.Is(err, preparation.ErrModelLimit) {
		return nil // the firm's model calls for today are used; the next look tries again
	} else if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	model, cancel := context.WithTimeout(ctx, time.Minute)
	revision, err := preparation.Revise(model, kind, note, paper.Input, own, others)
	cancel()
	if errors.Is(err, preparation.ErrInvalidRevision) {
		revision, err = preparation.Revision{Question: "I couldn't read the note into a change I could show from the documents. What should change?"}, nil
	}
	if err != nil {
		return err
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The return must still be the draft's latest decision, unrevised and not yet taken up.
	var current bool
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM client_closes WHERE id::text=$1 FOR UPDATE`, g.close); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id::text FROM workpaper_reviews WHERE workpaper_id::text=$1 ORDER BY id DESC LIMIT 1),'')=$2
 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id::text=$3 AND n.paper->>'revises'=$1)
 AND NOT EXISTS(SELECT 1 FROM close_goals cg CROSS JOIN jsonb_array_elements(cg.events) ev WHERE cg.close_id::text=$3 AND ev->>'revision'=$2)
 AND EXISTS(SELECT 1 FROM close_goals WHERE id=$4 AND state<>'stopped')`, paperID, reviewID, g.close, g.id).Scan(&current); err != nil || !current {
		return err
	}
	// The goal's owner may have lost the client while the model read the note.
	if allowed, err = goalAuthorMayUseModel(ctx, tx, g, entity, roles); err != nil || !allowed {
		if err == nil {
			err = record(tx, "Its goal's owner is no longer a member or no longer has this client, so it waits for a person to revise it.")
		}
		return err
	}
	if revision.Question != "" {
		return record(tx, "Before I revise it: "+revision.Question+" Return it again with the answer and I'll revise it.")
	}
	// A document the draft did not cite becomes a further support source, beside any it has (a ledger page, say).
	sources := paper.Sources
	if revision.Added != nil {
		sources = append(sources, *revision.Added)
		sort.SliceStable(sources, func(i, j int) bool { return sources[i].Role < sources[j].Role })
	}
	words := []string{}
	for _, c := range revision.Changes {
		from := "your note"
		for _, s := range append(own, others...) {
			if s.ID == c.SourceID {
				from = s.Name
			}
		}
		before := c.Before
		if before == "" {
			before = "blank"
		}
		words = append(words, fmt.Sprintf("%s from %s to %s (from %s)", strings.ReplaceAll(c.Field, "_", " "), before, c.Value, from))
	}
	changed := "I changed " + strings.Join(words, ", ")
	plan := preparation.Plan{Kind: kind, Input: revision.Input, Sources: sources, Revises: paperID}
	if _, err = preparation.Calculate(plan, entity, period); err != nil {
		return record(tx, changed+", but the calculation refuses it: "+err.Error()+" What should it be?")
	}
	body, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, `INSERT INTO preparation_plans(close_id,digest,plan) VALUES($1,$2,$3) ON CONFLICT(close_id,digest) DO NOTHING`, g.close, preparation.Digest(body), string(body)); err != nil {
		return err
	}
	plans, _ := json.Marshal([]preparation.Plan{plan})
	var job string
	if err = tx.QueryRowContext(ctx, `INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,digest) DO UPDATE SET state=CASE WHEN preparation_jobs.state IN ('cancelled','failed') THEN 'queued' ELSE preparation_jobs.state END,error='',finished_at=CASE WHEN preparation_jobs.state IN ('cancelled','failed') THEN NULL ELSE preparation_jobs.finished_at END RETURNING id::text`, g.close, preparation.Digest(plans), string(plans), g.author).Scan(&job); err != nil {
		return err
	}
	return record(tx, changed+", and queued the recalculation as job #"+job+". The revised draft comes back to "+reviewer+" for review.")
}

// goalAuthorMayUseModel is whether the goal's owner may still act on the client, as the goal's step decides it (a member
// who reviews, firm-wide or granted the client), and, where the model route requires it as background preparation's
// does, is still granted the client.
func goalAuthorMayUseModel(ctx context.Context, tx *sql.Tx, g goalRow, entity string, roles map[string]reviewer) (bool, error) {
	var member, granted bool
	err := tx.QueryRowContext(ctx, `SELECT $1='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted),
 $1='local-development' OR EXISTS(SELECT 1 FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($3) AND granted)`, g.org, g.author, entity).Scan(&member, &granted)
	return mayAct(roles, g.author, member, false, granted) && (granted || !llm.RequiresClientGrant(ctx)), err
}

func hasSource(list []preparation.IntakeSource, s preparation.Source) bool {
	for _, have := range list {
		if have.ID == s.ID && have.Digest == s.Digest {
			return true
		}
	}
	return false
}
