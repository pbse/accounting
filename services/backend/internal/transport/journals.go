package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/ledgers"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

// Writing back by file (docs/books-sources.md, Writing back; delivery step 10): a month whose books are its files has no
// ledger to post to, so its approved drafts' month-end entries are downloaded as a journal import batch for the client's
// ledger, each journal carrying its reference in its memo; a person records each as entered with the ledger's own
// journal reference.

// journalLine is one line of a journal as a ledger's journal import takes it.
type journalLine struct {
	Date    string `json:"date"`
	Account string `json:"account"`
	Debit   string `json:"debit,omitempty"`
	Credit  string `json:"credit,omitempty"`
	Memo    string `json:"memo"`
}

// journalDraft is an approved draft whose entries are to be exported, or why they cannot be.
type journalDraft struct {
	PaperID string        `json:"paper_id"`
	Kind    string        `json:"kind"`
	Source  string        `json:"source"`
	Lines   []journalLine `json:"lines"`
	Held    string        `json:"held,omitempty"`
	digest  string
}

// fileJournal is an exported journal and where it stands.
type fileJournal struct {
	ID              string          `json:"id"`
	Batch           int             `json:"batch"`
	Reference       string          `json:"reference"`
	PaperID         string          `json:"paper_id"`
	State           string          `json:"state"`
	LedgerReference string          `json:"ledger_reference"`
	Lines           json.RawMessage `json:"lines"`
	ExportedBy      string          `json:"exported_by"`
	ExportedAt      time.Time       `json:"exported_at"`
	EnteredBy       string          `json:"entered_by,omitempty"`
	EnteredAt       *time.Time      `json:"entered_at,omitempty"`
}

// draftEntries are an approved draft's month-end entries, as a posting would post them: a prepaid's proposal and any
// disposal, else what postable gives; with the draft's source reference.
func draftEntries(kind string, raw []byte, period string) ([]prepaids.Adjustment, string, error) {
	if kind != "prepaid" {
		return postable(kind, raw, period)
	}
	var paper prepaids.Workpaper
	if json.Unmarshal(raw, &paper) != nil {
		return nil, "", errors.New("This prepaid draft cannot be read")
	}
	entries := append([]prepaids.Adjustment{}, paper.Disposal...)
	if paper.Proposal != nil {
		entries = append([]prepaids.Adjustment{*paper.Proposal}, entries...)
	}
	if len(entries) == 0 {
		return nil, "", errors.New("This prepaid draft proposes no entry")
	}
	month, err := time.Parse("2006-01", period)
	if err != nil {
		return nil, "", err
	}
	for _, e := range entries {
		if e.Date != month.AddDate(0, 1, -1).Format("2006-01-02") {
			return nil, "", errors.New("Only entries dated at the close month-end are exported")
		}
	}
	return entries, paper.Input.SourceReference, nil
}

// pendingJournals are close id's current approved drafts whose entries are in no journal (but one found uncertain), each
// with its lines, or held with why: a procedure since changed or revoked authorizes none.
func pendingJournals(ctx context.Context, tx *sql.Tx, closeID, period string) ([]journalDraft, error) {
	rows, err := tx.QueryContext(ctx, `SELECT w.id::text,w.kind,w.digest,w.paper FROM workpaper_review_state w WHERE w.close_id::text=$1 AND w.review_state='approved'
 AND (jsonb_typeof(w.paper->'blockers') IS DISTINCT FROM 'array' OR w.paper->'blockers'='[]'::jsonb) AND w.kind IN ('prepaid','accrual','loan','owner','payroll','settlement')
 AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text)
 AND NOT EXISTS(SELECT 1 FROM file_journals j WHERE j.close_id=w.close_id AND j.workpaper_id=w.id AND j.state<>'uncertain') ORDER BY w.id`, closeID)
	if err != nil {
		return nil, err
	}
	type draft struct {
		id, kind, digest string
		paper            []byte
	}
	var found []draft
	for rows.Next() {
		var d draft
		if err = rows.Scan(&d.id, &d.kind, &d.digest, &d.paper); err != nil {
			break
		}
		found = append(found, d)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []journalDraft{}
	for _, d := range found {
		j := journalDraft{PaperID: d.id, Kind: d.kind, Lines: []journalLine{}, digest: d.digest}
		var policy preparation.PolicyRequired
		if err := preparation.ApprovalAuthority(ctx, tx, closeID, d.kind, d.paper); errors.As(err, &policy) {
			j.Held = err.Error()
		} else if err != nil {
			return nil, err
		}
		entries, source, err := draftEntries(d.kind, d.paper, period)
		if err != nil && j.Held == "" {
			j.Held = err.Error()
		}
		j.Source = source
		for _, e := range entries {
			j.Lines = append(j.Lines, journalLine{Date: e.Date, Account: e.DebitAccount, Debit: e.Amount}, journalLine{Date: e.Date, Account: e.CreditAccount, Credit: e.Amount})
		}
		out = append(out, j)
	}
	return out, nil
}

// journalsOf are close id's exported journals, newest batch first.
func journalsOf(ctx context.Context, q rowReader, closeID string) ([]fileJournal, error) {
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'batch',batch,'reference',reference,'paper_id',workpaper_id::text,'state',state,
 'ledger_reference',ledger_reference,'lines',lines,'exported_by',exported_by,'exported_at',exported_at,'entered_by',NULLIF(entered_by,''),'entered_at',entered_at) ORDER BY batch DESC,id),'[]')
 FROM file_journals WHERE close_id::text=$1`, closeID).Scan(&raw); err != nil {
		return nil, err
	}
	out := []fileJournal{}
	return out, json.Unmarshal(raw, &out)
}

func registerJournals(mux *http.ServeMux, db *sql.DB) {
	// The month's approved entries to export, the journals exported, and the client ledger's journal import.
	mux.HandleFunc("GET /v1/closes/{id}/journals", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "The journals are unavailable.")
			return
		}
		defer tx.Rollback()
		var org, entity, period string
		if err = tx.QueryRowContext(r.Context(), `SELECT organization_id,entity,period FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&org, &entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if source, err := closeBooksSource(r.Context(), tx, r.PathValue("id")); err != nil {
			problem(w, 503, "The journals are unavailable.")
			return
		} else if source != "files" {
			problem(w, 409, "This month's books are a ledger's: its approved entries are posted to it from each draft.")
			return
		}
		pending, err := pendingJournals(r.Context(), tx, r.PathValue("id"), period)
		var journals []fileJournal
		if err == nil {
			journals, err = journalsOf(r.Context(), tx, r.PathValue("id"))
		}
		var ledger, other string
		if err == nil {
			if e := tx.QueryRowContext(r.Context(), `SELECT ledger,other_name FROM current_client_ledgers WHERE organization_id=$1 AND client=client_identity($1,$2)`, org, entity).Scan(&ledger, &other); e != nil && e != sql.ErrNoRows {
				err = e
			}
		}
		if err != nil {
			problem(w, 503, "The journals are unavailable.")
			return
		}
		named, journalImport := other, ""
		if l, ok := ledgers.Find(ledger); ok {
			named, journalImport = l.Name, l.JournalImport
		}
		jsonReply(w, 200, map[string]any{"pending": pending, "journals": journals, "ledger": named, "journal_import": journalImport, "can_export": canReview(r)})
	})
	// A reviewer exports the approved entries not yet exported as one batch, or records a journal as entered in the
	// client's ledger with the ledger's own reference.
	mux.HandleFunc("POST /v1/closes/{id}/journals", func(w http.ResponseWriter, r *http.Request) {
		if !canReview(r) {
			problem(w, 403, "Exporting or entering journals requires workpapers:review.")
			return
		}
		var in struct {
			Action          string `json:"action"`
			Journal         string `json:"journal"`
			LedgerReference string `json:"ledger_reference"`
			Confirmed       bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the journals.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The journals are unavailable.")
			return
		}
		defer tx.Rollback()
		var entity, period string
		if err = tx.QueryRowContext(r.Context(), `SELECT entity,period FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if source, err := closeBooksSource(r.Context(), tx, r.PathValue("id")); err != nil {
			problem(w, 503, "The journals are unavailable.")
			return
		} else if source != "files" {
			problem(w, 409, "This month's books are a ledger's: its approved entries are posted to it from each draft.")
			return
		}
		if refuseSignedOff(w, r.Context(), tx, r.PathValue("id")) {
			return
		}
		switch in.Action {
		case "export":
			pending, err := pendingJournals(r.Context(), tx, r.PathValue("id"), period)
			if err != nil {
				problem(w, 503, "The journals are unavailable.")
				return
			}
			var batch int
			if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(max(batch),0)+1 FROM file_journals WHERE close_id::text=$1`, r.PathValue("id")).Scan(&batch); err != nil {
				problem(w, 503, "The journals are unavailable.")
				return
			}
			var file bytes.Buffer
			out := csv.NewWriter(&file)
			out.Write([]string{"Journal", "Date", "Account", "Debit", "Credit", "Memo"})
			n := 0
			for _, j := range pending {
				if j.Held != "" {
					continue
				}
				n++
				reference := fmt.Sprintf("KR-%s-%d-%d", r.PathValue("id"), batch, n)
				for i := range j.Lines {
					j.Lines[i].Memo = strings.TrimSpace(reference + " " + j.Kind + " " + j.Source)
					l := j.Lines[i]
					out.Write([]string{reference, l.Date, l.Account, l.Debit, l.Credit, l.Memo})
				}
				lines, _ := json.Marshal(j.Lines)
				if _, err = tx.ExecContext(r.Context(), `INSERT INTO file_journals(close_id,batch,reference,workpaper_id,paper_digest,lines,exported_by) VALUES($1::bigint,$2,$3,$4::bigint,$5,$6,$7)`,
					r.PathValue("id"), batch, reference, j.PaperID, j.digest, string(lines), reviewerName(r)); err != nil {
					problem(w, 409, "The journals changed while they were exported: reopen them.")
					return
				}
			}
			out.Flush()
			if n == 0 {
				problem(w, 409, "No approved entry is waiting to be exported.")
				return
			}
			if err = tx.Commit(); err != nil {
				problem(w, 503, "The batch could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]any{"batch": batch, "journals": n, "name": fmt.Sprintf("%s %s journals, batch %d.csv", entity, period, batch), "csv": file.String()})
		case "entered":
			in.LedgerReference = strings.TrimSpace(in.LedgerReference)
			if in.LedgerReference == "" || utf8.RuneCountInString(in.LedgerReference) > 120 {
				problem(w, 422, "Give the ledger's own reference for the journal (1–120 characters).")
				return
			}
			res, err := tx.ExecContext(r.Context(), `UPDATE file_journals SET state='entered',ledger_reference=$3,entered_by=$4,entered_at=now() WHERE id::text=$1 AND close_id::text=$2 AND state='exported'`,
				in.Journal, r.PathValue("id"), in.LedgerReference, reviewerName(r))
			if err != nil {
				problem(w, 503, "The journal could not be saved.")
				return
			}
			if n, _ := res.RowsAffected(); n != 1 {
				problem(w, 409, "Only an exported journal of this month can be recorded as entered.")
				return
			}
			if err = tx.Commit(); err != nil {
				problem(w, 503, "The journal could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]string{"state": "entered"})
		default:
			problem(w, 422, "Export the batch, or record a journal as entered.")
		}
	})
}
