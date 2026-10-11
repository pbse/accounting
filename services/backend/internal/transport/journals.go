package transport

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/auth"
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
	key     string
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
	WithdrawnBy     string          `json:"withdrawn_by,omitempty"`
	WithdrawnAt     *time.Time      `json:"withdrawn_at,omitempty"`
	WithdrawnReason string          `json:"withdrawn_reason,omitempty"`
	// Note says why a journal is uncertain, or which books verified it; found counts the month's ledger lines that
	// carry its reference.
	Note  string `json:"note,omitempty"`
	found int
}

// draftEntries are an approved draft's month-end entries, as a posting would post them: a prepaid's proposal (a
// disposal is not posted, so not exported), else what postable gives; with the draft's source reference.
func draftEntries(kind string, raw []byte, period string) ([]prepaids.Adjustment, string, error) {
	if kind != "prepaid" {
		return postable(kind, raw, period)
	}
	var paper prepaids.Workpaper
	if json.Unmarshal(raw, &paper) != nil {
		return nil, "", errors.New("This prepaid draft cannot be read")
	}
	if paper.Proposal == nil {
		return nil, "", errors.New("This prepaid draft proposes no entry")
	}
	month, err := time.Parse("2006-01", period)
	if err != nil {
		return nil, "", err
	}
	if paper.Proposal.Date != month.AddDate(0, 1, -1).Format("2006-01-02") {
		return nil, "", errors.New("Only entries dated at the close month-end are exported")
	}
	return []prepaids.Adjustment{*paper.Proposal}, paper.Input.SourceReference, nil
}

// pendingJournals are close id's current approved drafts whose entries are in no journal (but one uncertain or withdrawn), each
// with its lines, or held with why: a procedure since changed or revoked authorizes none, and, as for a posting, a
// source is entered once, from its newest draft.
func pendingJournals(ctx context.Context, tx *sql.Tx, closeID, period string) ([]journalDraft, error) {
	rows, err := tx.QueryContext(ctx, `SELECT w.id::text,w.kind,w.digest,w.paper FROM workpaper_review_state w WHERE w.close_id::text=$1 AND w.review_state='approved'
 AND (jsonb_typeof(w.paper->'blockers') IS DISTINCT FROM 'array' OR w.paper->'blockers'='[]'::jsonb) AND w.kind IN ('prepaid','accrual','loan','owner','payroll','settlement')
 AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text)
 AND NOT EXISTS(SELECT 1 FROM file_journals j WHERE j.close_id=w.close_id AND j.workpaper_id=w.id AND j.state NOT IN ('uncertain','withdrawn')) ORDER BY w.id`, closeID)
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
		if j.Held == "" {
			if j.key, err = postingSource(ctx, tx, closeID, d.id, d.kind, d.paper, source); err != nil {
				j.Held = err.Error()
			} else {
				var in string
				if err = tx.QueryRowContext(ctx, `SELECT reference FROM file_journals WHERE close_id::text=$1 AND source_key=$2 AND state NOT IN ('uncertain','withdrawn')`, closeID, j.key).Scan(&in); err == nil {
					j.Held = "Its source is already in journal " + in + "."
				} else if err != sql.ErrNoRows {
					return nil, err
				}
			}
		}
		for _, e := range entries {
			j.Lines = append(j.Lines, journalLine{Date: e.Date, Account: e.DebitAccount, Debit: e.Amount}, journalLine{Date: e.Date, Account: e.CreditAccount, Credit: e.Amount})
		}
		out = append(out, j)
	}
	return out, nil
}

// canEnterJournals reports whether the actor may record a journal as entered in the client's ledger.
func canEnterJournals(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && (a.Organization == "" || slices.Contains(a.Permissions, "posting:approve"))
}

// journalsOf are close id's exported journals, newest batch first, each exported or entered one where it stands in
// month, the month's books from its files (nil while it has none).
func journalsOf(ctx context.Context, q rowReader, closeID string, month *fileMonth) ([]fileJournal, error) {
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'batch',batch,'reference',reference,'paper_id',workpaper_id::text,'state',state,
 'ledger_reference',ledger_reference,'lines',lines,'exported_by',exported_by,'exported_at',exported_at,'entered_by',NULLIF(entered_by,''),'entered_at',entered_at,
 'withdrawn_by',NULLIF(withdrawn_by,''),'withdrawn_at',withdrawn_at,'withdrawn_reason',NULLIF(withdrawn_reason,'')) ORDER BY batch DESC,id),'[]')
 FROM file_journals WHERE close_id::text=$1`, closeID).Scan(&raw); err != nil {
		return nil, err
	}
	out := []fileJournal{}
	if err := json.Unmarshal(raw, &out); err != nil || month == nil {
		return out, err
	}
	for i := range out {
		if out[i].State != "exported" && out[i].State != "entered" {
			continue
		}
		if err := verifyJournal(ctx, q, &out[i], month); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// verifyJournal sets where an exported or entered journal stands in the month's books (docs/books-sources.md, Writing
// back): verified when the month's ledger lines carrying its reference in their memo, or the ledger's own reference,
// are exactly its lines; uncertain when they hold it twice or only partly, or when none does in a ledger exported on a
// day after it was entered; otherwise as it was. A Trial Balance alone, without lines, verifies none. The journal
// carries no dimensions (its draft has none), so none is compared.
func verifyJournal(ctx context.Context, q rowReader, j *fileJournal, month *fileMonth) error {
	var ledger bool
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM books_lines WHERE version_id=$1::bigint),COALESCE(jsonb_agg(cells ORDER BY position),'[]') FROM books_lines
 WHERE version_id=$1::bigint AND (cells->>'memo' ~* ('(^|[^a-z0-9-])'||$2||'($|[^a-z0-9-])') OR ($3<>'' AND lower(trim(cells->>'reference'))=lower($3)))`, month.Version, j.Reference, j.LedgerReference).Scan(&ledger, &raw); err != nil {
		return err
	}
	var found []struct{ Date, Account, Amount string }
	var lines []journalLine
	if err := json.Unmarshal(raw, &found); err != nil {
		return err
	}
	if err := json.Unmarshal(j.Lines, &lines); err != nil {
		return err
	}
	j.found = len(found)
	used, matched := make([]bool, len(found)), 0
	for _, l := range lines {
		want, _ := new(big.Rat).SetString(cmp.Or(l.Debit, "0"))
		if credit, ok := new(big.Rat).SetString(cmp.Or(l.Credit, "0")); ok {
			want.Sub(want, credit)
		}
		for k, f := range found {
			if have, ok := new(big.Rat).SetString(f.Amount); ok && !used[k] && f.Date == l.Date && have.Cmp(want) == 0 && sameAccount(f.Account, l.Account) {
				used[k], matched = true, matched+1
				break
			}
		}
	}
	exported := month.ExportedAt.Format("2 January 2006")
	switch {
	case len(found) == 0 && ledger && j.EnteredAt != nil && month.ExportedAt.After(*j.EnteredAt):
		j.State, j.Note = "uncertain", "Not found in "+month.File+", the ledger exported on "+exported+", after it was entered."
	case len(found) == 0:
	case matched == len(lines) && len(found) == len(lines):
		j.State, j.Note = "verified", "Found in "+month.File+", the ledger exported on "+exported+"."
	case matched == len(lines):
		j.State, j.Note = "uncertain", fmt.Sprintf("Found in %s more than once: %d lines carry its reference, for its %d.", month.File, len(found), len(lines))
	default:
		j.State, j.Note = "uncertain", fmt.Sprintf("Found in %s only in part: %d of its %d lines match.", month.File, matched, len(lines))
	}
	return nil
}

// sameAccount reports whether a ledger line's account, as read ("number name"), is a journal line's, named either way:
// by number where both have one, else by name, or one's number as the other's whole name.
func sameAccount(read, named string) bool {
	split := func(s string) (string, string) {
		s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
		if number, name, ok := strings.Cut(s, " "); ok && strings.ContainsAny(number, "0123456789") {
			return number, name
		}
		return "", s
	}
	rn, rname := split(read)
	jn, jname := split(named)
	if rn != "" && jn != "" {
		return rn == jn
	}
	return rname == jname || (rn != "" && rn == jname) || (jn != "" && jn == rname)
}

// writeJournal writes a journal's lines as rows of a batch's CSV, each with the journal's reference.
func writeJournal(out *csv.Writer, reference string, lines []journalLine) {
	for _, l := range lines {
		out.Write([]string{reference, l.Date, l.Account, l.Debit, l.Credit, l.Memo})
	}
}

var batchHeader = []string{"Journal", "Date", "Account", "Debit", "Credit", "Memo"}

func registerJournals(mux *http.ServeMux, db *sql.DB) {
	// A batch already exported, again: the file it was, from the journals kept.
	mux.HandleFunc("GET /v1/closes/{id}/journals/batches/{batch}", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), `SELECT j.reference,j.lines,c.entity,c.period FROM file_journals j JOIN client_closes c ON c.id=j.close_id WHERE j.close_id::text=$1 AND j.batch::text=$2 ORDER BY j.id`, r.PathValue("id"), r.PathValue("batch"))
		if err != nil {
			problem(w, 503, "The batch is unavailable.")
			return
		}
		defer rows.Close()
		var file bytes.Buffer
		out := csv.NewWriter(&file)
		out.Write(batchHeader)
		var entity, period string
		n := 0
		for rows.Next() {
			var reference string
			var raw []byte
			var lines []journalLine
			if err = rows.Scan(&reference, &raw, &entity, &period); err != nil || json.Unmarshal(raw, &lines) != nil {
				problem(w, 503, "The batch is unavailable.")
				return
			}
			writeJournal(out, reference, lines)
			n++
		}
		if rows.Err() != nil {
			problem(w, 503, "The batch is unavailable.")
			return
		}
		if n == 0 {
			problem(w, 404, "Batch not found.")
			return
		}
		out.Flush()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fmt.Sprintf("%s %s journals, batch %s.csv", entity, period, r.PathValue("batch"))}))
		w.Write(file.Bytes())
	})
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
			var month *fileMonth
			if _, month, err = fileMonthBooks(r.Context(), tx, r.PathValue("id")); err == nil {
				journals, err = journalsOf(r.Context(), tx, r.PathValue("id"), month)
			}
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
		jsonReply(w, 200, map[string]any{"pending": pending, "journals": journals, "ledger": named, "journal_import": journalImport, "can_export": canReview(r), "can_enter": canEnterJournals(r)})
	})
	// A reviewer exports the approved entries not yet exported as one batch, or records a journal as entered in the
	// client's ledger with the ledger's own reference.
	mux.HandleFunc("POST /v1/closes/{id}/journals", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action          string `json:"action"`
			Journal         string `json:"journal"`
			LedgerReference string `json:"ledger_reference"`
			Reason          string `json:"reason"`
			Confirmed       bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		ledgerAction := in.Action == "entered" || in.Action == "withdraw"
		if ledgerAction && !canEnterJournals(r) {
			problem(w, 403, "Recording a journal as entered, or withdrawing it, requires posting:approve.")
			return
		}
		if !ledgerAction && !canReview(r) {
			problem(w, 403, "Exporting journals requires workpapers:review.")
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
			out.Write(batchHeader)
			n := 0
			for _, j := range pending {
				if j.Held != "" {
					continue
				}
				n++
				reference := fmt.Sprintf("KR-%s-%d-%d", r.PathValue("id"), batch, n)
				for i := range j.Lines {
					j.Lines[i].Memo = strings.TrimSpace(reference + " " + j.Kind + " " + j.Source)
				}
				writeJournal(out, reference, j.Lines)
				lines, _ := json.Marshal(j.Lines)
				if _, err = tx.ExecContext(r.Context(), `INSERT INTO file_journals(close_id,batch,reference,workpaper_id,paper_digest,source_key,lines,exported_by) VALUES($1::bigint,$2,$3,$4::bigint,$5,$6,$7,$8)`,
					r.PathValue("id"), batch, reference, j.PaperID, j.digest, j.key, string(lines), reviewerName(r)); err != nil {
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
		case "withdraw":
			// A journal never imported into the client's ledger is withdrawn, with why; one the month's ledger holds any
			// line of is not.
			in.Reason = strings.TrimSpace(in.Reason)
			if in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
				problem(w, 422, "Say why the journal was not imported (1–500 characters).")
				return
			}
			_, month, err := fileMonthBooks(r.Context(), tx, r.PathValue("id"))
			var journals []fileJournal
			if err == nil {
				journals, err = journalsOf(r.Context(), tx, r.PathValue("id"), month)
			}
			if err != nil {
				problem(w, 503, "The journals are unavailable.")
				return
			}
			i := slices.IndexFunc(journals, func(j fileJournal) bool { return j.ID == in.Journal })
			if i < 0 || journals[i].State == "withdrawn" || journals[i].State == "verified" || journals[i].found > 0 {
				problem(w, 409, "Only a journal of this month that its ledger does not hold can be withdrawn.")
				return
			}
			if _, err = tx.ExecContext(r.Context(), `UPDATE file_journals SET state='withdrawn',withdrawn_by=$3,withdrawn_at=now(),withdrawn_reason=$4 WHERE id::text=$1 AND close_id::text=$2 AND state IN ('exported','entered')`,
				in.Journal, r.PathValue("id"), reviewerName(r), in.Reason); err != nil {
				problem(w, 503, "The journal could not be saved.")
				return
			}
			if err = tx.Commit(); err != nil {
				problem(w, 503, "The journal could not be saved.")
				return
			}
			jsonReply(w, 200, map[string]string{"state": "withdrawn"})
		default:
			problem(w, 422, "Export the batch, or record a journal as entered or withdrawn.")
		}
	})
}
