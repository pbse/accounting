package transport

import (
	"archive/zip"
	"bytes"
	"close.local/backend/internal/ledger"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/payroll"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/goregular"
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
	ReleaseReady      bool              `json:"release_ready"`
	RecordedDecisions json.RawMessage   `json:"recorded_decisions"`
	ScopeHistory      json.RawMessage   `json:"scope_history"`
	Readiness         *readiness        `json:"readiness,omitempty"`
	Evidence          []packageEvidence `json:"evidence"`
	LedgerSnapshots   []packageLedger   `json:"-"`

	Version              string          `json:"version"`
	Close                savedClose      `json:"close"`
	DataCutAt            time.Time       `json:"data_cut_at"`
	Scope                string          `json:"scope"`
	SignOff              string          `json:"sign_off"`
	Workpapers           []savedPaper    `json:"workpapers"`
	SandboxPostings      json.RawMessage `json:"sandbox_postings"`
	VendorCredits        json.RawMessage `json:"vendor_credits"`
	BillCorrections      json.RawMessage `json:"bill_corrections"`
	CreditApplications   json.RawMessage `json:"credit_applications"`
	ProductionPostings   json.RawMessage `json:"production_postings"`
	XeroPostings         json.RawMessage `json:"xero_postings"`
	XeroCorrections      json.RawMessage `json:"xero_corrections"`
	LedgerCertification  map[string]any  `json:"ledger_certification"`
	LedgerCompleteness   string          `json:"ledger_completeness"`
	HistoricalExports    *exportLabel    `json:"historical_exports"`
	NativeReconciliation map[string]any  `json:"native_reconciliation"`
	NativeSentences      []string        `json:"native_reconciliation_statements"`
	Reviews              json.RawMessage `json:"reviews"`
	WorkHistory          json.RawMessage `json:"work_history"`
	RequestDrafts        json.RawMessage `json:"request_drafts"`
}
type reviewPaper struct {
	ID, Kind, Source, Method string
	Issues                   []string
	// Scope says whether the declared sign-off scope covers the draft, once one is declared.
	Scope string
}

var packageHTML = template.Must(template.New("review").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>{{if .Package.ReleaseReady}}Accountant binder{{else}}Close review package{{end}}</title><style>body{font:16px system-ui;max-width:960px;margin:3rem auto;padding:0 1rem;color:#203c33}section{border-top:1px solid #ccc;margin-top:2rem}pre{white-space:pre-wrap;overflow-wrap:anywhere}button{padding:.6rem}@media print{button{display:none}section{break-inside:avoid}}</style><h1>{{.Package.Close.Entity}} · {{.Package.Close.Period}}</h1><p>Saved-work snapshot: {{.Package.DataCutAt}}</p><p>{{.Package.Scope}}</p><p><strong>Firm sign-off: {{.Package.SignOff}}</strong></p>{{if .Package.ReleaseReady}}<p>Accountant binder for the signed-off supported scope. Decision #{{.Package.Readiness.DecisionID}} binds snapshot digest {{.Package.Readiness.Digest}}. This is not an audit or assurance opinion. Verify current provider facts before external reliance.</p>{{end}}{{if .Package.Readiness}}<h2>Supported scope</h2><p>{{.Package.Readiness.Scope.Description}}</p><p>Required draft IDs: {{.Package.Readiness.Scope.RequiredPapers}}</p><ul>{{range .Package.Readiness.Scope.ExternalSteps}}<li>{{.Name}} · complete: {{.Complete}} · {{.Reference}}</li>{{end}}{{range .Package.Readiness.Blockers}}<li>{{.}}</li>{{end}}</ul><p>Decision history and the exact snapshot are preserved in manifest.json. Local development decisions are not verified individual firm identities.</p>{{end}}<p>All saved versions are included. Versions are not additive; do not sum alternative drafts. Source references are user-supplied, not verified attachments. Originals are in evidence/{id}.txt or .pdf; PDF extraction is in evidence/{id}-extracted.txt; no documents outside these uploads are included.</p><p>Portable workpapers.pdf contains every saved draft. The JSON files and checksums preserve exact calculations and provenance.</p><h2>Workpapers · {{len .Papers}}</h2>{{if not .Papers}}<p>No saved workpapers. This close has no prepared evidence in this package.</p>{{end}}{{range .Papers}}<section><h3>{{.Kind}} · draft #{{.ID}}</h3>{{if .Scope}}<p><strong>{{.Scope}}</strong></p>{{end}}<p>Source: {{.Source}}</p><p>Method: {{.Method}}</p><ul>{{range .Issues}}<li>{{.}}</li>{{end}}</ul><p>Full inputs and calculations: workpapers/{{.ID}}.json. Tables are in the same directory.</p></section>{{end}}<h2>Sandbox posting receipts</h2><p>These are stored sandbox states at export time, not a new provider readback. Executing or unknown means unresolved. Sandbox verification does not imply production posting or close sign-off.</p><pre>{{.Receipts}}</pre><h2>Native supplier credit receipts</h2><p>These are stored sandbox VendorCredit attempts and readbacks; unknown outcomes require investigation.</p><pre>{{.VendorReceipts}}</pre><h2>Native Bill correction receipts</h2><p>Stored sandbox Bill attempts and readbacks; unknown outcomes require investigation.</p><pre>{{.Package.BillCorrections}}</pre><h2>Production posting receipts</h2><p>Stored attempts and canonical readback at verification time; export does not perform a new QuickBooks read. Unknown outcomes need investigation.</p><pre>{{.Package.ProductionPostings}}</pre><h2>Ledger certification: agreement between stored reads</h2><p>{{.Package.LedgerCompleteness}}</p><h2>Native reconciliation flags</h2><p>Read from the ledger application, separate from this package's reconciliation workpapers and their reviewer-cited native status.</p><ul>{{range .Package.NativeSentences}}<li>{{.}}</li>{{end}}</ul><h2>Xero journal receipts</h2><p>Stored attempts and readback at verification time; export does not perform a new Xero read. Unknown outcomes need investigation.</p><pre>{{.Package.XeroPostings}}</pre><h2>Xero bill correction receipts</h2><p>Stored attempts and readback at verification time; unknown outcomes need investigation.</p><pre>{{.Package.XeroCorrections}}</pre><h2>Review still required</h2><ul><li>Verify source completeness and ledger freshness independently.</li><li>Review accounting assumptions, alternative draft versions and unresolved items.</li><li>Resolve uncertain sandbox attempts without blindly retrying a write.</li><li>Confirm that recorded sign-off covers your intended scope; unlisted work is not certified.</li></ul></html>`))

func exportClose(db *sql.DB, release bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent close snapshot.")
			return
		}
		defer tx.Rollback()
		pack := closePackage{Version: "close_package_v2", Scope: "Saved prepaid, accrued-expense and statement-reconciliation work within the recorded supported scope. Includes uploaded originals, available PDF extraction, captured QuickBooks report JSON, transaction capture digests and request draft history. Full transaction captures are separately downloadable from their close. Captured reports are sequential reads, not independently verified current ledger state.", SignOff: "not signed off", Workpapers: []savedPaper{}}
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
		if release {
			if status.State != "signed_off" || len(status.Blockers) != 0 {
				problem(w, 409, "This close is not release-ready. Resolve blockers and obtain a current accountant sign-off, then refresh readiness.")
				return
			}
			pack.Version, pack.ReleaseReady = "accountant_binder_v1", true
		}
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
		rows, err := tx.QueryContext(ctx, `SELECT id::text,kind,created_at,paper,digest,review_state,review_id FROM workpaper_review_state WHERE close_id=$1 ORDER BY workpaper_review_state.id LIMIT 1001`, pack.Close.ID)
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
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM vendor_credit_postings p WHERE close_id=$1`, pack.Close.ID).Scan(&pack.VendorCredits)
		if err != nil {
			problem(w, 503, "Could not read supplier credit receipts.")
			return
		}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM credit_applications p WHERE close_id=$1`, pack.Close.ID).Scan(&pack.CreditApplications); err != nil {
			problem(w, 503, "Could not read credit application receipts.")
			return
		}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM bill_corrections p WHERE close_id=$1`, pack.Close.ID).Scan(&pack.BillCorrections); err != nil {
			problem(w, 503, "Could not read Bill correction receipts.")
			return
		}
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM production_postings p WHERE close_id=$1`, pack.Close.ID).Scan(&pack.ProductionPostings)
		if err != nil {
			problem(w, 503, "Could not read production posting receipts.")
			return
		}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM xero_postings p WHERE close_id=$1`, pack.Close.ID).Scan(&pack.XeroPostings); err != nil {
			problem(w, 503, "Could not read Xero journal receipts.")
			return
		}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM xero_corrections p WHERE close_id=$1`, pack.Close.ID).Scan(&pack.XeroCorrections); err != nil {
			problem(w, 503, "Could not read Xero bill correction receipts.")
			return
		}
		if pack.LedgerCertification, err = ledgerCertification(ctx, tx, pack.Close.ID); err != nil {
			problem(w, 503, "Could not read ledger certification.")
			return
		}
		pack.LedgerCompleteness = certificationSentence(pack.LedgerCertification)
		if pack.HistoricalExports, err = latestExportLabel(ctx, tx, pack.Close.ID); err != nil {
			problem(w, 503, "Could not read the historical exports label.")
			return
		}
		if pack.HistoricalExports != nil {
			pack.LedgerCompleteness += " " + pack.HistoricalExports.sentence()
		}
		if pack.NativeReconciliation, err = nativeReconciliation(ctx, tx, pack.Close.ID); err != nil {
			problem(w, 503, "Could not read native reconciliation status.")
			return
		}
		pack.NativeSentences = nativeSentences(pack.NativeReconciliation)
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id),'[]'::jsonb) FROM workpaper_reviews r JOIN workpapers w ON w.id=r.workpaper_id WHERE w.close_id=$1`, pack.Close.ID).Scan(&pack.Reviews)
		if err != nil {
			problem(w, 503, "Could not read review history.")
			return
		}
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(d) ORDER BY d.id),'[]'::jsonb) FROM (SELECT * FROM request_drafts WHERE close_id=$1 ORDER BY id LIMIT 1001) d`, pack.Close.ID).Scan(&pack.RequestDrafts)
		if err != nil {
			problem(w, 503, "Could not read request drafts.")
			return
		}
		var drafts []json.RawMessage
		if json.Unmarshal(pack.RequestDrafts, &drafts) != nil {
			problem(w, 503, "Could not decode request drafts.")
			return
		}
		if len(drafts) > 1000 || len(pack.RequestDrafts) > 16*1024*1024 {
			problem(w, 413, "Request history exceeds the export limit; nothing was omitted.")
			return
		}

		err = tx.QueryRowContext(ctx, `SELECT jsonb_build_object(
            'jobs',COALESCE((SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM preparation_jobs j WHERE j.close_id=$1),'[]'::jsonb),
            'answers',COALESCE((SELECT jsonb_agg(to_jsonb(a)) FROM preparation_answers a JOIN preparation_jobs j ON j.id=a.job_id WHERE j.close_id=$1),'[]'::jsonb),
            'intakes',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.job_id) FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id=$1),'[]'::jsonb),
            'requests',COALESCE((SELECT jsonb_agg(to_jsonb(q)||jsonb_build_object('body',d.body) ORDER BY q.id) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'::jsonb),
            'request_links',COALESCE((SELECT jsonb_agg(jsonb_build_object('request_id',l.request_id,'issued_by',l.issued_by,'issued_at',l.issued_at,'expires_at',l.expires_at,'revoked_at',l.revoked_at,'used_at',l.used_at) ORDER BY l.issued_at) FROM client_request_links l JOIN work_requests q ON q.id=l.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'::jsonb),
            'quickbooks_snapshots',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'realm',s.realm,'digest',s.digest,'started_at',s.started_at,'captured_at',s.captured_at,'actor',s.actor,'file','ledger/snapshot-'||s.id::text||'.json') ORDER BY s.id) FROM quickbooks_snapshots s WHERE s.close_id=$1),'[]'::jsonb),
            'quickbooks_entity_syncs',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',s.id::text,'period',s.period,'state',s.state,'attempts',s.attempts,'digest',s.digest,'evidence_id',s.evidence_id::text,'counts',s.payload->'counts','created_at',s.created_at,'finished_at',s.finished_at) ORDER BY s.id) FROM quickbooks_entity_syncs s WHERE s.close_id=$1),'[]'::jsonb),
			'forwarded_files',COALESCE((SELECT jsonb_agg(to_jsonb(f)-'body'-'claim' ORDER BY f.id) FROM forwarded_files f WHERE f.close_id=$1),'[]'::jsonb),
			'inbound_replies',COALESCE((SELECT jsonb_agg((to_jsonb(i)-'body') || CASE WHEN i.state='released' THEN jsonb_build_object('body',i.body) ELSE '{}'::jsonb END ORDER BY i.id) FROM inbound_replies i WHERE i.close_id=$1),'[]'::jsonb),
            'forwarding_links',COALESCE((SELECT jsonb_agg(to_jsonb(l) ORDER BY l.issued_at) FROM forwarding_links l WHERE l.close_id=$1),'[]'::jsonb),
            'coverage_bindings',COALESCE((SELECT jsonb_agg(to_jsonb(b) ORDER BY b.id) FROM coverage_bindings b WHERE b.close_id=$1),'[]'),
            'request_targets',COALESCE((SELECT jsonb_agg(to_jsonb(t)) FROM work_request_targets t JOIN work_requests q ON q.id=t.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1),'[]'),
            'client_attachments',COALESCE((SELECT jsonb_agg(to_jsonb(a)-'body'-'claim' ORDER BY a.id) FROM client_attachments a WHERE a.close_id=$1),'[]'),
            'close_preparations',COALESCE((SELECT jsonb_agg(to_jsonb(g) ORDER BY g.id) FROM close_preparations g WHERE g.close_id=$1),'[]'::jsonb),
            'close_goals',COALESCE((SELECT jsonb_agg(to_jsonb(g) ORDER BY g.id) FROM close_goals g WHERE g.close_id=$1),'[]'::jsonb),
            'close_owner_changes',COALESCE((SELECT jsonb_agg(to_jsonb(o) ORDER BY o.id) FROM close_owner_changes o WHERE o.close_id=$1),'[]'::jsonb),
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
		ledgerRows, err := tx.QueryContext(ctx, `SELECT id::text,payload::text,digest FROM quickbooks_snapshots WHERE close_id=$1 ORDER BY quickbooks_snapshots.id LIMIT 21`, pack.Close.ID)
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

		evidenceRows, err := tx.QueryContext(ctx, `SELECT id::text,name,digest,content,original,extraction_state,extraction_error,superseded_by::text,replacement_reason FROM evidence WHERE close_id=$1 ORDER BY evidence.id LIMIT 101`, pack.Close.ID)
		if err != nil {
			problem(w, 503, "Could not read source evidence.")
			return
		}
		pack.Evidence = []packageEvidence{}
		evidenceBytes := 0
		for evidenceRows.Next() {
			var source packageEvidence
			if err = evidenceRows.Scan(&source.ID, &source.Name, &source.Digest, &source.Content, &source.Original, &source.ExtractionState, &source.ExtractionError, &source.SupersededBy, &source.ReplacementReason); err != nil {
				evidenceRows.Close()
				problem(w, 503, "Could not read source evidence.")
				return
			}
			evidenceBytes += len(source.Content) + len(source.Original)
			if len(pack.Evidence) == 100 || evidenceBytes > 40*1024*1024 {
				evidenceRows.Close()
				problem(w, 413, "Source evidence exceeds the export limit (100 files or 40 MiB). Nothing was omitted silently.")
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
		kind := "close-review"
		if release {
			kind = "accountant-binder"
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s-%s.zip"`, kind, pack.Close.ID, pack.Close.Period))
		w.Write(archive)
	}
}
func buildClosePackage(pack closePackage) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	var checksums bytes.Buffer
	write := func(name string, data []byte) error {
		file, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		if err == nil {
			fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(data), name)
		}
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
			summary.Method = strings.TrimPrefix(paper.Input.Schedule+" · "+paper.Input.Method, " · ")
			summary.Issues = []string{paper.Input.Currency + " accrual basis; selected method, any credit and booked balance are user-confirmed. Review all ledger and credit evidence.", "Draft calculation; not firm approval or authorization to post."}
			if carried.CarryForward != nil {
				summary.Issues = []string{"Carried schedule only; opening scheduled balance " + carried.CarryForward.OpeningBalance + " " + paper.Input.Currency + " is not a verified ledger balance.", "Prior-period draft #" + carried.CarryForward.PaperID + " from close #" + carried.CarryForward.CloseID + "; fresh review required. No journal proposal or inherited approval."}
			}
			for _, e := range paper.Disposal {
				summary.Issues = append(summary.Issues, fmt.Sprintf("Disposal entry (book by hand) %s: Dr %s / Cr %s %s", e.Date, e.DebitAccount, e.CreditAccount, ledger.Show(e.Amount, paper.Input.Currency)))
			}
			records := [][]string{{"Period", map[string]string{"": "Expense", prepaids.DeferredRevenue: "Revenue", prepaids.Depreciation: "Depreciation"}[paper.Input.Schedule] + " " + paper.Input.Currency, "Remaining " + paper.Input.Currency, "Credit " + paper.Input.Currency}}
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
			basis := map[string]string{accruals.Method: "Fixed incurred amount", accruals.ActualMethod: "Actual invoice against its prior estimate", accruals.RecurringMethod: "Fixed recurring incurred amount", accruals.ProRataMethod: "Pro-rata contract share by service days through month-end, less " + paper.Input.InvoicedToDate + " invoiced to date,"}[paper.Input.Method]
			summary.Issues = []string{basis + " and booked liability require evidence review. No posting is authorized.", "Any next-month reversal reverses only this proposed adjustment, not the entire booked liability."}
		case "reconciliation":
			var paper reconciliation.Workpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			summary.Source = paper.Input.StatementReference + " / " + paper.Input.LedgerReference
			summary.Method = paper.Method
			summary.Issues = append([]string{paper.Input.Currency + " " + paper.Method + "; match candidates are not confirmed matches or native reconciliation."}, paper.Blockers...)
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
				records = [][]string{{"ID", "Date", "Amount " + paper.Input.Currency}}
				for _, line := range source.lines {
					records = append(records, []string{csvText(line.ID), line.Date, line.Amount})
				}
				if err = csvFile(root+"-unmatched-"+source.name+".csv", records); err != nil {
					return nil, err
				}
			}
			records = [][]string{{"Side", "Source ID", "Lead", "Candidate IDs (first 5)", "Candidate count", "Exact groups (first 5)", "Group count", "Net splits (first 5)", "Net split count", "Question"}}
			for _, item := range paper.Exceptions {
				groups := make([]string, 0, len(item.CandidateGroups))
				for _, group := range item.CandidateGroups {
					groups = append(groups, strings.Join(group, " + "))
				}
				splits := make([]string, 0, len(item.SplitGroups))
				for _, group := range item.SplitGroups {
					splits = append(splits, strings.Join(group, " + "))
				}
				records = append(records, []string{item.Side, csvText(item.ID), item.Kind, csvText(strings.Join(item.CandidateIDs, "; ")), fmt.Sprint(item.CandidateCount), csvText(strings.Join(groups, "; ")), fmt.Sprint(item.GroupCount), csvText(strings.Join(splits, "; ")), fmt.Sprint(item.SplitCount), csvText(item.Question)})
			}
			if err = csvFile(root+"-exceptions.csv", records); err != nil {
				return nil, err
			}
		case "owner":
			var paper owners.Workpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			summary.Source = paper.Input.TransactionReference + " / " + paper.Input.LedgerReference
			summary.Method = paper.Method + " · " + paper.Input.EntityType
			summary.Issues = append([]string{"Owner treatment follows the client's confirmed purpose and recorded entity type; tax treatment is not decided here. Nothing is posted."}, paper.Blockers...)
			if paper.Question != "" {
				summary.Issues = append(summary.Issues, "Client question: "+paper.Question)
			}
			if paper.Proposal != nil {
				summary.Issues = append(summary.Issues, fmt.Sprintf("Proposed %s: Dr %s / Cr %s %s", paper.Proposal.Date, paper.Proposal.DebitAccount, paper.Proposal.CreditAccount, ledger.Show(paper.Proposal.Amount, paper.Input.Currency)))
			}
		case "payroll":
			var paper payroll.Workpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			summary.Source = paper.Input.SourceReference + " / " + paper.Input.LedgerReference
			summary.Method = paper.Method + " · " + paper.Input.Provider
			summary.Issues = append([]string{"Records the provider's payroll summary; payroll is not run here. Nothing is posted."}, paper.Blockers...)
			for _, p := range paper.Proposals {
				summary.Issues = append(summary.Issues, fmt.Sprintf("Proposed %s: Dr %s / Cr %s %s", p.Date, p.DebitAccount, p.CreditAccount, ledger.Show(p.Amount, paper.Input.Currency)))
			}
		case "loan":
			var paper loans.Workpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			summary.Source = paper.Input.SourceReference + " / " + paper.Input.AgreementReference
			summary.Method = paper.Method + " · " + loans.DayCounts[paper.Input.DayCount]
			summary.Issues = append(append([]string{"The lender statement's principal/interest split is authoritative; recomputed interest only raises questions. Proposed entries are not posted."}, paper.Blockers...), paper.Questions...)
			for _, entry := range paper.Proposals {
				summary.Issues = append(summary.Issues, fmt.Sprintf("Proposed %s: Dr %s / Cr %s %s", entry.Date, entry.DebitAccount, entry.CreditAccount, ledger.Show(entry.Amount, paper.Input.Currency)))
			}
			records := [][]string{{"Payment date", "Principal before " + paper.Input.Currency, "Days", "Principal " + paper.Input.Currency, "Lender interest " + paper.Input.Currency, "Note-rate interest " + paper.Input.Currency, "Difference " + paper.Input.Currency}}
			for _, row := range paper.Schedule {
				records = append(records, []string{row.Date, row.PrincipalBefore, fmt.Sprint(row.Days), row.Principal, row.Interest, row.ExpectedInterest, row.Difference})
			}
			if err = csvFile(root+"-payments.csv", records); err != nil {
				return nil, err
			}
		case "settlement":
			var paper reconciliation.SettlementWorkpaper
			if err = json.Unmarshal(saved.Paper, &paper); err != nil {
				return nil, err
			}
			summary.Source = paper.Input.ReportReference + " / " + paper.Input.BankReference
			if paper.Input.ActivityReference != "" {
				summary.Source += " / " + paper.Input.ActivityReference
			}
			summary.Method = paper.Method
			entry := ""
			for _, draft := range []struct {
				label string
				e     *prepaids.Adjustment
			}{{"fee entry", paper.Proposal}, {"refund entry (book by hand)", paper.RefundProposal}, {"revenue entry (book by hand)", paper.RevenueProposal}} {
				if draft.e != nil {
					entry += fmt.Sprintf("Proposed %s: debit %s, credit %s, %s on %s. ", draft.label, draft.e.DebitAccount, draft.e.CreditAccount, ledger.Show(draft.e.Amount, paper.Input.Currency), draft.e.Date)
				}
			}
			if entry == "" {
				entry = "No journal is proposed."
			} else {
				entry += "Rules: " + paper.Input.FeeEntry.RuleReference
				if paper.Input.RefundEntry != nil {
					entry += "; " + paper.Input.RefundEntry.RuleReference
				}
				if paper.Input.RevenueEntry != nil {
					entry += "; " + paper.Input.RevenueEntry.RuleReference
				}
				entry += "."
			}
			traced := " Stripe automatic payouts, with the transactions in each, traced to "
			if paper.Method == reconciliation.PayoutsMethod {
				traced = " Stripe payouts from the Payouts report, without their transactions, traced to "
			}
			summary.Issues = append(append([]string{paper.Input.Currency + traced + paper.Input.BankAccount + ". Matched deposits are candidates until this workpaper is approved; in-transit payouts cite an accountant rule. " + entry}, paper.Blockers...), paper.Questions...)
			// The Payouts report gives each payout's amount only: no transactions, gross or fees.
			lineage := paper.Method != reconciliation.PayoutsMethod
			records := [][]string{{"Payout", "Expected", "Transactions", "Gross " + paper.Input.Currency, "Fee " + paper.Input.Currency, "Net " + paper.Input.Currency, "Status", "Bank row", "Bank date", "After month-end", "Candidates", "Rule", "Reason"}}
			if !lineage {
				records = [][]string{{"Payout", "Expected arrival", "Amount " + paper.Input.Currency, "Status", "Bank row", "Bank date", "After month-end", "Candidates", "Rule", "Reason"}}
			}
			for _, payout := range paper.Payouts {
				row, date, late := "", "", ""
				if payout.Deposit != nil {
					row, date = payout.Deposit.ID, payout.Deposit.Date
					if !strings.HasPrefix(date, paper.Input.ClosePeriod) {
						late = "yes"
					}
				}
				if lineage {
					records = append(records, []string{csvText(payout.ID), payout.ExpectedDate, fmt.Sprint(payout.Count), payout.Gross, payout.Fee, payout.Net, payout.Status, csvText(row), date, late, csvText(strings.Join(payout.CandidateIDs, "; ")), csvText(payout.Policy), csvText(payout.Reason)})
				} else {
					records = append(records, []string{csvText(payout.ID), payout.ExpectedDate, payout.Net, payout.Status, csvText(row), date, late, csvText(strings.Join(payout.CandidateIDs, "; ")), csvText(payout.Policy), csvText(payout.Reason)})
				}
			}
			if err = csvFile(root+"-payouts.csv", records); err != nil {
				return nil, err
			}
			if !lineage && paper.RollForward == nil {
				break
			}
			records = [][]string{{"Stripe category", "Transactions", "Gross " + paper.Input.Currency, "Fee " + paper.Input.Currency, "Net " + paper.Input.Currency}}
			for _, category := range paper.Categories {
				records = append(records, []string{csvText(category.Category), fmt.Sprint(category.Count), category.Gross, category.Fee, category.Net})
			}
			if r := paper.RollForward; r != nil {
				records = append(records, []string{"activity", "", "", "", r.Activity}, []string{"paid out", "", "", "", "-" + r.PaidOut}, []string{"returned", "", "", "", r.Returned}, []string{"balance change (computed, not tied)", "", "", "", r.Change})
			} else {
				records = append(records, []string{"all", fmt.Sprint(paper.Total.Count), paper.Total.Gross, paper.Total.Fee, paper.Total.Net})
			}
			if err = csvFile(root+"-categories.csv", records); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported workpaper kind")
		}
		summary.Issues = append(summary.Issues, "Current review state: "+saved.ReviewState+". Workpaper review is not posting approval or firm sign-off.")
		// Sign-off covers only the scope's required drafts, so a draft left out of it says so rather than reading as closed.
		if s := pack.Readiness; s != nil && s.Scope.Description != "" {
			summary.Scope = "Outside the declared sign-off scope: sign-off does not cover this draft."
			if slices.Contains(s.Scope.RequiredPapers, saved.ID) {
				summary.Scope = "In the declared sign-off scope."
			}
		}
		papers = append(papers, summary)
	}
	var receiptJSON bytes.Buffer
	if err = json.Indent(&receiptJSON, pack.SandboxPostings, "", "  "); err != nil {
		return nil, err
	}
	var review bytes.Buffer
	if err = packageHTML.Execute(&review, struct {
		Package        closePackage
		Papers         []reviewPaper
		Receipts       string
		VendorReceipts string
	}{pack, papers, receiptJSON.String(), string(pack.VendorCredits)}); err != nil {
		return nil, err
	}
	if err = write("review.html", review.Bytes()); err != nil {
		return nil, err
	}
	pdf, err := workpapersPDF(pack.Workpapers, papers)
	if err != nil {
		return nil, err
	}
	if err = write("workpapers.pdf", pdf); err != nil {
		return nil, err
	}
	if err = write("checksums.sha256", checksums.Bytes()); err != nil {
		return nil, err
	}
	if err = archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func workpapersPDF(saved []savedPaper, summaries []reviewPaper) ([]byte, error) {
	pages := [][]string{}
	if len(saved) == 0 {
		pages = append(pages, []string{"No saved workpapers in this close."})
	}
	for index, paper := range saved {
		values := []string{"Draft #" + paper.ID + " - " + paper.Kind, "Review: " + paper.ReviewState + "; digest: " + paper.Digest}
		if summaries[index].Scope != "" {
			values = append(values, summaries[index].Scope)
		}
		values = append(values, "Source: "+summaries[index].Source, "Method: "+summaries[index].Method, "Full saved calculation and inputs:")
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, paper.Paper, "", "  "); err != nil {
			return nil, err
		}
		pages = append(pages, append(values, strings.Split(pretty.String(), "\n")...))
	}
	return linesPDF(pages)
}

// linesPDF writes each page's lines in order, wrapping long ones and continuing onto further pages.
func linesPDF(pages [][]string) ([]byte, error) {
	var pdf gopdf.GoPdf
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("Go", goregular.TTF); err != nil {
		return nil, err
	}
	if err := pdf.SetFont("Go", "", 9); err != nil {
		return nil, err
	}
	page := func() { pdf.AddPage() }
	y := 40.0
	line := func(value string) error {
		characters := []rune(value)
		if len(characters) == 0 {
			y += 12
			return nil
		}
		for len(characters) > 0 {
			if y > 770 {
				page()
				y = 40
			}
			n := len(characters)
			if n > 120 {
				n = 120
			}
			for n > 1 {
				width, err := pdf.MeasureTextWidth(string(characters[:n]))
				if err != nil {
					return err
				}
				if width <= 510 {
					break
				}
				n--
			}
			pdf.SetX(40)
			pdf.SetY(y)
			if err := pdf.Cell(&gopdf.Rect{W: 510, H: 12}, string(characters[:n])); err != nil {
				return err
			}
			y += 12
			characters = characters[n:]
		}
		return nil
	}
	for _, lines := range pages {
		page()
		y = 40
		for _, value := range lines {
			if err := line(value); err != nil {
				return nil, err
			}
		}
	}
	return pdf.GetBytesPdfReturnErr()
}
func csvText(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
