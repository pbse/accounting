package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"close.local/backend/internal/llm"
	"close.local/backend/internal/preparation"
)

// returnWaits is SQL for whether return (review) review of close closeID waits for the goal: never taken up, or the goal
// asked a question about it that someone has answered since, or that a document the close has read since may answer
// (one added after the question's snapshot, or one still being read then).
// Each outcome is a goal event naming the return ('revision'), an answer names it too ('answers'); the latest decides.
func returnWaits(closeID, review string) string {
	return `COALESCE((SELECT CASE WHEN ev ? 'answers' THEN true WHEN ev ? 'clarify' THEN EXISTS(SELECT 1 FROM evidence e WHERE e.close_id=` + closeID + ` AND e.purpose='work' AND e.superseded_by IS NULL
 AND e.extraction_state='ready' AND e.created_by NOT IN ('quickbooks-sync','scheduled-quickbooks-refresh') AND (e.id>(ev->>'evidence')::bigint OR COALESCE(ev->'reading','[]'::jsonb) ? e.id::text)) ELSE false END
 FROM close_goals cg CROSS JOIN jsonb_array_elements(cg.events) WITH ORDINALITY o(ev,n) WHERE cg.close_id=` + closeID + ` AND (ev->>'revision'=` + review + ` OR ev->>'answers'=` + review + `) ORDER BY cg.id DESC,o.n DESC LIMIT 1),true)`
}

// noteWords are the words of a note worth finding in a document's name or text: four letters or more, at most twenty.
func noteWords(note string) []string {
	common := map[string]bool{"this": true, "that": true, "with": true, "from": true, "have": true, "should": true, "would": true, "could": true, "please": true, "draft": true, "there": true, "their": true, "which": true, "what": true, "when": true, "into": true, "your": true, "also": true, "than": true, "then": true, "them": true, "they": true, "were": true, "been": true, "only": true, "does": true, "dont": true, "it's": true}
	words := []string{}
	for _, w := range strings.FieldsFunc(strings.ToLower(note), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 4 && !common[w] && !slices.Contains(words, w) && len(words) < 20 {
			words = append(words, w)
		}
	}
	return words
}

// reviseReturned carries a reviewer's return of a draft into a revision, as part of the close's goal: the oldest return
// waiting (returnWaits), one per look. The note, any question asked about it with its answers, the draft's sources and
// the close's documents most likely to bear on it go to the model; the changes it can show by a quote are recalculated
// as a revision that comes back to the reviewer, and anything else becomes one question. A question waits for an answer
// on the draft or a document the close reads afterwards, and the return is then taken up again with it; every other
// outcome settles the return. Nothing is posted or approved here.
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
 AND `+returnWaits("w.close_id", "x.id::text")+`
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
	// What the goal knows as it takes the return up: how many events already concern it, the newest document the close
	// holds and those still being read. A question recorded against this snapshot waits for a later answer or document.
	var seen int
	var newest string
	var reading []byte
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM close_goals cg CROSS JOIN jsonb_array_elements(cg.events) ev WHERE cg.close_id=$1::bigint AND (ev->>'revision'=$2 OR ev->>'answers'=$2)),
 (SELECT COALESCE(max(id),0)::text FROM evidence WHERE close_id=$1::bigint),(SELECT COALESCE(jsonb_agg(id::text),'[]') FROM evidence WHERE close_id=$1::bigint AND superseded_by IS NULL AND extraction_state<>'ready')`, g.close, reviewID).Scan(&seen, &newest, &reading); err != nil {
		return err
	}
	// record settles the return with what the goal did; with a question, the return waits for its answer or a document
	// read after the snapshot.
	record := func(tx *sql.Tx, text, question string) error {
		_, err := tx.ExecContext(ctx, `UPDATE close_goals SET events=events||jsonb_build_array(jsonb_build_object('actor','agent','text',$2::text,'at',now(),'revision',$3::text)
 ||CASE WHEN $4='' THEN '{}'::jsonb ELSE jsonb_build_object('clarify',true,'question',$4::text,'evidence',$5::text,'reading',$6::jsonb) END) WHERE id=$1`, g.id, returned+" "+text, reviewID, question, newest, string(reading))
		if err == nil {
			err = tx.Commit()
		}
		return err
	}
	// The questions asked about this return and their answers, in order, go to the model with the note.
	var asked []byte
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(ev ORDER BY cg.id,o.n),'[]') FROM close_goals cg CROSS JOIN jsonb_array_elements(cg.events) WITH ORDINALITY o(ev,n)
 WHERE cg.close_id=$1::bigint AND (ev->>'answers'=$2 OR ev->>'revision'=$2 AND ev ? 'clarify')`, g.close, reviewID).Scan(&asked); err != nil {
		return err
	}
	var exchange []struct {
		Question string   `json:"question"`
		Answer   string   `json:"answer"`
		Evidence string   `json:"evidence"`
		Reading  []string `json:"reading"`
	}
	json.Unmarshal(asked, &exchange)
	// The reviewer's words (the note and each answer) are what a quote may come from; the questions are context only.
	instruction, since, unread, questions := note, "0", []string{}, []string{}
	for _, e := range exchange {
		if e.Question != "" {
			questions = append(questions, e.Question)
			since, unread = e.Evidence, e.Reading
		} else {
			instruction += "\n\nThe reviewer answered: " + e.Answer
		}
	}
	// A draft that continues an approved schedule, or a kind whose facts are not a form's, is revised on its page.
	if !preparation.Revisable(kind) || len(paper.Carry) > 0 && string(paper.Carry) != "null" || len(paper.Import) > 0 && string(paper.Import) != "null" || len(paper.Accrual) > 0 && string(paper.Accrual) != "null" {
		return record(tx, "I can't revise this kind of draft from a note, so it waits for a person to revise it on its page.", "")
	}
	ctx = llm.WithIdentity(ctx, g.org, g.author)
	allowed, err := goalAuthorMayUseModel(ctx, tx, g, entity, roles)
	if err != nil {
		return err
	}
	if llm.Model(ctx, "preparation") == "" || !llm.Allows(ctx, g.org) || !allowed {
		return record(tx, "No preparation model is configured or authorized for this client, so the draft waits for a person to revise it.", "")
	}
	// The draft's own sources as they were cited, and up to eight of the close's other current documents: any added or read
	// since the goal last asked about this return first, then those whose name or text has most of the note's words,
	// newest first.
	ids := []string{}
	for _, s := range paper.Sources {
		ids = append(ids, s.ID)
	}
	rows, err := tx.QueryContext(ctx, `(SELECT e.id::text,e.name,e.digest,e.content,true FROM evidence e WHERE e.close_id=$1 AND e.purpose='work' AND e.superseded_by IS NULL AND e.extraction_state='ready' AND e.id::text=ANY($2::text[]))
 UNION ALL (SELECT e.id::text,e.name,e.digest,e.content,false FROM evidence e WHERE e.close_id=$1 AND e.purpose='work' AND e.superseded_by IS NULL AND e.extraction_state='ready' AND NOT e.id::text=ANY($2::text[])
 AND e.procedure_id IS NULL AND NOT EXISTS(SELECT 1 FROM work_replies r WHERE r.source_id=e.id) AND NOT EXISTS(SELECT 1 FROM preparation_answers a WHERE a.source_id=e.id)
 ORDER BY (e.id>$3::bigint OR e.id::text=ANY($5::text[])) DESC,(SELECT count(*) FROM unnest($4::text[]) k WHERE e.name ILIKE '%'||k||'%' OR e.content ILIKE '%'||k||'%') DESC,e.id DESC LIMIT 8)`, g.close, ids, since, noteWords(instruction), unread)
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
			return record(tx, "A source it cites changed or is no longer current, so it waits for a person to prepare it again.", "")
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
	revision, err := preparation.Revise(model, kind, instruction, questions, paper.Input, own, others)
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
	// An answer that arrived while the model read is not lost: a new event about the return leaves it for the next look.
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id::text FROM workpaper_reviews WHERE workpaper_id::text=$1 ORDER BY id DESC LIMIT 1),'')=$2
 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id::text=$3 AND n.paper->>'revises'=$1)
 AND `+returnWaits("$3::bigint", "$2")+`
 AND (SELECT count(*) FROM close_goals cg CROSS JOIN jsonb_array_elements(cg.events) ev WHERE cg.close_id=$3::bigint AND (ev->>'revision'=$2 OR ev->>'answers'=$2))=$5
 AND EXISTS(SELECT 1 FROM close_goals WHERE id=$4 AND state<>'stopped')`, paperID, reviewID, g.close, g.id, seen).Scan(&current); err != nil || !current {
		return err
	}
	// The goal's owner may have lost the client while the model read the note.
	if allowed, err = goalAuthorMayUseModel(ctx, tx, g, entity, roles); err != nil || !allowed {
		if err == nil {
			err = record(tx, "Its goal's owner is no longer a member or no longer has this client, so it waits for a person to revise it.", "")
		}
		return err
	}
	if revision.Question != "" {
		return record(tx, "Before I revise it: "+revision.Question+" Answer it on the draft, or add the document it needs to the close, and I'll revise it.", revision.Question)
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
		question := changed + ", but the calculation refuses it: " + err.Error() + " What should it be?"
		return record(tx, question, question)
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
	return record(tx, changed+", and queued the recalculation as job #"+job+". The revised draft comes back to "+reviewer+" for review.", "")
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
