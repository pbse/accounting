package transport

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/reconciliation"
)

// The inbox reads the queue's real states; it never maintains a second status cache.
const workSelect = `SELECT j.id::text,j.close_id::text,c.entity,c.period,j.state,j.error,j.workpaper_ids,
 COALESCE(i.agent_started_at::text,''),COALESCE(i.revision,0),COALESCE(i.agent_attempts,0),COALESCE(i.agent_checkpoint,'{}'::jsonb),COALESCE(i.proposal,'{}'::jsonb),COALESCE(e.id::text,''),COALESCE(e.name,'Confirmed preparation'),COALESCE(e.extraction_state,''),e.superseded_by IS NOT NULL,i.job_id IS NOT NULL,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',q.id::text,'consolidated',q.coverage_scope IS NOT NULL,'draft_id',q.draft_id::text,'body',d.body,'recipient',q.recipient,'approved_by',q.approved_by,'approved_at',q.approved_at,'delivered_at',q.delivered_at,'delivery_reference',q.delivery_reference,
 'email',(SELECT jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'provider_id',m.provider_id,'error',m.error,'approved_by',m.approved_by,'created_at',m.created_at) FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),
 'emails',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'approved_by',m.approved_by,'created_at',m.created_at,'error',m.error) ORDER BY m.created_at) FROM email_delivery m WHERE m.request_id=q.id),'[]'::jsonb),
 'reminder',` + reminderStatus + `,
 'answers',COALESCE((SELECT jsonb_agg(jsonb_build_object('position',a.position,'state',a.state) ORDER BY a.position) FROM work_request_answers a WHERE a.request_id=q.id),'[]'::jsonb),
 'partial',COALESCE((SELECT NOT s.complete FROM request_submissions s WHERE s.request_id=q.id ORDER BY s.id DESC LIMIT 1),false),
 'link', (SELECT jsonb_build_object('expires_at',l.expires_at,'revoked_at',l.revoked_at,'used_at',l.used_at) FROM client_request_links l WHERE l.request_id=q.id ORDER BY l.issued_at DESC LIMIT 1)) ORDER BY q.id DESC) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id IN (SELECT request_id FROM request_jobs WHERE job_id=j.id)),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'request_id',r.request_id::text,'source_id',r.source_id::text,'received_from',r.received_from,'recorded_by',r.recorded_by,'body',s.content) ORDER BY r.id) FROM work_replies r JOIN work_requests q ON q.id=r.request_id JOIN evidence s ON s.id=r.source_id WHERE q.id IN (SELECT request_id FROM request_jobs WHERE job_id=j.id)),'[]'::jsonb)
,
 COALESCE((SELECT jsonb_agg(problem) FROM (
  SELECT jsonb_array_elements_text(COALESCE(w.paper->'blockers','[]'::jsonb)) AS problem FROM workpaper_review_state w WHERE w.close_id=j.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text)
  UNION ALL SELECT 'Draft #'||w.id||': '||replace(w.review_state,'_',' ') FROM workpaper_review_state w WHERE w.close_id=j.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text) AND w.review_state IN ('source_changed','changes_requested')
 UNION ALL SELECT 'Email delivery: '||m.state||' / '||m.delivery||'. '||m.error FROM email_delivery m JOIN work_requests q ON q.id=m.request_id WHERE EXISTS(SELECT 1 FROM request_jobs rj WHERE rj.request_id=q.id AND rj.job_id=j.id) AND (m.delivery IN ('bounced','complained','failed','suppressed') OR m.state IN ('failed','unknown') OR (m.state='queued' AND m.error<>''))
 ) issues),'[]'::jsonb),COALESCE(c.due_on::text,''),c.priority,c.owner<>'' AND c.owner=$5` + workFrom

const priorityOrder = `CASE c.priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END`
const workFrom = ` FROM preparation_jobs j LEFT JOIN preparation_intakes i ON i.job_id=j.id JOIN client_closes c ON c.id=j.close_id LEFT JOIN evidence e ON e.id=i.source_id`
const workScope = `c.organization_id=$3 AND ($4 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$5 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) AND ($1='' OR j.close_id::text=$1) AND ($2='' OR c.entity ILIKE '%'||$2||'%' OR c.period=$2)`

var workViews = []string{"active", "mine", "needs_confirmation", "blocked", "running", "waiting_client", "completed", "all"}

func registerWork(mux *http.ServeMux, db *sql.DB) {
	// Drafts an accountant can approve now. The review view already marks revised predecessors as changed.
	mux.HandleFunc("GET /v1/closes/review-queue", func(w http.ResponseWriter, r *http.Request) {
		search := strings.TrimSpace(r.URL.Query().Get("client"))
		if len(search) > 200 {
			problem(w, 422, "Client search is too long.")
			return
		}
		where := ` FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) AND ($4='' OR c.entity ILIKE '%'||$4||'%' OR c.period=$4) AND w.review_state='pending' AND jsonb_array_length(COALESCE(w.paper->'sources','[]'))>0 AND jsonb_array_length(COALESCE(w.paper->'blockers','[]'))=0`
		var count int
		if err := db.QueryRowContext(r.Context(), `SELECT count(*)`+where, organization(r), firmAccess(r), subject(r), search).Scan(&count); err != nil {
			problem(w, 503, "Review queue unavailable.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT w.id::text,c.id::text,c.entity,c.period,w.kind,w.created_at,w.digest,w.review_id,`+bulkReason+`,COALESCE(c.due_on::text,''),c.priority,c.owner<>'' AND c.owner=$3,COALESCE((SELECT a.note FROM approval_requests a WHERE a.workpaper_id=w.id ORDER BY a.id DESC LIMIT 1),''),COALESCE((SELECT a.requested_by FROM approval_requests a WHERE a.workpaper_id=w.id ORDER BY a.id DESC LIMIT 1),''),COALESCE((SELECT a.agent FROM approval_requests a WHERE a.workpaper_id=w.id ORDER BY a.id DESC LIMIT 1),'')`+where+` ORDER BY c.due_on NULLS LAST,`+priorityOrder+`,w.id DESC LIMIT 100`, organization(r), firmAccess(r), subject(r), search)
		if err != nil {
			problem(w, 503, "Review queue unavailable.")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, closeID, entity, period, kind, digest, review, reason, dueOn, priority, approval, requester, agent string
			var created time.Time
			var mine bool
			if err := rows.Scan(&id, &closeID, &entity, &period, &kind, &created, &digest, &review, &reason, &dueOn, &priority, &mine, &approval, &requester, &agent); err != nil {
				problem(w, 503, "Review queue unavailable.")
				return
			}
			items = append(items, map[string]any{"workpaper_id": id, "close_id": closeID, "entity": entity, "period": period, "kind": kind, "created_at": created, "digest": digest, "review_id": review, "bulk_reason": reason, "due_on": dueOn, "priority": priority, "owned_by_me": mine, "approval_note": approval, "approval_requested_by": requester, "approval_agent": agent})
		}
		if rows.Err() != nil {
			problem(w, 503, "Review queue unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"items": items, "count": count})
	})
	mux.HandleFunc("GET /v1/closes/dependencies", func(w http.ResponseWriter, r *http.Request) {
		search := strings.TrimSpace(r.URL.Query().Get("client"))
		if len(search) > 200 {
			problem(w, 422, "Client search is too long.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT w.id::text,c.id::text,c.entity,c.period,w.kind,COALESCE(w.paper->'carry_forward'->>'paper_id',w.paper->'recurring_accrual'->>'paper_id','') FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) AND ($4='' OR c.entity ILIKE '%'||$4||'%' OR c.period=$4) AND w.review_state='source_changed' AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text) ORDER BY w.id DESC LIMIT 101`, organization(r), firmAccess(r), subject(r), search)
		if err != nil {
			problem(w, 503, "Dependency review unavailable.")
			return
		}
		defer rows.Close()
		items := []map[string]string{}
		for rows.Next() {
			var id, closeID, entity, period, kind, upstream string
			if err := rows.Scan(&id, &closeID, &entity, &period, &kind, &upstream); err != nil {
				problem(w, 503, "Dependency review unavailable.")
				return
			}
			items = append(items, map[string]string{"workpaper_id": id, "close_id": closeID, "entity": entity, "period": period, "kind": kind, "upstream_workpaper_id": upstream})
		}
		if rows.Err() != nil {
			problem(w, 503, "Dependency review unavailable.")
			return
		}
		more := len(items) > 100
		if more {
			items = items[:100]
		}
		predicted, predictedMore, err := predictedReviews(r, db, search)
		if err != nil {
			problem(w, 503, "Dependency review unavailable.")
			return
		}
		ledger, ledgerMore, err := ledgerDiffers(r, db, search)
		if err != nil {
			problem(w, 503, "Dependency review unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"items": items, "has_more": more, "predicted": predicted, "predicted_more": predictedMore, "no_longer_ties": ledger, "no_longer_ties_more": ledgerMore})
	})
	read := func(w http.ResponseWriter, r *http.Request) {
		view := r.URL.Query().Get("view")
		if view == "" {
			view = "all"
		}
		issues, predicates := workPredicates()
		predicate, ok := predicates[view]
		if !ok {
			problem(w, 422, "Unknown inbox view.")
			return
		}
		search := strings.TrimSpace(r.URL.Query().Get("client"))
		if len(search) > 200 {
			problem(w, 422, "Client search is too long.")
			return
		}
		// jobs narrows the list to named jobs, such as those an assistant card started.
		jobs := []string{}
		if raw := r.URL.Query().Get("jobs"); raw != "" {
			if jobs = strings.Split(raw, ","); len(jobs) > 100 {
				problem(w, 422, "Name at most 100 jobs.")
				return
			}
		}
		rows, err := db.QueryContext(r.Context(), workSelect+` WHERE `+workScope+` AND (`+predicate+`) AND (cardinality($6::text[])=0 OR j.id::text=ANY($6)) ORDER BY CASE WHEN j.state IN ('needs_confirmation','failed') THEN 0 WHEN (`+predicates["blocked"]+`) OR `+issues+` THEN 1 WHEN j.state='waiting_client' THEN 2 WHEN j.state IN ('waiting_evidence','interpreting','queued') THEN 3 ELSE 4 END,(c.owner<>'' AND c.owner=$5) DESC,c.due_on NULLS LAST,`+priorityOrder+`,j.id DESC LIMIT 101`, r.PathValue("id"), search, organization(r), firmAccess(r), subject(r), jobs)
		if err != nil {
			problem(w, 503, "Work inbox unavailable.")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, closeID, entity, period, state, message, sourceID, name, extraction, started string
			var revision, attempts int
			var stale, coordinated bool
			var papers, checkpoint, proposal, requests, replies, blockers json.RawMessage
			var dueOn, priority string
			var mine bool
			if err = rows.Scan(&id, &closeID, &entity, &period, &state, &message, &papers, &started, &revision, &attempts, &checkpoint, &proposal, &sourceID, &name, &extraction, &stale, &coordinated, &requests, &replies, &blockers, &dueOn, &priority, &mine); err != nil {
				problem(w, 503, "Could not read work.")
				return
			}
			items = append(items, map[string]any{"due_on": dueOn, "priority": priority, "owned_by_me": mine, "id": id, "close_id": closeID, "entity": entity, "period": period, "state": state, "error": message, "workpaper_ids": papers, "revision": revision, "agent_attempts": attempts, "agent_checkpoint": checkpoint, "proposal": proposal, "source_id": sourceID, "source_name": name, "extraction_state": extraction, "stale": stale, "coordinated": coordinated, "requests": requests, "replies": replies, "blockers": blockers, "agent_started_at": started})
		}
		if rows.Err() != nil {
			problem(w, 503, "Could not finish work snapshot.")
			return
		}
		more := len(items) > 100
		if more {
			items = items[:100]
		}
		filters := make([]string, len(workViews))
		totals := make([]any, len(workViews))
		for i, name := range workViews {
			filters[i] = "count(*) FILTER (WHERE " + predicates[name] + ")"
			totals[i] = new(int)
		}
		if err = db.QueryRowContext(r.Context(), `SELECT `+strings.Join(filters, ",")+workFrom+` WHERE `+workScope, r.PathValue("id"), search, organization(r), firmAccess(r), subject(r)).Scan(totals...); err != nil {
			problem(w, 503, "Could not count work.")
			return
		}
		counts := map[string]int{}
		for i, name := range workViews {
			counts[name] = *totals[i].(*int)
		}
		jsonReply(w, 200, map[string]any{"items": items, "has_more": more, "counts": counts})
	}
	mux.HandleFunc("GET /v1/closes/work", read)
	mux.HandleFunc("GET /v1/closes/{id}/work", read)
	mux.HandleFunc("POST /v1/closes/work/interpret", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text   string   `json:"text"`
			Labels []string `json:"labels"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if len(in.Text) == 0 || len(in.Text) > 300 || len(in.Labels) > 100 {
			problem(w, 422, "Ask in up to 300 characters about at most 100 visible closes.")
			return
		}
		model := llm.Model(r.Context(), "work_command")
		if model == "" {
			problem(w, 503, "Plain-language requests need the agent model. Use one of the listed commands.")
			return
		}
		if !modelBudget(w, r, db, 1) {
			return
		}
		action, target, err := interpretWork(r.Context(), in.Text, in.Labels)
		if err != nil {
			problem(w, 502, err.Error())
			return
		}
		command, ok := workCommand(action, target, in.Labels)
		if !ok {
			problem(w, 422, "That request is not something Work can find, explain or preview yet. Try ‘show blockers’ or ‘why is Client · YYYY-MM waiting?’.")
			return
		}
		jsonReply(w, 200, map[string]string{"command": command})
	})
	mux.HandleFunc("POST /v1/closes/{id}/work/{job}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Revision           int             `json:"revision"`
			Input              json.RawMessage `json:"input"`
			FactsConfirmed     bool            `json:"facts_confirmed"`
			InventoryConfirmed bool            `json:"inventory_confirmed"`
			Recipient          string          `json:"recipient"`
			Body               string          `json:"body"`
			DraftID            string          `json:"draft_id"`
			RequestID          string          `json:"request_id"`
			DeliveryReference  string          `json:"delivery_reference"`
			ReceivedFrom       string          `json:"received_from"`
			Alias              string          `json:"alias"`
			ClientConfirmed    bool            `json:"client_confirmed"`
			RosterConfirmed    bool            `json:"roster_confirmed"`
			Lines              []struct {
				Line     int    `json:"line"`
				Decision string `json:"decision"`
				Method   string `json:"method"`
				Reason   string `json:"reason"`
			} `json:"lines"`
		}
		if !decode(w, r, &in) {
			return
		}
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Work storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, entity, period string
		var opening bool
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity,period,takeover FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID, &entity, &period, &opening); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var jobID, state, sourceID string
		var revision int
		var raw []byte
		err = tx.QueryRowContext(r.Context(), `SELECT j.id::text,j.state,i.source_id::text,i.revision,i.proposal FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1 AND j.close_id=$2 FOR UPDATE OF j,i`, r.PathValue("job"), closeID).Scan(&jobID, &state, &sourceID, &revision, &raw)
		if err != nil {
			problem(w, 404, "Work not found in this close.")
			return
		}
		if in.Revision != revision {
			problem(w, 409, "Work changed. Refresh and review the latest facts and replies.")
			return
		}
		action := r.PathValue("action")
		if action != "revoke_link" && (state == "completed" || state == "queued" || (state == "cancelled" && action != "retry")) {
			problem(w, 409, "This work is already preparing or finished. Refresh its status.")
			return
		}
		var replaced, ready bool
		if err = tx.QueryRowContext(r.Context(), `SELECT superseded_by IS NOT NULL,extraction_state='ready' FROM evidence WHERE id=$1 AND close_id=$2`, sourceID, closeID).Scan(&replaced, &ready); err != nil {
			problem(w, 503, "Could not verify the source.")
			return
		}
		if action != "cancel" && action != "revoke_link" && (replaced || !ready) {
			code := "CONFLICT"
			if replaced {
				code = "SOURCE_STALE"
			}
			problemCode(w, 409, code, "The source must be current and fully extracted. Retry extraction or use its replacement.")
			return
		}
		var fresh preparation.Proposal
		var proposed preparation.Proposal
		var sources []preparation.IntakeSource
		if action == "confirm" || action == "request" || action == "import_schedule" {
			if json.Unmarshal(raw, &proposed) != nil {
				problem(w, 503, "Stored proposals unavailable.")
				return
			}
			var e error
			sources, e = preparation.CurrentProposalSources(r.Context(), tx, closeID, jobID, proposed)
			if e != nil {
				problem(w, 409, e.Error())
				return
			}
			for _, source := range sources {
				fresh.Sources = append(fresh.Sources, source.Source)
			}
			oldSources, _ := json.Marshal(proposed.Sources)
			newSources, _ := json.Marshal(fresh.Sources)
			if string(oldSources) != string(newSources) {
				problemCode(w, 409, "SOURCE_STALE", "Source evidence changed. Refresh proposals before confirming.")
				return
			}
		}
		linkToken, passcode := "", ""
		switch action {
		case "confirm":
			if state != "needs_confirmation" || !in.FactsConfirmed || (len(proposed.Unselected) > 0 && !in.InventoryConfirmed) {
				problem(w, 409, "Review the proposed facts, client replies, entity and any uncited evidence before confirming.")
				return
			}
			if proposed.Engine == "openai" && len(proposed.Questions) > 0 {
				problem(w, 409, "Answer the open questions and let the agent recheck the evidence before preparation.")
				return
			}
			if len(proposed.Lines) > 0 {
				problem(w, 409, "Decide every row of this prior schedule with Import schedule.")
				return
			}
			kind := proposed.Kind
			if kind == "" {
				kind = "prepaid"
			}
			var input []byte
			switch kind {
			case "prepaid":
				var value prepaids.Input
				if e := preparation.Strict(in.Input, &value); e != nil {
					problem(w, 422, e.Error())
					return
				}
				value.Entity = entity
				value.ClosePeriod = period
				value.SourceReference = "Evidence #" + sourceID + " · work #" + jobID
				input, _ = json.Marshal(value)
			case "accrual":
				var value accruals.Input
				if e := preparation.Strict(in.Input, &value); e != nil {
					problem(w, 422, e.Error())
					return
				}
				value.Entity = entity
				value.ClosePeriod = period
				value.SourceReference = "Evidence #" + sourceID + " · work #" + jobID
				input, _ = json.Marshal(value)
			case "reconciliation":
				var value reconciliation.Input
				if e := preparation.Strict(in.Input, &value); e != nil {
					problem(w, 422, e.Error())
					return
				}
				if len(value.PriorOutstanding) > 0 || len(value.Transfers) > 0 || value.AccountType == "credit_card" || slices.ContainsFunc(value.CurrentOutstanding, func(item reconciliation.CurrentItem) bool { return item.Treatment == "clearing" }) {
					problem(w, 422, "Card rules, timing items and transfers need separately linked support. Open the source-linked preparation review.")
					return
				}
				value.Entity = entity
				value.ClosePeriod = period
				for _, s := range sources {
					if s.Role == "statement" {
						value.StatementCSV = s.Content
						value.StatementReference = s.Name + " · " + s.Location
					}
					if s.Role == "ledger" {
						value.LedgerCSV = s.Content
						value.LedgerReference = s.Name + " · " + s.Location
					}
				}
				if value.StatementCSV == "" || value.LedgerCSV == "" {
					problem(w, 422, "Separate statement and ledger evidence is required.")
					return
				}
				input, _ = json.Marshal(value)
			case "settlement":
				problem(w, 422, "Choose the bank CSV on the Stripe settlement page to continue this job.")
				return
			default:
				problem(w, 422, "This work is unsupported or ambiguous. Answer the questions and recheck evidence first.")
				return
			}
			p := preparation.Plan{Kind: kind, Input: input, Sources: fresh.Sources}
			if _, e := preparation.Calculate(p, entity, period); e != nil {
				problem(w, 422, e.Error())
				return
			}
			plans, _ := json.Marshal([]preparation.Plan{p})
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET plans=$2,state='queued',error='',finished_at=NULL WHERE id=$1`, jobID, string(plans))
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET confirmed_by=$2,confirmed_at=now() WHERE job_id=$1`, jobID, actor)
			}
		case "import_schedule":
			// A prior accountant's schedule is decided whole on the client's opening month: each row imported as a schedule
			// with a method, or set aside with a reason. A corrected schedule revises the current drafts of rows imported from
			// an earlier version, named by the first version's evidence and the row's label.
			if state != "needs_confirmation" || len(proposed.Lines) == 0 {
				problem(w, 409, "This work is not a prior schedule waiting for a decision.")
				return
			}
			if !opening {
				problem(w, 409, "This close is no longer the client's opening month. Read the prior schedule on the opening month.")
				return
			}
			if !in.ClientConfirmed || !in.RosterConfirmed || (len(proposed.Unselected) > 0 && !in.InventoryConfirmed) {
				problem(w, 422, "Confirm the schedule is this client's and that the listed rows are all of its rows.")
				return
			}
			if len(proposed.Questions) > 0 {
				problem(w, 409, "Answer the schedule's open questions and let the agent read it again before importing.")
				return
			}
			decided := map[int]bool{}
			for _, d := range in.Lines {
				if d.Line < 1 || d.Line > len(proposed.Lines) || decided[d.Line] {
					problem(w, 422, "Decide each row once.")
					return
				}
				decided[d.Line] = true
			}
			if len(decided) != len(proposed.Lines) {
				problem(w, 422, "Decide every row of the schedule: import it with a method, or set it aside with a reason.")
				return
			}
			// A schedule that replaces more than one earlier document, or whose earlier versions' rows were imported under
			// another first version, is refused rather than guessed.
			var root string
			var tangled bool
			if err = tx.QueryRowContext(r.Context(), `WITH RECURSIVE chain(id) AS (SELECT $1::bigint UNION SELECT e.id FROM evidence e JOIN chain c ON e.superseded_by=c.id WHERE e.close_id=$2)
 SELECT (SELECT min(id)::text FROM chain),
  EXISTS(SELECT 1 FROM evidence e WHERE e.close_id=$2 AND e.superseded_by IN (SELECT id FROM chain) GROUP BY e.superseded_by HAVING count(*)>1)
  OR EXISTS(SELECT 1 FROM workpapers w WHERE w.close_id=$2 AND w.paper->'imported'->>'root_source_id' IN (SELECT id::text FROM chain) AND w.paper->'imported'->>'root_source_id'<>(SELECT min(id)::text FROM chain))`, sourceID, closeID).Scan(&root, &tangled); err != nil {
				problem(w, 503, "Could not read the schedule's earlier versions.")
				return
			}
			if tangled {
				problem(w, 409, "This schedule replaces more than one earlier document, or its earlier versions were imported separately. Correct one schedule at a time.")
				return
			}
			current := map[string]string{}
			rows, e := tx.QueryContext(r.Context(), `SELECT lower(w.paper->'imported'->>'label'),w.id::text FROM workpapers w WHERE w.close_id=$1 AND w.paper->'imported'->>'root_source_id'=$2 AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text)`, closeID, root)
			if e != nil {
				problem(w, 503, "Could not read the schedule's drafts.")
				return
			}
			for rows.Next() {
				var label, id string
				if e = rows.Scan(&label, &id); e != nil {
					break
				}
				current[label] = id
			}
			rows.Close()
			if e != nil || rows.Err() != nil {
				problem(w, 503, "Could not read the schedule's drafts.")
				return
			}
			schedule := ""
			if proposed.Kind == "deferred_revenue" {
				schedule = prepaids.DeferredRevenue
			}
			plans, listed := []preparation.Plan{}, map[string]bool{}
			for _, d := range in.Lines {
				line := &proposed.Lines[d.Line-1]
				value := map[string]string{}
				for _, f := range line.Facts {
					value[f.Field] = f.Value
				}
				label := value["label"]
				listed[strings.ToLower(label)] = true
				switch d.Decision {
				case "set_aside":
					if n := utf8.RuneCountInString(strings.TrimSpace(d.Reason)); n < 5 || n > 2000 {
						problem(w, 422, fmt.Sprintf("Say why row %d is set aside (5–2,000 characters).", d.Line))
						return
					}
					if current[strings.ToLower(label)] != "" {
						problem(w, 409, fmt.Sprintf("Row %d (%s) was imported from an earlier version of this schedule; import its corrected row.", d.Line, label))
						return
					}
					line.SetAside = strings.TrimSpace(d.Reason)
				case "import":
					for _, f := range line.Facts {
						if f.Question != "" || f.Value == "" {
							problem(w, 409, fmt.Sprintf("Row %d: %s", d.Line, cmp.Or(f.Question, "Confirm the "+f.Label+" of this row.")))
							return
						}
					}
					if d.Method != prepaids.Method && d.Method != prepaids.DailyMethod {
						problem(w, 422, fmt.Sprintf("Choose equal full months or actual days for row %d.", d.Line))
						return
					}
					reference := "Prior schedule #" + root + " · " + label
					if len(reference) > 200 {
						problem(w, 422, fmt.Sprintf("Row %d's label is too long to track; shorten it in the schedule.", d.Line))
						return
					}
					input, _ := json.Marshal(prepaids.Input{Basis: "accrual", Entity: entity, ClosePeriod: period, SourceReference: reference, Amount: value["amount"], ServiceStart: value["service_start"], ServiceEnd: value["service_end"],
						Currency: value["currency"], ExpenseAccount: value["expense_account"], PrepaidAccount: value["prepaid_account"], Method: d.Method, Schedule: schedule})
					p := preparation.Plan{Kind: "prepaid", Input: input, Sources: fresh.Sources, Revises: current[strings.ToLower(label)], Imported: &preparation.Imported{RootSourceID: root, Label: label, Remaining: value["remaining"]}}
					if _, e := preparation.Calculate(p, entity, period); e != nil {
						problem(w, 422, fmt.Sprintf("Row %d: %s", d.Line, e.Error()))
						return
					}
					plans = append(plans, p)
				default:
					problem(w, 422, fmt.Sprintf("Import row %d or set it aside.", d.Line))
					return
				}
			}
			for label := range current {
				if !listed[label] {
					problem(w, 409, "The corrected schedule must list every row imported from its earlier version, including "+label+".")
					return
				}
			}
			decision, _ := json.Marshal(proposed)
			saved, _ := json.Marshal(plans)
			next := "queued"
			if len(plans) == 0 {
				next = "completed"
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET plans=$2,state=$3,error='',finished_at=CASE WHEN $3='completed' THEN now() END WHERE id=$1`, jobID, string(saved), next)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET proposal=$2,confirmed_by=$3,confirmed_at=now() WHERE job_id=$1`, jobID, string(decision), actor)
			}
		case "request":
			if state != "needs_confirmation" {
				problem(w, 409, "Review the latest proposals before approving a request.")
				return
			}
			recipient, e := mail.ParseAddress(strings.TrimSpace(in.Recipient))
			if e != nil || recipient.Address != strings.TrimSpace(in.Recipient) || len(in.Recipient) > 254 {
				problem(w, 422, "Enter the exact recipient email address for this client.")
				return
			}
			body := strings.TrimSpace(in.Body)
			if len(body) == 0 || len(body) > 20000 {
				problem(w, 422, "Write a specific request of at most 20,000 bytes.")
				return
			}
			var draftID string
			if in.DraftID != "" {
				var savedBody, digest string
				var idsRaw []byte
				e = tx.QueryRowContext(r.Context(), `SELECT id::text,body,context_digest,workpaper_ids FROM request_drafts WHERE id::text=$1 AND close_id=$2`, in.DraftID, closeID).Scan(&draftID, &savedBody, &digest, &idsRaw)
				var ids []string
				if e != nil || json.Unmarshal(idsRaw, &ids) != nil {
					problem(w, 422, "Request draft not found in this close.")
					return
				}
				fresh, _, e := preparation.RequestContext(r.Context(), tx, closeID, ids)
				if e != nil || fresh != digest || body != strings.TrimSpace(savedBody) {
					problem(w, 409, "The draft or its evidence context changed. Regenerate and review before approval.")
					return
				}
			} else {
				digest, _, e := preparation.RequestContext(r.Context(), tx, closeID, nil)
				if e != nil {
					problem(w, 503, "Could not inspect request context.")
					return
				}
				err = tx.QueryRowContext(r.Context(), `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,$2,'[]',$3,$4) RETURNING id::text`, closeID, body, digest, actor).Scan(&draftID)
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by,approved_papers) VALUES($1,$2,$3,$4,$5,(SELECT `+preparation.RequestPapers+` FROM request_drafts d WHERE d.id=$2))`, jobID, draftID, revision, recipient.Address, actor)
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, jobID)
			}
		case "delivered":
			reference := strings.TrimSpace(in.DeliveryReference)
			if state != "waiting_client" || len(reference) == 0 || len(reference) > 500 {
				problem(w, 422, "Record the secure-channel delivery reference (1–500 bytes).")
				return
			}
			result, e := tx.ExecContext(r.Context(), `UPDATE work_requests SET delivered_by=$3,delivery_reference=$4,delivered_at=now() WHERE id::text=$1 AND job_id=$2 AND delivered_at IS NULL AND id=(SELECT max(id) FROM work_requests WHERE job_id=$2)`, in.RequestID, jobID, actor, reference)
			if e != nil {
				err = e
				break
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Request unavailable or delivery already recorded.")
				return
			}
		case "reply", "answer":
			if (action == "reply" && state != "waiting_client") || (action == "answer" && state != "needs_confirmation") {
				problem(w, 409, "This work is not waiting for a reply.")
				return
			}
			var requestID string
			if action == "reply" {
				if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM work_requests WHERE id::text=$1 AND job_id=$2 AND delivered_at IS NOT NULL AND id=(SELECT max(id) FROM work_requests WHERE job_id=$2)`, in.RequestID, jobID).Scan(&requestID); err != nil {
					problem(w, 422, "Record delivery of the approved request before attaching its reply.")
					return
				}
			}
			body, from := in.Body, strings.TrimSpace(in.ReceivedFrom)
			if action == "answer" {
				from = actor
			}

			status, e := saveWorkReply(r.Context(), tx, closeID, jobID, requestID, body, actor, from)
			if e != nil {
				problem(w, status, e.Error())
				return
			}
		case "link", "revoke_link":
			// A link that already has a reply still takes additions, so it can be withdrawn after the work moves on.
			if action == "link" && state != "waiting_client" {
				problem(w, 409, "This work is not waiting for a client.")
				return
			}
			var requestID string
			if e := tx.QueryRowContext(r.Context(), `SELECT id::text FROM work_requests WHERE id::text=$1 AND job_id=$2 AND id=(SELECT max(id) FROM work_requests WHERE job_id=$2)`, in.RequestID, jobID).Scan(&requestID); e != nil {
				problem(w, 404, "Approved request not found.")
				return
			}
			if action == "link" {
				if e := requestLinkable(r.Context(), tx, closeID, requestID); e != nil {
					problem(w, 409, e.Error())
					return
				}
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id=$1 AND revoked_at IS NULL`, requestID)
			if err == nil && action == "link" {
				linkToken = rand.Text()
				passcode, err = issueLink(r.Context(), tx, requestID, actor, linkToken)
			}

		case "retry", "alias":
			if state != "failed" && state != "needs_confirmation" && state != "waiting_client" && state != "cancelled" {
				problem(w, 409, "Only stopped or unresolved work can be rechecked.")
				return
			}
			reason := "Work inbox recheck"
			if action == "alias" {
				// Only the one other name the blocked source gives can become the client's alias.
				if json.Unmarshal(raw, &proposed) != nil || proposed.Engine != "identity_blocked" || documents.NormalizeEntity(proposed.EntityName) == "" || documents.NormalizeEntity(in.Alias) != documents.NormalizeEntity(proposed.EntityName) {
					problem(w, 409, "Confirm the one other client name this source gives.")
					return
				}
				if _, err = tx.ExecContext(r.Context(), `INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) SELECT organization_id,entity,$2,$3 FROM client_closes WHERE id=$1 ON CONFLICT DO NOTHING`, closeID, documents.NormalizeEntity(proposed.EntityName), actor); err != nil {
					break
				}
				reason = "Client alias confirmed: " + proposed.EntityName
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO recovery_actions(organization_id,close_id,job_id,action,prior_state,actor,reason) VALUES($1,$2,$3,'retry_preparation',$4,$5,$6)`, organization(r), closeID, jobID, state, actor, reason)
			if err != nil {
				break
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id IN (SELECT id FROM work_requests WHERE job_id=$1) AND revoked_at IS NULL`, jobID)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='waiting_evidence',error='',finished_at=NULL WHERE id=$1`, jobID)
			}
		case "resume":
			// A partial client reply waits for the firm; resuming prepares this work from what the client has sent so far.
			var partial bool
			if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT NOT complete FROM request_submissions WHERE request_id=(SELECT max(request_id) FROM request_jobs WHERE job_id=$1) ORDER BY id DESC LIMIT 1),false)`, jobID).Scan(&partial); err != nil {
				break
			}
			if state != "waiting_client" || !partial {
				problem(w, 409, "Only work waiting on a partial client reply to its current request can resume.")
				return
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='waiting_evidence',error='' WHERE id=$1`, jobID)
		case "cancel":
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='cancelled',finished_at=now() WHERE id=$1`, jobID)
		default:
			problem(w, 404, "Unknown work action.")
			return
		}
		if err == nil && action != "link" && action != "revoke_link" {
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL,agent_attempts=CASE WHEN $2 IN ('retry','alias','reply','answer','resume') THEN 0 ELSE agent_attempts END WHERE job_id=$1`, jobID, action)
		}
		if err != nil {
			jobProblem(w, err, "Work update could not be confirmed. Refresh before retrying.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Work update could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": jobID, "link_token": linkToken, "passcode": passcode})
	})
}

var askViews = map[string]string{"show_decisions": "show decisions", "show_blockers": "show blockers", "show_running": "show running work", "show_requests": "show client requests", "show_prepared": "show prepared work", "show_all": "show all work", "show_dependencies": "show dependencies"}

func workCommand(action, target string, labels []string) (string, bool) {
	if view, ok := askViews[action]; ok {
		return view, target == ""
	}
	visible := slices.Contains(labels, target)
	switch action {
	case "prepare", "recheck", "stop":
		return action + " " + target, visible || target == "all visible"
	case "rehearse":
		return "rehearse " + target, visible
	case "explain":
		return "why is " + target + " waiting?", visible
	}
	return "", false
}

func interpretWork(ctx context.Context, text string, labels []string) (string, string, error) {
	actions := []string{"unsupported", "prepare", "recheck", "stop", "rehearse", "explain"}
	for action := range askViews {
		actions = append(actions, action)
	}
	slices.Sort(actions)
	targets := append([]string{"", "all visible"}, labels...)
	schema, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "target"}, "properties": map[string]any{
		"action": map[string]any{"type": "string", "enum": actions}, "target": map[string]any{"type": "string", "enum": targets}}})
	input, _ := json.Marshal(map[string]any{"request": text, "visible_closes": labels})
	request := llm.Request{Purpose: "work_command", MaxResponseBytes: 32768, MaxOutputTokens: 200, Instructions: "Map an accountant's request about their close work to one command. Actions: show_* filter the inbox (target empty); prepare, recheck or stop coordinate a close's source jobs (target one visible close, or \"all visible\"); rehearse opens a close's production journal rehearsal and explain says why a close is waiting (target one visible close). Choose a target only when the request clearly names one visible close; otherwise use unsupported. Never invent a close.", Data: json.RawMessage(input), Schema: &llm.OutputSchema{Name: "work_command", Definition: json.RawMessage(schema)}}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	response, err := llm.Generate(callCtx, request)
	if err != nil {
		return "", "", err
	}
	var answer struct {
		Action string `json:"action"`
		Target string `json:"target"`
	}
	if preparation.Strict([]byte(response.Text), &answer) != nil {
		return "", "", errors.New("The agent model's answer was incomplete; use one of the listed commands")
	}
	return answer.Action, answer.Target, nil
}

var predictedAccounts = map[string][]string{"prepaid": {"prepaid_account"}, "accrual": {"liability_account"}, "payroll": {"clearing_account", "liability_account"}, "loan": {"loan_account", "accrued_interest_account", "current_portion_account"}, "owner": {"owner_account"}, "settlement": {"bank_account"}, "reconciliation": {"account"}}

func predictedReviews(r *http.Request, db *sql.DB, search string) ([]map[string]any, bool, error) {
	rows, err := db.QueryContext(r.Context(), `SELECT w.id::text,c.id::text,c.entity,c.period,w.kind,w.paper->'input',v.created_at,t.kind,t.provider_id,t.provider_updated_at,t.record
 FROM workpaper_review_state w JOIN client_closes c ON c.id=w.close_id JOIN workpaper_reviews v ON v.id::text=w.review_id
 JOIN quickbooks_entity_syncs s ON s.close_id=c.id AND s.state='completed' AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=c.id AND z.state='completed' AND z.connection_id=`+quickbooks.ReadingConnection("c.id")+`)
 JOIN evidence e ON e.id=s.evidence_id AND e.close_id=c.id AND e.superseded_by IS NULL
 JOIN quickbooks_transaction_rows t ON t.sync_id=s.id AND t.provider_updated_at>v.created_at
 WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) AND ($4='' OR c.entity ILIKE '%'||$4||'%' OR c.period=$4)
 AND w.review_state='approved' AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
 ORDER BY w.id DESC,t.provider_updated_at DESC`, organization(r), firmAccess(r), subject(r), search)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items, byPaper := []map[string]any{}, map[string]map[string]any{}
	for rows.Next() {
		var id, closeID, entity, period, kind, txnKind, provider string
		var input, record json.RawMessage
		var approved, updated time.Time
		if err = rows.Scan(&id, &closeID, &entity, &period, &kind, &input, &approved, &txnKind, &provider, &updated, &record); err != nil {
			return nil, false, err
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(input, &fields)
		supported := map[string]bool{}
		for _, key := range predictedAccounts[kind] {
			var name string
			if json.Unmarshal(fields[key], &name) == nil && strings.TrimSpace(name) != "" {
				supported[strings.ToLower(strings.TrimSpace(name))] = true
			}
		}
		for _, account := range quickbooks.RecordAccounts(record) {
			if !supported[strings.ToLower(strings.TrimSpace(account))] {
				continue
			}
			if item := byPaper[id]; item != nil {
				item["count"] = item["count"].(int) + 1
			} else if len(items) < 101 {
				byPaper[id] = map[string]any{"workpaper_id": id, "close_id": closeID, "entity": entity, "period": period, "kind": kind, "transaction": txnKind + " " + provider, "account": account, "updated_at": updated, "approved_at": approved, "count": 1}
				items = append(items, byPaper[id])
			}
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	return items[:min(len(items), 100)], len(items) > 100, nil
}

// ledgerDiffers lists each approved draft in an open close whose account no longer ties in a Trial Balance captured after
// the approval, with both balances: the tie-out's own "differs", across up to 10 closes, newest Trial Balance first. A
// close whose tie-out cannot be computed is left out; its own tie-out says why.
func ledgerDiffers(r *http.Request, db *sql.DB, search string) ([]map[string]any, bool, error) {
	type candidate struct{ id, entity, period string }
	var closes []candidate
	rows, err := db.QueryContext(r.Context(), `SELECT c.id::text,c.entity,c.period FROM client_closes c
 CROSS JOIN LATERAL (SELECT GREATEST((SELECT max(s.captured_at) FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload ? 'period' AND s.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,s.connection_id)),(SELECT max(captured_at) FROM xero_snapshots WHERE close_id=c.id)) AS captured) l
 WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) AND ($4='' OR c.entity ILIKE '%'||$4||'%' OR c.period=$4)
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
 AND EXISTS(SELECT 1 FROM workpaper_review_state w JOIN workpaper_reviews v ON v.id::text=w.review_id WHERE w.close_id=c.id AND w.review_state='approved' AND v.created_at<l.captured)
 ORDER BY l.captured DESC,c.id DESC LIMIT 11`, organization(r), firmAccess(r), subject(r), search)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.entity, &c.period); err != nil {
			rows.Close()
			return nil, false, err
		}
		closes = append(closes, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	items := []map[string]any{}
	for _, c := range closes[:min(len(closes), 10)] {
		found, err := closeDiffers(r, db, c.id)
		if err != nil {
			return nil, false, err
		}
		for _, item := range found {
			item["close_id"], item["entity"], item["period"] = c.id, c.entity, c.period
			items = append(items, item)
		}
	}
	return items, len(closes) > 10, nil
}

// closeDiffers ties out one close in its own short read and returns its approved drafts on differing rows, approved
// before that Trial Balance was captured, once per draft and account.
func closeDiffers(r *http.Request, db *sql.DB, closeID string) ([]map[string]any, error) {
	tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	tie, err := closeTieOut(r.Context(), tx, closeID)
	at, _ := tie.Snapshot["captured_at"].(time.Time)
	if err != nil || at.IsZero() {
		return nil, nil
	}
	type support struct {
		row   tieRow
		paper tieSupport
	}
	var differing []support
	ids := []string{}
	for _, row := range tie.Rows {
		seen := map[string]bool{}
		for _, s := range row.Supports {
			if row.Status == "differs" && s.Review == "approved" && !seen[s.PaperID] {
				seen[s.PaperID] = true
				differing = append(differing, support{row, s})
				ids = append(ids, s.PaperID)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	approved := map[string]time.Time{}
	rows, err := tx.QueryContext(r.Context(), `SELECT w.id::text,v.created_at FROM workpaper_review_state w JOIN workpaper_reviews v ON v.id::text=w.review_id WHERE w.close_id::text=$1 AND w.id::text=ANY($2::text[])`, closeID, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var when time.Time
		if err = rows.Scan(&id, &when); err != nil {
			break
		}
		approved[id] = when
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, d := range differing {
		if when, ok := approved[d.paper.PaperID]; ok && at.After(when) {
			items = append(items, map[string]any{"workpaper_id": d.paper.PaperID, "kind": d.paper.Kind, "account": d.row.Account, "balance": d.row.Balance, "expected": *d.row.Expected, "difference": *d.row.Difference, "captured_at": at, "approved_at": when})
		}
	}
	return items, nil
}

// workPredicates are the inbox views as SQL over a job j, its intake i, evidence e and close c, with the member at $5.
func workPredicates() (issues string, predicates map[string]string) {
	issues = `EXISTS(SELECT 1 FROM workpaper_review_state w WHERE w.close_id=j.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text) AND (w.review_state IN ('source_changed','changes_requested') OR jsonb_array_length(COALESCE(w.paper->'blockers','[]'))>0))`
	predicates = map[string]string{
		"active":             "j.state NOT IN ('completed','cancelled') OR " + issues,
		"mine":               "c.owner<>'' AND c.owner=$5 AND (j.state NOT IN ('completed','cancelled') OR " + issues + ")",
		"needs_confirmation": "j.state='needs_confirmation'",
		"blocked":            "j.state='failed' OR e.superseded_by IS NOT NULL OR i.proposal->>'kind'='unsupported' OR EXISTS(SELECT 1 FROM email_delivery m JOIN work_requests q ON q.id=m.request_id WHERE EXISTS(SELECT 1 FROM request_jobs rj WHERE rj.request_id=q.id AND rj.job_id=j.id) AND (m.delivery IN ('bounced','complained','failed','suppressed') OR m.state IN ('failed','unknown') OR (m.state='queued' AND m.error<>''))) OR " + issues,
		"running":            "j.state IN ('waiting_evidence','interpreting','queued')",
		"waiting_client":     "j.state='waiting_client'",
		"completed":          "j.state='completed'",
		"all":                "true",
	}
	return
}
