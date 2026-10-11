package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Scope and basis (docs/books-sources.md, How an upload becomes the month's books, steps 3 and 5): a reviewer attests
// what a file's confirmed reading covers (the book it was exported from, the filters it applied, whether it is the
// client's complete books for the month, else what is missing) and what basis the export represents. The basis is
// compared with the client's approved basis procedure, which stays the authority: a difference is said here, and holds
// what is built on the file once a file can be a month's books (step 9).

// importScope is a reviewer's attestation of a file's scope and basis.
type importScope struct {
	ID       string `json:"id"`
	LayoutID string `json:"layout_id"`
	Book     string `json:"book"`
	Filters  string `json:"filters"`
	Complete bool   `json:"complete"`
	Missing  string `json:"missing"`
	Basis    string `json:"basis"`
	// Recalculated attests the file is a copy recalculated after every link was refreshed: it releases formula amounts.
	Recalculated bool      `json:"recalculated"`
	Reason       string    `json:"reason"`
	Actor        string    `json:"actor"`
	At           time.Time `json:"at"`
}

// basisLabels are the bases as a person reads them.
var basisLabels = map[string]string{"accrual": "accrual", "cash": "cash", "modified_cash": "modified cash"}

// onBasis is "on an accrual basis", "on a cash basis" or "on a modified cash basis".
func onBasis(basis string) string {
	if basis == "accrual" {
		return "on an accrual basis"
	}
	return "on a " + basisLabels[basis] + " basis"
}

// latestImportScope is import id's latest attestation, or nil.
func latestImportScope(ctx context.Context, q rowReader, org, name, id string) (*importScope, error) {
	var s importScope
	err := q.QueryRowContext(ctx, `SELECT s.id::text,s.layout_id::text,s.book,s.filters,s.complete,s.missing,s.basis,s.recalculated,s.reason,s.actor,s.created_at FROM books_import_scopes s
 WHERE s.organization_id=$1 AND client_identity(s.organization_id,s.client_entity)=client_identity($1,$2) AND s.import_id::text=$3 ORDER BY s.id DESC LIMIT 1`, org, name, id).
		Scan(&s.ID, &s.LayoutID, &s.Book, &s.Filters, &s.Complete, &s.Missing, &s.Basis, &s.Recalculated, &s.Reason, &s.Actor, &s.At)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

// approvedBasis is client name's approved basis for month period (this month when ""), as a close reads it: the client's
// own override, else the firm's, with which it is; "" when none is approved.
func approvedBasis(ctx context.Context, q rowReader, org, name, period string) (method, scope string, err error) {
	err = q.QueryRowContext(ctx, `SELECT p.method,CASE WHEN p.client_entity<>'' THEN 'client' ELSE 'firm' END FROM procedure_versions p WHERE p.organization_id=$1 AND p.kind='basis' AND (p.client_entity='' OR lower(p.client_entity)=lower($2)) AND (p.effective_from IS NULL OR p.effective_from<=COALESCE(NULLIF($3,''),to_char(now(),'YYYY-MM'))) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1`, org, name, period).Scan(&method, &scope)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return method, scope, err
}

// scopeNote is what the attestation says against the client's approved basis: "" when they agree or nothing is attested.
func scopeNote(s *importScope, approved string) string {
	switch {
	case s == nil:
		return ""
	case approved == "":
		return "No basis is approved for the client yet: the file's " + basisLabels[s.Basis] + " basis is compared once one is."
	case s.Basis != approved:
		return "The file is " + onBasis(s.Basis) + ", and the client's approved basis is " + basisLabels[approved] + ": statements, comparisons and sign-off built on it are held until the source data agrees, an approved conversion is evidenced, or the procedure changes."
	}
	return ""
}

func registerImportScopes(mux *http.ServeMux, db *sql.DB) {
	// A file's scope and basis as attested, whether that attestation is of its current confirmed reading, and the
	// client's approved basis beside it.
	mux.HandleFunc("GET /v1/closes/client/import/scope", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importOwner(w, r, db)
		if !ok {
			return
		}
		org, id := organization(r), r.URL.Query().Get("id")
		if _, err := importFormat(r.Context(), db, org, name, id); errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "File not found.")
			return
		} else if err != nil {
			problem(w, 503, "The file is unavailable.")
			return
		}
		layout, err := latestLayout(r.Context(), db, org, name, id)
		var scope *importScope
		var method, basisScope string
		if err == nil {
			scope, err = latestImportScope(r.Context(), db, org, name, id)
		}
		if err == nil {
			method, basisScope, err = approvedBasis(r.Context(), db, org, name, importMonth(r.Context(), db, org, id))
		}
		// How many of the reading's month-end amounts are formula results: the attestation of a recalculated copy is
		// asked only of a file that has them.
		formulas := 0
		if err == nil && layout != nil {
			err = db.QueryRowContext(r.Context(), `SELECT count(*) FROM books_layouts l,jsonb_array_elements(COALESCE(l.reading->'lines','[]'::jsonb)) e
 WHERE l.id::text=$1 AND e->>'kind'='posting' AND COALESCE(e->'formulas','[]'::jsonb) ?| ARRAY['debit','credit','balance']`, layout.ID).Scan(&formulas)
		}
		on, onErr := fileImportsOn(r.Context(), db, org)
		if err != nil || onErr != nil {
			problem(w, 503, "The file's scope is unavailable.")
			return
		}
		out := map[string]any{"scope": scope, "confirmed": layout != nil, "formulas": formulas, "current": scope != nil && layout != nil && scope.LayoutID == layout.ID,
			"approved_basis": nil, "note": scopeNote(scope, method), "on": on, "can_confirm": canReview(r) && on && layout != nil}
		if method != "" {
			out["approved_basis"] = map[string]string{"method": method, "scope": basisScope}
		}
		jsonReply(w, 200, out)
	})
	// A reviewer attests a file's scope and basis, against its confirmed reading.
	mux.HandleFunc("POST /v1/closes/client/import/scope", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		if !canReview(r) {
			problem(w, 403, "Attesting a file's scope requires workpapers:review.")
			return
		}
		var in struct {
			Book         string `json:"book"`
			Filters      string `json:"filters"`
			Complete     bool   `json:"complete"`
			Missing      string `json:"missing"`
			Basis        string `json:"basis"`
			Recalculated bool   `json:"recalculated"`
			Reason       string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Book, in.Filters, in.Missing, in.Reason = strings.TrimSpace(in.Book), strings.TrimSpace(in.Filters), strings.TrimSpace(in.Missing), strings.TrimSpace(in.Reason)
		switch {
		case basisLabels[in.Basis] == "":
			problem(w, 422, "Say what basis the export is on: accrual, cash or modified cash.")
			return
		case !in.Complete && in.Missing == "":
			problem(w, 422, "Say what the export leaves out, or attest that it is the client's complete books for the month.")
			return
		case utf8.RuneCountInString(in.Book) > 200 || utf8.RuneCountInString(in.Filters) > 500 || utf8.RuneCountInString(in.Missing) > 500:
			problem(w, 422, "Keep the book to 200 characters, and the filters and what is missing to 500.")
			return
		case in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500:
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		if in.Complete {
			in.Missing = ""
		}
		org, id := organization(r), r.URL.Query().Get("id")
		layout, err := latestLayout(r.Context(), db, org, name, id)
		if err != nil {
			problem(w, 503, "The file's layout is unavailable.")
			return
		}
		if layout == nil {
			problem(w, 409, "Confirm how the file is read before attesting what it covers.")
			return
		}
		// The attestation is of the reading confirmed now: one confirmed meanwhile is never attested unseen. It takes the
		// client's map lock, as an acceptance does, so none is given on an attestation this one replaces while it runs.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The file's scope could not be saved.")
			return
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtext('books_account_maps:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
			problem(w, 503, "The file's scope could not be saved.")
			return
		}
		var attested string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO books_import_scopes(organization_id,client_entity,import_id,layout_id,book,filters,complete,missing,basis,reason,actor,recalculated)
 SELECT $1,$2,l.import_id,l.id,$4,$5,$6,$7,$8,$9,$10,$11 FROM books_layouts l WHERE l.id::text=$3 AND l.organization_id=$1
  AND l.id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=l.import_id) RETURNING id::text`,
			org, name, layout.ID, in.Book, in.Filters, in.Complete, in.Missing, in.Basis, in.Reason, reviewerName(r), in.Recalculated).Scan(&attested)
		if err == sql.ErrNoRows {
			problem(w, 409, "The file's layout was confirmed again meanwhile: reopen it to attest that reading.")
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The file's scope could not be saved.")
			return
		}
		method, _, err := approvedBasis(r.Context(), db, org, name, importMonth(r.Context(), db, org, id))
		if err != nil {
			problem(w, 503, "The client's basis is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"id": attested, "note": scopeNote(&importScope{Basis: in.Basis}, method)})
	})
}

// importMonth is import id's month, or "" when it cannot be read (the basis is then this month's).
func importMonth(ctx context.Context, q rowReader, org, id string) string {
	var period string
	q.QueryRowContext(ctx, `SELECT period FROM books_imports WHERE id::text=$1 AND organization_id=$2`, id, org).Scan(&period)
	return period
}
