package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

// The assistant answers from existing reads, replayed through the saved-closes mux under the caller's own
// permissions, and writes only its own history. It calls no POST route and no model.

type chatCitation struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

type chatReply struct {
	body      string
	lines     []string
	citations []chatCitation
}

type chatMessage struct {
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	Body        string          `json:"body"`
	WorkpaperID string          `json:"workpaper_id,omitempty"`
	Citations   json.RawMessage `json:"citations"`
	CreatedAt   time.Time       `json:"created_at"`
}

var chatFollowUp = regexp.MustCompile(`(?i)\b(that|it|same|those|them)\b`)

var chatFind = []string{"find ", "search for ", "search ", "look for ", "where is ", "where's ", "show me "}

// chatQuery is what a find question asks for: the text after its opening phrase, or "" when it is not a find question.
func chatQuery(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range chatFind {
		if strings.HasPrefix(t, prefix) {
			q := strings.TrimSpace(strings.TrimSpace(text)[len(prefix):])
			q = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(q, "the ")), "?")
			return strings.TrimSpace(q)
		}
	}
	return ""
}

// chatIntent picks the read a question is about; the first match wins, and "" means the help reply.
func chatIntent(text string, closeScoped, paper bool) string {
	if chatQuery(text) != "" {
		return "find"
	}
	t := strings.ToLower(text)
	has := func(words ...string) bool {
		return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(t, w) })
	}
	if !closeScoped {
		switch {
		case has("review", "approv", "queue"):
			return "review"
		case has("block", "stuck", "fail"):
			return "blocked"
		case has("today", "need", "morning", "attention"):
			return "today"
		}
		return ""
	}
	switch {
	case paper && has("draft", "workpaper"):
		return "draft"
	case has("block", "sign-off", "sign off", "signoff", "ready"):
		return "blockers"
	case has("tie", "balance", "differ", "support"):
		return "tieout"
	case has("change", "flux", "movement", "moved", "last month", "compare"):
		return "changes"
	case has("exception", "duplicate", "holding", "suspense", "payee", "issue"):
		return "exceptions"
	case has("ledger", "certif", "complete"):
		return "ledger"
	case has("status", "stand", "summary", "left", "next", "overview", "brief", "where"):
		return "status"
	}
	return ""
}

// chatNamed keeps the items whose account the question names, or, when it names none, those the previous question of
// the same kind named, or all of them.
func chatNamed[T any](text, previous string, items []T, account func(T) string) ([]T, bool) {
	for _, question := range []string{text, previous} {
		t := strings.ToLower(question)
		out := []T{}
		for _, item := range items {
			if a := strings.ToLower(strings.TrimSpace(account(item))); a != "" && strings.Contains(t, a) {
				out = append(out, item)
			}
		}
		if len(out) > 0 {
			return out, true
		}
	}
	return items, false
}

var chatTieStatus = map[string]string{"ties": "ties", "ties_after_entries": "ties after proposed entries", "within_materiality": "differs by less than the firm's materiality", "differs": "differs", "posting_unknown": "posting outcome unknown", "unsupported": "no support yet"}

// closeAnswer answers a question about one close from its reads.
func closeAnswer(read func(string, any) bool, closeID, paperID, intent, text, previous string) chatReply {
	// Only a question that refers back ("that", "it", "same") keeps the previous question's account.
	if chatIntent(previous, true, paperID != "") != intent || !chatFollowUp.MatchString(text) {
		previous = ""
	}
	base := "/dashboard/closes/" + closeID
	unread := func(what string) chatReply {
		return chatReply{body: "I couldn't read " + what + " right now. Try again, or open the close.", citations: []chatCitation{{"Close overview", base}}}
	}
	switch intent {
	case "status", "ledger":
		var b struct {
			Headline string      `json:"headline"`
			Lines    []briefLine `json:"lines"`
		}
		if !read("/brief", &b) {
			return unread("this close's status")
		}
		reply := chatReply{body: b.Headline, citations: []chatCitation{{"Close overview", base}}}
		for _, line := range b.Lines {
			if intent == "ledger" && line.Area == "ledger" {
				return chatReply{body: line.Text, citations: []chatCitation{{"Ledger snapshot", base + "?tab=evidence"}}}
			}
			reply.lines = append(reply.lines, line.Area+": "+line.Text)
		}
		if intent == "ledger" {
			return chatReply{body: "The close brief has no ledger line yet.", citations: reply.citations}
		}
		return reply
	case "blockers":
		var ready struct {
			Blockers []string `json:"blockers"`
		}
		if !read("/readiness", &ready) {
			return unread("sign-off readiness")
		}
		link := []chatCitation{{"Close & sign-off", base + "?tab=signoff"}}
		if len(ready.Blockers) == 0 {
			return chatReply{body: "Nothing blocks sign-off of the defined scope right now.", citations: link}
		}
		return chatReply{body: plural(len(ready.Blockers), "thing blocks", "things block") + " sign-off:", lines: ready.Blockers, citations: link}
	case "tieout":
		var t struct {
			Snapshot any      `json:"snapshot"`
			Error    string   `json:"error"`
			Currency string   `json:"currency"`
			Rows     []tieRow `json:"rows"`
		}
		if !read("/tie-out", &t) {
			return unread("the balance sheet tie-out")
		}
		link := []chatCitation{{"Balance sheet tie-out", base}}
		if t.Snapshot == nil || t.Error != "" {
			return chatReply{body: "There is no readable month-end Trial Balance to tie out yet." + strings.TrimSpace(" "+t.Error), citations: link}
		}
		rows, named := chatNamed(text, previous, t.Rows, func(r tieRow) string { return r.Account })
		if !named {
			rows = slices.DeleteFunc(slices.Clone(rows), func(r tieRow) bool { return r.Status == "ties" || r.Status == "ties_after_entries" })
		}
		if len(rows) == 0 {
			return chatReply{body: "Every asset and liability ties, or ties after the proposed entries.", citations: link}
		}
		reply := chatReply{body: plural(len(rows), "account needs", "accounts need") + " a look:", citations: link}
		for _, r := range rows {
			line := fmt.Sprintf("%s: %s; Trial Balance %s %s", r.Account, chatTieStatus[r.Status], r.Balance, t.Currency)
			if r.Expected != nil && r.Difference != nil {
				line += fmt.Sprintf(", supported %s, difference %s", *r.Expected, *r.Difference)
			}
			reply.lines = append(reply.lines, line)
		}
		return reply
	case "changes":
		type change struct {
			Account   string `json:"account"`
			ThisMonth string `json:"this_month"`
			LastMonth string `json:"last_month"`
			Change    string `json:"change"`
		}
		var f struct {
			State    string   `json:"state"`
			Reason   string   `json:"reason"`
			Currency string   `json:"currency"`
			Changes  []change `json:"changes"`
		}
		if !read("/flux", &f) {
			return unread("the changes from last month")
		}
		link := []chatCitation{{"Changes from last month", base}}
		if f.State != "ready" {
			return chatReply{body: strings.TrimSpace("Changes from last month aren't available yet. " + f.Reason), citations: link}
		}
		changes, _ := chatNamed(text, previous, f.Changes, func(c change) string { return c.Account })
		if len(changes) > 5 {
			changes = changes[:5]
		}
		if len(changes) == 0 {
			return chatReply{body: "No Profit and Loss account moved from last month.", citations: link}
		}
		reply := chatReply{body: "The largest movements from last month, in " + f.Currency + ":", citations: link}
		for _, c := range changes {
			reply.lines = append(reply.lines, fmt.Sprintf("%s: %s (this month %s, last month %s)", c.Account, c.Change, c.ThisMonth, c.LastMonth))
		}
		return reply
	case "exceptions":
		var e struct {
			Capture any                    `json:"capture"`
			Items   []quickbooks.Exception `json:"items"`
		}
		if !read("/quickbooks/exceptions", &e) {
			return unread("the ledger exceptions")
		}
		link := []chatCitation{{"Exceptions", base}}
		if e.Capture == nil {
			return chatReply{body: "There is no transaction capture to check yet.", citations: link}
		}
		if len(e.Items) == 0 {
			return chatReply{body: "The latest capture found no exceptions.", citations: link}
		}
		reply := chatReply{body: plural(len(e.Items), "exception", "exceptions") + " in the latest capture:", citations: link}
		for i, item := range e.Items {
			if i == 5 {
				reply.lines = append(reply.lines, fmt.Sprintf("and %d more on the close.", len(e.Items)-5))
				break
			}
			reply.lines = append(reply.lines, strings.TrimSpace(item.Finding+" "+item.Amount))
		}
		return reply
	case "find":
		q := chatQuery(text)
		var s struct {
			Results  []searchHit `json:"results"`
			Searched string      `json:"searched"`
		}
		if !read("/search?q="+url.QueryEscape(q), &s) {
			return chatReply{body: "I couldn't search for “" + q + "”. Use 3–100 characters, or try a more specific phrase."}
		}
		return chatReply{body: fmt.Sprintf("%s for “%s” in this close. I searched %s.", plural(len(s.Results), "match", "matches"), q, s.Searched), citations: searchCitations(s.Results)}
	case "draft":
		var p struct {
			Kind        string `json:"kind"`
			ReviewState string `json:"review_state"`
			Paper       struct {
				Blockers  []string `json:"blockers"`
				Questions []string `json:"questions"`
			} `json:"paper"`
		}
		if !read("/workpapers/"+url.PathEscape(paperID)+"?record=1", &p) {
			return unread("this draft")
		}
		reply := chatReply{body: fmt.Sprintf("Draft #%s (%s) is %s.", paperID, p.Kind, strings.ReplaceAll(p.ReviewState, "_", " ")), citations: []chatCitation{{"Draft #" + paperID, base + "/workpapers/" + url.PathEscape(paperID)}}}
		for _, b := range p.Paper.Blockers {
			reply.lines = append(reply.lines, "Blocker: "+b)
		}
		for _, q := range p.Paper.Questions {
			reply.lines = append(reply.lines, "Question: "+q)
		}
		return reply
	}
	help := "I answer from this close's saved work: ask where it stands, what blocks sign-off, which balances don't tie, what changed from last month, what exceptions the ledger shows, or whether the ledger capture is certified."
	if paperID != "" {
		help += " Ask about this draft to see its review state, blockers and questions."
	}
	return chatReply{body: help}
}

// firmAnswer answers a firm-wide question. Only a generic reply is stored; the live lines are shown once, so history
// never keeps client details a member may later lose access to.
func firmAnswer(read func(string, any) bool, intent, text string) (chatReply, []string, []chatCitation) {
	work := []chatCitation{{"Work", "/dashboard"}}
	unread := chatReply{body: "I couldn't read that right now. Open Work for the current list.", citations: work}
	label := func(entity, period string) string { return entity + " · " + period }
	switch intent {
	case "find":
		q := chatQuery(text)
		var found struct {
			Results  []searchHit `json:"results"`
			Searched string      `json:"searched"`
		}
		if !read("/search?q="+url.QueryEscape(q), &found) {
			return chatReply{body: "I couldn't search for that. Use 3–100 characters, or try a more specific phrase."}, nil, nil
		}
		return chatReply{body: "I searched your accessible closes. The matches are shown once; search again to see them."}, []string{fmt.Sprintf("%s for “%s”. I searched %s.", plural(len(found.Results), "match", "matches"), q, found.Searched)}, searchCitations(found.Results)
	case "today":
		var m struct {
			Needing int `json:"needing"`
			Closes  []struct {
				Entity string `json:"entity"`
				Period string `json:"period"`
			} `json:"closes"`
		}
		if !read("/morning", &m) {
			return unread, nil, nil
		}
		lines := []string{plural(m.Needing, "close needs", "closes need") + " a person today."}
		for i, c := range m.Closes {
			if i < 5 {
				lines = append(lines, label(c.Entity, c.Period))
			}
		}
		return chatReply{body: "Here is what needs a person today. Open Work for the current list.", citations: work}, lines, nil
	case "review":
		var q struct {
			Count int `json:"count"`
			Items []struct {
				Entity      string `json:"entity"`
				Period      string `json:"period"`
				Kind        string `json:"kind"`
				WorkpaperID string `json:"workpaper_id"`
			} `json:"items"`
		}
		if !read("/review-queue", &q) {
			return unread, nil, nil
		}
		lines := []string{plural(q.Count, "draft is", "drafts are") + " ready for review."}
		for i, item := range q.Items {
			if i < 5 {
				lines = append(lines, fmt.Sprintf("%s: %s draft #%s", label(item.Entity, item.Period), item.Kind, item.WorkpaperID))
			}
		}
		return chatReply{body: "Here is the review queue. Open Work to review the current drafts.", citations: work}, lines, nil
	case "blocked":
		var b struct {
			Items []struct {
				Entity     string `json:"entity"`
				Period     string `json:"period"`
				SourceName string `json:"source_name"`
			} `json:"items"`
		}
		if !read("/work?view=blocked", &b) {
			return unread, nil, nil
		}
		lines := []string{plural(len(b.Items), "item is", "items are") + " blocked."}
		for i, item := range b.Items {
			if i < 5 {
				lines = append(lines, label(item.Entity, item.Period)+": "+item.SourceName)
			}
		}
		return chatReply{body: "Here is the blocked work. Open Work for the current list.", citations: work}, lines, nil
	}
	return chatReply{body: "Across the firm I can find documents, drafts, transactions, procedures and review decisions, and tell you what needs a person today, what is ready for review, and what work is blocked. Open a client close to ask about it."}, nil, nil
}

// searchCitations links each match with a label that says what it is.
func searchCitations(hits []searchHit) []chatCitation {
	out := []chatCitation{}
	for _, h := range hits {
		label := h.Kind + ": " + h.Title
		if h.Snippet != "" {
			label += " — " + h.Snippet
		}
		if len(label) > 220 {
			label = label[:217] + "…"
		}
		out = append(out, chatCitation{label + " (" + h.Close + ")", h.Href})
	}
	return out
}

func registerChat(mux *http.ServeMux, db *sql.DB) {
	// scope binds every query to the firm, the signed-in author and one conversation.
	const scope = `organization_id=$1 AND author=$2 AND close_id IS NOT DISTINCT FROM NULLIF($3,'')::bigint`
	history := func(w http.ResponseWriter, r *http.Request, extra map[string]any) {
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,role,body,COALESCE(workpaper_id::text,''),citations,created_at FROM chat_messages WHERE `+scope+` AND (close_id IS NOT NULL OR created_at>now()-interval '30 days') ORDER BY id DESC LIMIT 200`, organization(r), subject(r), r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Conversation unavailable.")
			return
		}
		defer rows.Close()
		messages := []chatMessage{}
		for rows.Next() {
			var m chatMessage
			if rows.Scan(&m.ID, &m.Role, &m.Body, &m.WorkpaperID, &m.Citations, &m.CreatedAt) != nil {
				problem(w, 503, "Conversation unavailable.")
				return
			}
			messages = append(messages, m)
		}
		if rows.Err() != nil {
			problem(w, 503, "Conversation unavailable.")
			return
		}
		slices.Reverse(messages)
		out := map[string]any{"messages": messages}
		for k, v := range extra {
			out[k] = v
		}
		jsonReply(w, 200, out)
	}
	read := func(r *http.Request, path string, out any) bool {
		req, err := http.NewRequestWithContext(r.Context(), "GET", path, nil)
		if err != nil {
			return false
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code == 200 && json.Unmarshal(rec.Body.Bytes(), out) == nil
	}
	post := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action      string `json:"action"`
			Text        string `json:"text"`
			WorkpaperID string `json:"workpaper_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, author, closeID := organization(r), subject(r), r.PathValue("id")
		if in.Action == "clear" {
			if _, err := db.ExecContext(r.Context(), `DELETE FROM chat_messages WHERE `+scope, org, author, closeID); err != nil {
				problem(w, 503, "Conversation could not be cleared.")
				return
			}
			history(w, r, nil)
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if len(in.Text) == 0 || len(in.Text) > 1000 {
			problem(w, 422, "Ask in 1–1,000 characters.")
			return
		}
		if in.WorkpaperID != "" {
			var found bool
			if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM workpapers WHERE id::text=$1 AND close_id::text=$2)`, in.WorkpaperID, closeID).Scan(&found); err != nil || closeID == "" || !found {
				problem(w, 404, "That draft is not in this close.")
				return
			}
		}
		var reply chatReply
		var live []string
		var linked []chatCitation
		if closeID == "" {
			reply, live, linked = firmAnswer(func(path string, out any) bool { return read(r, "/v1/closes"+path, out) }, chatIntent(in.Text, false, false), in.Text)
		} else {
			// A follow-up may keep the account of the previous question in this conversation.
			var previous string
			if err := db.QueryRowContext(r.Context(), `SELECT body FROM chat_messages WHERE `+scope+` AND role='user' ORDER BY id DESC LIMIT 1`, org, author, closeID).Scan(&previous); err != nil && err != sql.ErrNoRows {
				problem(w, 503, "Conversation unavailable.")
				return
			}
			// A question that refers back without naming a read asks the previous question's read again.
			intent := chatIntent(in.Text, true, in.WorkpaperID != "")
			if intent == "" && chatFollowUp.MatchString(in.Text) {
				intent = chatIntent(previous, true, in.WorkpaperID != "")
			}
			reply = closeAnswer(func(path string, out any) bool { return read(r, "/v1/closes/"+url.PathEscape(closeID)+path, out) }, closeID, in.WorkpaperID, intent, in.Text, previous)
		}
		body := strings.Join(append([]string{reply.body}, reply.lines...), "\n")
		if len(body) > 8000 {
			body = body[:7990] + "…"
		}
		citations, _ := json.Marshal(append([]chatCitation{}, reply.citations...))
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Conversation unavailable.")
			return
		}
		defer tx.Rollback()
		// Holding the firm row serializes this write with a member's removal, which deletes their history; membership
		// is read in a second statement so it sees a removal the lock waited for.
		var enabled, member bool
		if err = tx.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1 FOR SHARE`, org).Scan(&enabled); err == nil && enabled {
			err = tx.QueryRowContext(r.Context(), `SELECT $1='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)`, org, author).Scan(&member)
		}
		if err != nil || !member {
			problem(w, 403, "Your firm membership changed. Nothing was saved.")
			return
		}
		for _, m := range []struct{ role, body, citations string }{{"user", in.Text, "[]"}, {"assistant", body, string(citations)}} {
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO chat_messages(organization_id,author,close_id,workpaper_id,role,body,citations) VALUES($1,$2,NULLIF($3,'')::bigint,NULLIF($4,'')::bigint,$5,$6,$7)`, org, author, closeID, in.WorkpaperID, m.role, m.body, m.citations); err != nil {
				break
			}
		}
		// A conversation keeps its latest 200 messages; firm-wide ones also expire after 30 days.
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM chat_messages WHERE `+scope+` AND (id<(SELECT min(id) FROM (SELECT id FROM chat_messages WHERE `+scope+` ORDER BY id DESC LIMIT 200) kept) OR ($3='' AND created_at<now()-interval '30 days'))`, org, author, closeID)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The message could not be saved.")
			return
		}
		history(w, r, map[string]any{"live": live, "live_citations": append([]chatCitation{}, linked...)})
	}
	mux.HandleFunc("GET /v1/closes/chat", func(w http.ResponseWriter, r *http.Request) { history(w, r, nil) })
	mux.HandleFunc("POST /v1/closes/chat", post)
	mux.HandleFunc("GET /v1/closes/{id}/chat", func(w http.ResponseWriter, r *http.Request) { history(w, r, nil) })
	mux.HandleFunc("POST /v1/closes/{id}/chat", post)
}

// RunChatExpiry removes firm-wide assistant messages older than 30 days, whether or not their author returns.
func RunChatExpiry(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if _, err := db.ExecContext(ctx, `DELETE FROM chat_messages WHERE close_id IS NULL AND created_at<now()-interval '30 days'`); err != nil && ctx.Err() == nil {
			log.Print("Expired assistant messages could not be removed: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
