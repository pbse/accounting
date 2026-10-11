package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

// planStep is one step of a close's plan: who it is with (state) and where it stands. States: done, agent (the agent is
// on it), you (a person), client, waits (on an earlier step), none (does not apply) and unknown (could not be read).
type planStep struct {
	Key   string   `json:"key"`
	Title string   `json:"title"`
	State string   `json:"state"`
	Text  string   `json:"text"`
	Lines []string `json:"lines,omitempty"`
	Link  string   `json:"link,omitempty"`
}

// planWords says each state in words, as the card does.
var planWords = map[string]string{"done": "done", "agent": "I'm on it", "you": "needs you", "client": "waiting on the client", "waits": "waits", "none": "note", "unknown": "not checked"}

// booksRead is where the close's books come from, following the ledger reads: the newest month-end capture, Xero's when
// it is newer than QuickBooks', and otherwise the client's connections. QuickBooks is read through its reading connection.
// daily is whether the scheduled reads take this close: its month is current or just ended, or an agent works on it.
// elsewhere is a QuickBooks period capture the ledger reads do not use, which keeps the scheduled first capture away.
type booksRead struct {
	source           string // the capture in use: quickbooks, xero or none
	captured         time.Time
	stale            bool
	quickbooks, xero string       // the client's connection states, "" without one
	sync, failure    string       // QuickBooks: the latest transaction read of the month and why it failed
	failed           sql.NullTime // when that read finished, if it failed
	synced           sql.NullTime
	ended, daily     bool
	elsewhere        bool
}

func readBooks(ctx context.Context, tx *sql.Tx, closeID string) (booksRead, error) {
	var b booksRead
	var qbAt, xeroAt sql.NullTime
	var qbStale, xeroStale bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(x.state,''),q.captured_at,COALESCE(q.stale,false),COALESCE(s.state,''),COALESCE(s.error,''),CASE WHEN s.state='failed' THEN s.finished_at END,
 (SELECT max(z.finished_at) FROM quickbooks_entity_syncs z WHERE z.close_id=c.id AND z.connection_id=x.id AND z.period=c.period AND z.state IN ('completed','unchanged')),
 COALESCE(h.state,''),xs.captured_at,COALESCE(xs.stale,false),c.period<to_char(now() AT TIME ZONE 'UTC','YYYY-MM'),
 (c.period BETWEEN to_char((now() AT TIME ZONE 'UTC')-interval '1 month','YYYY-MM') AND to_char(now() AT TIME ZONE 'UTC','YYYY-MM') OR EXISTS(SELECT 1 FROM close_goals g WHERE g.close_id=c.id AND g.state='active'))
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off',
 EXISTS(SELECT 1 FROM quickbooks_snapshots z WHERE z.close_id=c.id AND z.payload ? 'period')
 FROM client_closes c
 LEFT JOIN quickbooks_connections x ON x.id=`+quickbooks.ReadingConnection("c.id")+`
 LEFT JOIN LATERAL (SELECT s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR k.state<>'active' AS stale FROM quickbooks_snapshots s JOIN quickbooks_connections k ON k.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
  WHERE s.close_id=c.id AND s.payload ? 'period' AND s.connection_id=COALESCE(x.id,s.connection_id) ORDER BY s.captured_at DESC,s.id DESC LIMIT 1) q ON true
 LEFT JOIN LATERAL (SELECT z.state,z.error,z.finished_at FROM quickbooks_entity_syncs z WHERE z.close_id=c.id AND z.connection_id=x.id AND z.period=c.period ORDER BY z.id DESC LIMIT 1) s ON true
 LEFT JOIN xero_connections h ON h.organization_id=c.organization_id AND lower(h.client_entity)=lower(c.entity) AND h.state<>'disconnected'
 LEFT JOIN LATERAL (SELECT s.captured_at,k.state<>'active' AS stale FROM xero_snapshots s JOIN xero_connections k ON k.id=s.connection_id WHERE s.close_id=c.id ORDER BY s.captured_at DESC,s.id DESC LIMIT 1) xs ON true
 WHERE c.id=$1`, closeID).Scan(&b.quickbooks, &qbAt, &qbStale, &b.sync, &b.failure, &b.failed, &b.synced, &b.xero, &xeroAt, &xeroStale, &b.ended, &b.daily, &b.elsewhere)
	b.elsewhere = b.elsewhere && !qbAt.Valid
	switch {
	case err != nil:
	case xeroAt.Valid && (!qbAt.Valid || xeroAt.Time.After(qbAt.Time)):
		b.source, b.captured, b.stale = "xero", xeroAt.Time, xeroStale
	case qbAt.Valid:
		b.source, b.captured, b.stale = "quickbooks", qbAt.Time, qbStale
	default:
		b.source = "none"
	}
	return b, err
}

// booksStep says what the agent reads the books from and what, if anything, a person must do for it.
func booksStep(b booksRead, closeID, entity, period string) planStep {
	s := planStep{Key: "books", Title: "Read the books", Link: "/dashboard/closes/" + closeID + "?tab=books#quickbooks-ledger"}
	xero := "/dashboard/closes/" + closeID + "?tab=books#xero"
	month := period
	if at, err := time.Parse("2006-01", period); err == nil {
		month = at.Format("January 2006")
	}
	day := func(t time.Time) string { return t.UTC().Format("Jan 2") }
	// Only a close the scheduled reads take is read again each day.
	again := func(what string) string {
		return map[bool]string{true: " I read " + what + " again each day."}[b.daily]
	}
	// A current capture is read; otherwise an active connection, QuickBooks' first, can read the books; only then does a
	// connection need a person.
	switch {
	case b.source == "xero" && !b.stale:
		s.State, s.Text, s.Link = "done", "Read from Xero's Trial Balance captured "+day(b.captured)+"."+again("it"), xero
	case b.source == "quickbooks" && !b.stale:
		s.State, s.Text = "done", "Read from QuickBooks: "+month+" captured "+day(b.captured)+"."
		if b.synced.Valid {
			s.Text += " Transactions read " + day(b.synced.Time) + "."
		}
		// A read that failed after the capture is not hidden behind it: the books are only as current as that read. Only a
		// close the scheduled reads take is read again on its own.
		failed := b.sync == "failed" && (!b.failed.Valid || b.failed.Time.After(b.captured))
		switch {
		case failed && b.daily:
			s.State, s.Text = "agent", strings.TrimSpace(s.Text+" My last read of QuickBooks failed: "+b.failure)+" I'll try again within a day."
		case failed:
			s.State, s.Text = "you", strings.TrimSpace(s.Text+" My last read of QuickBooks failed: "+b.failure)+" Read it again in the QuickBooks panel."
		case b.synced.Valid:
			s.Text += again("them")
		}
	case b.source == "quickbooks" && b.quickbooks == "active":
		s.State, s.Text = "you", "The QuickBooks capture of "+month+" is out of date. Capture it again in the QuickBooks panel."
	case b.quickbooks == "active":
		s.State = "agent"
		switch {
		case b.sync == "queued" || b.sync == "running":
			s.Text = "Reading " + month + " from QuickBooks now."
		case !b.daily || b.elsewhere:
			s.State, s.Text = "you", "Capture "+month+" in the QuickBooks panel."
		case !b.ended:
			s.Text = "I read QuickBooks each day, and capture " + month + " once it ends."
		case b.sync == "failed":
			s.Text = strings.TrimSpace("My last read of QuickBooks failed: "+b.failure) + " I'll try again within a day."
		default:
			s.Text = "I'll capture " + month + " from QuickBooks within a day."
		}
	case b.xero == "active" && !b.daily:
		s.State, s.Text, s.Link = "you", "Capture "+month+" in the Xero panel.", xero
	case b.xero == "active":
		s.State, s.Text, s.Link = "agent", "I'll capture "+month+" from Xero within a day.", xero
	case b.quickbooks != "":
		s.State, s.Text = "you", "Reconnect QuickBooks for "+entity+" so I can read the books."
	case b.xero != "":
		s.State, s.Text, s.Link = "you", "Reconnect Xero for "+entity+" so I can read the books.", xero
	default:
		s.State, s.Text = "none", "No ledger is connected, so I cover the uploaded documents only. Connect QuickBooks or Xero to check the books."
	}
	return s
}

// goalPlan orders what getting the close to its finish takes, from its progress: the books, the documents, the accounts,
// then review and, for a sign-off goal, sign-off. A step waits only on one it cannot start without.
func goalPlan(p goalProgress, b booksRead, closeID, entity, period, finish string) []planStep {
	base := "/dashboard/closes/" + closeID
	by := map[string]int{}
	for _, item := range p.Items {
		by[item.Status]++
	}
	// A sign-off goal's drafts awaiting approval are prepared documents; approving them is the review step's.
	active, prepared := len(p.Items)-by["stopped"], by["prepared"]+p.approve
	needs := by["needs_you"] + by["unsupported"] - p.approve
	// Sign-off's own need is the sign-off step's.
	docs := planStep{Key: "documents", Title: "Prepare every document", Lines: slices.DeleteFunc(slices.Clone(p.Needs), func(n string) bool { return n == coverageLimitNeed })}
	switch {
	case active == 0:
		// With nothing to prepare, what the close needs first says what to do.
		docs.State = "you"
		if len(docs.Lines) > 0 {
			docs.Text, docs.Lines = docs.Lines[0], docs.Lines[1:]
		}
	default:
		parts := []string{fmt.Sprintf("%d of %d prepared", prepared, active)}
		for _, part := range []struct {
			n    int
			text string
		}{{needs, "need you"}, {by["client"], "wait on the client"}, {by["working"], "in progress"}} {
			if part.n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", part.n, part.text))
			}
		}
		docs.Text = strings.Join(parts, ", ") + "."
		switch {
		case needs > 0 || len(docs.Lines) > 0:
			docs.State = "you"
		case by["client"] > 0:
			docs.State = "client"
		case by["working"] > 0:
			docs.State = "agent"
		default:
			docs.State = "done"
		}
	}
	accounts := planStep{Key: "accounts", Title: "Cover every account", Link: base + "#coverage-work"}
	// The accounts come from the books' capture, QuickBooks' or Xero's (ledgerCoverage).
	capture := "QuickBooks"
	if p.accounts != nil && strings.HasPrefix(p.accounts.SnapshotID, xeroCapture) {
		capture = "Xero"
	}
	switch {
	case p.accounts != nil && p.accounts.Stale:
		accounts.State, accounts.Text = "waits", "Waits on a new "+capture+" capture: these accounts come from an out-of-date one."
	case p.Coverage != nil && p.Coverage.Open == 0:
		accounts.State, accounts.Text = "done", fmt.Sprintf("All %d accounts in the %s capture are resolved or excluded.", p.Coverage.Total, capture)
	case p.Coverage != nil:
		accounts.State, accounts.Text = "you", fmt.Sprintf("%d of %d accounts in the %s capture still need a coverage decision or resolution.", p.Coverage.Open, p.Coverage.Total, capture)
		// A review goal's finish covers the documents, so its accounts are a note for sign-off.
		if finish == "review" {
			accounts.State, accounts.Text = "none", "Before sign-off, "+accounts.Text
		}
	case p.limited:
		accounts.State, accounts.Text = "unknown", "This close has more than 100 intake jobs, more than account coverage reads, so its accounts are not shown."
	case b.quickbooks != "":
		accounts.State, accounts.Text = "waits", "Waits on the month's QuickBooks capture."
	case b.xero != "":
		accounts.State, accounts.Text = "waits", "Waits on the month's Xero capture."
	default:
		accounts.State, accounts.Text = "none", "Account coverage needs a QuickBooks or Xero capture of the month."
	}
	review := planStep{Key: "review", Title: "Get every draft ready for review", Link: base + "?tab=workpapers"}
	switch {
	case finish == "signoff" && p.approve > 0:
		review.Title, review.State, review.Text = "Approve every draft", "you", "Review and approve the drafts of "+plural(p.approve, "document", "documents")+"."
	case finish == "signoff" && p.Ready:
		review.Title, review.State, review.Text = "Approve every draft", "done", "Every draft is approved."
	case p.Ready:
		review.State, review.Text = "done", "Every document has a draft ready for your review."
	default:
		review.State, review.Text = "waits", "Waits on the documents."
		if n := active - prepared; n > 0 {
			review.Text = "Waits on " + plural(n, "document", "documents") + "."
		}
		if finish == "signoff" {
			review.Title = "Approve every draft"
		}
	}
	plan := []planStep{booksStep(b, closeID, entity, period), docs, accounts, {Key: "verify", Title: "Reconcile and verify", State: "unknown", Text: "Checking…", Link: base + "?tab=books"}, review}
	if finish != "signoff" {
		return plan
	}
	signoff := planStep{Key: "signoff", Title: "Sign off", Link: base + "?tab=signoff"}
	switch s := p.Signoff; {
	case p.limited:
		signoff.State, signoff.Text = "you", coverageLimitNeed
	case s == nil:
		signoff.State, signoff.Text = "waits", "Waits on the drafts."
	case s.State == "signed_off":
		signoff.State, signoff.Text = "done", "Signed off by "+s.Signer+"."
	case s.State == "reopen_required":
		signoff.State, signoff.Text = "you", "Something changed after the sign-off. Reopen the close on its sign-off tab."
	case len(s.Needs) > 0 || p.Coverage != nil && p.Coverage.Open > 0:
		n := len(s.Needs)
		if p.Coverage != nil && p.Coverage.Open > 0 {
			n++
		}
		signoff.State, signoff.Text = "you", "Sign-off waits on "+plural(n, "item", "items")+"."
	default:
		signoff.State, signoff.Text = "you", "Ready to sign off."
	}
	return append(plan, signoff)
}

// verifyStep is what the close's brief found when it tied the balance sheet and scanned the ledger, in its own words, and,
// once the books are read, whether QuickBooks' two reads agree; the books step says what is missing before that.
func verifyStep(lines []briefLine, base string, books bool) planStep {
	s := planStep{Key: "verify", Title: "Reconcile and verify", State: "done", Text: "The balance sheet ties and the ledger raises no questions.", Link: base + "?tab=books", Lines: []string{}}
	rank := map[string]int{"done": 0, "info": 0, "waiting": 1, "unknown": 2, "attention": 3}
	worst := 0
	for _, l := range lines {
		if l.Area != "balance sheet" && l.Area != "exceptions" && (l.Area != "ledger" || !books || l.State == "done") {
			continue
		}
		s.Lines = append(s.Lines, l.Text)
		worst = max(worst, rank[l.State])
	}
	switch worst {
	case 1:
		s.State, s.Text = "waits", "Waits on the books."
	case 2:
		s.State, s.Text = "unknown", "Some checks could not be read."
	case 3:
		s.State, s.Text = "you", "Balances or ledger questions need you."
	}
	return s
}

// verified puts what the close's brief finds, read through mux as the member, in the plan's verify step.
func verified(mux *http.ServeMux, r *http.Request, closeID string, plan []planStep) {
	var b struct {
		Lines []briefLine `json:"lines"`
	}
	base := "/dashboard/closes/" + closeID
	step := planStep{Key: "verify", Title: "Reconcile and verify", State: "unknown", Text: "The checks could not be read right now.", Link: base + "?tab=books"}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(r.Context(), "GET", "/v1/closes/"+url.PathEscape(closeID)+"/brief", nil))
	if rec.Code == 200 && json.Unmarshal(rec.Body.Bytes(), &b) == nil {
		step = verifyStep(b.Lines, base, slices.ContainsFunc(plan, func(s planStep) bool { return s.Key == "books" && s.State == "done" }))
	}
	for i := range plan {
		if plan[i].Key == "verify" {
			plan[i] = step
		}
	}
}
