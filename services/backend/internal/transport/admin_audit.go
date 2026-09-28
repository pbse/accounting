package transport

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// The staff audit (docs/admin-panel.md, phase A4): every staff action and reveal, searchable across firms and exported
// as CSV. Who did which action to what, and whether it went through, always show; reasons, request details and refusal
// errors are staff members' free text that can name a client, so they are masked until revealed, and a reveal is itself
// recorded against each firm whose entries it shows.
func registerAdminAudit(mux *http.ServeMux, db *sql.DB, token string) {
	read := func(next func(http.ResponseWriter, *http.Request, *sql.DB)) http.HandlerFunc {
		return adminGuard(token, db, platformRead, func(w http.ResponseWriter, r *http.Request) { next(w, r, db) })
	}
	mux.HandleFunc("GET /v1/admin/audit", read(func(w http.ResponseWriter, r *http.Request, db *sql.DB) {
		if q, ok := auditFilters(w, r); ok {
			adminAuditPage(w, r, db, q)
		}
	}))
	mux.HandleFunc("GET /v1/admin/audit/export", read(adminAuditExport))
}

type adminAuditRow struct {
	ID           int64           `json:"id"`
	Organization *string         `json:"organization"`
	StaffUser    string          `json:"staff_user"`
	Action       string          `json:"action"`
	TargetType   string          `json:"target_type"`
	TargetID     string          `json:"target_id"`
	Reason       string          `json:"reason"`
	Request      json.RawMessage `json:"request"`
	Result       string          `json:"result"`
	Error        string          `json:"error"`
	CreatedAt    string          `json:"created_at"`
}

// auditQuery searches the audit: a firm ("platform" for entries on no firm, "" for any), a staff member, an action or
// its start ("plan.", "content.reveal"), a result, and entries before an ID (paging by ID: a revealed read adds its own
// entry, which would shift an offset).
type auditQuery struct {
	firm, staff, action, result string
	before                      int64
	limit                       int
}

// auditFilters reads an audit search from the query string; a bad one answers 422 and returns false.
func auditFilters(w http.ResponseWriter, r *http.Request) (auditQuery, bool) {
	values, ok := adminFilters(w, r, "firm", "staff", "action", "result")
	if !ok {
		return auditQuery{}, false
	}
	q := auditQuery{firm: values[0].(string), staff: values[1].(string), action: values[2].(string), result: values[3].(string)}
	if raw := r.URL.Query().Get("before"); raw != "" {
		var err error
		if q.before, err = strconv.ParseInt(raw, 10, 64); err != nil || q.before < 1 {
			problem(w, 422, "before is an audit entry's ID.")
			return auditQuery{}, false
		}
	}
	return q, true
}

// readAudit reads entries newest first, masked unless revealed.
func readAudit(ctx context.Context, tx *sql.Tx, q auditQuery, reveal bool) ([]adminAuditRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,organization_id,staff_user,action,target_type,target_id,reason,request,result,error,created_at::text FROM platform_audit
 WHERE ($1='' OR ($1='platform' AND organization_id IS NULL) OR organization_id=$1) AND ($2='' OR staff_user=$2) AND ($3='' OR starts_with(action,$3))
 AND ($4='' OR result=$4) AND ($5=0 OR id<$5) ORDER BY id DESC LIMIT $6`, q.firm, q.staff, q.action, q.result, q.before, q.limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []adminAuditRow{}
	for rows.Next() {
		var e adminAuditRow
		if err = rows.Scan(&e.ID, &e.Organization, &e.StaffUser, &e.Action, &e.TargetType, &e.TargetID, &e.Reason, &e.Request, &e.Result, &e.Error, &e.CreatedAt); err != nil {
			return nil, err
		}
		if !reveal {
			e.Reason, e.Request, e.Error = maskText(e.Reason), only(e.Request), maskText(e.Error)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// revealAudit records a reveal of these entries against every firm they belong to (entries on no firm at platform
// level), in one transaction, so each firm's own audit shows it, with the search and the IDs it covered. It answers the
// request itself and returns false when refused.
func revealAudit(w http.ResponseWriter, r *http.Request, db *sql.DB, q auditQuery, entries []adminAuditRow, what string) bool {
	firms := []string{}
	for _, e := range entries {
		firm := ""
		if e.Organization != nil {
			firm = *e.Organization
		}
		if !slices.Contains(firms, firm) {
			firms = append(firms, firm)
		}
	}
	if q.firm != "" && q.firm != "platform" {
		firms = []string{q.firm} // one firm's search is that firm's, even when it shows nothing
	}
	shown := map[string]any{"firm": q.firm, "staff": q.staff, "action": q.action, "result": q.result, "entries": len(entries)}
	if len(entries) > 0 {
		shown["newest_id"], shown["oldest_id"] = entries[0].ID, entries[len(entries)-1].ID
	}
	return adminRevealAcross(w, r, db, firms, "audit", what, shown)
}

// adminAuditPage answers one page of an audit search.
func adminAuditPage(w http.ResponseWriter, r *http.Request, db *sql.DB, q auditQuery) {
	reveal := r.URL.Query().Get("reveal") == "1"
	q.limit = adminPage + 1
	var entries []adminAuditRow
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) (err error) {
		entries, err = readAudit(ctx, tx, q, reveal)
		return err
	})
	if err != nil {
		adminFirmProblem(w, err, "audit read")
		return
	}
	next := any(nil)
	if len(entries) > adminPage {
		entries = entries[:adminPage]
		next = entries[len(entries)-1].ID
	}
	if reveal && !revealAudit(w, r, db, q, entries, "search") {
		return
	}
	jsonReply(w, 200, map[string]any{"entries": entries, "next_before": next, "revealed": reveal})
}

// auditExportLimit is the most entries one export holds; X-Truncated says when the search had more, and X-Next-Before
// is the before that exports the next batch.
const auditExportLimit = 5000

// spreadsheetSafe keeps a spreadsheet from running a value as a formula: text whose first character is whitespace or a
// control character, or whose first other character starts a formula (=, +, -, @), is prefixed with an apostrophe.
func spreadsheetSafe(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	if trimmed != value || (trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}

// adminAuditExport is an audit search as CSV, masked unless revealed.
func adminAuditExport(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	q, ok := auditFilters(w, r)
	if !ok {
		return
	}
	reveal := r.URL.Query().Get("reveal") == "1"
	q.limit = auditExportLimit + 1
	var entries []adminAuditRow
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) (err error) {
		entries, err = readAudit(ctx, tx, q, reveal)
		return err
	})
	if err != nil {
		adminFirmProblem(w, err, "audit export")
		return
	}
	truncated := len(entries) > auditExportLimit
	if truncated {
		entries = entries[:auditExportLimit]
	}
	if reveal && !revealAudit(w, r, db, q, entries, "export") {
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="staff-audit.csv"`)
	w.Header().Set("X-Truncated", strconv.FormatBool(truncated))
	if truncated {
		w.Header().Set("X-Next-Before", strconv.FormatInt(entries[len(entries)-1].ID, 10))
	}
	out := csv.NewWriter(w)
	out.Write([]string{"id", "created_at", "organization", "staff_user", "action", "target_type", "target_id", "result", "reason", "error", "request"})
	for _, e := range entries {
		firm := ""
		if e.Organization != nil {
			firm = *e.Organization
		}
		row := []string{strconv.FormatInt(e.ID, 10), e.CreatedAt, firm, e.StaffUser, e.Action, e.TargetType, e.TargetID, e.Result, e.Reason, e.Error, string(e.Request)}
		for i := range row {
			row[i] = spreadsheetSafe(row[i])
		}
		out.Write(row)
	}
	out.Flush()
}
