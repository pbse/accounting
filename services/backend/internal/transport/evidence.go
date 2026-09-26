package transport

import (
	"database/sql"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"slices"
	"sort"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
)

func registerEvidence(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/{id}/evidence", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 6*1024*1024)
		if err := r.ParseMultipartForm(6 * 1024 * 1024); err != nil {
			problem(w, 400, "Upload text/CSV up to 512 KiB or a PDF up to 5 MiB.")
			return
		}
		defer r.MultipartForm.RemoveAll()
		purpose := r.FormValue("purpose")
		if purpose != "" && purpose != "procedure" {
			problem(w, 422, "Choose a supported evidence purpose.")
			return
		}
		if purpose == "procedure" {
			if _, ok := learnedPermission(w, r); !ok {
				return
			}
		} else {
			purpose = "work"
		}
		if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
			problem(w, 400, "Choose one file.")
			return
		}
		file, h, err := r.FormFile("file")
		if err != nil {
			problem(w, 400, "Choose a file.")
			return
		}
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, 5242881))
		name := h.Filename
		pdf := strings.HasSuffix(strings.ToLower(name), ".pdf")
		invalid := err != nil || !validEvidenceFile(name, body)
		if invalid {
			problem(w, 422, "Choose nonempty UTF-8 text/CSV (512 KiB maximum) or a complete PDF (5 MiB maximum).")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Evidence storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		digest := preparation.Digest(body)
		var id string
		added := false
		err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM evidence WHERE close_id=$1 AND digest=$2`, closeID, digest).Scan(&id)
		if err == sql.ErrNoRows {
			added = true
			var count, size int
			if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, closeID).Scan(&count, &size); err != nil {
				problem(w, 503, "Could not check evidence storage.")
				return
			}
			// ponytail: 100 files / 20 MiB per close in Postgres; move originals to R2 when larger intake is supported.
			if count >= 100 || size+len(body) > 20*1024*1024 {
				problem(w, 413, "This close has reached its evidence limit (100 files or 20 MiB).")
				return
			}
			actor := auth.Actor(r.Context())
			subject := "local-workspace"
			if actor != nil && actor.Subject != "" {
				subject = actor.Subject
			}
			content, state := string(body), "ready"
			var original []byte
			if pdf {
				content = ""
				state = "queued"
				original = body
			}
			err = tx.QueryRowContext(r.Context(), `INSERT INTO evidence(close_id,name,digest,content,created_by,original,extraction_state,purpose) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, closeID, name, digest, content, subject, original, state, purpose).Scan(&id)
		}
		if err == nil && !added {
			var currentPurpose string
			if err = tx.QueryRowContext(r.Context(), `SELECT purpose FROM evidence WHERE id::text=$1 AND close_id=$2`, id, closeID).Scan(&currentPurpose); err != nil {
				problem(w, 503, "Evidence purpose unavailable.")
				return
			}
			if currentPurpose != purpose {
				problem(w, 409, "This file already has a different evidence purpose. Use its existing source or choose distinct content.")
				return
			}
		}
		jobID := ""
		if err == nil && purpose == "work" {
			jobID, err = preparation.StartIntake(r.Context(), tx, closeID, id, auth.Actor(r.Context()).Subject)
		}
		if err == nil && added && purpose == "work" {
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs j SET state='waiting_evidence',error='' FROM preparation_intakes i WHERE i.job_id=j.id AND j.close_id=$1 AND j.id::text<>$2 AND j.state='needs_confirmation' AND i.proposal->>'engine'='openai' AND (jsonb_array_length(COALESCE(i.proposal->'questions','[]'))>0 OR EXISTS(SELECT 1 FROM jsonb_array_elements(i.proposal->'facts') f WHERE COALESCE(f->>'question','')<>''))`, closeID, jobID)
		}
		if err != nil {
			jobProblem(w, err, "Upload could not be confirmed. Retry the same file safely.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Upload could not be confirmed. Retry the same file safely.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "digest": digest, "job_id": jobID})
	})
	mux.HandleFunc("GET /v1/closes/{id}/evidence", func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		inspect := r.URL.Query().Get("checks") == "1"
		var entity string
		if inspect && db.QueryRowContext(r.Context(), `SELECT entity FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&entity) != nil {
			problem(w, 404, "Close not found.")
			return
		}
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'name',name,'digest',digest,'bytes',COALESCE(octet_length(original),octet_length(content)),'purpose',purpose,'procedure_id',procedure_id::text,'procedure_kind',(SELECT kind FROM procedure_versions WHERE id=procedure_id),'content',CASE WHEN $2 AND superseded_by IS NULL THEN content ELSE '' END,'extraction_state',extraction_state,'extraction_error',extraction_error,'created_at',created_at,'superseded_by',superseded_by::text,'replacement_reason',replacement_reason,'replaced_at',replaced_at) ORDER BY id DESC),'[]'::jsonb) FROM evidence WHERE close_id::text=$1`, r.PathValue("id"), inspect).Scan(&body)
		if err != nil {
			problem(w, 503, "Could not load evidence.")
			return
		}
		var files []map[string]json.RawMessage
		if json.Unmarshal(body, &files) != nil {
			problem(w, 503, "Could not inspect evidence.")
			return
		}
		var aliases []string
		if inspect {
			if aliases, err = preparation.ClientAliases(r.Context(), db, r.PathValue("id")); err != nil {
				problem(w, 503, "Could not inspect the client's confirmed names.")
				return
			}
		}
		for _, file := range files {
			var name, content, state string
			json.Unmarshal(file["name"], &name)
			json.Unmarshal(file["content"], &content)
			json.Unmarshal(file["extraction_state"], &state)
			delete(file, "content")
			if inspect && state == "ready" && string(file["superseded_by"]) == "null" {
				checks := documents.Check(name, content)
				if issue, _ := documents.EntityIssue(content, entity, aliases); issue != "" {
					checks.Questions = append(checks.Questions, issue)
				}
				file["checks"], _ = json.Marshal(checks)
			}
		}
		jsonReply(w, 200, files)
	})
	mux.HandleFunc("GET /v1/closes/{id}/evidence/{evidence}", func(w http.ResponseWriter, r *http.Request) {
		var name, body string
		var original []byte
		err := db.QueryRowContext(r.Context(), `SELECT name,content,original FROM evidence WHERE id::text=$1 AND close_id::text=$2`, r.PathValue("evidence"), r.PathValue("id")).Scan(&name, &body, &original)
		if err == sql.ErrNoRows {
			problem(w, 404, "Evidence not found in this close.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not read evidence.")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		if original != nil {
			w.Header().Set("Content-Type", "application/pdf")
			w.Write(original)
		} else {
			io.WriteString(w, body)
		}
	})
	mux.HandleFunc("GET /v1/closes/{id}/evidence/{evidence}/text", func(w http.ResponseWriter, r *http.Request) {
		var body, state, message string
		err := db.QueryRowContext(r.Context(), `SELECT content,extraction_state,extraction_error FROM evidence WHERE id::text=$1 AND close_id::text=$2`, r.PathValue("evidence"), r.PathValue("id")).Scan(&body, &state, &message)
		if err == sql.ErrNoRows {
			problem(w, 404, "Evidence not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Evidence unavailable.")
			return
		}
		if state != "ready" {
			problem(w, 409, "Extraction "+state+". "+message)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		io.WriteString(w, body)
	})
	mux.HandleFunc("POST /v1/closes/{id}/evidence/{evidence}/retry", func(w http.ResponseWriter, r *http.Request) {
		var id string
		err := db.QueryRowContext(r.Context(), `UPDATE evidence SET extraction_state='queued',extraction_error='' WHERE id::text=$1 AND close_id::text=$2 AND extraction_state='failed' AND original IS NOT NULL RETURNING id::text`, r.PathValue("evidence"), r.PathValue("id")).Scan(&id)
		if err != nil {
			problem(w, 409, "Only failed PDF extraction can be retried. Refresh evidence.")
			return
		}
		jsonReply(w, 202, map[string]string{"id": id})
	})

	mux.HandleFunc("POST /v1/closes/{id}/preparations", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			preparation.Plan
			WorkID       string `json:"work_id"`
			WorkRevision int    `json:"work_revision"`
		}
		if !decode(w, r, &request) {
			return
		}
		p := request.Plan
		if p.CarryForward != nil {
			problem(w, 422, "Use the reviewed next-month carry-forward action.")
			return
		}
		if len(p.Sources) < 1 || len(p.Sources) > 8 {
			problem(w, 422, "Link source evidence and the ledger/credit support for the selected method.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Preparation storage unavailable.")
			return
		}
		defer tx.Rollback()
		var entity, period string
		if err := tx.QueryRowContext(r.Context(), `SELECT entity,period FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if p.Revises != "" {
			var valid bool
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM workpapers WHERE id::text=$1 AND close_id::text=$2 AND kind=$3 AND paper->'carry_forward' IS NULL)`, p.Revises, r.PathValue("id"), p.Kind).Scan(&valid)
			if err != nil || !valid {
				problem(w, 422, "Revision must refer to the same kind of workpaper in this close.")
				return
			}
		}
		content := map[string]string{}
		procedureID := ""
		selectedIDs := map[string]bool{}
		for i := range p.Sources {
			s := &p.Sources[i]
			s.Location = strings.TrimSpace(s.Location)
			if s.Location == "" || len(s.Location) > 200 || content[s.Role] != "" || selectedIDs[s.ID] {
				problem(w, 422, "Supply a unique source role and a location (lines or section) for each source.")
				return
			}
			selectedIDs[s.ID] = true
			if ((p.Kind == "prepaid" || p.Kind == "accrual") && s.Role != "invoice" && s.Role != "support" && s.Role != "ledger") || (p.Kind == "reconciliation" && s.Role != "statement" && s.Role != "ledger" && s.Role != "support" && s.Role != "clearing" && s.Role != "procedure" && s.Role != "other_statement" && s.Role != "other_ledger" && s.Role != "native_status") || (p.Kind == "settlement" && s.Role != "processor_report" && s.Role != "bank" && s.Role != "activity_report") || (p.Kind == "loan" && s.Role != "statement" && s.Role != "support" && s.Role != "ledger") || (p.Kind == "owner" && s.Role != "support" && s.Role != "ledger") {
				problem(w, 422, "Unsupported source role.")
				return
			}
			var text string
			var approved sql.NullString
			err := tx.QueryRowContext(r.Context(), `SELECT name,digest,content,procedure_id::text FROM evidence WHERE id::text=$1 AND close_id::text=$2 AND superseded_by IS NULL AND extraction_state='ready'`, s.ID, r.PathValue("id")).Scan(&s.Name, &s.Digest, &text, &approved)
			if err != nil {
				problem(w, 422, "Every source must be current, fully extracted, and belong to this close.")
				return
			}
			content[s.Role] = text
			if s.Role == "procedure" {
				valid := false
				if approved.Valid {
					if e := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.id::text=$2 AND p.kind='reconciliation' AND p.enabled AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)))`, r.PathValue("id"), approved.String).Scan(&valid); e != nil {
						problem(w, 503, "Approved procedures are unavailable.")
						return
					}
				}
				if !valid {
					problemCode(w, 422, "POLICY_REQUIRED", "Select an applied, approved reconciliation procedure for this close.")
					return
				}
				current, currentErr := preparation.CurrentProcedure(r.Context(), tx, r.PathValue("id"), "reconciliation")
				if currentErr != nil && currentErr != sql.ErrNoRows {
					problem(w, 503, "Approved procedures are unavailable.")
					return
				}
				if !current.Enabled {
					problemCode(w, 422, "POLICY_REQUIRED", "The current reconciliation procedure is disabled; request a new accountant-approved rule.")
					return
				}
				procedureID = approved.String
			}
		}
		if p.Kind == "reconciliation" {
			if content["statement"] == "" || content["ledger"] == "" {
				problem(w, 422, "Choose separate statement and ledger evidence.")
				return
			}
			var in reconciliation.Input
			if err := preparation.Strict(p.Input, &in); err != nil {
				problem(w, 422, err.Error())
				return
			}
			if len(in.PriorOutstanding) > 0 && content["support"] == "" {
				problem(w, 422, "Link reviewed prior-period evidence before preparing outstanding items.")
				return
			}
			if in.AccountType == "credit_card" {
				if procedureID == "" {
					problemCode(w, 422, "POLICY_REQUIRED", "Apply and link the client accountant's approved card reconciliation procedure.")
					return
				}
				in.CardRuleReference = "Approved reconciliation procedure v" + procedureID
			}
			for i := range in.CurrentOutstanding {
				if in.CurrentOutstanding[i].Treatment == "clearing" && content["clearing"] == "" {
					problem(w, 422, "Link clearing-account evidence for the reviewed current item.")
					return
				}
			}
			if len(in.Transfers) > 0 && (content["other_statement"] == "" || content["other_ledger"] == "") {
				problem(w, 422, "Link both account records before preparing a transfer pair.")
				return
			}
			if (in.NativeStatus == "reconciled" || in.NativeStatus == "unreconciled") && content["native_status"] == "" {
				problem(w, 422, "Link the QuickBooks reconciliation-status report before recording its observed status.")
				return
			}
			in.StatementCSV = content["statement"]
			in.LedgerCSV = content["ledger"]
			in.OtherStatementCSV = content["other_statement"]
			in.OtherLedgerCSV = content["other_ledger"]
			for _, s := range p.Sources {
				if s.Role == "statement" {
					in.StatementReference = s.Name + " · " + s.Location
				} else if s.Role == "ledger" {
					in.LedgerReference = s.Name + " · " + s.Location
				} else if s.Role == "other_statement" {
					in.OtherStatementRef = s.Name + " · " + s.Location
				} else if s.Role == "other_ledger" {
					in.OtherLedgerRef = s.Name + " · " + s.Location
				} else if s.Role == "native_status" {
					in.NativeStatusRef = s.Name + " · " + s.Location
				} else if s.Role == "clearing" {
					for i := range in.CurrentOutstanding {
						if in.CurrentOutstanding[i].Treatment == "clearing" {
							in.CurrentOutstanding[i].ClearingRef = s.Name + " · " + s.Location
						}
					}
				}
			}
			p.Input, _ = json.Marshal(in)
		}
		if p.Kind == "settlement" {
			if content["processor_report"] == "" || content["bank"] == "" {
				problem(w, 422, "Choose the Stripe payout export and the bank CSV as separate evidence.")
				return
			}
			var in reconciliation.SettlementInput
			if err := preparation.Strict(p.Input, &in); err != nil {
				problem(w, 422, err.Error())
				return
			}
			in.ReportCSV, in.BankCSV, in.ActivityCSV, in.ActivityReference = content["processor_report"], content["bank"], content["activity_report"], ""
			for _, s := range p.Sources {
				if s.Role == "processor_report" {
					in.ReportReference = s.Name + " · " + s.Location
				} else if s.Role == "bank" {
					in.BankReference = s.Name + " · " + s.Location
				} else if s.Role == "activity_report" {
					in.ActivityReference = s.Name + " · " + s.Location
				}
			}
			p.Input, _ = json.Marshal(in)
		}
		jobID := ""
		if request.WorkID != "" {
			actor, ok := reviewActor(w, r)
			if !ok {
				return
			}
			var revision int
			var state, sourceID string
			var proposalRaw []byte
			err = tx.QueryRowContext(r.Context(), `SELECT j.id::text,j.state,i.revision,i.source_id::text,i.proposal FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1 AND j.close_id::text=$2 FOR UPDATE OF j,i`, request.WorkID, r.PathValue("id")).Scan(&jobID, &state, &revision, &sourceID, &proposalRaw)
			linked := false
			for _, source := range p.Sources {
				if source.ID == sourceID {
					linked = true
				}
			}
			if err != nil || revision != request.WorkRevision || state != "needs_confirmation" || !linked || p.Revises != "" {
				problem(w, 409, "Work changed, or its original source is not linked. Reopen the inbox before confirming.")
				return
			}
			var proposal preparation.Proposal
			if json.Unmarshal(proposalRaw, &proposal) != nil {
				problem(w, 503, "Could not read intake proposal.")
				return
			}
			if proposal.Engine == "openai" {
				if proposal.Kind != p.Kind || len(proposal.Questions) > 0 || proposal.Kind == "unsupported" {
					problem(w, 409, "Resolve agent questions and confirm the proposed work before continuing this job.")
					return
				}
				for _, chosen := range p.Sources {
					if chosen.Role == "procedure" || chosen.Role == "clearing" {
						continue
					}
					found := false
					for _, selected := range proposal.Sources {
						if chosen.ID == selected.ID && (chosen.Role == selected.Role || (selected.Role == "support" && (chosen.Role == "other_statement" || chosen.Role == "other_ledger"))) {
							found = true
						}
					}
					if !found {
						problem(w, 409, "Source selection changed. Clarify the evidence with the agent before continuing this job.")
						return
					}
				}
			}
			sources, e := preparation.CurrentProposalSources(r.Context(), tx, r.PathValue("id"), jobID, proposal)
			if e != nil {
				problem(w, 503, "Could not verify intake sources.")
				return
			}
			freshSources := []preparation.Source{}
			for _, source := range sources {
				freshSources = append(freshSources, source.Source)
			}
			fresh, _ := json.Marshal(freshSources)
			prior, _ := json.Marshal(proposal.Sources)
			if string(fresh) != string(prior) {
				problemCode(w, 409, "SOURCE_STALE", "Intake evidence changed. Review fresh proposals first.")
				return
			}
			for _, s := range sources {
				if (s.Role == "client_reply" || (proposal.Engine == "openai" && s.Role == "support")) && !slices.ContainsFunc(p.Sources, func(selected preparation.Source) bool { return selected.ID == s.ID }) {
					p.Sources = append(p.Sources, s.Source)
				}
			}
			if _, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET confirmed_by=$2,confirmed_at=now(),revision=revision+1 WHERE job_id=$1`, jobID, actor); err != nil {
				problem(w, 503, "Could not confirm intake.")
				return
			}
		}
		if _, err := preparation.Calculate(p, entity, period); err != nil {
			problem(w, 422, err.Error())
			return
		}
		sort.Slice(p.Sources, func(i, j int) bool { return p.Sources[i].Role < p.Sources[j].Role })
		body, _ := json.Marshal(p)
		var id string
		// Each revision remains explicit. Users select which instructions a close job should run.
		err = tx.QueryRowContext(r.Context(), `INSERT INTO preparation_plans(close_id,digest,plan) SELECT id,$2,$3 FROM client_closes WHERE id::text=$1 ON CONFLICT(close_id,digest) DO UPDATE SET digest=preparation_plans.digest RETURNING id::text`, r.PathValue("id"), preparation.Digest(body), string(body)).Scan(&id)
		if err != nil {
			problem(w, 503, "Could not save preparation instructions.")
			return
		}
		if jobID != "" {
			plans, _ := json.Marshal([]preparation.Plan{p})
			if _, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET plans=$2,state='queued',error='' WHERE id=$1`, jobID, string(plans)); err != nil {
				jobProblem(w, err, "Could not queue preparation.")
				return
			}
		}
		if p.Revises != "" {
			actor := auth.Actor(r.Context())
			subject := "local-workspace"
			if actor != nil {
				subject = actor.Subject
			}
			plans, _ := json.Marshal([]preparation.Plan{p})
			err = tx.QueryRowContext(r.Context(), `INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,digest) DO UPDATE SET state=CASE WHEN preparation_jobs.state IN ('cancelled','failed') THEN 'queued' ELSE preparation_jobs.state END,error='',finished_at=CASE WHEN preparation_jobs.state IN ('cancelled','failed') THEN NULL ELSE preparation_jobs.finished_at END RETURNING id::text`, r.PathValue("id"), preparation.Digest(plans), string(plans), subject).Scan(&jobID)
			if err != nil {
				jobProblem(w, err, "Could not queue recalculation.")
				return
			}
		}
		if tx.Commit() != nil {
			problem(w, 503, "Save could not be confirmed; retry the same revision safely.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "job_id": jobID})
	})
	mux.HandleFunc("GET /v1/closes/{id}/preparations", func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'plan',plan,'created_at',created_at) ORDER BY id DESC),'[]'::jsonb) FROM (SELECT * FROM preparation_plans WHERE close_id::text=$1 ORDER BY id DESC LIMIT 100) p`, r.PathValue("id")).Scan(&body)
		if err != nil {
			problem(w, 503, "Could not load preparation instructions.")
			return
		}
		jsonReply(w, 200, json.RawMessage(body))
	})
	mux.HandleFunc("POST /v1/closes/{id}/jobs", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Plans []string `json:"plans"`
		}
		if !decode(w, r, &input) {
			return
		}
		if len(input.Plans) == 0 || len(input.Plans) > 20 {
			problem(w, 422, "Select between 1 and 20 preparation instructions.")
			return
		}
		sort.Strings(input.Plans)
		plans := []preparation.Plan{}
		for i, id := range input.Plans {
			if i > 0 && id == input.Plans[i-1] {
				problem(w, 422, "Select each instruction once.")
				return
			}
			var raw []byte
			if err := db.QueryRowContext(r.Context(), `SELECT plan FROM preparation_plans WHERE id::text=$1 AND close_id::text=$2`, id, r.PathValue("id")).Scan(&raw); err != nil {
				problem(w, 422, "Preparation instructions must belong to this close.")
				return
			}
			var p preparation.Plan
			if json.Unmarshal(raw, &p) != nil {
				problem(w, 500, "Invalid saved instructions.")
				return
			}
			plans = append(plans, p)
		}
		body, _ := json.Marshal(plans)
		var id string
		actor := auth.Actor(r.Context())
		subject := "local-workspace"
		if actor != nil && actor.Subject != "" {
			subject = actor.Subject
		}
		err := db.QueryRowContext(r.Context(), `INSERT INTO preparation_jobs(close_id,digest,plans,created_by) SELECT id,$2,$3,$4 FROM client_closes WHERE id::text=$1 ON CONFLICT(close_id,digest) DO UPDATE SET state=CASE WHEN preparation_jobs.state='cancelled' THEN 'queued' ELSE preparation_jobs.state END,finished_at=CASE WHEN preparation_jobs.state='cancelled' THEN NULL ELSE preparation_jobs.finished_at END RETURNING id::text`, r.PathValue("id"), preparation.Digest(body), string(body), subject).Scan(&id)
		if err != nil {
			jobProblem(w, err, "Queueing could not be confirmed. Retry the same selection safely.")
			return
		}
		jsonReply(w, 202, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /v1/closes/{id}/jobs", func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(j) ORDER BY j.created_at DESC),'[]'::jsonb) FROM (SELECT id::text,state,workpaper_ids,error,created_at,finished_at FROM preparation_jobs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 20) j`, r.PathValue("id")).Scan(&body)
		if err != nil {
			problem(w, 503, "Could not read preparation jobs.")
			return
		}
		jsonReply(w, 200, json.RawMessage(body))
	})
	mux.HandleFunc("POST /v1/closes/{id}/jobs/{job}/cancel", func(w http.ResponseWriter, r *http.Request) {
		// Do not wait for an active calculation; cancellation wins only before claim.
		var id string
		err := db.QueryRowContext(r.Context(), `UPDATE preparation_jobs SET state='cancelled',finished_at=now() WHERE id IN (SELECT id FROM preparation_jobs WHERE id::text=$1 AND close_id::text=$2 AND state='queued' FOR UPDATE SKIP LOCKED) RETURNING id::text`, r.PathValue("job"), r.PathValue("id")).Scan(&id)
		if err != nil {
			problem(w, 409, "This job is being processed, already finished, or unavailable. Refresh its status.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "state": "cancelled"})
	})
}
