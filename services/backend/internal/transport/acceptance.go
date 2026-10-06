package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/auth"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// AcceptanceFirm is the firm a run's cases are loaded into. Left empty, each case gets a firm of its own, worked as
// "acceptance" and disabled afterwards. A live run through Context Machine names instead the firm and person its access
// profile serves, since that provider reads only for them: every case is then a close of that one firm (so no two cases
// may share a client), and each case's unfinished work is cancelled once observed.
type AcceptanceFirm struct{ Organization, Subject string }

// RunAcceptanceCase loads one corpus case into db, adds its documents as an upload would, drives extraction, reading and
// preparation until nothing is left to do, and reports what happened (docs/acceptance-corpus.md). model gives the reading
// model once the documents have their evidence IDs (each ID's file); it may return nil to read without a model. After
// the case, its firm is disabled or, in a named firm, its unfinished jobs cancelled, so later cases' queue steps never
// take its leftover work. The steps take work from the whole database, so db must hold nothing else that is waiting.
func RunAcceptanceCase(ctx context.Context, db *sql.DB, c acceptance.Case, firm AcceptanceFirm, model func(files map[string]string) (*llm.Client, error)) (acceptance.CaseObserved, error) {
	var observed acceptance.CaseObserved
	org, subject := firm.Organization, firm.Subject
	if org == "" {
		org, subject = "org_Acceptance"+rand.Text(), "acceptance"
		if _, err := db.ExecContext(ctx, `INSERT INTO organizations(id,name,max_model_calls_per_day) VALUES($1,$2,100000)`, org, "Acceptance "+c.ID); err != nil {
			return observed, err
		}
		defer db.ExecContext(context.WithoutCancel(ctx), `UPDATE organizations SET enabled=false WHERE id=$1`, org)
	} else if _, err := db.ExecContext(ctx, `INSERT INTO organizations(id,name,max_model_calls_per_day) VALUES($1,'Acceptance run',100000) ON CONFLICT (id) DO NOTHING`, org); err != nil {
		return observed, err
	}
	ctx = auth.WithActor(ctx, &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org})
	var closeID string
	if err := db.QueryRowContext(ctx, `INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, org, c.Client, c.Period).Scan(&closeID); err != nil {
		return observed, err
	}
	if firm.Organization != "" {
		defer db.ExecContext(context.WithoutCancel(ctx), `UPDATE preparation_jobs SET state='cancelled' WHERE close_id::text=$1 AND state IN ('queued','waiting_evidence','interpreting')`, closeID)
	}
	if err := acceptanceSetup(ctx, db, org, closeID, c); err != nil {
		return observed, fmt.Errorf("case %s setup: %w", c.ID, err)
	}

	// The documents arrive together, as one upload of several files would. A file whose bytes repeat an earlier one's is
	// kept as that document, as the app keeps it: files lists every file under its evidence ID, the first first.
	files, uploads := map[string]string{}, map[string][]string{}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return observed, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT id FROM client_closes WHERE id=$1 FOR UPDATE`, closeID); err != nil {
		return observed, err
	}
	for _, d := range c.Documents {
		body, err := os.ReadFile(filepath.Join(c.Dir, d.File))
		if err != nil {
			return observed, err
		}
		// As the upload route does: a workbook becomes CSV text of its first sheet, named with .csv added.
		name := d.File
		if strings.HasSuffix(strings.ToLower(name), ".xlsx") {
			text, err := documents.SpreadsheetText(body)
			if err != nil {
				return observed, fmt.Errorf("case %s: %s: %w", c.ID, d.File, err)
			}
			name, body = name+".csv", []byte(text)
		}
		if !validEvidenceFile(name, body) {
			return observed, fmt.Errorf("case %s: %s is not a file an upload accepts (UTF-8 text/CSV up to 512 KiB, or a complete PDF up to 5 MiB)", c.ID, d.File)
		}
		id, _, _, refused := addEvidence(ctx, tx, closeID, name, body, strings.HasSuffix(strings.ToLower(name), ".pdf"), "work")
		if refused != nil {
			return observed, fmt.Errorf("case %s: %s: %s", c.ID, d.File, refused.message)
		}
		if _, ok := files[id]; !ok {
			files[id] = d.File
		}
		uploads[id] = append(uploads[id], d.File)
	}
	if err = tx.Commit(); err != nil {
		return observed, err
	}

	reading := ctx
	if model != nil {
		client, err := model(files)
		if err != nil {
			return observed, err
		}
		if client != nil {
			reading = llm.WithClient(ctx, client)
		}
	}
	// As the worker does, one step of each kind at a time, until a whole round finds nothing to do.
	for round := 0; ; round++ {
		if round == 1000 {
			return observed, fmt.Errorf("case %s did not settle in 1,000 rounds", c.ID)
		}
		extracted, e1 := documents.ProcessOne(ctx, db)
		read, e2 := preparation.ProcessIntake(reading, db)
		prepared, e3 := preparation.ProcessOne(ctx, db)
		if err = errors.Join(e1, e2, e3); err != nil {
			return observed, err
		}
		if !extracted && !read && !prepared {
			break
		}
	}
	if s := c.Signoff; s != nil {
		if err = acceptanceSignoff(ctx, db, closeID, *s); err != nil {
			return observed, fmt.Errorf("case %s sign-off: %w", c.ID, err)
		}
	}
	return acceptanceObserve(ctx, db, closeID, uploads)
}

// acceptanceSignoff does the accountant's part once the agent is done, recorded under "acceptance reviewer": approve every
// current draft, when the case asks, and define what sign-off covers (every draft). Readiness then reads as sign-off would.
func acceptanceSignoff(ctx context.Context, db *sql.DB, closeID string, s acceptance.Signoff) error {
	if s.ApproveDrafts {
		if _, err := db.ExecContext(ctx, `INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,'approved','Approved in the acceptance run','acceptance reviewer'
 FROM workpapers w WHERE close_id=$1 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)`, closeID); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `INSERT INTO close_scopes(close_id,scope,actor) SELECT $1,jsonb_build_object('description',$2::text,'required_papers',COALESCE(jsonb_agg(id::text ORDER BY id),'[]'::jsonb),'external_steps','[]'::jsonb),'acceptance reviewer'
 FROM workpapers w WHERE close_id=$1 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)`, closeID, s.Description)
	return err
}

// acceptanceSetup gives the case's firm the procedure and ledger capture the case describes, as a firm would set them up.
func acceptanceSetup(ctx context.Context, db *sql.DB, org, closeID string, c acceptance.Case) error {
	if p := c.Setup.Prepaid; p != nil {
		if _, err := db.ExecContext(ctx, `INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor) VALUES($1,$2,'prepaid',$3,'Acceptance corpus procedure',$4,$5,true,'Acceptance corpus','acceptance')`,
			org, c.Client, p.Method, p.ExpenseAccount, p.PrepaidAccount); err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		procedure, err := preparation.CurrentProcedure(ctx, tx, closeID, "prepaid")
		if err == nil {
			_, err = preparation.AttachProcedure(ctx, tx, closeID, procedure, false, "acceptance")
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return err
		}
	}
	l := c.Setup.QuickBooks
	if l == nil {
		return nil
	}
	var connection, captured, syncID, snapshot string
	if err := db.QueryRowContext(ctx, `INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,$2,$3,'acceptance','active',$4,now(),'acceptance',$5,1) RETURNING id::text`,
		closeID, org, c.Client, rand.Text(), l.HomeCurrency).Scan(&connection); err != nil {
		return err
	}
	evidence := func(name, body string) (string, error) {
		var id string
		err := db.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'acceptance','ready') RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body).Scan(&id)
		return id, err
	}
	var err error
	if captured, err = evidence("QuickBooks transactions "+c.Period, "acceptance capture "+closeID); err != nil {
		return err
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,$3,'acceptance','completed',$4,now()-interval '1 hour',now()-interval '1 hour','{}') RETURNING id::text`,
		closeID, connection, c.Period, captured).Scan(&syncID); err != nil {
		return err
	}
	accounts := map[string]string{}
	for _, a := range l.Accounts {
		accounts[a.ID] = a.Name
	}
	for i, b := range l.Bills {
		record, _ := json.Marshal(map[string]any{"DocNumber": b.Number, "TotalAmt": json.Number(b.Amount), "VendorRef": map[string]string{"value": fmt.Sprint(100 + i), "name": b.Vendor},
			"Line": []map[string]any{{"Amount": json.Number(b.Amount), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"value": b.AccountID, "name": accounts[b.AccountID]}}}}})
		if _, err = db.ExecContext(ctx, `INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill',$2,'1',$3,now(),$4,$5,$6)`,
			syncID, fmt.Sprint(1000+i), b.Date, l.HomeCurrency, b.Amount, string(record)); err != nil {
			return err
		}
	}
	if snapshot, err = evidence("QuickBooks "+c.Period+" accrual period evidence.txt", "acceptance period snapshot "+closeID); err != nil {
		return err
	}
	chart := []map[string]string{}
	for _, a := range l.Accounts {
		chart = append(chart, map[string]string{"Id": a.ID, "Name": a.Name})
	}
	payload, _ := json.Marshal(map[string]any{"period": c.Period, "source_id": snapshot, "accounts": chart, "reports": map[string]any{}})
	_, err = db.ExecContext(ctx, `INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'acceptance',$3,$4,now(),now(),'acceptance')`, closeID, connection, rand.Text(), string(payload))
	return err
}

// acceptanceObserve reads back what the pipeline did with each document and with the close. uploads lists each evidence
// ID's files, the first first; a later file is the same document and is observed as it, noting which it repeats.
func acceptanceObserve(ctx context.Context, db *sql.DB, closeID string, uploads map[string][]string) (acceptance.CaseObserved, error) {
	observed := acceptance.CaseObserved{Documents: []acceptance.Observed{}, Blockers: []string{}}
	for id, names := range uploads {
		o := acceptance.Observed{File: names[0], Facts: map[string]string{}}
		var proposal []byte
		var confirmedBy, papers, reason sql.NullString
		err := db.QueryRowContext(ctx, `SELECT j.state,j.error,COALESCE(i.proposal,'{}'::jsonb),i.confirmed_by,j.workpaper_ids::text,i.agent_checkpoint->>'straight_through' FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id
 WHERE i.source_id::text=$1 AND j.state<>'cancelled' ORDER BY j.id DESC LIMIT 1`, id).Scan(&o.JobState, &o.Error, &proposal, &confirmedBy, &papers, &reason)
		o.Reason = reason.String
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return observed, err
		}
		var p preparation.Proposal
		json.Unmarshal(proposal, &p)
		o.Engine, o.Kind, o.Questions = p.Engine, p.Kind, len(p.Questions)
		for _, f := range p.Facts {
			o.Facts[f.Field] = f.Value
		}
		o.Accepted = confirmedBy.String == "agent" && o.JobState == "completed"
		var ids []string
		json.Unmarshal([]byte(papers.String), &ids)
		o.Drafts = []acceptance.Entry{}
		for _, paper := range ids {
			var entry []byte
			if err = db.QueryRowContext(ctx, `SELECT COALESCE(paper->'proposal','{}'::jsonb) FROM workpapers WHERE id::text=$1`, paper).Scan(&entry); err != nil {
				return observed, err
			}
			var e acceptance.Entry
			json.Unmarshal(entry, &e)
			o.Drafts = append(o.Drafts, e)
		}
		observed.Documents = append(observed.Documents, o)
		for _, again := range names[1:] {
			repeat := o
			repeat.File, repeat.SameAs = again, names[0]
			observed.Documents = append(observed.Documents, repeat)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM production_postings WHERE close_id=$1)+(SELECT count(*) FROM sandbox_postings WHERE close_id=$1)+(SELECT count(*) FROM xero_postings WHERE close_id=$1)
 +(SELECT count(*) FROM vendor_credit_postings WHERE close_id=$1)+(SELECT count(*) FROM bill_corrections WHERE close_id=$1)+(SELECT count(*) FROM credit_applications WHERE close_id=$1)+(SELECT count(*) FROM xero_corrections WHERE close_id=$1),
 (SELECT count(*) FROM (SELECT i.source_id FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id=$1 AND j.state NOT IN ('cancelled','failed') GROUP BY i.source_id HAVING count(*)>1) d)`, closeID).Scan(&observed.ExternalActions, &observed.Duplicates); err != nil {
		return observed, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return observed, err
	}
	defer tx.Rollback()
	ready, err := closeReadiness(ctx, tx, closeID)
	if err != nil {
		return observed, err
	}
	observed.ReadinessState, observed.Blockers = ready.State, ready.Blockers
	return observed, nil
}
