package transport

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

type briefLine struct {
	Area  string `json:"area"`
	State string `json:"state"`
	Text  string `json:"text"`
	Tab   string `json:"tab,omitempty"`
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// named lists up to three names and how many more.
func named(names []string) string {
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
	}
	return strings.Join(names, ", ")
}

// closeBrief narrates where a close stands from its existing reads, dispatched through the same close-scoped mux.
func closeBrief(mux *http.ServeMux, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		read := func(path string, out any) bool {
			req, err := http.NewRequestWithContext(r.Context(), "GET", "/v1/closes/"+url.PathEscape(id)+path, nil)
			if err != nil {
				return false
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			return rec.Code == 200 && json.Unmarshal(rec.Body.Bytes(), out) == nil
		}
		lines, needs, unchecked := []briefLine{}, []string{}, []string{}
		add := func(area, state, text, tab string) {
			lines = append(lines, briefLine{area, state, text, tab})
			if state == "unknown" {
				unchecked = append(unchecked, area)
			}
		}

		var board struct {
			Snapshot *struct {
				Source     string    `json:"source"`
				CapturedAt time.Time `json:"captured_at"`
				Stale      bool      `json:"stale"`
			} `json:"snapshot"`
			Error string `json:"error"`
			Rows  []struct {
				Account     string `json:"account"`
				Status      string `json:"status"`
				Provisional bool   `json:"provisional"`
			} `json:"rows"`
			Unmatched    []json.RawMessage `json:"unmatched"`
			Unclassified []json.RawMessage `json:"unclassified"`
		}
		boardRead := read("/tie-out", &board)
		switch {
		case !boardRead:
			add("ledger", "unknown", "The ledger snapshot could not be read.", "evidence")
		case board.Snapshot == nil:
			add("ledger", "attention", "No month-end Trial Balance yet. Capture a period snapshot so balances can be tied.", "evidence")
			needs = append(needs, "capture a month-end snapshot")
		default:
			source := "QuickBooks"
			if board.Snapshot.Source == "xero" {
				source = "Xero"
			}
			when := board.Snapshot.CapturedAt.UTC().Format("Jan 2, 15:04 UTC")
			switch {
			case board.Error != "":
				add("ledger", "unknown", fmt.Sprintf("The %s Trial Balance captured %s could not be read.", source, when), "evidence")
			case board.Snapshot.Stale:
				add("ledger", "attention", fmt.Sprintf("The %s Trial Balance from %s is out of date: its source was replaced or the connection is inactive. Capture a new snapshot.", source, when), "evidence")
				needs = append(needs, "capture a new snapshot")
			default:
				add("ledger", "done", fmt.Sprintf("Books read from the %s Trial Balance captured %s.", source, when), "evidence")
			}
		}

		var jobs [5]int
		err := db.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE state='completed'),count(*) FILTER (WHERE state IN ('queued','interpreting','waiting_evidence')),count(*) FILTER (WHERE state='needs_confirmation'),count(*) FILTER (WHERE state='waiting_client'),count(*) FILTER (WHERE state='failed') FROM preparation_jobs WHERE close_id::text=$1 AND state<>'cancelled'`, id).Scan(&jobs[0], &jobs[1], &jobs[2], &jobs[3], &jobs[4])
		switch total := jobs[0] + jobs[1] + jobs[2] + jobs[3] + jobs[4]; {
		case err != nil:
			add("preparation", "unknown", "Preparation progress could not be read.", "overview")
		case total == 0:
			add("preparation", "attention", "No client documents yet. Upload or forward statements, invoices and schedules; the agent prepares work from them.", "evidence")
			needs = append(needs, "add client documents")
		default:
			parts := []string{fmt.Sprintf("%d prepared", jobs[0])}
			for i, label := range []string{"", "in progress", "need your confirmation", "waiting on the client", "failed"} {
				if i > 0 && jobs[i] > 0 {
					parts = append(parts, fmt.Sprintf("%d %s", jobs[i], label))
				}
			}
			state := "done"
			if jobs[1]+jobs[3] > 0 {
				state = "waiting"
			}
			if jobs[2]+jobs[4] > 0 {
				state = "attention"
			}
			add("preparation", state, plural(total, "document", "documents")+": "+strings.Join(parts, ", ")+".", "overview")
			if jobs[2] > 0 {
				needs = append(needs, "confirm "+plural(jobs[2], "document", "documents"))
			}
			if jobs[4] > 0 {
				needs = append(needs, "retry "+plural(jobs[4], "failed document", "failed documents"))
			}
		}

		var approved, ready, unsourced, attention, drafts int
		err = db.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE review_state='approved'),count(*) FILTER (WHERE review_state='pending' AND sourced AND NOT blocked),count(*) FILTER (WHERE review_state='pending' AND NOT sourced AND NOT blocked),count(*) FILTER (WHERE review_state IN ('changes_requested','source_changed') OR blocked),count(*)
 FROM (SELECT w.review_state,COALESCE(jsonb_typeof(w.paper->'sources')='array' AND w.paper->'sources'<>'[]'::jsonb,false) AS sourced,COALESCE(jsonb_typeof(w.paper->'blockers')='array' AND w.paper->'blockers'<>'[]'::jsonb,false) AS blocked FROM workpaper_review_state w WHERE w.close_id::text=$1 AND NOT EXISTS (SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text)) current`, id).Scan(&approved, &ready, &unsourced, &attention, &drafts)
		switch {
		case err != nil:
			add("review", "unknown", "Draft review state could not be read.", "workpapers")
		case drafts == 0:
			add("review", "info", "No drafts yet.", "workpapers")
		default:
			state := "done"
			if ready+attention > 0 || approved < drafts {
				state = "waiting"
			}
			if ready+unsourced+attention > 0 {
				state = "attention"
			}
			text := fmt.Sprintf("Of %s, %d approved, %d ready for your approval and %d need attention (blockers, requested changes or changed evidence).", plural(drafts, "current draft", "current drafts"), approved, ready, attention)
			if ready > 0 {
				needs = append(needs, "approve "+plural(ready, "draft", "drafts"))
			}
			if unsourced > 0 {
				text += " " + plural(unsourced, "draft cites", "drafts cite") + " no evidence yet and cannot be approved."
				needs = append(needs, "add evidence to "+plural(unsourced, "draft", "drafts"))
			}
			add("review", state, text, "workpapers")
			if attention > 0 {
				needs = append(needs, "fix "+plural(attention, "draft", "drafts"))
			}
		}

		switch {
		case !boardRead:
			add("balance sheet", "unknown", "The balance sheet tie-out could not be read.", "overview")
		case board.Snapshot == nil:
			add("balance sheet", "waiting", "The tie-out waits on a month-end snapshot.", "overview")
		case board.Error != "":
			add("balance sheet", "unknown", "The Trial Balance could not be read: "+board.Error+".", "overview")
		default:
			by := map[string][]string{}
			provisional := 0
			for _, row := range board.Rows {
				by[row.Status] = append(by[row.Status], row.Account)
				if row.Provisional && (row.Status == "ties" || row.Status == "ties_after_entries" || row.Status == "within_materiality") {
					provisional++
				}
			}
			parts := []string{fmt.Sprintf("%d of %s tie.", len(by["ties"]), plural(len(board.Rows), "balance-sheet account", "balance-sheet accounts"))}
			for _, s := range []struct{ status, text string }{
				{"ties_after_entries", "Tie once proposed entries post: "},
				{"within_materiality", "Differ by less than the firm's materiality: "},
				{"differs", "Differ: "},
				{"posting_unknown", "Posting outcome unknown: "},
				{"unsupported", "No support yet: "},
			} {
				if names := by[s.status]; len(names) > 0 {
					parts = append(parts, s.text+named(names)+".")
				}
			}
			if provisional > 0 {
				parts = append(parts, plural(provisional, "match rests", "matches rest")+" on unapproved or blocked support.")
			}
			if n := len(board.Unmatched); n > 0 {
				parts = append(parts, plural(n, "workpaper item was", "workpaper items were")+" not compared.")
			}
			if n := len(board.Unclassified); n > 0 {
				parts = append(parts, plural(n, "account has", "accounts have")+" no class, so the board may be incomplete.")
			}
			problems := len(by["ties_after_entries"]) + len(by["differs"]) + len(by["posting_unknown"]) + len(by["unsupported"]) + len(board.Unmatched) + len(board.Unclassified)
			state := "done"
			if provisional > 0 {
				state = "waiting"
			}
			if problems > 0 {
				state = "attention"
			}
			if board.Snapshot.Stale && state == "done" {
				state = "waiting"
			}
			add("balance sheet", state, strings.Join(parts, " "), "overview")
			if n := len(by["differs"]); n > 0 {
				needs = append(needs, "explain "+plural(n, "balance difference", "balance differences"))
			}
			if n := len(by["unsupported"]); n > 0 {
				needs = append(needs, "support "+plural(n, "balance", "balances"))
			}
			if n := len(by["posting_unknown"]); n > 0 {
				needs = append(needs, "confirm "+plural(n, "posting outcome", "posting outcomes"))
			}
			if n := len(by["ties_after_entries"]); n > 0 {
				needs = append(needs, "post the proposed entries for "+plural(n, "balance", "balances"))
			}
			if n := len(board.Unmatched); n > 0 {
				needs = append(needs, "fix "+plural(n, "workpaper account", "workpaper accounts"))
			}
			if n := len(board.Unclassified); n > 0 {
				needs = append(needs, "classify "+plural(n, "ledger account", "ledger accounts"))
			}
		}

		var scan struct {
			Capture  *json.RawMessage  `json:"capture"`
			Items    []json.RawMessage `json:"items"`
			Balances struct {
				Snapshot *json.RawMessage  `json:"snapshot"`
				Items    []json.RawMessage `json:"items"`
				Error    string            `json:"error"`
			} `json:"balances"`
			Schedules *struct {
				State   string   `json:"state"`
				Unknown []string `json:"unknown_accounts"`
				Items   int      `json:"item_lines"`
			} `json:"schedule_scan"`
		}
		switch {
		case !read("/quickbooks/exceptions", &scan):
			add("exceptions", "unknown", "The exception scan could not be read.", "overview")
		case scan.Capture == nil && scan.Balances.Snapshot == nil:
			add("exceptions", "waiting", "No transaction capture or snapshot to scan yet. Captures run daily once QuickBooks is connected.", "evidence")
		default:
			questions := len(scan.Items) + len(scan.Balances.Items)
			text := fmt.Sprintf("The agent found %s for the client.", plural(questions, "question", "questions"))
			state := "done"
			if questions > 0 {
				state = "attention"
				needs = append(needs, "send "+plural(questions, "client question", "client questions"))
			}
			if scan.Capture == nil {
				text += " Transactions are not scanned until a capture runs."
				if state == "done" {
					state = "waiting"
				}
			}
			if scan.Balances.Error != "" {
				text += " The balance check could not read the Trial Balance."
				if state == "done" {
					state = "unknown"
				} else {
					unchecked = append(unchecked, "balance check")
				}
			}
			if s := scan.Schedules; s != nil && s.State == "no_directory" {
				text += " Schedule discovery waits on a month-end snapshot."
				if state == "done" {
					state = "waiting"
				}
			} else if s != nil && s.State == "incomplete" {
				text += " Schedule discovery is incomplete"
				if len(s.Unknown) > 0 {
					text += "; not in the snapshot's account list: " + named(s.Unknown)
				}
				if s.Items > 0 {
					text += "; " + plural(s.Items, "line posts", "lines post") + " through items whose accounts are not captured"
				}
				text += "."
				if state == "done" {
					state = "unknown"
				} else {
					unchecked = append(unchecked, "schedule check")
				}
			}
			add("exceptions", state, text, "overview")
		}

		var status struct {
			State    string   `json:"state"`
			Blockers []string `json:"blockers"`
		}
		switch {
		case !read("/readiness", &status):
			add("sign-off", "unknown", "Sign-off readiness could not be read.", "signoff")
		case status.State == "signed_off":
			add("sign-off", "done", "Signed off.", "signoff")
		case status.State == "reopen_required":
			add("sign-off", "attention", "Work changed after sign-off. Reopen the close or sign off again.", "signoff")
			needs = append(needs, "reopen or re-sign the close")
		case len(status.Blockers) == 0:
			add("sign-off", "attention", "Ready to sign off.", "signoff")
			needs = append(needs, "sign off the close")
		default:
			add("sign-off", "waiting", fmt.Sprintf("Sign-off waits on %s: %s", plural(len(status.Blockers), "item", "items"), named(status.Blockers)), "signoff")
		}

		headline := "Nothing needs you right now."
		if len(needs) > 0 {
			verb := "things need"
			if len(needs) == 1 {
				verb = "thing needs"
			}
			headline = fmt.Sprintf("%d %s you: %s.", len(needs), verb, strings.Join(needs, ", "))
		} else if status.State == "signed_off" {
			headline = "Signed off."
		}
		if len(unchecked) > 0 {
			headline += " Not checked: " + strings.Join(unchecked, ", ") + "."
		}
		jsonReply(w, 200, map[string]any{"headline": headline, "lines": lines})
	}
}
