package transport

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

// Source selection (docs/books-sources.md, Migration step 2; delivery step 5): the source a client's month's books come
// from, as books_source (migration 148) says. "newest" keeps each reader's own rule for choosing between a QuickBooks
// capture and a Xero capture; "quickbooks" or "xero" restricts every reader to that source, so no two parts of the
// product disagree about which books a month has.

// closeBooksSource is the source in force for a close's month.
func closeBooksSource(ctx context.Context, q rowReader, closeID string) (string, error) {
	var source string
	err := q.QueryRowContext(ctx, `SELECT books_source(organization_id,entity,period) FROM client_closes WHERE id::text=$1`, closeID).Scan(&source)
	return source, err
}

// readsQuickBooks and readsXero say whether a month whose source is source may read that provider's captures.
func readsQuickBooks(source string) bool { return source != "xero" }
func readsXero(source string) bool       { return source != "quickbooks" }

// sourceAllows is preparation.SourceAllows.
func sourceAllows(closeExpr, provider string) string {
	return preparation.SourceAllows(closeExpr, provider)
}

// chosenSource is a source in force for a client: its default (Period "") or one month's.
type chosenSource struct {
	ID     string    `json:"id"`
	Period string    `json:"period"`
	Source string    `json:"source"`
	Reason string    `json:"reason"`
	Actor  string    `json:"actor"`
	At     time.Time `json:"at"`
}

// clientSources is the client's sources in force under any of its names: its default first, if one was chosen, then
// each month's, newest month first.
func clientSources(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org, name string) ([]chosenSource, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT ON (COALESCE(period,'')) id::text,COALESCE(period,''),source,reason,actor,created_at FROM books_sources
 WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2) ORDER BY COALESCE(period,''),books_sources.id DESC`, org, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chosenSource{}
	for rows.Next() {
		var c chosenSource
		if err = rows.Scan(&c.ID, &c.Period, &c.Source, &c.Reason, &c.Actor, &c.At); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Period == "" || (out[j].Period != "" && out[i].Period > out[j].Period)
	})
	return out, rows.Err()
}

// readFrom is the provider whose capture a month reads under source, from when each provider's newest capture was
// taken: the newer for the newest capture (a tie is QuickBooks', as the readers have it), else the source's own, if any.
func readFrom(source string, quickbooksAt, xeroAt sql.NullTime) string {
	switch {
	case source == "quickbooks" && quickbooksAt.Valid, source == "newest" && quickbooksAt.Valid && (!xeroAt.Valid || !xeroAt.Time.After(quickbooksAt.Time)):
		return "quickbooks"
	case source == "xero" && xeroAt.Valid, source == "newest" && xeroAt.Valid:
		return "xero"
	}
	return ""
}

func registerBooksSources(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/client/books-source", func(w http.ResponseWriter, r *http.Request) {
		// Choosing a source is connecting the books, as connecting a ledger is (docs/books-sources.md, Who does what).
		if !canConnect(r) {
			problem(w, 403, "Choosing where a client's books come from requires integrations:manage.")
			return
		}
		var in struct {
			Client string `json:"client"`
			Period string `json:"period"` // "" for the client's default
			Source string `json:"source"`
			Reason string `json:"reason"`
			Rev    string `json:"rev"` // the newest choice as read, "" for none
		}
		if !decode(w, r, &in) {
			return
		}
		in.Client, in.Period, in.Reason = strings.TrimSpace(in.Client), strings.TrimSpace(in.Period), strings.TrimSpace(in.Reason)
		if in.Client == "" || utf8.RuneCountInString(in.Client) > 200 {
			problem(w, 422, "Name the client.")
			return
		}
		if in.Source != "newest" && in.Source != "quickbooks" && in.Source != "xero" {
			problem(w, 422, "Choose the newest capture, QuickBooks or Xero.")
			return
		}
		if in.Period != "" {
			if _, err := time.Parse("2006-01", in.Period); err != nil {
				problem(w, 422, "Enter the month as YYYY-MM, or none for the client's default.")
				return
			}
		}
		if in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
			problem(w, 422, "Say why (1–500 characters).")
			return
		}
		ctx := r.Context()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			problem(w, 503, "The client's books source is unavailable.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		name, known, allowed, whole, err := clientNamed(ctx, tx, org, in.Client, firmAccess(r), subject(r))
		if err != nil {
			problem(w, 503, "The client's books source is unavailable.")
			return
		}
		if !known || !allowed {
			problem(w, 404, "Client not found.")
			return
		}
		if !whole {
			problem(w, 403, "A client's books source is chosen under its own name; ask for access to "+name+".")
			return
		}
		// One change at a time, against the newest choice the person read.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('books_sources:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
			problem(w, 503, "The client's books source is unavailable.")
			return
		}
		var newest sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT max(id) FROM books_sources WHERE organization_id=$1 AND client_identity(organization_id,client_entity)=client_identity($1,$2)`, org, name).Scan(&newest); err != nil {
			problem(w, 503, "The client's books source is unavailable.")
			return
		}
		if read := map[bool]string{true: strconv.FormatInt(newest.Int64, 10), false: ""}[newest.Valid]; in.Rev != read {
			problem(w, 409, "The client's books source changed since you read it. Review it and try again.")
			return
		}
		// A signed-off month keeps the books it was signed off with, and approved work the books it was prepared from: a
		// choice that would change which capture such a month reads waits for it, or its work, to be reopened. The months
		// are locked first, so a sign-off or an approval either waits for this change or is seen by it.
		if _, err = tx.ExecContext(ctx, `SELECT c.id FROM client_closes c WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client_identity($1,$2) AND ($3='' OR c.period=$3) ORDER BY c.id FOR UPDATE`, org, name, in.Period); err != nil {
			problem(w, 503, "The client's books source is unavailable.")
			return
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.period,books_source(c.organization_id,c.entity,c.period),
 (SELECT max(s.captured_at) FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload ? 'period' AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+`),
 (SELECT max(s.captured_at) FROM xero_snapshots s WHERE s.close_id=c.id),
 COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY close_signoffs.id DESC LIMIT 1),'')='signed_off'
  OR EXISTS(SELECT 1 FROM workpaper_review_state w WHERE w.close_id=c.id AND w.review_state='approved')
 FROM client_closes c WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client_identity($1,$2)
 AND (($3<>'' AND c.period=$3) OR ($3='' AND NOT EXISTS(SELECT 1 FROM books_sources o WHERE o.organization_id=c.organization_id AND client_identity(o.organization_id,o.client_entity)=client_identity(c.organization_id,c.entity) AND o.period=c.period)))
 ORDER BY c.period`, org, name, in.Period)
		if err != nil {
			problem(w, 503, "The client's books source is unavailable.")
			return
		}
		var held []string
		var changed []int64
		for rows.Next() {
			var id int64
			var period, now string
			var qbo, xero sql.NullTime
			var settled bool
			if err = rows.Scan(&id, &period, &now, &qbo, &xero, &settled); err != nil {
				break
			}
			switch {
			case readFrom(now, qbo, xero) == readFrom(in.Source, qbo, xero):
			case settled:
				held = append(held, period)
			default:
				changed = append(changed, id)
			}
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			problem(w, 503, "The client's books source is unavailable.")
			return
		}
		if len(held) > 0 {
			problem(w, 409, "This would change the books of months already signed off or with approved work ("+strings.Join(held, ", ")+"). Reopen them, or their work, first, or choose a source for other months alone.")
			return
		}
		// A month whose books change asks for its drafts again: each draft still in review was prepared from the books it
		// no longer reads, so changes are requested of it, saying why.
		if len(changed) > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest)
 SELECT w.id::bigint,'changes_requested',$2,$3,w.digest FROM workpaper_review_state w WHERE w.close_id=ANY($1::bigint[]) AND w.review_state='pending'
  AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)`,
				changed, "The month's books now come from "+map[string]string{"newest": "the newest capture", "quickbooks": "QuickBooks alone", "xero": "Xero alone"}[in.Source]+": prepare this from those books.", reviewerName(r)); err != nil {
				problem(w, 503, "The client's books source could not be saved.")
				return
			}
		}
		var period any
		if in.Period != "" {
			period = in.Period
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO books_sources(organization_id,client_entity,period,source,reason,actor) VALUES($1,$2,$3,$4,$5,$6)`, org, name, period, in.Source, in.Reason, reviewerName(r)); err != nil {
			problem(w, 503, "The client's books source could not be saved.")
			return
		}
		sources, err := clientSources(ctx, tx, org, name)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The client's books source could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"books_sources": sources})
	})
}
