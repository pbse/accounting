package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

type carryItem struct {
	jobState string
	ID       string `json:"id"`
	Digest   string `json:"digest"`
	ReviewID string `json:"review_id"`
	Source   string `json:"source"`
	Balance  string `json:"balance"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
	JobID    string `json:"job_id"`
	paper    struct {
		prepaids.Workpaper
		Sources []preparation.Source `json:"sources"`
	}
}
type unresolvedCarryItem struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	State string `json:"state"`
}

func unresolvedCarry(ctx context.Context, tx *sql.Tx, closeID string) ([]unresolvedCarryItem, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT kind,id,state FROM (
	 SELECT 'job' kind,j.id::text id,j.state FROM preparation_jobs j WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled','supporting')
	 UNION ALL
	 SELECT 'draft',w.id::text,w.review_state FROM workpaper_review_state w WHERE w.close_id=$1 AND (w.review_state<>'approved' OR jsonb_array_length(COALESCE(w.paper->'blockers','[]'::jsonb))>0)
	) pending ORDER BY kind,id::bigint LIMIT 21`, closeID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []unresolvedCarryItem{}
	for rows.Next() {
		var item unresolvedCarryItem
		if err = rows.Scan(&item.Kind, &item.ID, &item.State); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(items)
	return items, preparation.Digest(raw), nil
}

func carryHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Items            []carryItem `json:"items"`
			ProcedureIDs     []string    `json:"procedure_ids"`
			CarryUnresolved  bool        `json:"carry_unresolved"`
			UnresolvedDigest string      `json:"unresolved_digest"`
			Confirmed        bool        `json:"confirmed"`
		}
		actor := ""
		if r.Method == "POST" {
			var ok bool
			if actor, ok = reviewActor(w, r); !ok {
				return
			}
			if !decode(w, r, &in) {
				return
			}
			if !in.Confirmed || (len(in.Items) == 0 && len(in.ProcedureIDs) == 0 && !in.CarryUnresolved) || len(in.Items) > 20 || len(in.ProcedureIDs) > 3 || (len(in.Items) > 0 && len(in.ProcedureIDs) > 0) {
				problem(w, 422, "Choose either reviewed schedules or current approved procedures in one action. Applying a new procedure can invalidate a carried schedule.")
				return
			}
		}
		// A next month this opens takes the firm's monthly close template; the firm's roles, for the owner it may give the
		// close, are read before any lock is taken when the firm names an owner.
		roles := templateRoles(organization(r))
		if r.Method == "POST" {
			warmTemplateRoles(r.Context(), db, organization(r), roles)
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Carry-forward unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, entity, period string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity,period FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID, &entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		// A client the firm's roster says has left owes no more months, so no work is carried into one.
		if r.Method == "POST" {
			var left bool
			if err = tx.QueryRowContext(r.Context(), `SELECT `+rosterLeft("$1", "$2"), organization(r), entity).Scan(&left); err != nil {
				problem(w, 503, "Carry-forward unavailable.")
				return
			}
			if left {
				problem(w, 409, entity+" is inactive on the firm's roster. Reactivate it before carrying work into another month.")
				return
			}
		}
		date, e := time.Parse("2006-01", period)
		if e != nil {
			problem(w, 422, "Invalid close month.")
			return
		}
		next := date.AddDate(0, 1, 0).Format("2006-01")
		if len(next) != 7 {
			problem(w, 422, "Next month is outside the supported calendar.")
			return
		}
		rows, err := tx.QueryContext(r.Context(), `SELECT w.id::text,w.digest,w.review_id,w.review_state,w.paper,COALESCE(next_job.id::text,''),COALESCE(next_job.state,'')
 FROM workpaper_review_state w LEFT JOIN LATERAL (
 SELECT j.id,j.state FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id
 WHERE c.organization_id=$4 AND lower(c.entity)=lower($2) AND c.period=$3 AND j.digest='carry-forward:'||w.id::text||':'||w.review_id
 ) next_job ON true WHERE w.close_id=$1 AND w.kind='prepaid' ORDER BY w.id LIMIT 101`, closeID, entity, next, organization(r))
		if err != nil {
			problem(w, 503, "Could not inspect prior schedules.")
			return
		}
		items := []carryItem{}
		for rows.Next() {
			var item carryItem
			var state string
			var raw []byte
			if err = rows.Scan(&item.ID, &item.Digest, &item.ReviewID, &state, &raw, &item.JobID, &item.jobState); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &item.paper); err != nil {
				break
			}
			item.Source = item.paper.Input.SourceReference
			item.Balance = item.paper.PrepaidBalance
			switch {
			case item.JobID != "" && item.jobState != "cancelled" && item.jobState != "failed":
				item.Reason = "Already carried forward; open the next close."
			case state != "approved":
				item.Reason = "Requires a current approved review."
			case len(item.paper.Sources) == 0:
				item.Reason = "Requires linked source evidence."
			case len(item.paper.Schedule) == 0 || item.paper.Schedule[len(item.paper.Schedule)-1].Period < next:
				item.Reason = "Schedule ends before next month."
			default:
				check := item.paper.Input
				check.ClosePeriod = next
				if _, e := prepaids.ScheduleOnly(check); e != nil {
					item.Reason = e.Error()
				} else {
					item.Eligible = true
				}
			}
			items = append(items, item)
		}
		readErr := rows.Err()
		rows.Close()
		if err != nil || readErr != nil {
			problem(w, 503, "Could not read prior schedules.")
			return
		}
		if len(items) > 100 {
			problem(w, 422, "This close exceeds the 100-schedule carry-forward limit.")
			return
		}
		unresolved, unresolvedDigest, err := unresolvedCarry(r.Context(), tx, closeID)
		if err != nil {
			problem(w, 503, "Could not inspect unresolved prior work.")
			return
		}
		if len(unresolved) > 20 && r.Method == "POST" && in.CarryUnresolved {
			problem(w, 422, "This close has more than 20 unresolved items. Resolve or narrow the prior scope before carrying them.")
			return
		}
		if r.Method == "POST" && in.CarryUnresolved && (len(unresolved) == 0 || in.UnresolvedDigest != unresolvedDigest) {
			problem(w, 409, "Unresolved prior work changed. Refresh the preview.")
			return
		}
		procedures := []preparation.Procedure{}
		for _, kind := range []string{"prepaid", "reconciliation", "accrual"} {
			p, e := preparation.CurrentProcedure(r.Context(), tx, closeID, kind)
			if e == sql.ErrNoRows {
				continue
			}
			if e != nil {
				problem(w, 503, "Could not inspect approved procedures.")
				return
			}
			procedures = append(procedures, p)
		}
		if r.Method == "GET" {
			var existing string
			if e := tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM client_closes WHERE organization_id=$3 AND lower(entity)=lower($1) AND period=$2),'')`, entity, next, organization(r)).Scan(&existing); e != nil {
				problem(w, 503, "Could not inspect the next close.")
				return
			}
			jsonReply(w, 200, map[string]any{"period": next, "items": items, "procedures": procedures, "unresolved": unresolved, "unresolved_more": len(unresolved) > 20, "unresolved_digest": unresolvedDigest, "close_id": existing})
			return
		}
		selected := []carryItem{}
		seen := map[string]bool{}
		for _, want := range in.Items {
			found := false
			for _, item := range items {
				if item.ID == want.ID && item.Digest == want.Digest && item.ReviewID == want.ReviewID && (item.Eligible || item.JobID != "") && !seen[item.ID] {
					selected = append(selected, item)
					seen[item.ID] = true
					found = true
					break
				}
			}
			if !found {
				problem(w, 409, "A selected schedule or its review changed. Refresh the preview.")
				return
			}
		}
		selectedProcedures := []preparation.Procedure{}
		seenProcedure := map[string]bool{}
		for _, id := range in.ProcedureIDs {
			found := false
			for _, p := range procedures {
				if p.ID == id && p.Enabled && !seenProcedure[id] {
					selectedProcedures = append(selectedProcedures, p)
					seenProcedure[id] = true
					found = true
					break
				}
			}
			if !found {
				problem(w, 409, "A selected procedure changed or is disabled. Refresh the preview.")
				return
			}
		}
		var target, targetEntity string
		// Opening next month counts against the firm's close limit, as when a person opens a close; one opened meanwhile
		// is simply used.
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,entity FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3 FOR UPDATE`, organization(r), entity, next).Scan(&target, &targetEntity)
		if err == sql.ErrNoRows {
			var limit, count int
			var opened bool
			if err = tx.QueryRowContext(r.Context(), `SELECT max_closes FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, organization(r)).Scan(&limit); err == nil {
				err = tx.QueryRowContext(r.Context(), `SELECT count(*),COALESCE(bool_or(lower(entity)=lower($2) AND period=$3),false) FROM client_closes WHERE organization_id=$1`, organization(r), entity, next).Scan(&count, &opened)
			}
			if err == nil && !opened && count >= limit {
				problem(w, 429, "This firm has reached its close limit. Archive or retain existing work according to policy before opening another period.")
				return
			}

			if err == nil {
				var created bool
				err = tx.QueryRowContext(r.Context(), `INSERT INTO client_closes(entity,period,organization_id) VALUES($1,$2,$3) ON CONFLICT(organization_id,lower(entity),period) DO UPDATE SET entity=client_closes.entity RETURNING id::text,entity,xmax=0`, entity, next, organization(r)).Scan(&target, &targetEntity, &created)
				if err == nil && created {
					err = applyCloseTemplate(r.Context(), tx, target, roles)
				}
			}
		}
		if err != nil {
			problem(w, 503, "Could not create the next close.")
			return
		}
		// Read again under the firm lock, which roster changes take, whether the month was opened here or already open, in a
		// statement of its own so it sees a change that committed while it waited: a client marked inactive meanwhile gets
		// no work carried into it.
		var left bool
		if _, err = tx.ExecContext(r.Context(), `SELECT 1 FROM organizations WHERE id=$1 FOR SHARE`, organization(r)); err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT `+rosterLeft("$1", "$2"), organization(r), entity).Scan(&left)
		}
		if err != nil {
			problem(w, 503, "Carry-forward unavailable.")
			return
		}
		if left {
			problem(w, 409, entity+" is inactive on the firm's roster. Reactivate it before carrying work into another month.")
			return
		}
		var lastDecision string
		e = tx.QueryRowContext(r.Context(), `SELECT decision FROM close_signoffs WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, target).Scan(&lastDecision)
		if e != nil && e != sql.ErrNoRows {
			problem(w, 503, "Could not inspect next-month sign-off.")
			return
		}
		if lastDecision == "signed_off" {
			problem(w, 409, "Explicitly reopen the next-month close before changing its work.")
			return
		}
		for _, p := range selectedProcedures {
			if _, e := preparation.AttachProcedure(r.Context(), tx, target, p, true, actor); e != nil {
				problem(w, 409, "Could not apply procedure "+p.Kind+": "+e.Error())
				return
			}
		}
		if in.CarryUnresolved {
			scope := closeScope{Description: fmt.Sprintf("Carry-over follow-up from %s %s. This scope does not certify the new month's ledger or accounting work.", entity, period), RequiredPapers: []string{}, ExternalSteps: []externalStep{}}
			var prior []byte
			e = tx.QueryRowContext(r.Context(), `SELECT scope FROM close_scopes WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, target).Scan(&prior)
			if e != nil && e != sql.ErrNoRows {
				problem(w, 503, "Could not inspect next-month scope.")
				return
			}
			if e == nil && json.Unmarshal(prior, &scope) != nil {
				problem(w, 503, "Next-month scope is invalid.")
				return
			}
			seen := map[string]bool{}
			for _, step := range scope.ExternalSteps {
				seen[step.Name] = true
			}
			added := false
			for _, item := range unresolved {
				name := fmt.Sprintf("Resolve close #%s %s #%s", closeID, item.Kind, item.ID)
				if !seen[name] {
					scope.ExternalSteps = append(scope.ExternalSteps, externalStep{Name: name})
					added = true
				}
			}
			if len(scope.ExternalSteps) > 20 {
				problem(w, 422, "Next-month scope cannot hold all unresolved items (20-step limit).")
				return
			}
			if added {
				body, _ := json.Marshal(scope)
				if _, e = tx.ExecContext(r.Context(), `INSERT INTO close_scopes(close_id,scope,actor) VALUES($1,$2,$3)`, target, string(body), actor); e != nil {
					problem(w, 503, "Could not record next-month unresolved work.")
					return
				}
			}
		}
		jobs := []string{}
		for _, item := range selected {
			if item.JobID != "" && item.jobState != "cancelled" && item.jobState != "failed" {
				jobs = append(jobs, item.JobID)
				continue
			}
			input := item.paper.Input
			input.Entity = targetEntity
			input.ClosePeriod = next
			input.BookedBalance = ""
			input.BookingReference = ""
			input.ExamplesConfirmed = false
			input.MethodConfirmed = false
			input.BookingConfirmed = false
			raw, _ := json.Marshal(input)
			carry := &preparation.CarryForward{CloseID: closeID, PaperID: item.ID, Digest: item.Digest, ReviewID: item.ReviewID, Period: period, OpeningBalance: item.Balance, SourceIDs: map[string]string{}}
			p := preparation.Plan{Kind: "prepaid", Input: raw, Sources: []preparation.Source{}, CarryForward: carry}
			for _, s := range item.paper.Sources {
				oldID := s.ID
				err = tx.QueryRowContext(r.Context(), `INSERT INTO evidence(close_id,name,digest,content,original,extraction_state,created_by,procedure_id,purpose) SELECT $1,name,digest,content,original,'ready',$5,procedure_id,purpose FROM evidence WHERE id::text=$2 AND close_id=$3 AND digest=$4 AND superseded_by IS NULL AND extraction_state='ready' ON CONFLICT(close_id,digest) DO UPDATE SET digest=evidence.digest WHERE evidence.superseded_by IS NULL AND evidence.extraction_state='ready' AND evidence.purpose=EXCLUDED.purpose RETURNING id::text`, target, oldID, closeID, s.Digest, actor).Scan(&s.ID)
				if err != nil {
					problem(w, 409, "Source evidence changed or could not be copied.")
					return
				}
				carry.SourceIDs[s.ID] = oldID
				p.Sources = append(p.Sources, s)
			}
			var count, size int
			if e := tx.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, target).Scan(&count, &size); e != nil || count > 100 || size > 20*1024*1024 {
				problem(w, 413, "Next close evidence capacity would be exceeded.")
				return
			}
			if e := preparation.ValidateCarry(r.Context(), tx, p, target, targetEntity, next); e != nil {
				problem(w, 409, e.Error())
				return
			}
			if _, e := preparation.Calculate(p, targetEntity, next); e != nil {
				problem(w, 422, e.Error())
				return
			}
			plans, _ := json.Marshal([]preparation.Plan{p})
			var job string
			if err = tx.QueryRowContext(r.Context(), `INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,digest) DO UPDATE SET plans=EXCLUDED.plans,state='queued',error='',finished_at=NULL WHERE preparation_jobs.state IN ('failed','cancelled') RETURNING id::text`, target, "carry-forward:"+item.ID+":"+item.ReviewID, string(plans), actor).Scan(&job); err != nil {
				jobProblem(w, err, "Could not queue schedule #"+item.ID+".")
				return
			}
			jobs = append(jobs, job)
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Carry-forward could not be confirmed. Retry the same selection safely.")
			return
		}
		jsonReply(w, 200, map[string]any{"close_id": target, "period": next, "job_ids": jobs})
	}
}

func registerCarry(mux *http.ServeMux, db *sql.DB) {
	handler := carryHandler(db)
	mux.HandleFunc("GET /v1/closes/{id}/carry-forward", handler)
	mux.HandleFunc("POST /v1/closes/{id}/carry-forward", handler)
}

// autoCarry queues every eligible reviewed schedule not yet carried (up to the 20-item limit) for
// next-month preparation, through the same preview and carry-forward rules as the page, as the requesting
// reviewer. It returns how many were queued, or false when the carry-forward could not run.
func autoCarry(db *sql.DB, r *http.Request, closeID string) (int, bool) {
	// A client the firm's roster says has left gets no next month; that is not a failure to report.
	var left bool
	if err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT `+rosterLeft("c.organization_id", "c.entity")+` FROM client_closes c WHERE c.id::text=$1),false)`, closeID).Scan(&left); err != nil {
		return 0, false
	}
	if left {
		return 0, true
	}
	carry := carryHandler(db)
	call := func(method string, body []byte) *httptest.ResponseRecorder {
		req, _ := http.NewRequestWithContext(r.Context(), method, "/v1/closes/"+closeID+"/carry-forward", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", closeID)
		out := httptest.NewRecorder()
		carry(out, req)
		return out
	}
	var preview struct {
		Items []carryItem `json:"items"`
	}
	if got := call("GET", nil); got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &preview) != nil {
		return 0, false
	}
	eligible := []carryItem{}
	for _, item := range preview.Items {
		if item.Eligible && item.JobID == "" && len(eligible) < 20 {
			eligible = append(eligible, item)
		}
	}
	if len(eligible) == 0 {
		return 0, true
	}
	body, _ := json.Marshal(map[string]any{"items": eligible, "confirmed": true})
	if call("POST", body).Code != 200 {
		return 0, false
	}
	return len(eligible), true
}
