package transport

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
)

type packageEvidence struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Digest            string  `json:"digest"`
	Content           string  `json:"-"`
	Original          []byte  `json:"-"`
	ExtractionState   string  `json:"extraction_state"`
	ExtractionError   string  `json:"extraction_error"`
	SupersededBy      *string `json:"superseded_by"`
	ReplacementReason *string `json:"replacement_reason"`
}
type packageLedger struct {
	ID      string
	Payload []byte
}
type closePackage struct {
	RecordedDecisions json.RawMessage   `json:"recorded_decisions"`
	ScopeHistory      json.RawMessage   `json:"scope_history"`
	Readiness         *readiness        `json:"readiness,omitempty"`
	Evidence          []packageEvidence `json:"evidence"`
	LedgerSnapshots   []packageLedger   `json:"-"`

	Version         string          `json:"version"`
	Close           savedClose      `json:"close"`
	DataCutAt       time.Time       `json:"data_cut_at"`
	Scope           string          `json:"scope"`
	SignOff         string          `json:"sign_off"`
	Workpapers      []savedPaper    `json:"workpapers"`
	SandboxPostings json.RawMessage `json:"sandbox_postings"`
	Reviews         json.RawMessage `json:"reviews"`
	WorkHistory     json.RawMessage `json:"work_history"`
	RequestDrafts   json.RawMessage `json:"request_drafts_latest_20"`
}
type reviewPaper struct {
	ID, Kind, Source, Method string
	Issues                   []string
}

var packageHTML = template.Must(template.New("review").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Close review package</title><style>body{font:16px system-ui;max-width:960px;margin:3rem auto;padding:0 1rem;color:#203c33}section{border-top:1px solid #ccc;margin-top:2rem}pre{white-space:pre-wrap;overflow-wrap:anywhere}button{padding:.6rem}@media print{button{display:none}section{break-inside:avoid}}</style><h1>{{.Package.Close.Entity}} · {{.Package.Close.Period}}</h1><p>Saved-work snapshot: {{.Package.DataCutAt}}</p><p>{{.Package.Scope}}</p><p><strong>Firm sign-off: {{.Package.SignOff}}</strong></p>{{if .Package.Readiness}}<h2>Supported scope</h2><p>{{.Package.Readiness.Scope.Description}}</p><p>Required draft IDs: {{.Package.Readiness.Scope.RequiredPapers}}</p><ul>{{range .Package.Readiness.Scope.ExternalSteps}}<li>{{.Name}} · complete: {{.Complete}} · {{.Reference}}</li>{{end}}{{range .Package.Readiness.Blockers}}<li>{{.}}</li>{{end}}</ul><p>Decision history and the exact snapshot are preserved in manifest.json. Local development decisions are not verified individual firm identities.</p>{{end}}<p>All saved versions are included. Versions are not additive; do not sum alternative drafts. Source references are user-supplied, not verified attachments. Originals are in evidence/{id}.txt or .pdf; PDF extraction is in evidence/{id}-extracted.txt; no documents outside these uploads are included.</p><p>Use your browser’s Print / Save as PDF command to retain this review summary as a PDF.</p><h2>Workpapers · {{len .Papers}}</h2>{{if not .Papers}}<p>No saved workpapers. This close has no prepared evidence in this package.</p>{{end}}{{range .Papers}}<section><h3>{{.Kind}} · draft #{{.ID}}</h3><p>Source: {{.Source}}</p><p>Method: {{.Method}}</p><ul>{{range .Issues}}<li>{{.}}</li>{{end}}</ul><p>Full inputs and calculations: workpapers/{{.ID}}.json. Tables are in the same directory.</p></section>{{end}}<h2>Sandbox posting receipts</h2><p>These are stored sandbox states at export time, not a new provider readback. Executing or unknown means unresolved. Sandbox verification does not imply production posting or close sign-off.</p><pre>{{.Receipts}}</pre><h2>Review still required</h2><ul><li>Verify source completeness and ledger freshness independently.</li><li>Review accounting assumptions, alternative draft versions and unresolved items.</li><li>Resolve uncertain sandbox attempts without blindly retrying a write.</li><li>Confirm that recorded sign-off covers your intended scope; unlisted work is not certified.</li></ul></html>`))

func exportClose(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent close snapshot.")
			return
		}
		defer tx.Rollback()
		pack := closePackage{Version: "close_package_v1", Scope: "Saved prepaid, accrued-expense and statement-reconciliation drafts only. Includes uploaded originals, available PDF extraction and captured QuickBooks report JSON. Latest 20 request draft revisions are included in the manifest; these are not sent messages. Captured reports are sequential reads, not independently verified current ledger state. Supported-scope decisions, when present, are recorded separately.", SignOff: "not signed off", Workpapers: []savedPaper{}}
		err = tx.QueryRowContext(ctx, `SELECT id::text,entity,period,transaction_timestamp() FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&pack.Close.ID, &pack.Close.Entity, &pack.Close.Period, &pack.DataCutAt)
		if err == sql.ErrNoRows {
			problem(w, 404, "Close not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not read the close snapshot.")
			return
		}
		status, e := closeReadiness(ctx, tx, pack.Close.ID)
		if e != nil {
			problem(w, 503, "Could not read scope and sign-off history.")
			return
		}
		pack.Readiness = &status
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.id),'[]') FROM close_signoffs s WHERE close_id=$1`, pack.Close.ID).Scan(&pack.RecordedDecisions); e != nil {
			problem(w, 503, "Could not export signed snapshots.")
			return
		}
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.id),'[]') FROM close_scopes s WHERE close_id=$1`, pack.Close.ID).Scan(&pack.ScopeHistory); e != nil {
			problem(w, 503, "Could not export scope history.")
			return
		}
		if len(pack.RecordedDecisions)+len(pack.ScopeHistory) > 16*1024*1024 {
			problem(w, 413, "Decision history exceeds the 16 MiB export limit. Nothing was omitted silently.")
			return
		}
		pack.SignOff = strings.ReplaceAll(status.State, "_", " ")
		rows, err := tx.QueryContext(ctx, `SELECT id::text,kind,created_at,paper,digest,review_state,review_id FROM workpaper_review_state WHERE close_id=$1 ORDER BY id LIMIT 1001`, pack.Close.ID)
		if err != nil {
			problem(w, 503, "Could not read saved workpapers.")
			return
		}
		total := 0
		for rows.Next() {
			var paper savedPaper
			if rows.Scan(&paper.ID, &paper.Kind, &paper.CreatedAt, &paper.Paper, &paper.Digest, &paper.ReviewState, &paper.ReviewID) != nil {
				rows.Close()
				problem(w, 503, "Could not read a saved workpaper.")
				return
			}
			total += len(paper.Paper)
			// ponytail: bounded in-memory archive; use streaming/object storage if a close exceeds this development ceiling.
			if len(pack.Workpapers) == 1000 || total > 16*1024*1024 {
				rows.Close()
				problem(w, 413, "This close exceeds the export limit (1,000 workpapers or 16 MiB). Nothing was exported; no records were silently omitted.")
				return
			}
			pack.Workpapers = append(pack.Workpapers, paper)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			problem(w, 503, "Could not finish the workpaper snapshot.")
			return
		}
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM sandbox_postings p WHERE close_id=$1`, pack.Close.ID).Scan(&pack.SandboxPostings)
		if err != nil {
			problem(w, 503, "Could not read sandbox posting receipts.")
			return
		}
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id),'[]'::jsonb) FROM workpaper_reviews r JOIN workpapers w ON w.id=r.workpaper_id WHERE w.close_id=$1`, pack.Close.ID).Scan(&pack.Reviews)
		if err != nil {
			problem(w, 503, "Could not read review history.")
			return
		}
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(d) ORDER BY d.id DESC),'[]'::jsonb) FROM (SELECT * FROM request_drafts WHERE close_id=$1 ORDER BY id DESC LIMIT 20) d`, pack.Close.ID).Scan(&pack.RequestDrafts)
		if err != nil {
			problem(w, 503, "Could not read request drafts.")
			return
		}

		err = tx.QueryRowContext(ctx, `SELECT jsonb_build_object(
            'jobs',COALESCE((SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM preparation_jobs j WHERE j.close_id=$1),'[]'::jsonb),
            'answers',COALESCE((SELECT jsonb_agg(to_jsonb(a)) FROM preparation_answers a JOIN preparation_jobs j ON j.id=a.job_id WHERE j.close_id=$1),'[]'::jsonb),
            'intakes',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.job_id) FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id=$1),'[]'::jsonb),
            'requests',COALESCE((SELECT jsonb_agg(to_jsonb(q)||jsonb_build_object('body',d.body) ORDER BY q.id) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'::jsonb),
            'request_links',COALESCE((SELECT jsonb_agg(jsonb_build_object('request_id',l.request_id,'issued_by',l.issued_by,'issued_at',l.issued_at,'expires_at',l.expires_at,'revoked_at',l.revoked_at,'used_at',l.used_at) ORDER BY l.issued_at) FROM client_request_links l JOIN work_requests q ON q.id=l.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'::jsonb),
            'quickbooks_snapshots',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'realm',s.realm,'digest',s.digest,'started_at',s.started_at,'captured_at',s.captured_at,'actor',s.actor,'file','ledger/snapshot-'||s.id::text||'.json') ORDER BY s.id) FROM quickbooks_snapshots s WHERE s.close_id=$1),'[]'::jsonb),
            'coverage_bindings',COALESCE((SELECT jsonb_agg(to_jsonb(b) ORDER BY b.id) FROM coverage_bindings b WHERE b.close_id=$1),'[]'),
            'request_targets',COALESCE((SELECT jsonb_agg(to_jsonb(t)) FROM work_request_targets t JOIN work_requests q ON q.id=t.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'),
            'client_attachments',COALESCE((SELECT jsonb_agg(to_jsonb(a)-'body'-'claim' ORDER BY a.id) FROM client_attachments a WHERE a.close_id=$1),'[]'),
            'close_preparations',COALESCE((SELECT jsonb_agg(to_jsonb(g) ORDER BY g.id) FROM close_preparations g WHERE g.close_id=$1),'[]'::jsonb),
            'emails',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'request_id',m.request_id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'approved_by',m.approved_by,'approved_until',m.approved_until,'attempts',m.attempts,'provider_id',m.provider_id,'error',m.error,'created_at',m.created_at,'finished_at',m.finished_at) ORDER BY m.created_at) FROM email_delivery m JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'::jsonb),
            'replies',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM work_replies r JOIN work_requests q ON q.id=r.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'::jsonb))`, pack.Close.ID).Scan(&pack.WorkHistory)
		if err != nil {
			problem(w, 503, "Could not read work and request history.")
			return
		}
		if len(pack.WorkHistory) > 16*1024*1024 {
			problem(w, 413, "Work history exceeds the 16 MiB export limit; nothing was omitted.")
			return
		}
		ledgerRows, err := tx.QueryContext(ctx, `SELECT id::text,payload::text,digest FROM quickbooks_snapshots WHERE close_id=$1 ORDER BY id LIMIT 21`, pack.Close.ID)
		if err != nil {
			problem(w, 503, "Could not read captured ledger reports.")
			return
		}
		ledgerBytes := 0
		for ledgerRows.Next() {
			var snapshot packageLedger
			var digest string
			if err = ledgerRows.Scan(&snapshot.ID, &snapshot.Payload, &digest); err != nil {
				break
			}
			if preparation.Digest(snapshot.Payload) != digest {
				ledgerRows.Close()
				problem(w, 409, "Captured ledger report digest does not match its stored content. Investigate before exporting.")
				return
			}
			ledgerBytes += len(snapshot.Payload)
			pack.LedgerSnapshots = append(pack.LedgerSnapshots, snapshot)
		}
		readErr := ledgerRows.Err()
		ledgerRows.Close()
		if err != nil || readErr != nil {
			problem(w, 503, "Could not finish the ledger snapshot.")
			return
		}
		if len(pack.LedgerSnapshots) > 20 || ledgerBytes > 16*1024*1024 {
			problem(w, 413, "Captured ledger reports exceed the export limit; nothing was omitted.")
			return
		}

		evidenceRows, err := tx.QueryContext(ctx, `SELECT id::text,name,digest,content,original,extraction_state,extraction_error,superseded_by::text,replacement_reason FROM evidence WHERE close_id=$1 ORDER BY id`, pack.Close.ID)
		if err != nil {
			problem(w, 503, "Could not read source evidence.")
			return
		}
		pack.Evidence = []packageEvidence{}
		for evidenceRows.Next() {
			var source packageEvidence
			if err = evidenceRows.Scan(&source.ID, &source.Name, &source.Digest, &source.Content, &source.Original, &source.ExtractionState, &source.ExtractionError, &source.SupersededBy, &source.ReplacementReason); err != nil {
				evidenceRows.Close()
				problem(w, 503, "Could not read source evidence.")
				return
			}
			pack.Evidence = append(pack.Evidence, source)
		}
		err = evidenceRows.Err()
		evidenceRows.Close()
		if err != nil {
			problem(w, 503, "Could not finish source evidence snapshot.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Could not complete the snapshot.")
			return
		}
		archive, err := buildClosePackage(pack)
		if err != nil {
			problem(w, 500, "A saved workpaper could not be exported. No partial package was downloaded.")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="close-%s-%s.zip"`, pack.Close.ID, pack.Close.Period))
		w.Write(archive)
	}
}
func buildClosePackage(pack closePackage) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	write := func(name string, data []byte) error {
		file, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		return err
	}
	csvFile := func(name string, records [][]string) error {
		var data bytes.Buffer
		writer := csv.NewWriter(&data)
		writer.WriteAll(records)
		if err := writer.Error(); err != nil {
			return err
		}
		return write(name, data.Bytes())
	}
	manifest, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = write("manifest.json", manifest); err != nil {
		return nil, err
	}
	for _, source := range pack.Evidence {
		if source.Original != nil {
			if err = write("evidence/"+source.ID+".pdf", source.Original); err != nil {
				return nil, err
			}
			if err = write("evidence/"+source.ID+"-extracted.txt", []byte(source.Content)); err != nil {
				return nil, err
			}
			continue
		}
		if err = write("evidence/"+source.ID+".txt", []byte(source.Content)); err != nil {
			return nil, err
		}
	}
	for _, snapshot := range pack.LedgerSnapshots {
		if err = write("ledger/snapshot-"+snapshot.ID+".json", snapshot.Payload); err != nil {
			return nil, err
		}
	}
	papers := []reviewPaper{}
	for _, saved := range pack.Workpapers {
		root := "workpapers/" + saved.ID
		if err = write(root+".json", saved.Paper); err != nil {
			return nil, err
		}
		summary := reviewPaper{ID: saved.ID, Kind: saved.Kind}
		switch saved.Kind {
		case "prepaid":
			var paper prepaids.Workpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			var carried struct {
				CarryForward *preparation.CarryForward `json:"carry_forward"`
			}
			json.Unmarshal(saved.Paper, &carried)
			summary.Source = paper.Input.SourceReference
			summary.Method = paper.Input.Method
			summary.Issues = []string{"USD accrual basis; selected method, any credit and booked balance are user-confirmed. Review all ledger and credit evidence.", "Draft calculation; not firm approval or authorization to post."}
			if carried.CarryForward != nil {
				summary.Issues = []string{"Carried schedule only; opening scheduled prepaid balance " + carried.CarryForward.OpeningBalance + " USD is not a verified ledger balance.", "Prior-period draft #" + carried.CarryForward.PaperID + " from close #" + carried.CarryForward.CloseID + "; fresh review required. No journal proposal or inherited approval."}
			}
			records := [][]string{{"Period", "Expense USD", "Remaining USD", "Credit USD"}}
			for _, row := range paper.Schedule {
				records = append(records, []string{row.Period, row.Expense, row.Remaining, row.Credit})
			}
			if err = csvFile(root+"-schedule.csv", records); err != nil {
				return nil, err
			}
		case "accrual":
			var paper accruals.Workpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			summary.Source = paper.Input.SourceReference
			summary.Method = paper.Input.Method
			summary.Issues = []string{"Fixed incurred amount and booked liability require evidence review. No posting is authorized.", "Any next-month reversal reverses only this proposed adjustment, not the entire booked liability."}
		case "reconciliation":
			var paper reconciliation.Workpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			summary.Source = paper.Input.StatementReference + " / " + paper.Input.LedgerReference
			summary.Method = paper.Method
			summary.Issues = append([]string{"USD bank-account method; match candidates are not confirmed matches or native reconciliation."}, paper.Blockers...)
			if len(paper.Blockers) == 0 {
				summary.Issues = append(summary.Issues, "No arithmetic blockers in imported rows; source completeness still requires review.")
			}
			records := [][]string{{"Statement ID", "Ledger ID", "Reason"}}
			for _, match := range paper.Matches {
				records = append(records, []string{csvText(match.StatementID), csvText(match.LedgerID), csvText(match.Reason)})
			}
			if err = csvFile(root+"-matches.csv", records); err != nil {
				return nil, err
			}
			for _, source := range []struct {
				name  string
				lines []reconciliation.Line
			}{{"statement", paper.UnmatchedStatement}, {"ledger", paper.UnmatchedLedger}} {
				records = [][]string{{"ID", "Date", "Amount USD"}}
				for _, line := range source.lines {
					records = append(records, []string{csvText(line.ID), line.Date, line.Amount})
				}
				if err = csvFile(root+"-unmatched-"+source.name+".csv", records); err != nil {
					return nil, err
				}
			}
			records = [][]string{{"Side", "Source ID", "Lead", "Candidate IDs (first 5)", "Candidate count", "Question"}}
			for _, item := range paper.Exceptions {
				records = append(records, []string{item.Side, csvText(item.ID), item.Kind, csvText(strings.Join(item.CandidateIDs, "; ")), fmt.Sprint(item.CandidateCount), csvText(item.Question)})
			}
			if err = csvFile(root+"-exceptions.csv", records); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported workpaper kind")
		}
		summary.Issues = append(summary.Issues, "Current review state: "+saved.ReviewState+". Workpaper review is not posting approval or firm sign-off.")
		papers = append(papers, summary)
	}
	var receiptJSON bytes.Buffer
	if err = json.Indent(&receiptJSON, pack.SandboxPostings, "", "  "); err != nil {
		return nil, err
	}
	var review bytes.Buffer
	if err = packageHTML.Execute(&review, struct {
		Package  closePackage
		Papers   []reviewPaper
		Receipts string
	}{pack, papers, receiptJSON.String()}); err != nil {
		return nil, err
	}
	if err = write("review.html", review.Bytes()); err != nil {
		return nil, err
	}
	if err = archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
func csvText(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
