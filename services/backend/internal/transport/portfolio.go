package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/auth"
)

// closeAccess is the organization boundary's check that member $4 can open close $1 in firm $2 ($3: all clients).
const closeAccess = `SELECT EXISTS(SELECT 1 FROM client_closes WHERE id::text=$1 AND organization_id=$2 AND ($3 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=client_closes.organization_id AND g.subject=$4 AND lower(g.client_entity)=lower(client_closes.entity) AND g.granted)))`

type portfolioGoal struct {
	action, due, compare, state, finish string
	mine, visible                       bool
}

var errPortfolioGoal = errors.New("Try a goal such as “prepare my clients due Friday”, “hand over my clients due Friday”, “recheck failed work”, “stop work waiting on the client” or “prepare all visible”. A goal selects closes by: my clients, due on, before or by a day or date, work waiting on the client, failed work, or the inbox's current view.")

var portfolioFiller = strings.Fields("all the closes close clients client work everything jobs that are is with and of in")

// parsePortfolioGoal reads a goal such as "prepare my clients due Friday" without a model; today resolves day names.
func parsePortfolioGoal(text string, today time.Time) (portfolioGoal, error) {
	words := strings.Fields(strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!?")))
	var g portfolioGoal
	next := func(i int) string {
		if i < len(words) {
			return words[i]
		}
		return ""
	}
	start := 1
	switch {
	case len(words) == 0:
		return g, errPortfolioGoal
	case words[0] == "hand" || words[0] == "delegate":
		// Handing over gets each close ready for review, or signed off when a whole sign-off phrase says so.
		g.action = "delegate"
		if words[0] == "hand" && next(1) == "over" {
			start = 2
		}
	case slices.Contains([]string{"prepare", "recheck", "stop"}, words[0]):
		g.action = words[0]
	default:
		return g, errPortfolioGoal
	}
	for i := start; i < len(words); i++ {
		switch w := words[i]; {
		case g.action == "delegate" && w == "to":
			if next(i+1) == "the" {
				i++
			}
			if next(i+1) != "agent" {
				return g, errPortfolioGoal
			}
			i++
		case g.action == "delegate" && (w == "ready" || w == "for" || w == "signed"):
			// Only a whole phrase sets the finish: "[ready] for review", "[ready] for sign-off" (or signoff, sign off) or
			// "signed off"; a second one is refused rather than one of them being ignored.
			finish := ""
			if w == "ready" && next(i+1) == "for" {
				w, i = "for", i+1
			}
			switch {
			case w == "signed" && next(i+1) == "off":
				finish, i = "signoff", i+1
			case w == "for" && next(i+1) == "review":
				finish, i = "review", i+1
			case w == "for" && (next(i+1) == "sign-off" || next(i+1) == "signoff"):
				finish, i = "signoff", i+1
			case w == "for" && next(i+1) == "sign" && next(i+2) == "off":
				finish, i = "signoff", i+2
			}
			if finish == "" || g.finish != "" {
				return g, errPortfolioGoal
			}
			g.finish = finish
		case w == "my" || w == "mine":
			g.mine = true
		case w == "visible":
			g.visible = true
		case w == "failed":
			g.state = "failed"
		case w == "waiting" && (next(i+1) == "on" || next(i+1) == "for"):
			i += 2
			if next(i) == "the" {
				i++
			}
			if !slices.Contains([]string{"client", "clients", "replies"}, next(i)) {
				return g, errPortfolioGoal
			}
			g.state = "waiting_client"
		case w == "due":
			g.compare = "by"
			if c := next(i + 1); c == "on" || c == "before" || c == "by" {
				g.compare = c
				i++
			}
			day, last, ok := parseDay(words, i+1, today)
			if !ok {
				return g, errPortfolioGoal
			}
			i = last
			g.due = day.Format("2006-01-02")
		case !slices.Contains(portfolioFiller, w):
			return g, errPortfolioGoal
		}
	}
	if g.action == "delegate" && g.finish == "" {
		g.finish = "review"
	}
	return g, nil
}

var weekdays = map[string]time.Weekday{"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday}

// browserToday is the member's date as their browser sent it, trusted within a day of the server's, so day names
// resolve in their own time zone.
func browserToday(sent string) time.Time {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if t, err := time.Parse("2006-01-02", sent); err == nil && t.Sub(today).Abs() <= 24*time.Hour {
		return t
	}
	return today
}

// parseDay reads the day named at words[i]: today, tomorrow, a weekday (today's or the next), "this week" (to Sunday),
// "the 10th" (the next date with that day of the month) or YYYY-MM-DD. It returns the day and the index of its last word.
func parseDay(words []string, i int, today time.Time) (time.Time, int, bool) {
	at := func(n int) string {
		if n < len(words) {
			return words[n]
		}
		return ""
	}
	if at(i) == "the" {
		i++
	}
	for _, suffix := range []string{"st", "nd", "rd", "th"} {
		if !strings.HasSuffix(at(i), suffix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSuffix(at(i), suffix)); err == nil && n >= 1 && n <= 31 {
			for month := today.Month(); month < today.Month()+12; month++ {
				if day := time.Date(today.Year(), month, n, 0, 0, 0, 0, time.UTC); day.Day() == n && !day.Before(today) {
					return day, i, true
				}
			}
		}
	}
	switch d := at(i); {
	case d == "today":
		return today, i, true
	case d == "tomorrow":
		return today.AddDate(0, 0, 1), i, true
	case d == "this" && at(i+1) == "week":
		return today.AddDate(0, 0, (7-int(today.Weekday()))%7), i + 1, true
	}
	if wd, ok := weekdays[at(i)]; ok {
		return today.AddDate(0, 0, (int(wd)-int(today.Weekday())+7)%7), i, true
	}
	day, err := time.Parse("2006-01-02", at(i))
	return day, i, err == nil
}

// describe says in words which closes the goal selects, with its date resolved.
func (g portfolioGoal) describe(view, client string) string {
	parts := []string{"Closes you can access that are not signed off"}
	if g.mine {
		parts = append(parts, "that you own")
	}
	if g.due != "" {
		day, _ := time.Parse("2006-01-02", g.due)
		parts = append(parts, "due "+map[string]string{"on": "on", "before": "before", "by": "on or before"}[g.compare]+" "+day.Format("Mon 2 Jan 2006"))
	}
	if g.state == "waiting_client" {
		parts = append(parts, "with work waiting on the client")
	} else if g.state == "failed" {
		parts = append(parts, "with failed work")
	}
	if g.visible {
		scope := "with work in the inbox's " + strings.ReplaceAll(view, "_", " ") + " view"
		if client != "" {
			scope += " matching “" + client + "”"
		}
		parts = append(parts, scope)
	}
	return strings.Join(parts, ", ")
}

type portfolioItem struct {
	CloseID string `json:"close_id"`
	Label   string `json:"label"`
	Count   int    `json:"count"`
	State   string `json:"state"`
	Result  string `json:"result"`
	DueOn   string `json:"due_on,omitempty"`
}

type portfolioOperation struct {
	ID        string          `json:"id"`
	Action    string          `json:"action"`
	Finish    string          `json:"finish,omitempty"`
	Goal      string          `json:"goal"`
	Scope     string          `json:"scope"`
	HasMore   bool            `json:"has_more"`
	Reason    string          `json:"reason"`
	CreatedAt time.Time       `json:"created_at"`
	Items     []portfolioItem `json:"items"`
}

// readPortfolio returns the member's own operation; a close they can no longer access shows neither its name nor its result.
func readPortfolio(ctx context.Context, db *sql.DB, r *http.Request, id string) (portfolioOperation, error) {
	var op portfolioOperation
	err := db.QueryRowContext(ctx, `SELECT id::text,action,COALESCE(finish,''),goal,scope,has_more,COALESCE(reason,''),created_at FROM portfolio_operations WHERE id::text=$1 AND organization_id=$2 AND author=$3`, id, organization(r), subject(r)).Scan(&op.ID, &op.Action, &op.Finish, &op.Goal, &op.Scope, &op.HasMore, &op.Reason, &op.CreatedAt)
	if err != nil {
		return op, err
	}
	rows, err := db.QueryContext(ctx, `SELECT p.close_id::text,p.label,p.count,p.state,p.result,COALESCE(p.due_on::text,''),($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) FROM portfolio_items p JOIN client_closes c ON c.id=p.close_id WHERE p.operation_id::text=$1 ORDER BY p.position`, id, firmAccess(r), subject(r))
	if err != nil {
		return op, err
	}
	defer rows.Close()
	op.Items = []portfolioItem{}
	for rows.Next() {
		var item portfolioItem
		var allowed bool
		if err = rows.Scan(&item.CloseID, &item.Label, &item.Count, &item.State, &item.Result, &item.DueOn, &allowed); err != nil {
			return op, err
		}
		if !allowed {
			item.CloseID, item.Label, item.Result, item.DueOn = "", "A close you can no longer access", "", ""
		}
		op.Items = append(op.Items, item)
	}
	return op, rows.Err()
}

func registerPortfolio(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/portfolio", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text   string `json:"text"`
			View   string `json:"view"`
			Client string `json:"client"`
			Today  string `json:"today"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Text, in.Client = strings.TrimSpace(in.Text), strings.TrimSpace(in.Client)
		goal, err := parsePortfolioGoal(in.Text, browserToday(in.Today))
		if err != nil || len(in.Text) > 300 || len(in.Client) > 200 {
			problem(w, 422, errPortfolioGoal.Error())
			return
		}
		view := "true"
		if goal.visible {
			if in.View == "" {
				in.View = "all"
			}
			_, predicates := workPredicates()
			predicate, ok := predicates[in.View]
			if !ok {
				problem(w, 422, "Unknown inbox view.")
				return
			}
			view = `EXISTS(SELECT 1 FROM preparation_jobs j LEFT JOIN preparation_intakes i ON i.job_id=j.id LEFT JOIN evidence e ON e.id=i.source_id WHERE j.close_id=c.id AND (` + predicate + `))`
		} else {
			in.Client = ""
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Portfolio goals are unavailable.")
			return
		}
		defer tx.Rollback()
		rows, err := tx.QueryContext(r.Context(), `SELECT c.id::text,c.entity||' · '||c.period,COALESCE(c.due_on::text,'') FROM client_closes c WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$5 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
 AND ($3='' OR ($6='on' AND c.due_on=$3::date) OR ($6='before' AND c.due_on<$3::date) OR ($6='by' AND c.due_on<=$3::date))
 AND (NOT $4 OR (c.owner<>'' AND c.owner=$5))
 AND ($7='' OR EXISTS(SELECT 1 FROM preparation_jobs j WHERE j.close_id=c.id AND j.state=$7))
 AND ($8='' OR c.entity ILIKE '%'||$8||'%' OR c.period=$8)
 AND `+view+` ORDER BY c.due_on NULLS LAST,c.id LIMIT 101`, organization(r), firmAccess(r), goal.due, goal.mine, subject(r), goal.compare, goal.state, in.Client)
		if err != nil {
			problem(w, 503, "Could not select closes for this goal.")
			return
		}
		type selected struct{ id, label, due string }
		closes := []selected{}
		for rows.Next() {
			var c selected
			if err = rows.Scan(&c.id, &c.label, &c.due); err != nil {
				break
			}
			closes = append(closes, c)
		}
		if rows.Close(); err != nil || rows.Err() != nil {
			problem(w, 503, "Could not select closes for this goal.")
			return
		}
		more := len(closes) > 100
		closes = closes[:min(len(closes), 100)]
		var opID string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO portfolio_operations(organization_id,author,action,goal,scope,has_more,finish) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')) RETURNING id::text`, organization(r), subject(r), goal.action, in.Text, goal.describe(in.View, in.Client), more, goal.finish).Scan(&opID); err != nil {
			problem(w, 503, "Could not save this goal.")
			return
		}
		// A hand-over keeps each close's due date as the agent's deadline when the close page's hand-over would accept it.
		today := time.Now().UTC().Truncate(24 * time.Hour)
		for n, c := range closes {
			var digest, due, skipped string
			var count int
			switch goal.action {
			case "delegate":
				var owner string
				var since time.Time
				switch err = tx.QueryRowContext(r.Context(), `SELECT author,created_at FROM close_goals WHERE close_id=$1 AND state<>'stopped'`, c.id).Scan(&owner, &since); err {
				case nil:
					skipped = "Already handed to the agent by " + owner + " on " + since.UTC().Format("2 Jan 2006") + "."
				case sql.ErrNoRows:
					var inventory []byte
					var sources []json.RawMessage
					if inventory, err = preparationInventory(r.Context(), tx, c.id); err == nil {
						err = json.Unmarshal(scopeSources(inventory), &sources)
						count = len(sources)
					}
					if day, e := time.Parse("2006-01-02", c.due); e == nil && !day.Before(today.AddDate(0, 0, -1)) && !day.After(today.AddDate(1, 0, 1)) {
						due = c.due
					}
				}
			case "prepare":
				var inventory []byte
				var coverage json.RawMessage
				var sources []json.RawMessage
				if inventory, err = preparationInventory(r.Context(), tx, c.id); err == nil {
					if coverage, err = ledgerCoverage(r.Context(), tx, c.id); err == nil {
						digest = scopeDigest(inventory, coverage)
						err = json.Unmarshal(scopeSources(inventory), &sources)
						count = len(sources)
					}
				}
			case "recheck":
				var jobs []cancellableJob
				jobs, digest, err = recheckableJobs(r.Context(), tx, c.id, false)
				count = len(jobs)
			case "stop":
				var jobs []cancellableJob
				jobs, digest, err = cancellableJobs(r.Context(), tx, c.id, false)
				count = len(jobs)
			}
			state, result := "pending", ""
			switch {
			case skipped != "":
				state, result = "skipped", skipped
			case count == 0 && goal.action != "delegate":
				state, result = "skipped", "Nothing to "+goal.action+"."
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO portfolio_items(operation_id,position,close_id,label,digest,count,state,result,due_on) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::date)`, opID, n, c.id, c.label, digest, count, state, result, due)
			}
			if err != nil {
				problem(w, 503, "Could not preview this goal.")
				return
			}
		}
		if tx.Commit() != nil {
			problem(w, 503, "Could not save this goal.")
			return
		}
		op, err := readPortfolio(r.Context(), db, r, opID)
		if err != nil {
			problem(w, 503, "Could not read this goal.")
			return
		}
		jsonReply(w, 200, op)
	})
	mux.HandleFunc("GET /v1/closes/portfolio", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), `SELECT id::text FROM portfolio_operations o WHERE organization_id=$1 AND author=$2 AND EXISTS(SELECT 1 FROM portfolio_items p WHERE p.operation_id=o.id AND p.state='pending') ORDER BY o.id DESC LIMIT 5`, organization(r), subject(r))
		if err != nil {
			problem(w, 503, "Portfolio goals are unavailable.")
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if rows.Close(); err != nil || rows.Err() != nil {
			problem(w, 503, "Portfolio goals are unavailable.")
			return
		}
		ops := []portfolioOperation{}
		for _, id := range ids {
			op, err := readPortfolio(r.Context(), db, r, id)
			if err != nil {
				problem(w, 503, "Portfolio goals are unavailable.")
				return
			}
			ops = append(ops, op)
		}
		jsonReply(w, 200, map[string]any{"operations": ops})
	})
	// run acts on at most ten pending closes. Each close's action and its result commit together, under the member's own
	// permission and current access, with the digest its preview showed.
	mux.HandleFunc("POST /v1/closes/portfolio/run", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			ID        string `json:"id"`
			Confirmed bool   `json:"confirmed"`
			Reason    string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the listed closes before running this goal.")
			return
		}
		op, err := readPortfolio(r.Context(), db, r, in.ID)
		if err == sql.ErrNoRows {
			problem(w, 404, "Goal not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not read this goal.")
			return
		}
		reason := op.Reason
		if op.Action == "stop" && reason == "" {
			if reason = strings.TrimSpace(in.Reason); len(reason) < 1 || len(reason) > 500 {
				problem(w, 422, "Give a reason of 1–500 characters for stopping this work.")
				return
			}
			// The first saved reason is the one every close is stopped with.
			if err = db.QueryRowContext(r.Context(), `UPDATE portfolio_operations SET reason=COALESCE(reason,$2) WHERE id::text=$1 RETURNING reason`, op.ID, reason).Scan(&reason); err != nil {
				problem(w, 503, "Could not save the reason.")
				return
			}
		}
		for range 10 {
			ran, err := runPortfolioItem(r.Context(), db, r, op, reason, actor)
			if err != nil {
				problem(w, 503, "The run stopped before finishing. Run the goal again to continue.")
				return
			}
			if !ran {
				break
			}
		}
		if op, err = readPortfolio(r.Context(), db, r, op.ID); err != nil {
			problem(w, 503, "Could not read this goal.")
			return
		}
		jsonReply(w, 200, op)
	})
}

// runPortfolioItem claims one pending close and reports whether it did.
func runPortfolioItem(ctx context.Context, db *sql.DB, r *http.Request, op portfolioOperation, reason, actor string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var closeID, digest, due string
	var count int
	err = tx.QueryRowContext(ctx, `SELECT close_id::text,digest,count,COALESCE(due_on::text,'') FROM portfolio_items WHERE operation_id::text=$1 AND state='pending' ORDER BY position LIMIT 1 FOR UPDATE SKIP LOCKED`, op.ID).Scan(&closeID, &digest, &count, &due)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var allowed bool
	if err = tx.QueryRowContext(ctx, closeAccess, closeID, organization(r), firmAccess(r), subject(r)).Scan(&allowed); err != nil {
		return false, err
	}
	state, result := "failed", "You can no longer access this close."
	if allowed {
		if _, err = tx.ExecContext(ctx, `SAVEPOINT portfolio_action`); err != nil {
			return false, err
		}
		var refused *closeActionError
		switch op.Action {
		case "prepare":
			var id string
			var created bool
			id, created, refused = prepareClose(ctx, tx, closeID, digest, actor, auth.Agent(ctx))
			result = fmt.Sprintf("Coordinated %s as run #%s.", plural(count, "source document", "source documents"), id)
			if !created {
				result = "Already being prepared as run #" + id + "."
			}
		case "recheck":
			var n int
			n, refused = recheckClose(ctx, tx, organization(r), subject(r), closeID, digest)
			result = "Rechecking " + plural(n, "job", "jobs") + "."
		case "stop":
			var n int
			n, refused = stopClose(ctx, tx, closeID, digest, reason, actor)
			result = "Stopped " + plural(n, "job", "jobs") + "."
		case "delegate":
			// The goal's author is the member, as when they hand a close over from its page.
			g := goalRow{author: subject(r), state: "active", finish: op.Finish}
			if err = tx.QueryRowContext(ctx, `SELECT id::text,organization_id FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&g.close, &g.org); err != nil {
				return false, err
			}
			text := "Handed over by " + g.author + " with the goal “" + op.Goal + "”, to get it " + goalFinishes[op.Finish]
			if day, e := time.Parse("2006-01-02", due); e == nil {
				text += " by " + day.Format("Mon 2 Jan 2006")
			}
			if refused = handOver(ctx, tx, &g, due, text); refused == nil {
				result = "Handed to the agent as goal #" + g.id + "."
			}
		}
		state = "done"
		if refused != nil && refused.status >= 500 {
			return false, errors.New(refused.message)
		}
		if refused != nil {
			if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT portfolio_action`); err != nil {
				return false, err
			}
			state, result = "failed", refused.message
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE portfolio_items SET state=$3,result=$4,finished_at=now() WHERE operation_id::text=$1 AND close_id::text=$2`, op.ID, closeID, state, result); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
