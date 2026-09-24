package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
)

// The inbox reads the queue's real states; it never maintains a second status cache.
const workSelect = `SELECT j.id::text,j.close_id::text,c.entity,c.period,j.state,j.error,j.workpaper_ids,
 COALESCE(i.agent_started_at::text,''),COALESCE(i.revision,0),COALESCE(i.agent_attempts,0),COALESCE(i.agent_checkpoint,'{}'::jsonb),COALESCE(i.proposal,'{}'::jsonb),COALESCE(e.id::text,''),COALESCE(e.name,'Confirmed preparation'),COALESCE(e.extraction_state,''),e.superseded_by IS NOT NULL,i.job_id IS NOT NULL,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',q.id::text,'consolidated',q.coverage_scope IS NOT NULL,'draft_id',q.draft_id::text,'body',d.body,'recipient',q.recipient,'approved_by',q.approved_by,'approved_at',q.approved_at,'delivered_at',q.delivered_at,'delivery_reference',q.delivery_reference,
 'email',(SELECT jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'provider_id',m.provider_id,'error',m.error,'approved_by',m.approved_by,'created_at',m.created_at) FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),
 'emails',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'approved_by',m.approved_by,'created_at',m.created_at,'error',m.error) ORDER BY m.created_at) FROM email_delivery m WHERE m.request_id=q.id),'[]'::jsonb),
 'link', (SELECT jsonb_build_object('expires_at',l.expires_at,'revoked_at',l.revoked_at,'used_at',l.used_at) FROM client_request_links l WHERE l.request_id=q.id ORDER BY l.issued_at DESC LIMIT 1)) ORDER BY q.id DESC) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id IN (SELECT request_id FROM request_jobs WHERE job_id=j.id)),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'request_id',r.request_id::text,'source_id',r.source_id::text,'received_from',r.received_from,'recorded_by',r.recorded_by,'body',s.content) ORDER BY r.id) FROM work_replies r JOIN work_requests q ON q.id=r.request_id JOIN evidence s ON s.id=r.source_id WHERE q.id IN (SELECT request_id FROM request_jobs WHERE job_id=j.id)),'[]'::jsonb)
,
 COALESCE((SELECT jsonb_agg(problem) FROM (
  SELECT jsonb_array_elements_text(COALESCE(w.paper->'blockers','[]'::jsonb)) AS problem FROM workpaper_review_state w WHERE w.close_id=j.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text)
  UNION ALL SELECT 'Draft #'||w.id||': '||replace(w.review_state,'_',' ') FROM workpaper_review_state w WHERE w.close_id=j.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text) AND w.review_state IN ('source_changed','changes_requested')
 UNION ALL SELECT 'Email delivery: '||m.state||' / '||m.delivery||'. '||m.error FROM email_delivery m JOIN work_requests q ON q.id=m.request_id WHERE EXISTS(SELECT 1 FROM request_jobs rj WHERE rj.request_id=q.id AND rj.job_id=j.id) AND (m.delivery IN ('bounced','complained','failed','suppressed') OR m.state IN ('failed','unknown') OR (m.state='queued' AND m.error<>''))
 ) issues),'[]'::jsonb)` + workFrom

const workFrom = ` FROM preparation_jobs j LEFT JOIN preparation_intakes i ON i.job_id=j.id JOIN client_closes c ON c.id=j.close_id LEFT JOIN evidence e ON e.id=i.source_id`
const workScope = `c.organization_id=$3 AND ($4 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$5 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) AND ($1='' OR j.close_id::text=$1) AND ($2='' OR c.entity ILIKE '%'||$2||'%' OR c.period=$2)`

var workViews = []string{"active", "needs_confirmation", "blocked", "running", "waiting_client", "completed", "all"}

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
		rows, err := db.QueryContext(r.Context(), `SELECT w.id::text,c.id::text,c.entity,c.period,w.kind,w.created_at`+where+` ORDER BY w.id DESC LIMIT 100`, organization(r), firmAccess(r), subject(r), search)
		if err != nil {
			problem(w, 503, "Review queue unavailable.")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, closeID, entity, period, kind string
			var created time.Time
			if err := rows.Scan(&id, &closeID, &entity, &period, &kind, &created); err != nil {
				problem(w, 503, "Review queue unavailable.")
				return
			}
			items = append(items, map[string]any{"workpaper_id": id, "close_id": closeID, "entity": entity, "period": period, "kind": kind, "created_at": created})
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
		jsonReply(w, 200, map[string]any{"items": items, "has_more": more})
	})
	read := func(w http.ResponseWriter, r *http.Request) {
		view := r.URL.Query().Get("view")
		if view == "" {
			view = "all"
		}
		issues := `EXISTS(SELECT 1 FROM workpaper_review_state w WHERE w.close_id=j.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text) AND (w.review_state IN ('source_changed','changes_requested') OR jsonb_array_length(COALESCE(w.paper->'blockers','[]'))>0))`
		predicates := map[string]string{
			"active":             "j.state NOT IN ('completed','cancelled') OR " + issues,
			"needs_confirmation": "j.state='needs_confirmation'",
			"blocked":            "j.state='failed' OR e.superseded_by IS NOT NULL OR i.proposal->>'kind'='unsupported' OR EXISTS(SELECT 1 FROM email_delivery m JOIN work_requests q ON q.id=m.request_id WHERE EXISTS(SELECT 1 FROM request_jobs rj WHERE rj.request_id=q.id AND rj.job_id=j.id) AND (m.delivery IN ('bounced','complained','failed','suppressed') OR m.state IN ('failed','unknown') OR (m.state='queued' AND m.error<>''))) OR " + issues,
			"running":            "j.state IN ('waiting_evidence','interpreting','queued')",
			"waiting_client":     "j.state='waiting_client'",
			"completed":          "j.state='completed'",
			"all":                "true",
		}
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
		rows, err := db.QueryContext(r.Context(), workSelect+` WHERE `+workScope+` AND (`+predicate+`) ORDER BY CASE WHEN j.state IN ('needs_confirmation','failed') THEN 0 WHEN j.state='waiting_client' THEN 1 WHEN j.state IN ('waiting_evidence','interpreting','queued') THEN 2 ELSE 3 END,j.id DESC LIMIT 101`, r.PathValue("id"), search, organization(r), firmAccess(r), subject(r))
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
			if err = rows.Scan(&id, &closeID, &entity, &period, &state, &message, &papers, &started, &revision, &attempts, &checkpoint, &proposal, &sourceID, &name, &extraction, &stale, &coordinated, &requests, &replies, &blockers); err != nil {
				problem(w, 503, "Could not read work.")
				return
			}
			items = append(items, map[string]any{"id": id, "close_id": closeID, "entity": entity, "period": period, "state": state, "error": message, "workpaper_ids": papers, "revision": revision, "agent_attempts": attempts, "agent_checkpoint": checkpoint, "proposal": proposal, "source_id": sourceID, "source_name": name, "extraction_state": extraction, "stale": stale, "coordinated": coordinated, "requests": requests, "replies": replies, "blockers": blockers, "agent_started_at": started})
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
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity,period FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID, &entity, &period); err != nil {
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
		if state == "completed" || state == "queued" || (state == "cancelled" && action != "retry") {
			problem(w, 409, "This work is already preparing or finished. Refresh its status.")
			return
		}
		var current bool
		if err = tx.QueryRowContext(r.Context(), `SELECT superseded_by IS NULL AND extraction_state='ready' FROM evidence WHERE id=$1 AND close_id=$2`, sourceID, closeID).Scan(&current); err != nil {
			problem(w, 503, "Could not verify the source.")
			return
		}
		if action != "cancel" && action != "revoke_link" && !current {
			problem(w, 409, "The source must be current and fully extracted. Retry extraction or use its replacement.")
			return
		}
		var fresh preparation.Proposal
		var proposed preparation.Proposal
		var sources []preparation.IntakeSource
		if action == "confirm" || action == "request" || action == "link" {
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
				problem(w, 409, "Source evidence changed. Refresh proposals before confirming.")
				return
			}
		}
		linkToken := ""
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
				if e != nil || fresh != digest || body != savedBody {
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
				_, err = tx.ExecContext(r.Context(), `INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by) VALUES($1,$2,$3,$4,$5)`, jobID, draftID, revision, recipient.Address, actor)
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
			if state != "waiting_client" {
				problem(w, 409, "This work is not waiting for a client.")
				return
			}
			var requestID string
			if e := tx.QueryRowContext(r.Context(), `SELECT id::text FROM work_requests WHERE id::text=$1 AND job_id=$2 AND id=(SELECT max(id) FROM work_requests WHERE job_id=$2)`, in.RequestID, jobID).Scan(&requestID); e != nil {
				problem(w, 404, "Approved request not found.")
				return
			}
			if action == "link" {
				if e := preparation.FreshClientRequest(r.Context(), tx, closeID, requestID); e != nil {
					problem(w, 409, e.Error())
					return
				}
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id=$1 AND revoked_at IS NULL AND used_at IS NULL`, requestID)
			if err == nil && action == "link" {
				linkToken = rand.Text()
				_, err = tx.ExecContext(r.Context(), `INSERT INTO client_request_links(digest,request_id,issued_by) VALUES($1,$2,$3)`, preparation.Digest([]byte(linkToken)), requestID, actor)
			}

		case "retry":
			if state != "failed" && state != "needs_confirmation" && state != "waiting_client" && state != "cancelled" {
				problem(w, 409, "Only stopped or unresolved work can be rechecked.")
				return
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO recovery_actions(organization_id,close_id,job_id,action,prior_state,actor,reason) VALUES($1,$2,$3,'retry_preparation',$4,$5,'Work inbox recheck')`, organization(r), closeID, jobID, state, actor)
			if err != nil {
				break
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id IN (SELECT id FROM work_requests WHERE job_id=$1) AND used_at IS NULL AND revoked_at IS NULL`, jobID)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='waiting_evidence',error='',finished_at=NULL WHERE id=$1`, jobID)
			}
		case "cancel":
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='cancelled',finished_at=now() WHERE id=$1`, jobID)
		default:
			problem(w, 404, "Unknown work action.")
			return
		}
		if err == nil && action != "link" && action != "revoke_link" {
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL,agent_attempts=CASE WHEN $2 IN ('retry','reply','answer') THEN 0 ELSE agent_attempts END WHERE job_id=$1`, jobID, action)
		}
		if err != nil {
			jobProblem(w, err, "Work update could not be confirmed. Refresh before retrying.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Work update could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": jobID, "link_token": linkToken})
	})
}
