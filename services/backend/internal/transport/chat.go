package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"close.local/backend/internal/quickbooks"
)

// The assistant answers from existing reads, replayed through the saved-closes mux under the caller's own
// permissions, and writes only its own history. Only when a firm turns written answers on does a close question
// that no fixed read matches go to the model, with those reads. An action card runs the close page's own route,
// replayed the same way, only when its author confirms the scope the card showed.

type chatCitation struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

type chatReply struct {
	body      string
	lines     []string
	citations []chatCitation
	action    string
	due       string
	finish    string
	monthly   bool
}

type chatMessage struct {
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	Body        string          `json:"body"`
	WorkpaperID string          `json:"workpaper_id,omitempty"`
	Citations   json.RawMessage `json:"citations"`
	Action      string          `json:"action,omitempty"`
	DueOn       string          `json:"due_on,omitempty"`
	Finish      string          `json:"finish,omitempty"`
	EveryMonth  bool            `json:"every_month,omitempty"`
	Outcome     json.RawMessage `json:"outcome"`
	Proposal    json.RawMessage `json:"proposal,omitempty"`
	TargetClose string          `json:"target_close_id,omitempty"`
	Steps       json.RawMessage `json:"steps,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// commandText is the question a slash command asks, exactly as the deterministic reads and cards expect it; "" when
// the command is not one the server answers.
func commandText(command, argument string, closeScoped bool) string {
	argument = strings.TrimSpace(argument)
	switch command {
	case "ask":
		return argument
	case "status":
		if closeScoped {
			return "Where does this close stand?"
		}
		return "What needs me today?"
	case "explain":
		if argument != "" {
			return "Does " + argument + " tie?"
		}
		return "What blocks sign-off?"
	case "compare":
		if argument != "" {
			return "What changed in " + argument + " from last month?"
		}
		return "What changed from last month?"
	case "find":
		if argument == "" {
			return ""
		}
		return "Find " + argument
	case "queue":
		return "What is ready for review?"
	case "blocked":
		return "What is blocked?"
	case "delegated":
		return "How are my delegated closes doing?"
	case "delegate":
		if argument != "" {
			return "Get this close ready for review by " + argument
		}
		return "Get this close ready for review"
	case "prepare":
		return "Prepare this close"
	case "stop":
		return "Stop this close's unresolved work"
	case "request":
		return "Draft a client request"
	}
	return ""
}

// chatHelp names the commands the server answers without a model.
func chatHelp(closeScoped bool) string {
	if !closeScoped {
		return "I answer /status, /queue, /blocked, /delegated and /find <words> here, and in a close /explain, /compare, /prepare, /stop, /request and /delegate."
	}
	return "I answer /status, /explain [account], /compare [account], /find <words>, /prepare, /stop, /request and /delegate [day] here."
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

var chatActionWords = strings.Fields("this the close close's work unresolved remaining failed pending jobs proposals preparation all everything it now please again")

// chatAction is the close action a message asks for: it starts with the verb, and every other word only names the
// close's work, so "prepare me for sign-off" or "stop showing that" is not an action.
func chatAction(text string) string {
	words := strings.Fields(strings.ToLower(strings.TrimRight(strings.TrimSpace(text), ".!")))
	if len(words) > 0 && words[0] == "please" {
		words = words[1:]
	}
	if len(words) > 0 && words[0] == "draft" && slices.Contains(words, "request") {
		for _, w := range words[1:] {
			if !slices.Contains(chatRequestWords, w) {
				return ""
			}
		}
		return "request"
	}
	if len(words) == 0 || !slices.Contains([]string{"prepare", "recheck", "re-check", "stop"}, words[0]) {
		return ""
	}
	for _, w := range words[1:] {
		if !slices.Contains(chatActionWords, w) {
			return ""
		}
	}
	return strings.ReplaceAll(words[0], "-", "")
}

var chatRequestWords = strings.Fields("a an the this close close's client consolidated request for now please")

var chatActionReply = map[string]string{
	"request": "Here is what a consolidated client request would ask. Check the drafts and the wording, then confirm on the card to save it as a draft; nothing is approved or sent, and you can edit it in Requests.",
	"prepare": "Here is what preparing this close would coordinate. Check the listed documents and confirm on the card to start; facts and methods still need their own review, and closing the assistant does not stop the work.",
	"recheck": "Here are the failed or pending proposals that can be rechecked. Check the list and confirm on the card to recheck them.",
	"stop":    "Here is the unresolved work in this close. Check the list, give a reason and confirm on the card to stop it; prepared drafts and sent messages stay in their history.",
}

// chatWords splits text into lowercase words without punctuation or a possessive.
func chatWords(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(".,!?;:\"“”()", r) })
	for i, w := range words {
		words[i] = strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "’s"), "'’")
	}
	return words
}

var chatPolite = strings.Fields("please can could would will you thanks thank")

// nameAt is where the client's name (its words) first appears in words, as [start, end), or an empty span.
func nameAt(words, name []string) (int, int) {
	for i := 0; len(name) > 0 && i+len(name) <= len(words); i++ {
		if slices.Equal(words[i:i+len(name)], name) {
			return i, i + len(name)
		}
	}
	return 0, 0
}

// monthlyAt says whether words[i] starts "every month", "each month" or "monthly", asking for the monthly hand-over;
// the client's name, where it appears (from to end), never does.
func monthlyAt(words []string, i, from, end int) bool {
	return (i < from || i >= end) && (words[i] == "monthly" || (words[i] == "every" || words[i] == "each") && i+1 < len(words) && words[i+1] == "month" && (i+1 < from || i+1 >= end))
}

// chatMonthly says whether a request to hand entity's close over asks for every month too.
func chatMonthly(text, entity string) bool {
	words := chatWords(text)
	from, end := nameAt(words, chatWords(entity))
	for i := range words {
		if monthlyAt(words, i, from, end) {
			return true
		}
	}
	return false
}

var chatDelegateWords = strings.Fields("this the a my our close books work month it now to over agent for ready review get have make hand delegate signed sign off signoff sign-off signed-off closed")

// chatDelegate reads a request to hand the close (entity, period) to the agent, such as "Get this close ready for
// review by Friday" or "Close the books by the 10th": ok says the text has that shape, finish is where it ends (review or
// signoff), due is its deadline ("" for none) and extra lists the words it would not act on. A request with extra
// words is not handed over, so an instruction is never silently dropped.
func chatDelegate(text string, today time.Time, entity, period string) (finish, due string, extra []string, ok bool) {
	words := chatWords(text)
	for len(words) > 0 && slices.Contains(chatPolite, words[0]) {
		words = words[1:]
	}
	month, err := time.Parse("2006-01", period)
	if len(words) == 0 || err != nil {
		return "", "", nil, false
	}
	signoff := slices.ContainsFunc(words, func(w string) bool { return w == "signoff" || w == "sign-off" || w == "signed-off" })
	for i := 1; i < len(words); i++ {
		signoff = signoff || words[i] == "off" && (words[i-1] == "sign" || words[i-1] == "signed")
	}
	switch finish = "review"; words[0] {
	case "get", "have", "make":
		if signoff {
			finish = "signoff"
		} else if !slices.Contains(words, "ready") || !slices.Contains(words, "review") {
			return "", "", nil, false
		}
	case "close":
		if !slices.Contains(words, "review") {
			finish = "signoff"
		}
	case "hand", "delegate":
		if signoff {
			finish = "signoff"
		}
	default:
		return "", "", nil, false
	}
	name := strings.ToLower(month.Month().String())
	known := append(chatWords(entity), period, name, name[:3], strconv.Itoa(month.Year()))
	from, end := nameAt(words, chatWords(entity))
	for i := 1; i < len(words); i++ {
		switch w := words[i]; {
		case (w == "by" || w == "due" || w == "before") && due == "":
			if w == "due" && i+1 < len(words) && (words[i+1] == "by" || words[i+1] == "on") {
				i++
			}
			day, last, found := parseDay(words, i+1, today)
			if !found {
				return "", "", nil, false
			}
			if w == "before" {
				day = day.AddDate(0, 0, -1)
			}
			due, i = day.Format("2006-01-02"), last
		case monthlyAt(words, i, from, end):
			if w != "monthly" {
				i++
			}
		case !slices.Contains(chatDelegateWords, w) && !slices.Contains(chatPolite, w) && !slices.Contains(known, w):
			extra = append(extra, w)
		}
	}
	return finish, due, extra, true
}

// delegateTargets links to each open close a firm-wide request to hand a close to the agent names, by any distinctive
// word of its client's name and its month if one is named; the link asks that close's assistant with the deadline.
// extra is what such a request would not act on, for the named close it fits best, when none can be linked.
func delegateTargets(r *http.Request, db *sql.DB, text string, today time.Time) ([]chatCitation, []string, error) {
	// Any word may name a client here, so this only checks that the text has a request's shape.
	if _, _, _, ok := chatDelegate(text, today, text, "2000-01"); !ok {
		return nil, nil, nil
	}
	rows, err := db.QueryContext(r.Context(), `SELECT c.id::text,c.entity,c.period FROM client_closes c WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off' ORDER BY c.period DESC,c.id DESC`, organization(r), firmAccess(r), subject(r))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	words, links := chatWords(text), []chatCitation{}
	var extra []string
	for rows.Next() {
		var id, entity, period string
		if err = rows.Scan(&id, &entity, &period); err != nil {
			return nil, nil, err
		}
		named := slices.ContainsFunc(chatWords(entity), func(w string) bool {
			return slices.Contains(words, w) && !slices.Contains(chatDelegateWords, w) && !slices.Contains(chatPolite, w)
		})
		finish, due, unknown, ok := chatDelegate(text, today, entity, period)
		switch {
		case !named || !ok:
		case len(unknown) > 0:
			if extra == nil || len(unknown) < len(extra) {
				extra = unknown
			}
		default:
			ask := map[string]string{"review": "Get this close ready for review", "signoff": "Get this close signed off"}[finish]
			if due != "" {
				ask += " by " + due
			}
			if chatMonthly(text, entity) {
				ask += " every month"
			}
			links = append(links, chatCitation{entity + " · " + period + ": hand it to the agent", "/dashboard/closes/" + id + "?ask=" + url.QueryEscape(ask)})
		}
	}
	return links, extra, rows.Err()
}

// chatIntent picks the read a question is about; the first match wins, and "" means the help reply.
func chatIntent(text string, closeScoped, paper bool) string {
	// Across the firm, a question about delegated closes comes before a search or another list: asked plainly it lists
	// them, and a more specific one ("which delegated closes are blocked?") goes to written answers, never to a list of
	// other work.
	if asked, listing := chatDelegated(text); !closeScoped && asked {
		if listing {
			return "delegated"
		}
		return ""
	}
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
	case slices.Contains(chatWords(text), "plan"):
		return "plan"
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
	case "plan":
		var g struct {
			Goal *struct {
				State string `json:"state"`
			} `json:"goal"`
			Progress struct {
				Plan []planStep `json:"plan"`
			} `json:"progress"`
		}
		if !read("/goal?verify=1", &g) {
			return unread("this close's plan")
		}
		reply := chatReply{body: "Here's my plan for this close, and where each step stands:", citations: []chatCitation{{"Close overview", base}}}
		if g.Goal == nil || g.Goal.State == "stopped" {
			reply.body = "This close isn't handed to me. Here's how I'd take it on; say “Get this close ready for review by Friday” to hand it over:"
		}
		for i, s := range g.Progress.Plan {
			reply.lines = append(reply.lines, fmt.Sprintf("%d. %s (%s): %s", i+1, s.Title, planWords[s.State], s.Text))
			for _, l := range s.Lines {
				reply.lines = append(reply.lines, "   • "+l)
			}
		}
		return reply
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
		return chatReply{body: fmt.Sprintf("Showing %s for “%s” in this close. I searched %s.", plural(len(s.Results), "match", "matches"), q, s.Searched), citations: searchCitations(s.Results)}
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
	help := "I answer from this close's saved work: ask for my plan, where it stands, what blocks sign-off, which balances don't tie, what changed from last month, what exceptions the ledger shows, or whether the ledger capture is certified. Say “Get this close ready for review by Friday” to hand it to me."
	if paperID != "" {
		help += " Ask about this draft to see its review state, blockers and questions."
	}
	return chatReply{body: help}
}

var chatDelegatedWords = strings.Fields("show me list how what which are is was have has i we you our my the all a of to on over with delegated closes close agent agent's working doing handed work status report")

// chatDelegated says whether a question is about the closes handed to the agent ("delegated", or the agent with its
// work: "What is the agent working on?"), and whether it only asks for them, in listing words alone.
func chatDelegated(text string) (asked, listing bool) {
	words := chatWords(text)
	asked = slices.Contains(words, "delegated") || slices.Contains(words, "agent") && slices.ContainsFunc(words, func(w string) bool {
		return w == "working" || w == "doing" || w == "handed" || w == "closes" || w == "work"
	})
	return asked, asked && !slices.ContainsFunc(words, func(w string) bool { return !slices.Contains(chatDelegatedWords, w) && !slices.Contains(chatPolite, w) })
}

// delegatedWords says where a delegated close stands from what the agent last found, in the words of Work's list.
func delegatedWords(finish, state string, needs int, ready bool, signoff, asOf string) string {
	switch {
	case asOf == "":
		return "updating"
	case signoff == "reopen_required":
		return "needs reopening"
	case signoff == "signed_off":
		return "signed off"
	case finish == "review" && state == "reached":
		return "ready for review"
	case needs > 0:
		return plural(needs, "thing needs", "things need") + " a person"
	case signoff == "open" && ready:
		return "ready to sign off"
	}
	return "the agent is working"
}

// portfolioListing words a "show" goal's selection, shown once with the first ten closes linked; Work has the rest.
func portfolioListing(op portfolioOperation, work chatCitation) (string, []string, []chatCitation) {
	count := plural(len(op.Items), "close matches", "closes match")
	if op.HasMore {
		count = "More than 100 closes match"
	}
	lines, links := []string{count + ": " + strings.ToLower(op.Scope[:1]) + op.Scope[1:] + "."}, []chatCitation{}
	for i, item := range op.Items {
		if i == 10 {
			lines = append(lines, "The rest are in Work.")
			break
		}
		lines = append(lines, item.Label+" · "+item.Result)
		links = append(links, chatCitation{item.Label, "/dashboard/closes/" + item.CloseID})
	}
	return "Here are the closes this selects, shown once. Ask again, or open Work, for the current list.", lines, append(links, work)
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
		return chatReply{body: "I searched your accessible closes. The matches are shown once; search again to see them."}, []string{fmt.Sprintf("Showing %s for “%s”. I searched %s.", plural(len(found.Results), "match", "matches"), q, found.Searched)}, searchCitations(found.Results)
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
	case "delegated":
		var g struct {
			Goals []struct {
				CloseID string `json:"close_id"`
				Entity  string `json:"entity"`
				Period  string `json:"period"`
				Finish  string `json:"finish"`
				DueOn   string `json:"due_on"`
				State   string `json:"state"`
				Author  string `json:"author"`
				Summary struct {
					Needs   int    `json:"needs"`
					Ready   bool   `json:"ready"`
					Signoff string `json:"signoff"`
					AsOf    string `json:"as_of"`
				} `json:"summary"`
			} `json:"goals"`
			More bool `json:"more"`
		}
		if !read("/goals", &g) {
			return unread, nil, nil
		}
		count := plural(len(g.Goals), "close is", "closes are") + " handed to the agent, those needing a person first."
		if g.More {
			count = "More than 100 closes are handed to the agent, those needing a person first."
		}
		lines, links := []string{count}, []chatCitation{}
		for i, d := range g.Goals {
			if i == 10 {
				lines = append(lines, "The rest are in Work.")
				break
			}
			month := d.Period
			if at, err := time.Parse("2006-01", d.Period); err == nil {
				month = at.Format("January 2006")
			}
			line := d.Entity + " · " + month + ": " + delegatedWords(d.Finish, d.State, d.Summary.Needs, d.Summary.Ready, d.Summary.Signoff, d.Summary.AsOf)
			// Progress more than ten minutes old says when it was found, as Work does.
			if at, err := time.Parse(time.RFC3339Nano, d.Summary.AsOf); err == nil && time.Since(at) > 10*time.Minute {
				line += " (as of " + at.UTC().Format("Jan 2, 15:04 UTC") + ")"
			}
			if day, err := time.Parse("2006-01-02", d.DueOn); err == nil {
				line += " · due " + day.Format("Mon 2 Jan")
			}
			lines = append(lines, line+" · handed over by "+d.Author)
			links = append(links, chatCitation{d.Entity + " · " + d.Period, "/dashboard/closes/" + d.CloseID})
		}
		return chatReply{body: "Here are the closes handed to the agent. Open Work for the current list.", citations: work}, lines, append(links, work...)
	case "blocked":
		// Work lists at most 100 items; its counts hold the whole number.
		var b struct {
			Items []struct {
				Entity     string `json:"entity"`
				Period     string `json:"period"`
				SourceName string `json:"source_name"`
			} `json:"items"`
			Counts struct {
				Blocked int `json:"blocked"`
			} `json:"counts"`
		}
		if !read("/work?view=blocked", &b) {
			return unread, nil, nil
		}
		lines := []string{plural(b.Counts.Blocked, "item is", "items are") + " blocked."}
		for i, item := range b.Items {
			if i < 5 {
				lines = append(lines, label(item.Entity, item.Period)+": "+item.SourceName)
			}
		}
		return chatReply{body: "Here is the blocked work. Open Work for the current list.", citations: work}, lines, nil
	}
	return chatReply{body: "Across the firm I can find documents, drafts, transactions, procedures and review decisions, and tell you what needs a person today, what is ready for review, what work is blocked, how the closes handed to me stand, and which closes a goal such as “show my clients due Friday” selects. Open a client close to ask about it, or name one to hand it to me (“Get Acme ready for review by Friday”)."}, nil, nil
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
		// Pairs whose reads touched a close the member can no longer open, or that no longer exists, are deleted first.
		if err := pruneConversation(r.Context(), db, organization(r), subject(r), r.PathValue("id"), firmAccess(r)); err != nil {
			problem(w, 503, "Conversation unavailable.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT id::text,role,body,COALESCE(workpaper_id::text,''),citations,COALESCE(action,''),COALESCE(due_on::text,''),COALESCE(finish,''),every_month,COALESCE(outcome,'null'::jsonb),COALESCE(proposal,'null'::jsonb),COALESCE(target_close_id::text,''),COALESCE(steps,'null'::jsonb),created_at FROM chat_messages WHERE `+scope+` AND (close_id IS NOT NULL OR created_at>now()-interval '30 days') ORDER BY chat_messages.id DESC LIMIT 200`, organization(r), subject(r), r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Conversation unavailable.")
			return
		}
		defer rows.Close()
		messages := []chatMessage{}
		for rows.Next() {
			var m chatMessage
			if rows.Scan(&m.ID, &m.Role, &m.Body, &m.WorkpaperID, &m.Citations, &m.Action, &m.DueOn, &m.Finish, &m.EveryMonth, &m.Outcome, &m.Proposal, &m.TargetClose, &m.Steps, &m.CreatedAt) != nil {
				problem(w, 503, "Conversation unavailable.")
				return
			}
			if string(m.Proposal) == "null" {
				m.Proposal = nil
			}
			if string(m.Steps) == "null" {
				m.Steps = nil
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
	replay := func(r *http.Request, method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequestWithContext(r.Context(), method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	read := func(r *http.Request, path string, out any) bool {
		rec := replay(r, "GET", path, nil)
		return rec.Code == 200 && json.Unmarshal(rec.Body.Bytes(), out) == nil
	}
	post := func(w http.ResponseWriter, r *http.Request) {
		// A written answer streams; once its first line is out, the reply keeps its status and ends with the conversation.
		var streamed *streamWriter
		var attempted, trace string
		var latency sql.NullInt64
		var in struct {
			Action      string `json:"action"`
			Text        string `json:"text"`
			Command     string `json:"command"`
			Argument    string `json:"argument"`
			WorkpaperID string `json:"workpaper_id"`
			Today       string `json:"today"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, author, closeID := organization(r), subject(r), r.PathValue("id")
		// A command runs a fixed read or card without a model; free text goes to the agent when the firm allows it.
		var level string
		if err := db.QueryRowContext(r.Context(), `SELECT agent_chat FROM organizations WHERE id=$1`, org).Scan(&level); err != nil && in.Action != "clear" {
			problem(w, 503, "Conversation unavailable.")
			return
		}
		command := strings.ToLower(strings.TrimSpace(in.Command))
		if command != "" {
			in.Text = commandText(command, in.Argument, closeID != "")
			if in.Text == "" {
				problem(w, 422, chatHelp(closeID != ""))
				return
			}
		}
		agent := command == "" && (level == "firm" || level == "closes" && closeID != "")
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
		today := browserToday(in.Today)
		var entity, period string
		if closeID != "" {
			if err := db.QueryRowContext(r.Context(), `SELECT entity,period FROM client_closes WHERE id::text=$1`, closeID).Scan(&entity, &period); err != nil {
				problem(w, 503, "Conversation unavailable.")
				return
			}
		}
		var reply chatReply
		var live []string
		var linked []chatCitation
		var targets []chatCitation
		var extra []string
		var result agentResult
		// With the agent on, the model chooses the reads for free text; otherwise the fixed reads and cards answer as
		// they always have, from the words of a command or of the message, without a model.
		if agent {
		} else if closeID == "" {
			var err error
			if targets, extra, err = delegateTargets(r, db, in.Text, today); err != nil {
				problem(w, 503, "Conversation unavailable.")
				return
			}
		}
		finish, due, unknown, delegate := chatDelegate(in.Text, today, entity, period)
		if closeID != "" && delegate {
			extra = unknown
		}
		if goal, err := parsePortfolioGoal(in.Text, today); agent {
		} else if closeID == "" && err == nil {
			work := chatCitation{"Open this goal in Work", "/dashboard?goal=" + url.QueryEscape(in.Text)}
			reply = chatReply{body: "Goals across your closes run from Work, which previews exactly which closes a goal selects before anything starts.", citations: []chatCitation{work}}
			// A listing selects and changes nothing, so it is answered here, from Work's own selection for the member.
			var op portfolioOperation
			if goal.action == "show" {
				reply.body = "Work shows what this selects."
				if rec := replay(r, "POST", "/v1/closes/portfolio", map[string]string{"text": in.Text, "today": in.Today}); rec.Code == 200 && json.Unmarshal(rec.Body.Bytes(), &op) == nil {
					reply.body, live, linked = portfolioListing(op, work)
					reply.citations = nil
				}
			}
		} else if len(targets) == 1 {
			// Only a generic reply is kept; the close itself is linked once, like any firm-wide answer.
			reply, linked = chatReply{body: "I found the close you mean. Open it to see my plan and hand it over."}, targets
		} else if len(targets) > 1 {
			reply, linked = chatReply{body: "More than one open close matches. Open the one to hand to me."}, targets[:min(len(targets), 5)]
		} else if len(extra) > 0 {
			say := "“Get this close ready for review by Friday”"
			if closeID == "" {
				say = "“Get <client> ready for review by Friday”"
			}
			reply = chatReply{body: "I can take a close from here, but I don't act on other instructions, such as “" + strings.Join(extra, " ") + "”. Say " + say + " to hand it to me."}
		} else if closeID == "" {
			intent := chatIntent(in.Text, false, false)
			reply, live, linked = firmAnswer(func(path string, out any) bool { return read(r, "/v1/closes"+path, out) }, intent, in.Text)
		} else if delegate {
			reply = chatReply{body: "I can take this close from here. Check my plan and hand it over.", action: "delegate", due: due, finish: finish, monthly: chatMonthly(in.Text, entity)}
		} else if action := chatAction(in.Text); action != "" {
			link := chatCitation{"Close preparation", "/dashboard/closes/" + closeID + "?tab=overview"}
			if action == "request" {
				link = chatCitation{"Client requests", "/dashboard/closes/" + closeID + "?tab=requests"}
			}
			reply = chatReply{body: chatActionReply[action], citations: []chatCitation{link}, action: action}
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
		// still says whether the member may still see what a written answer was made from: the close, or across the firm
		// the same clients as when the reads were made.
		var clients string
		clientsNow := func(q querier) (string, error) {
			var c string
			return c, q.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(lower(client_entity) ORDER BY lower(client_entity)),'[]')::text FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND granted`, org, author).Scan(&c)
		}
		still := func(q querier) (bool, error) {
			var ok bool
			if closeID != "" {
				return ok, q.QueryRowContext(r.Context(), closeAccess, closeID, org, firmAccess(r), author).Scan(&ok)
			}
			now, err := clientsNow(q)
			return now == clients, err
		}
		if agent {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(60 * time.Second))
			if closeID == "" {
				var err error
				if clients, err = clientsNow(db); err != nil {
					problem(w, 503, "Conversation unavailable.")
					return
				}
			}
			// The first event waits until the member's firm membership and access are checked again.
			denied := false
			emit := func(kind string, payload map[string]any) bool {
				if streamed == nil {
					allowed, err := still(db)
					var member bool
					if err == nil && allowed {
						err = db.QueryRowContext(r.Context(), `SELECT enabled AND ($1='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)) FROM organizations WHERE id=$1`, org, author).Scan(&member)
					}
					if err != nil || !allowed || !member {
						denied = true
						return false
					}
					w.Header().Set("Content-Type", "application/x-ndjson")
					w.Header().Set("Cache-Control", "no-store")
					w.WriteHeader(http.StatusOK)
					streamed = &streamWriter{w}
					w = streamed
				}
				payload["type"] = kind
				line, _ := json.Marshal(payload)
				if _, err := streamed.ResponseWriter.Write(append(line, '\n')); err != nil {
					return false
				}
				_ = http.NewResponseController(streamed.ResponseWriter).Flush()
				return true
			}
			var firmName string
			if err := db.QueryRowContext(r.Context(), `SELECT name FROM organizations WHERE id=$1`, org).Scan(&firmName); err != nil {
				problem(w, 503, "Conversation unavailable.")
				return
			}
			if err := pruneConversation(r.Context(), db, org, author, closeID, firmAccess(r)); err != nil {
				problem(w, 503, "Conversation unavailable.")
				return
			}
			earlier, err := agentHistory(r.Context(), db, org, author, closeID)
			if err != nil {
				problem(w, 503, "Conversation unavailable.")
				return
			}
			began := time.Now()
			loopCtx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			result = runAgent(loopCtx, db, org, closeID, in.WorkpaperID, in.Text, firmName, entity, period, today.Format("2006-01-02"), actorPermissions(r), level, earlier, func(method, path string, body any) (string, bool) {
				rec := replay(r, method, path, body)
				return rec.Body.String(), rec.Code == 200
			}, func(id string) bool {
				var ok bool
				return db.QueryRowContext(r.Context(), closeAccess, id, org, firmAccess(r), author).Scan(&ok) == nil && ok
			}, emit)
			cancel()
			if denied {
				problem(w, 403, "Your access changed. Nothing was saved.")
				return
			}
			if result.outcome == "failed" {
				// Nothing from the model is saved and the question is not kept, but the attempt itself is recorded: it
				// spent model calls, and the firm's counts and failure telemetry must see it.
				answerScope := "close"
				if closeID == "" {
					answerScope = "firm"
				}
				var trace any
				if raw, err := json.Marshal(result.steps); err == nil && len(result.steps) > 0 {
					trace = string(raw)
				}
				_, _ = db.ExecContext(r.Context(), `INSERT INTO assistant_answers(organization_id,author,close_id,outcome,latency_ms,trace,scope,rounds,tool_calls) VALUES($1,$2,NULLIF($3,'')::bigint,'failed',$4,$5::jsonb,$6,$7,$8)`, org, author, closeID, time.Since(began).Milliseconds(), trace, answerScope, result.rounds, result.calls)
				// The panel keeps the member's text and offers to retry.
				if streamed != nil {
					emit("error", map[string]any{"message": result.why})
					return
				}
				problem(w, 503, result.why)
				return
			}
			// Before the text goes out, access to every close the reads touched is checked again; a read without lineage
			// is covered by the member's openable closes being the set it was made with (still).
			for _, id := range result.closes {
				var ok bool
				if err := db.QueryRowContext(r.Context(), closeAccess, id, org, firmAccess(r), author).Scan(&ok); err != nil || !ok {
					denied = true
				}
			}
			if allowed, err := still(db); err != nil || !allowed || denied {
				problem(w, 403, "Your access changed. Nothing was saved.")
				return
			}
			if raw, err := json.Marshal(result.steps); err == nil && len(result.steps) > 0 {
				trace = string(raw)
			}
			attempted, latency = result.outcome, sql.NullInt64{Int64: time.Since(began).Milliseconds(), Valid: result.outcome != "limit"}
			links := []chatCitation{}
			for _, step := range result.steps {
				if step.Link != nil && !slices.Contains(links, *step.Link) {
					links = append(links, *step.Link)
				}
			}
			switch result.outcome {
			case "kept":
				reply = result.reply
				if !result.known {
					// An answer built on a read without lineage (a firm-wide list, or a cross-client read inside a close) is
					// shown once and not kept, as today's lists are: nothing verified which clients it drew on.
					for _, line := range strings.Split(reply.body, "\n") {
						if line = strings.TrimSpace(line); line != "" {
							live = append(live, line)
						}
					}
					linked = reply.citations
					reply = chatReply{body: "I answered from reads that cannot be tied to particular closes, so the answer was shown once and not kept; ask again to see it.", citations: []chatCitation{{"Work", "/dashboard"}}}
					// A card proposed in such a turn carries the model's words about those reads; it is not kept either.
					if result.proposal != nil {
						result.proposal = nil
						reply.body += " The action it proposed was not kept; ask for it on its own."
					}
				}
				if !emit("text", map[string]any{"body": strings.Join(append([]string{result.reply.body}, result.reply.lines...), "\n"), "citations": result.reply.citations}) {
					problem(w, 403, "Your access changed. Nothing was saved.")
					return
				}
			case "limit":
				reply = chatReply{body: result.why}
			default:
				reply = chatReply{body: "I couldn't support an answer from what I read, so none was kept. Rephrase the question, or open what I read.", citations: links}
			}
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
		// Access is read again too, since an agent answer can take seconds.
		if err == nil && member && (closeID != "" || agent) {
			member, err = still(tx)
		}
		if err != nil || !member {
			problem(w, 403, "Your access changed. Nothing was saved.")
			return
		}
		if attempted != "" {
			answerScope := "close"
			if closeID == "" {
				answerScope = "firm"
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO assistant_answers(organization_id,author,close_id,outcome,latency_ms,trace,scope,rounds,tool_calls) VALUES($1,$2,NULLIF($3,'')::bigint,$4,$5,NULLIF($6,'')::jsonb,$7,$8,$9)`, org, author, closeID, attempted, latency, trace, answerScope, result.rounds, result.calls)
		}
		// Both messages of the pair carry the closes the reads touched; a proposal and its target close sit on the answer.
		// A lineage-less firm-wide turn records no read's closes, but a card's target close is always recorded, so losing
		// that close removes the card and its question together.
		closes := closesLiteral(nil)
		if agent && (closeID != "" || result.known) {
			closes = closesLiteral(result.closes)
		} else if agent && result.proposal != nil {
			// A card's closes are recorded even when no read's lineage is known: the target close, or a goal's selection.
			targets := []string{}
			if result.proposal.TargetClose != "" {
				targets = append(targets, result.proposal.TargetClose)
			}
			if selected, ok := result.proposal.Args["closes"].([]string); ok {
				targets = append(targets, selected...)
			}
			closes = closesLiteral(targets)
		}
		proposal, target := "", ""
		if result.proposal != nil && agent && reply.action != "" {
			raw, _ := json.Marshal(result.proposal)
			proposal, target = string(raw), result.proposal.TargetClose
		}
		steps := ""
		if agent && len(result.steps) > 0 {
			raw, _ := json.Marshal(result.steps)
			steps = string(raw)
		}
		// Only a kept answer and its question are replayed to the model; a question whose answer was not kept stays on
		// screen but never reaches a later request.
		replayable := !agent || result.outcome == "kept" && result.known
		for _, m := range []struct {
			role, body, citations, action, due, finish, proposal, target, steps string
			monthly                                                             bool
		}{{"user", in.Text, "[]", "", "", "", "", "", "", false}, {"assistant", body, string(citations), reply.action, reply.due, reply.finish, proposal, target, steps, reply.monthly}} {
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO chat_messages(organization_id,author,close_id,workpaper_id,role,body,citations,action,due_on,finish,every_month,proposal,target_close_id,steps,closes,replayable) VALUES($1,$2,NULLIF($3,'')::bigint,NULLIF($4,'')::bigint,$5,$6,$7,NULLIF($8,''),NULLIF($9,'')::date,NULLIF($10,''),$11,NULLIF($12,'')::jsonb,NULLIF($13,'')::bigint,NULLIF($14,'')::jsonb,$15::bigint[],$16)`, org, author, closeID, in.WorkpaperID, m.role, m.body, m.citations, m.action, m.due, m.finish, m.monthly, m.proposal, m.target, m.steps, closes, replayable); err != nil {
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
	// confirm runs a card's action through the close page's route with the digest the author confirmed, and records
	// the jobs it covered on the card. The route checks permission and re-checks the digest under its own lock.
	confirm := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Digest     string `json:"digest"`
			Reason     string `json:"reason"`
			DueOn      string `json:"due_on"`
			Finish     string `json:"finish"`
			EveryMonth bool   `json:"every_month"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, author, closeID := organization(r), subject(r), r.PathValue("id")
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Conversation unavailable.")
			return
		}
		defer tx.Rollback()
		var action, target, proposalRaw string
		if err = tx.QueryRowContext(r.Context(), `SELECT action,COALESCE(target_close_id::text,''),COALESCE(proposal::text,'{}') FROM chat_messages WHERE `+scope+` AND id::text=$4 AND action IS NOT NULL AND outcome IS NULL FOR UPDATE`, org, author, closeID, r.PathValue("message")).Scan(&action, &target, &proposalRaw); err != nil {
			problem(w, 409, "This card was already confirmed or is no longer in your conversation.")
			return
		}
		var proposal agentProposal
		json.Unmarshal([]byte(proposalRaw), &proposal)
		// A firm-wide card acts on the close it named; access to it is checked now, as a close conversation's is on
		// every read.
		acting := closeID
		if acting == "" {
			acting = target
			var ok bool
			if action != "portfolio" && (acting == "" || tx.QueryRowContext(r.Context(), closeAccess, acting, org, firmAccess(r), author).Scan(&ok) != nil || !ok) {
				problem(w, 403, "You can no longer open the close this card acts on.")
				return
			}
		}
		type job struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		base := "/v1/closes/" + url.PathEscape(acting)
		relay := func(rec *httptest.ResponseRecorder) {
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
		}
		// record keeps what the confirmation started on the card, once.
		record := func(outcome map[string]any, failure string) {
			raw, _ := json.Marshal(outcome)
			if _, err = tx.ExecContext(r.Context(), `UPDATE chat_messages SET outcome=$5::jsonb||jsonb_build_object('at',now()) WHERE `+scope+` AND id::text=$4`, org, author, closeID, r.PathValue("message"), string(raw)); err != nil || tx.Commit() != nil {
				problem(w, 503, failure)
				return
			}
			history(w, r, nil)
		}
		if action == "delegate" {
			// Handing over binds no digest: the goal follows the close's documents as they change.
			rec := replay(r, "POST", base+"/goal", map[string]any{"confirmed": true, "due_on": in.DueOn, "finish": in.Finish, "every_month": in.EveryMonth})
			if rec.Code != 200 {
				relay(rec)
				return
			}
			var goal struct {
				ID string `json:"id"`
			}
			json.Unmarshal(rec.Body.Bytes(), &goal)
			record(map[string]any{"goal_id": goal.ID}, "The agent has the close, but this card could not record it. Open the close to see its progress.")
			return
		}
		if action == "approval" {
			// Asking a person to review a draft: the note the model proposed, through the route's own checks.
			paper, _ := proposal.Args["workpaper_id"].(string)
			note, _ := proposal.Args["note"].(string)
			rec := replay(r, "POST", base+"/workpapers/"+url.PathEscape(paper)+"/approval-request", map[string]any{"note": note})
			if rec.Code != 200 {
				relay(rec)
				return
			}
			record(map[string]any{"workpaper_id": paper}, "The review was requested, but this card could not record it.")
			return
		}
		if action == "portfolio" {
			// The goal is previewed again; a selection that no longer matches the closes the card showed is refused, so
			// a close never shown is never acted on. The verb is the goal's own. One batch runs now; the card follows the
			// rest.
			goal, _ := proposal.Args["text"].(string)
			goalDay, _ := proposal.Args["today"].(string)
			var shown []string
			if raw, ok := proposal.Args["closes"].([]any); ok {
				for _, id := range raw {
					if s, isText := id.(string); isText {
						shown = append(shown, s)
					}
				}
			}
			var op portfolioOperation
			if rec := replay(r, "POST", "/v1/closes/portfolio", map[string]any{"text": goal, "preview": true, "today": goalDay}); rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &op) != nil {
				problem(w, 409, "This goal can no longer be previewed. Ask for it again.")
				return
			}
			now := []string{}
			for _, item := range op.Items {
				now = append(now, item.CloseID)
			}
			slices.Sort(now)
			slices.Sort(shown)
			if op.HasMore || !slices.Equal(now, shown) || len(now) == 0 {
				problem(w, 409, "The closes this goal selects changed since the card showed them. Ask for it again and check the new list.")
				return
			}
			rec := replay(r, "POST", "/v1/closes/portfolio", map[string]any{"text": goal, "today": goalDay})
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &op) != nil {
				relay(rec)
				return
			}
			// The saved operation is checked against the card once more: a close that started matching between the two
			// previews is never acted on from here (the operation stays in Work, unrun).
			saved := []string{}
			for _, item := range op.Items {
				saved = append(saved, item.CloseID)
			}
			slices.Sort(saved)
			if !slices.Equal(saved, shown) {
				problem(w, 409, "The closes this goal selects changed as it was saved. Open Work to review the goal, or ask for it again.")
				return
			}
			// Each run acts on at most ten closes; the card keeps running batches until none is pending.
			pending := len(op.Items)
			for batch := 0; batch < 10 && pending > 0; batch++ {
				ran := replay(r, "POST", "/v1/closes/portfolio/run", map[string]any{"id": op.ID, "confirmed": true, "reason": in.Reason})
				if ran.Code != 200 || json.Unmarshal(ran.Body.Bytes(), &op) != nil {
					relay(ran)
					return
				}
				before := pending
				pending = 0
				for _, item := range op.Items {
					if item.State == "pending" {
						pending++
					}
				}
				if pending == before {
					break
				}
			}
			record(map[string]any{"operation_id": op.ID, "action": op.Action, "closes": len(op.Items), "pending": pending}, "The goal started, but this card could not record it. Open Work to follow it.")
			return
		}
		if action == "request" {
			// The card saves the wording it showed, through the Requests route's own freshness check; nothing is sent.
			var c struct {
				Workpapers    []struct{ ID string } `json:"workpapers"`
				LatestID      string                `json:"latest_id"`
				ContextDigest string                `json:"context_digest"`
				Body          string                `json:"body"`
				Digest        string                `json:"digest"`
			}
			if !read(r, base+"/requests/candidates", &c) {
				problem(w, 503, "Client requests unavailable.")
				return
			}
			if len(c.Workpapers) == 0 {
				problem(w, 409, "No draft needs information from the client now.")
				return
			}
			if in.Digest == "" || in.Digest != c.Digest {
				problem(w, 409, "The drafts or the wording changed since the card showed them. Check the updated card and confirm again.")
				return
			}
			ids := []string{}
			for _, p := range c.Workpapers {
				ids = append(ids, p.ID)
			}
			rec := replay(r, "POST", base+"/requests", map[string]any{"expected_id": c.LatestID, "workpaper_ids": ids, "body": c.Body, "context_digest": c.ContextDigest})
			if rec.Code != 200 {
				relay(rec)
				return
			}
			var saved struct {
				ID string `json:"id"`
			}
			json.Unmarshal(rec.Body.Bytes(), &saved)
			// The route returns the latest draft rather than saving it again when its wording and context are the same.
			record(map[string]any{"draft_id": saved.ID, "created": saved.ID != c.LatestID}, "The request draft was saved, but this card could not record it. Open Requests to see it.")
			return
		}
		var scoped struct {
			Inventory []struct {
				JobID string `json:"job_id"`
				Name  string `json:"name"`
			} `json:"inventory"`
			InventoryDigest string `json:"inventory_digest"`
			Stop            struct {
				Digest string `json:"digest"`
				Jobs   []job  `json:"jobs"`
			} `json:"stop"`
			Recheck struct {
				Digest string `json:"digest"`
				Jobs   []job  `json:"jobs"`
			} `json:"recheck"`
		}
		if !read(r, base+"/prepare-close", &scoped) {
			problem(w, 503, "Close preparation unavailable.")
			return
		}
		route, digest, jobs, body := "/prepare-close", scoped.InventoryDigest, []job{}, map[string]any{"inventory_digest": in.Digest, "confirmed": true}
		for _, i := range scoped.Inventory {
			jobs = append(jobs, job{i.JobID, i.Name})
		}
		switch action {
		case "recheck":
			route, digest, jobs, body = "/prepare-close/recheck", scoped.Recheck.Digest, scoped.Recheck.Jobs, map[string]any{"digest": in.Digest, "confirmed": true}
		case "stop":
			route, digest, jobs, body = "/prepare-close/cancel", scoped.Stop.Digest, scoped.Stop.Jobs, map[string]any{"digest": in.Digest, "reason": in.Reason, "confirmed": true}
		}
		if in.Digest == "" || in.Digest != digest {
			problem(w, 409, "The work changed since the card showed it. Check the updated list and confirm again.")
			return
		}
		rec := replay(r, "POST", base+route, body)
		if rec.Code != 200 {
			relay(rec)
			return
		}
		var started struct {
			ID      string `json:"id"`
			Created bool   `json:"created"`
		}
		json.Unmarshal(rec.Body.Bytes(), &started)
		// The prepare route returns an earlier run of the same scope rather than starting one; the card claims only its own.
		if action == "prepare" && !started.Created {
			problem(w, 409, "This exact scope is already being prepared as run #"+started.ID+". Open the close to follow it.")
			return
		}
		record(map[string]any{"run_id": started.ID, "jobs": jobs}, "The work started, but this card could not record it. Open the close to see its status.")
	}
	mux.HandleFunc("POST /v1/closes/{id}/chat/{message}/confirm", confirm)
	mux.HandleFunc("POST /v1/closes/chat/confirm/{message}", confirm)
	mux.HandleFunc("GET /v1/closes/chat", func(w http.ResponseWriter, r *http.Request) { history(w, r, nil) })
	mux.HandleFunc("POST /v1/closes/chat", post)
	mux.HandleFunc("GET /v1/closes/{id}/chat", func(w http.ResponseWriter, r *http.Request) { history(w, r, nil) })
	mux.HandleFunc("POST /v1/closes/{id}/chat", post)
}

// expireChat removes firm-wide assistant messages older than 30 days, whether or not their author returns.
func expireChat(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `DELETE FROM chat_messages WHERE close_id IS NULL AND created_at<now()-interval '30 days'`)
	return err
}
