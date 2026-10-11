package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strings"

	"close.local/backend/internal/preparation"
)

type coverageRequirement struct {
	AccountID              string   `json:"account_id"`
	Name                   string   `json:"name"`
	Kind                   string   `json:"kind"`
	Question               string   `json:"question"`
	BindingID              string   `json:"binding_id"`
	JobIDs                 []string `json:"job_ids"`
	Excluded               bool     `json:"excluded"`
	Reason                 string   `json:"reason"`
	State                  string   `json:"state"`
	SuggestedJobIDs        []string `json:"suggested_job_ids"`
	SuggestedLedgerQueries []string `json:"suggested_ledger_queries"`
}
type coverageJob struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Revision int    `json:"revision"`
	Facts    []struct {
		Field string `json:"field"`
		Value string `json:"value"`
	} `json:"facts"`
	Kind   string `json:"kind"`
	Papers []struct {
		ID, Kind, State string
		Blockers        []string
		Sources         []json.RawMessage
	} `json:"papers"`
}
type coverageView struct {
	SnapshotID string                `json:"snapshot_id"`
	Stale      bool                  `json:"stale"`
	Items      []coverageRequirement `json:"items"`
	Jobs       []coverageJob         `json:"jobs"`
	Ledger     json.RawMessage       `json:"ledger"`
	Digest     string                `json:"digest"`
}

// errCoverageLimit is coverage refusing a close with more intake jobs than it reads.
var errCoverageLimit = errors.New("Coverage supports up to 100 intake jobs per close")

func currentCoverage(ctx context.Context, tx *sql.Tx, closeID string) (coverageView, error) {
	out := coverageView{Items: []coverageRequirement{}, Jobs: []coverageJob{}}
	raw, err := ledgerCoverage(ctx, tx, closeID)
	if err != nil {
		return out, err
	}
	out.Ledger = raw
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if out.SnapshotID == "" {
		raw, _ = json.Marshal(out)
		out.Digest = preparation.Digest(raw)
		return out, nil
	}
	// Include current paper approvals in the optimistic fence and derive progress;
	// there is no independently writable "resolved" flag.
	var jobs []byte
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(v ORDER BY (v->>'id')::bigint),'[]') FROM (SELECT jsonb_build_object('id',j.id::text,'name',e.name,'state',j.state,'revision',i.revision,'kind',i.proposal->>'kind','facts',(SELECT COALESCE(jsonb_agg(jsonb_build_object('field',f->>'field','value',f->>'value')),'[]'::jsonb) FROM jsonb_array_elements(COALESCE(i.proposal->'facts','[]'::jsonb)) f WHERE f->>'field' IN ('prepaid_account','liability_account','account','booking_reference','credit_reference','actual_reference')),'papers',COALESCE((SELECT jsonb_agg(jsonb_build_object('ID',w.id::text,'Kind',w.kind,'State',w.review_state,'Blockers',COALESCE(w.paper->'blockers','[]'),'Sources',COALESCE(w.paper->'sources','[]')) ORDER BY w.id) FROM workpaper_review_state w WHERE w.close_id=j.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text)),'[]')) v FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 ORDER BY j.id LIMIT 101) x`, closeID).Scan(&jobs)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(jobs, &out.Jobs); err != nil {
		return out, err
	}
	if len(out.Jobs) > 100 {
		return out, errCoverageLimit
	}
	bindings := map[string]struct {
		ID       string
		Jobs     []string
		Excluded bool
		Reason   string
	}{}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT ON (account_id) account_id,id::text,job_ids,excluded,reason FROM coverage_bindings WHERE close_id=$1 AND `+bindingCapture("coverage_bindings")+`=$2 ORDER BY account_id,coverage_bindings.id DESC`, closeID, out.SnapshotID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var account, id, reason string
		var ids []byte
		var excluded bool
		if err = rows.Scan(&account, &id, &ids, &excluded, &reason); err != nil {
			break
		}
		var jobs []string
		if err = json.Unmarshal(ids, &jobs); err != nil {
			break
		}
		bindings[account] = struct {
			ID       string
			Jobs     []string
			Excluded bool
			Reason   string
		}{id, jobs, excluded, reason}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	// Excluding an account is the engagement's scope, not a fact about one capture: an account with no decision on this
	// capture keeps its latest decision from an earlier capture of this month, or else from the client's latest earlier
	// month, when that decision was an exclusion. The person sees where it came from and can change it.
	carried := map[string]string{}
	// Only a decision on the same ledger company counts: another company (a sandbox, another file) may give the same
	// account ID to a different account.
	rows, err = tx.QueryContext(ctx, `WITH cur(q,x) AS (SELECT `+captureColumns("$2")+`)
 SELECT DISTINCT ON (b.account_id) b.account_id,b.excluded,b.reason,CASE WHEN c.id=me.id THEN '' ELSE c.period END
 FROM coverage_bindings b JOIN client_closes c ON c.id=b.close_id JOIN client_closes me ON me.id=$1 CROSS JOIN cur
 WHERE c.organization_id=me.organization_id AND client_identity(c.organization_id,c.entity)=client_identity(me.organization_id,me.entity)
 AND (c.id=me.id AND `+bindingCapture("b")+`<>$2 OR c.period<me.period)
 AND `+captureCompany("b.snapshot_id", "b.xero_snapshot_id")+`=`+captureCompany("cur.q", "cur.x")+`
 ORDER BY b.account_id,c.period DESC,b.id DESC`, closeID, out.SnapshotID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var account, reason, period string
		var excluded bool
		if err = rows.Scan(&account, &excluded, &reason, &period); err != nil {
			break
		}
		if excluded {
			from := "an earlier capture of this month"
			if period != "" {
				from = period
			}
			carried[account] = "Carried from " + from + ": " + reason
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	for n := range out.Items {
		item := &out.Items[n]
		item.JobIDs = []string{}
		item.SuggestedJobIDs = []string{}
		item.SuggestedLedgerQueries = []string{}
		item.State = "missing_evidence"
		for _, job := range out.Jobs {
			if job.Kind != item.Kind {
				continue
			}
			field := map[string]string{"prepaid": "prepaid_account", "accrual": "liability_account", "reconciliation": "account"}[item.Kind]
			for _, fact := range job.Facts {
				if fact.Field == field && fact.Value != "" && (strings.EqualFold(fact.Value, item.Name) || fact.Value == item.AccountID) {
					item.SuggestedJobIDs = append(item.SuggestedJobIDs, job.ID)
					for _, reference := range job.Facts {
						if (reference.Field == "booking_reference" || reference.Field == "credit_reference" || reference.Field == "actual_reference") && len(reference.Value) >= 3 && len(reference.Value) <= 100 && !slices.Contains(item.SuggestedLedgerQueries, reference.Value) && len(item.SuggestedLedgerQueries) < 5 {
							item.SuggestedLedgerQueries = append(item.SuggestedLedgerQueries, reference.Value)
						}
					}
					break
				}
			}
		}
		if reason, ok := carried[item.AccountID]; ok && bindings[item.AccountID].ID == "" {
			item.Excluded, item.Reason, item.State = true, reason, "excluded"
		}
		if binding, ok := bindings[item.AccountID]; ok {
			item.BindingID, item.JobIDs, item.Excluded, item.Reason = binding.ID, binding.Jobs, binding.Excluded, binding.Reason
			if item.Excluded {
				item.State = "excluded"
			} else if len(item.JobIDs) > 0 {
				item.State = "resolved"
				for _, id := range item.JobIDs {
					found := false
					for _, job := range out.Jobs {
						if job.ID != id {
							continue
						}
						found = true
						if job.State != "completed" || len(job.Papers) == 0 {
							item.State = "awaiting_work"
						}
						for _, paper := range job.Papers {
							if paper.State != "approved" || paper.Kind != item.Kind || len(paper.Blockers) > 0 || len(paper.Sources) == 0 {
								item.State = "needs_review"
							}
						}
					}
					if !found {
						item.State = "needs_review"
					}
				}
			}
		}
		if out.Stale {
			item.State = "source_changed"
		}
	}
	raw, err = json.Marshal(out)
	out.Digest = preparation.Digest(raw)
	return out, err
}
func registerCoverage(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/coverage", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Coverage unavailable.")
			return
		}
		defer tx.Rollback()
		view, err := currentCoverage(r.Context(), tx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, err.Error())
			return
		}
		var requests, attachments json.RawMessage
		err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('email',(SELECT jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'error',m.error,'created_at',m.created_at) FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),'emails',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'approved_by',m.approved_by,'error',m.error,'created_at',m.created_at) ORDER BY m.created_at) FROM email_delivery m WHERE m.request_id=q.id),'[]'::jsonb),'id',q.id::text,'body',d.body,'recipient',q.recipient,'created_at',q.approved_at,'scope',q.coverage_scope,'reminder',`+reminderStatus+`,'link',(SELECT jsonb_build_object('used_at',used_at,'revoked_at',revoked_at,'expires_at',expires_at) FROM client_request_links WHERE request_id=q.id ORDER BY issued_at DESC LIMIT 1),
 'replied',`+requestReplied("q.id")+`,
 'live',COALESCE((SELECT revoked_at IS NULL AND expires_at>now() FROM client_request_links WHERE request_id=q.id ORDER BY issued_at DESC LIMIT 1),false)) ORDER BY q.id DESC),'[]') FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1 AND q.coverage_scope IS NOT NULL`, r.PathValue("id")).Scan(&requests)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'request_id',request_id::text,'name',name,'digest',digest,'state',state,'error',error,'source_id',source_id::text,'scan_version',scan_version,'scanned_at',scanned_at,'decision_note',decision_note) ORDER BY client_attachments.id),'[]') FROM client_attachments WHERE close_id=$1`, r.PathValue("id")).Scan(&attachments)
		}
		// While its link is live, a replied request says whether the client can still add to it, and an unanswered one
		// why the client can no longer answer it, as the link decides.
		list := []map[string]any{}
		if err == nil {
			err = json.Unmarshal(requests, &list)
		}
		for _, q := range list {
			id, _ := q["id"].(string)
			switch {
			case err != nil || q["live"] != true:
			case q["replied"] == true:
				q["open"], err = requestOpen(r.Context(), tx, r.PathValue("id"), id)
			default:
				if stale := preparation.FreshClientRequest(r.Context(), tx, r.PathValue("id"), id); stale != nil {
					q["stale"] = stale.Error()
				}
			}
		}
		if err != nil {
			problem(w, 503, "Request history unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"coverage": view, "requests": list, "attachments": attachments})
	})
	mux.HandleFunc("POST /v1/closes/{id}/coverage", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action    string   `json:"action"`
			Digest    string   `json:"digest"`
			AccountID string   `json:"account_id"`
			Accounts  []string `json:"accounts"`
			Jobs      []string `json:"jobs"`
			Excluded  bool     `json:"excluded"`
			Reason    string   `json:"reason"`
			Body      string   `json:"body"`
			Recipient string   `json:"recipient"`
			RequestID string   `json:"request_id"`
			Confirmed bool     `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the exact scope, mappings and decision.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Coverage storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if in.Action == "link" || in.Action == "revoke" {
			var rid string
			if err = tx.QueryRowContext(r.Context(), `SELECT q.id::text FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id::text=$1 AND d.close_id=$2 AND q.coverage_scope IS NOT NULL`, in.RequestID, closeID).Scan(&rid); err != nil {
				problem(w, 404, "Request not found.")
				return
			}
			token, passcode := "", ""
			if in.Action == "link" {
				if err = requestLinkable(r.Context(), tx, closeID, rid); err != nil {
					problem(w, 409, err.Error())
					return
				}
				token = rand.Text()
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id=$1 AND revoked_at IS NULL`, rid)
			if err == nil && in.Action == "revoke" {
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='needs_confirmation' WHERE id IN (SELECT t.job_id FROM work_request_targets t JOIN preparation_intakes i ON i.job_id=t.job_id WHERE t.request_id=$1 AND t.revision=i.revision) AND state='waiting_client'`, rid)
			}
			if err == nil && token != "" {
				passcode, err = issueLink(r.Context(), tx, rid, actor, token)
			}
			if err != nil || tx.Commit() != nil {
				problem(w, 503, "Link update unconfirmed.")
				return
			}
			jsonReply(w, 200, map[string]string{"link_token": token, "passcode": passcode})
			return
		}
		view, err := currentCoverage(r.Context(), tx, closeID)
		if err != nil {
			problem(w, 503, "Coverage unavailable.")
			return
		}
		if view.Stale || view.SnapshotID == "" || view.Digest != in.Digest {
			problem(w, 409, "Coverage changed or ledger unavailable. Refresh before approving.")
			return
		}
		slices.Sort(in.Jobs)
		if len(in.Jobs) > 20 {
			problem(w, 422, "Select at most 20 jobs.")
			return
		}
		for n, id := range in.Jobs {
			if id == "" || (n > 0 && id == in.Jobs[n-1]) || !slices.ContainsFunc(view.Jobs, func(j coverageJob) bool { return j.ID == id }) {
				problem(w, 422, "Choose distinct jobs from this close.")
				return
			}
		}
		switch in.Action {
		case "bind":
			index := slices.IndexFunc(view.Items, func(i coverageRequirement) bool { return i.AccountID == in.AccountID })
			if index < 0 || len(strings.TrimSpace(in.Reason)) == 0 || len(in.Reason) > 2000 || (in.Excluded && len(in.Jobs) > 0) || (!in.Excluded && (len(in.Jobs) == 0 || view.Items[index].Kind == "accountant_review")) {
				problem(w, 422, "Define supported required work, or record an explicit exclusion with reason. Unsupported work belongs in external close scope.")
				return
			}
			ids, _ := json.Marshal(in.Jobs)
			if in.Jobs == nil {
				ids = []byte("[]")
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO coverage_bindings(close_id,snapshot_id,xero_snapshot_id,account_id,job_ids,excluded,reason,actor) VALUES($1,`+captureColumns("$2")+`,$3,$4,$5,$6,$7)`, closeID, view.SnapshotID, in.AccountID, string(ids), in.Excluded, in.Reason, actor)
		case "request":
			recipient, e := mail.ParseAddress(strings.TrimSpace(in.Recipient))
			if e != nil || recipient.Address != strings.TrimSpace(in.Recipient) || len(in.Recipient) > 254 || len(strings.TrimSpace(in.Body)) == 0 || len(in.Body) > 20000 || len(in.Accounts) == 0 || len(in.Accounts) > 20 {
				problem(w, 422, "Review up to 20 requirements, exact recipient and request text (1–20,000 bytes).")
				return
			}
			bindings := []map[string]string{}
			slices.Sort(in.Accounts)
			for n, id := range in.Accounts {
				ix := slices.IndexFunc(view.Items, func(i coverageRequirement) bool { return i.AccountID == id })
				if ix < 0 || (n > 0 && id == in.Accounts[n-1]) || slices.Contains([]string{"resolved", "excluded"}, view.Items[ix].State) {
					problem(w, 409, "Select distinct unresolved requirements.")
					return
				}
				bindings = append(bindings, map[string]string{"account_id": id, "id": view.Items[ix].BindingID})
				for _, job := range view.Items[ix].JobIDs {
					if !slices.Contains(in.Jobs, job) {
						in.Jobs = append(in.Jobs, job)
					}
				}
			}
			if len(in.Jobs) > 20 {
				problem(w, 422, "Request covers more than 20 jobs; narrow it.")
				return
			}
			for _, id := range in.Jobs {
				ix := slices.IndexFunc(view.Jobs, func(j coverageJob) bool { return j.ID == id })
				if ix < 0 || !slices.Contains([]string{"needs_confirmation", "failed", "waiting_evidence"}, view.Jobs[ix].State) {
					problem(w, 409, "Affected work is running, finished or already awaiting a request. Refresh or withdraw that request first.")
					return
				}
			}
			accountsJSON, _ := json.Marshal(in.Accounts)
			var existing, count int
			if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1`, closeID).Scan(&count); err != nil || count >= 100 {
				problem(w, 422, "Close request capacity reached.")
				return
			}
			if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1 AND q.coverage_scope IS NOT NULL AND EXISTS(SELECT 1 FROM client_request_links l WHERE l.request_id=q.id AND l.used_at IS NULL AND l.revoked_at IS NULL AND l.expires_at>now()) AND EXISTS(SELECT 1 FROM jsonb_array_elements(q.coverage_scope->'bindings') b WHERE b->>'account_id' IN (SELECT jsonb_array_elements_text($2::jsonb)))`, closeID, string(accountsJSON)).Scan(&existing); err != nil || existing > 0 {
				problem(w, 409, "An open request already covers a selected requirement. Withdraw it before approving a replacement.")
				return
			}
			contextDigest, _, e := preparation.RequestContext(r.Context(), tx, closeID, nil)
			if e != nil {
				problem(w, 503, "Request context unavailable.")
				return
			}
			// The work approved with the request, told apart later from work its released documents start.
			scope, _ := json.Marshal(map[string]any{"snapshot_id": view.SnapshotID, "bindings": bindings, "jobs": append([]string{}, in.Jobs...)})
			var draftID, rid string
			err = tx.QueryRowContext(r.Context(), `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,$2,'[]',$3,$4) RETURNING id::text`, closeID, in.Body, contextDigest, actor).Scan(&draftID)
			if err == nil {
				err = tx.QueryRowContext(r.Context(), `INSERT INTO work_requests(draft_id,revision,recipient,approved_by,coverage_scope) VALUES($1,0,$2,$3,$4) RETURNING id::text`, draftID, recipient.Address, actor, string(scope)).Scan(&rid)
			}
			for _, id := range in.Jobs {
				if err != nil {
					break
				}
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='waiting_client',error='' WHERE id=$1`, id)
				if err == nil {
					_, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL WHERE job_id=$1`, id)
				}
				if err == nil {
					_, err = tx.ExecContext(r.Context(), `INSERT INTO work_request_targets(request_id,job_id,revision) SELECT $1,job_id,revision FROM preparation_intakes WHERE job_id=$2`, rid, id)
				}
			}
			token, passcode := rand.Text(), ""
			if err == nil {
				passcode, err = issueLink(r.Context(), tx, rid, actor, token)
			}
			if err != nil || tx.Commit() != nil {
				problem(w, 409, "Request could not be confirmed. Refresh history before retrying.")
				return
			}
			jsonReply(w, 200, map[string]string{"id": rid, "link_token": token, "passcode": passcode})
			return
		default:
			problem(w, 422, "Unknown coverage action.")
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Coverage save unconfirmed. Refresh.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
	})
}

// captureCompany is SQL for the ledger company of a capture, given its QuickBooks and Xero snapshot ID expressions.
func captureCompany(quickbooksSnapshot, xeroSnapshot string) string {
	return `COALESCE((SELECT 'quickbooks:'||x.environment||':'||x.realm FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id WHERE s.id=` + quickbooksSnapshot + `),
 (SELECT 'xero:'||s.tenant_id FROM xero_snapshots s WHERE s.id=` + xeroSnapshot + `))`
}
