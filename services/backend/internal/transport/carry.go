package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
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

func registerCarry(mux *http.ServeMux, db *sql.DB) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Items     []carryItem `json:"items"`
			Confirmed bool        `json:"confirmed"`
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
			if !in.Confirmed || len(in.Items) < 1 || len(in.Items) > 20 {
				problem(w, 422, "Confirm 1–20 reviewed schedules to carry forward.")
				return
			}
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
			case item.paper.Input.ServiceEnd[:min(7, len(item.paper.Input.ServiceEnd))] < next:
				item.Reason = "Schedule ends before next month."
			default:
				check := item.paper.Input
				check.ClosePeriod = next
				check.ExamplesConfirmed = true
				check.MethodConfirmed = true
				check.BookingConfirmed = true
				if _, e := prepaids.Prepare(check); e != nil {
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
		if r.Method == "GET" {
			var existing string
			if e := tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM client_closes WHERE organization_id=$3 AND lower(entity)=lower($1) AND period=$2),'')`, entity, next, organization(r)).Scan(&existing); e != nil {
				problem(w, 503, "Could not inspect the next close.")
				return
			}
			jsonReply(w, 200, map[string]any{"period": next, "items": items, "close_id": existing})
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
		var target, targetEntity string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO client_closes(entity,period,organization_id) VALUES($1,$2,$3) ON CONFLICT(organization_id,lower(entity),period) DO UPDATE SET entity=client_closes.entity RETURNING id::text,entity`, entity, next, organization(r)).Scan(&target, &targetEntity)
		if err != nil {
			problem(w, 503, "Could not create the next close.")
			return
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
			input.ExamplesConfirmed = false
			input.MethodConfirmed = false
			input.BookingConfirmed = false
			raw, _ := json.Marshal(input)
			carry := &preparation.CarryForward{CloseID: closeID, PaperID: item.ID, Digest: item.Digest, ReviewID: item.ReviewID, Period: period, OpeningBalance: item.Balance, SourceIDs: map[string]string{}}
			p := preparation.Plan{Kind: "prepaid", Input: raw, Sources: []preparation.Source{}, CarryForward: carry}
			for _, s := range item.paper.Sources {
				oldID := s.ID
				err = tx.QueryRowContext(r.Context(), `INSERT INTO evidence(close_id,name,digest,content,original,extraction_state,created_by,procedure_id) SELECT $1,name,digest,content,original,'ready',$5,procedure_id FROM evidence WHERE id::text=$2 AND close_id=$3 AND digest=$4 AND superseded_by IS NULL AND extraction_state='ready' ON CONFLICT(close_id,digest) DO UPDATE SET digest=evidence.digest WHERE evidence.superseded_by IS NULL AND evidence.extraction_state='ready' RETURNING id::text`, target, oldID, closeID, s.Digest, actor).Scan(&s.ID)
				if err != nil {
					problem(w, 409, "Source evidence changed or could not be copied.")
					return
				}
				carry.SourceIDs[s.ID] = oldID
				p.Sources = append(p.Sources, s)
			}
			var count, size int
			if e := tx.QueryRowContext(r.Context(), `SELECT count(*),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))),0) FROM evidence WHERE close_id=$1`, target).Scan(&count, &size); e != nil || count > 100 || size > 20*1024*1024 {
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
				problem(w, 503, "Could not queue schedule #"+item.ID+".")
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
	mux.HandleFunc("GET /v1/closes/{id}/carry-forward", handler)
	mux.HandleFunc("POST /v1/closes/{id}/carry-forward", handler)
}
