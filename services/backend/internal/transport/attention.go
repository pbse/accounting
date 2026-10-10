package transport

import (
	"fmt"
	"crypto/sha256"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

// attentionKinds are the events that need a person; running, queued, waiting and finished work never is one.
var attentionKinds = []string{"answered", "confirm", "failed", "approve", "changed", "fix", "release", "send", "email", "unanswered", "goal", "handoff"}

// undeliveredEmail is SQL for an email m, the latest for its request, that did not reach the client.
const undeliveredEmail = `(m.delivery IN ('bounced','complained','failed','suppressed') OR m.state IN ('failed','unknown') OR (m.state='queued' AND m.error<>''))`

// waitingRequest is SQL for a request q that is the latest one of work still waiting on the client and has no link reply.
const waitingRequest = `EXISTS(SELECT 1 FROM request_jobs rj JOIN preparation_jobs j ON j.id=rj.job_id WHERE rj.request_id=q.id AND j.state='waiting_client' AND NOT EXISTS(SELECT 1 FROM request_jobs n WHERE n.job_id=rj.job_id AND n.request_id>rj.request_id))
 AND NOT EXISTS(SELECT 1 FROM request_submissions WHERE request_id=q.id)`

// waitingCoverage is SQL for a request q about accounts alone, before any work, still current on its close's capture, with
// no reply and no newer request for any of its accounts.
const waitingCoverage = `(q.coverage_scope IS NOT NULL AND NOT EXISTS(SELECT 1 FROM request_jobs WHERE request_id=q.id) AND NOT EXISTS(SELECT 1 FROM request_submissions WHERE request_id=q.id)
 AND ` + preparation.RequestCoverageIsCurrent + `
 AND NOT EXISTS(SELECT 1 FROM work_requests n JOIN request_drafts nd ON nd.id=n.draft_id CROSS JOIN jsonb_array_elements(n.coverage_scope->'bindings') nb
  WHERE nd.close_id=d.close_id AND n.id>q.id AND q.coverage_scope->'bindings' @> jsonb_build_array(jsonb_build_object('account_id',nb->>'account_id'))))`

// attentionQuery derives each event from current work, review and request state; a key changes when the situation does.
const attentionQuery = `WITH scope AS (
 SELECT c.id,c.entity,c.period,COALESCE(c.due_on::text,'') AS due_on,c.priority,CASE WHEN c.owner='' THEN 'none' WHEN c.owner=$3 THEN 'mine' ELSE 'other' END AS owner
 FROM client_closes c WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
), drafts AS (
 SELECT x.id,x.close_id,x.kind,x.created_at,x.review_id,x.review_state,x.paper,
  COALESCE(jsonb_typeof(x.paper->'sources')='array' AND x.paper->'sources'<>'[]'::jsonb,false) AS sourced,
  COALESCE(jsonb_typeof(x.paper->'blockers')='array' AND x.paper->'blockers'<>'[]'::jsonb,false) AS blocked
 FROM workpaper_review_state x WHERE x.close_id IN (SELECT id FROM scope) AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=x.close_id AND n.paper->>'revises'=x.id::text)
), candidates AS MATERIALIZED (
 -- Requests still unanswered in the person's closes; reminder and waiting checks run on these alone, not on every request.
 SELECT q.*,s.owner AS close_owner FROM scope s JOIN request_drafts d ON d.close_id=s.id JOIN work_requests q ON q.draft_id=d.id
 WHERE NOT EXISTS(SELECT 1 FROM request_submissions WHERE request_id=q.id)
), events AS (
 SELECT 'answered:'||s.id AS key,'answered' AS kind,d.close_id,s.created_at AS at,'Client replied to request #'||s.request_id||CASE WHEN s.complete THEN '' ELSE ' (partial)' END AS text,'' AS workpaper
  FROM request_submissions s JOIN work_requests q ON q.id=s.request_id JOIN request_drafts d ON d.id=q.draft_id
  WHERE d.close_id IN (SELECT id FROM scope) AND s.id=(SELECT max(id) FROM request_submissions WHERE request_id=s.request_id) AND s.created_at>now()-interval '14 days'
 UNION ALL
 SELECT CASE j.state WHEN 'failed' THEN 'failed:' ELSE 'confirm:' END||j.id||':'||i.revision,CASE j.state WHEN 'failed' THEN 'failed' ELSE 'confirm' END,j.close_id,j.state_changed_at,
  CASE j.state WHEN 'failed' THEN 'Preparation failed for ' ELSE 'Confirm the facts read from ' END||e.name,''
  FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.close_id IN (SELECT id FROM scope) AND j.state IN ('needs_confirmation','failed')
 UNION ALL
 SELECT 'approve:'||x.id,'approve',x.close_id,x.created_at,'Draft #'||x.id||' ('||x.kind||') is ready for approval',x.id::text FROM drafts x WHERE x.review_state='pending' AND x.sourced AND NOT x.blocked
 UNION ALL
 SELECT 'fix:'||x.id||':'||x.review_id,'fix',x.close_id,(SELECT created_at FROM workpaper_reviews WHERE id::text=x.review_id),'Changes were requested on draft #'||x.id,x.id::text FROM drafts x WHERE x.review_state='changes_requested'
 UNION ALL
 SELECT 'changed:'||x.id||':'||left(md5(COALESCE(c.sources,'')||'|'||COALESCE(c.parent,'')),16),'changed',x.close_id,GREATEST(x.created_at,c.replaced_at),
  'A source of draft #'||x.id||' changed'||CASE WHEN (SELECT decision FROM workpaper_reviews WHERE workpaper_id=x.id ORDER BY id DESC LIMIT 1)='approved' THEN ' after it was approved' ELSE '' END,x.id::text
  FROM drafts x CROSS JOIN LATERAL (SELECT
   (SELECT string_agg(e.id||'/'||e.digest||'/'||COALESCE(e.superseded_by::text,''),',' ORDER BY e.id) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(x.paper->'sources')='array' THEN x.paper->'sources' ELSE '[]'::jsonb END) src JOIN evidence e ON e.id::text=src->>'id') AS sources,
   (SELECT max(e.replaced_at) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(x.paper->'sources')='array' THEN x.paper->'sources' ELSE '[]'::jsonb END) src JOIN evidence e ON e.id::text=src->>'id') AS replaced_at,
   (SELECT p.review_id||'/'||p.digest FROM workpaper_source_review_state p WHERE p.id::text=COALESCE(x.paper->'carry_forward'->>'paper_id',x.paper->'recurring_accrual'->>'paper_id')) AS parent) c
  WHERE x.review_state='source_changed'
 UNION ALL
 SELECT 'release:'||a.id,'release',a.close_id,COALESCE(a.scanned_at,a.created_at),'Scanned client file '||a.name||' is waiting for release','' FROM client_attachments a WHERE a.close_id IN (SELECT id FROM scope) AND a.state='clean'
 UNION ALL
 SELECT 'held:'||i.id,'release',i.close_id,i.received_at,'An email reply to request #'||i.request_id||' is held for review','' FROM inbound_replies i WHERE i.close_id IN (SELECT id FROM scope) AND i.state='held'
 UNION ALL
 SELECT 'send:'||q.id,'send',d.close_id,q.approved_at,'Request #'||q.id||' is approved; no delivery is recorded','' FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id
  WHERE d.close_id IN (SELECT id FROM scope) AND q.delivered_at IS NULL AND NOT EXISTS(SELECT 1 FROM email_delivery m WHERE m.request_id=q.id) AND ` + waitingRequest + `
 UNION ALL
 SELECT 'email:'||m.id||':'||m.state||':'||COALESCE(m.delivery,''),'email',d.close_id,m.created_at,'The email for request #'||q.id||' was not delivered',''
  FROM candidates q JOIN request_drafts d ON d.id=q.draft_id JOIN LATERAL (SELECT * FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1) m ON true
  WHERE ` + undeliveredEmail + ` AND (` + waitingRequest + ` OR ` + waitingCoverage + `)
 UNION ALL
 SELECT 'unanswered:'||q.id||':'||f.follow,'unanswered',d.close_id,COALESCE((SELECT max(created_at) FROM email_outbox WHERE request_id=q.id),q.approved_at),
  'Request #'||q.id||CASE f.follow WHEN 'exhausted' THEN ' has had no reply after every reminder the firm allows' ELSE ' cannot be reminded again before its link expires' END||'. Contact the client another way, or withdraw and replace the request.',''
  FROM candidates q JOIN request_drafts d ON d.id=q.draft_id CROSS JOIN LATERAL (SELECT ` + reminderFollow + ` AS follow) f
  WHERE q.close_owner IN ('mine','none') AND f.follow IN ('exhausted','expiring')
  AND NOT EXISTS(SELECT 1 FROM (SELECT * FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1) m WHERE ` + undeliveredEmail + `)
  AND (` + waitingRequest + ` OR ` + waitingCoverage + `)
 UNION ALL
 SELECT 'goal:'||g.id||':'||CASE g.state WHEN 'reached' THEN floor(extract(epoch FROM g.reached_at))::text ELSE 'stopped' END,'goal',g.close_id,COALESCE(g.reached_at,g.finished_at),
  CASE g.state WHEN 'reached' THEN 'Every uploaded document has a draft ready for review' ELSE g.events->-1->>'text' END,''
  FROM close_goals g WHERE g.close_id IN (SELECT id FROM scope) AND g.author=$3
  AND (g.state='reached' OR (g.state='stopped' AND g.events->-1->>'actor' NOT IN ($3,'agent') AND g.finished_at>now()-interval '14 days'))
 UNION ALL
 SELECT 'handoff:'||h.id,'handoff',g.close_id,h.created_at,h.created_by||' handed you '||h.document||CASE WHEN h.note<>'' THEN ': '||h.note ELSE '' END,''
  FROM goal_handoffs h JOIN close_goals g ON g.id=h.goal_id WHERE g.close_id IN (SELECT id FROM scope) AND h.assignee=$3 AND h.closed_at IS NULL
 UNION ALL
 SELECT 'close-handoff:'||o.id,'handoff',o.close_id,o.created_at,o.actor||' handed you this close'||CASE WHEN o.reason<>'' THEN ': '||o.reason ELSE '' END,''
  FROM close_owner_changes o WHERE o.close_id IN (SELECT id FROM scope WHERE owner='mine') AND o.to_owner=$3 AND o.actor<>$3
  AND o.id=(SELECT max(id) FROM close_owner_changes WHERE close_id=o.close_id)
 UNION ALL
 SELECT 'handoff:'||h.id||':reminder','handoff',g.close_id,h.created_at+interval '2 days',h.document||' has been with you for at least 2 days, handed by '||h.created_by||'.',''
  FROM goal_handoffs h JOIN close_goals g ON g.id=h.goal_id WHERE g.close_id IN (SELECT id FROM scope) AND h.assignee=$3 AND h.closed_at IS NULL AND h.created_at<=now()-interval '2 days'
 UNION ALL
 SELECT 'handoff-owner:'||h.id,'goal',g.close_id,` + handoffEscalated + `,` + handoffStall + `,''
  FROM goal_handoffs h JOIN close_goals g ON g.id=h.goal_id WHERE g.close_id IN (SELECT id FROM scope) AND g.author=$3 AND h.assignee<>$3 AND h.closed_at IS NULL AND ` + handoffEscalated + `<=now()
)
SELECT v.key,v.kind,v.close_id::text,v.at,v.text,v.workpaper,COALESCE(h.job_id::text,CASE WHEN uq.coverage_scope IS NULL THEN (SELECT min(job_id)::text FROM request_jobs WHERE request_id=uq.id) END,''),COALESCE(h.account_id,''),s.entity,s.period,s.due_on,s.priority,s.owner,mk.read_at IS NOT NULL,mk.snoozed_until
FROM events v JOIN scope s ON s.id=v.close_id LEFT JOIN attention_marks mk ON mk.organization_id=$1 AND mk.subject=$3 AND mk.key=v.key
LEFT JOIN goal_handoffs h ON h.id=CASE WHEN v.key LIKE 'handoff:%' THEN split_part(v.key,':',2)::bigint END
LEFT JOIN work_requests uq ON uq.id=CASE WHEN v.key LIKE 'unanswered:%' THEN split_part(v.key,':',2)::bigint END
ORDER BY v.at DESC,v.key`

type attentionEvent struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Yours says the reader holds the authority the event's action needs (a reviewer's for drafts and facts, a sender's
	// for requests); otherwise it waits on a teammate, and Home counts it apart.
	Yours        bool       `json:"yours"`
	Text         string     `json:"text"`
	At           time.Time  `json:"at"`
	WorkpaperID  string     `json:"workpaper_id,omitempty"`
	JobID        string     `json:"job_id,omitempty"`
	AccountID    string     `json:"account_id,omitempty"`
	Read         bool       `json:"read"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	group        attentionGroup
}

type attentionGroup struct {
	CloseID  string           `json:"close_id"`
	Entity   string           `json:"entity"`
	Period   string           `json:"period"`
	DueOn    string           `json:"due_on"`
	Priority string           `json:"priority"`
	Owner    string           `json:"owner"`
	Events   []attentionEvent `json:"events"`
}

// reader is a database or a transaction: the reads below run on either, so Home can take them in its own snapshot.
type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// eventYours says whether the reader may take the action an event of kind names: replies, drafts, facts, failures,
// files and request follow-ups need the reviewer role, an undelivered email the sender role with it, and a goal or a
// hand-off only the reader it names.
func eventYours(r *http.Request, kind string) bool {
	a := auth.Actor(r.Context())
	if a == nil {
		return false
	}
	if a.Organization == "" {
		return true
	}
	switch kind {
	case "answered", "approve", "confirm", "failed", "fix", "changed", "release", "send", "unanswered":
		// A reviewer acts on an approved request with no delivery by recording one made another way (the work handler's
		// delivery action needs the reviewer role alone), and follows up or withdraws an unanswered one; only sending the
		// email itself, the email kind below, needs the sender role too.
		return slices.Contains(a.Permissions, "workpapers:review")
	case "email":
		// Sending the email again is a sender's act as well as a reviewer's, as the send handler requires.
		return slices.Contains(a.Permissions, "requests:send") && slices.Contains(a.Permissions, "workpapers:review")
	}
	return true
}

func attentionEvents(ctx context.Context, db reader, r *http.Request) ([]attentionEvent, error) {
	rows, err := db.QueryContext(ctx, attentionQuery, organization(r), firmAccess(r), subject(r))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []attentionEvent{}
	for rows.Next() {
		var e attentionEvent
		var snoozed sql.NullTime
		g := &e.group
		if err = rows.Scan(&e.Key, &e.Kind, &g.CloseID, &e.At, &e.Text, &e.WorkpaperID, &e.JobID, &e.AccountID, &g.Entity, &g.Period, &g.DueOn, &g.Priority, &g.Owner, &e.Read, &snoozed); err != nil {
			return nil, err
		}
		if snoozed.Valid && snoozed.Time.After(time.Now()) {
			e.SnoozedUntil = &snoozed.Time
		}
		e.Yours = eventYours(r, e.Kind)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A hand-off two days old is one decision, not two: once its reminder exists, the reminder stands for it (its own
	// key, so it is new again even when the hand-off was read) and the hand-off itself is not listed.
	reminded := map[string]bool{}
	for _, e := range events {
		if base, found := strings.CutSuffix(e.Key, ":reminder"); found {
			reminded[base] = true
		}
	}
	kept := events[:0]
	for _, e := range events {
		if !reminded[e.Key] {
			kept = append(kept, e)
		}
	}
	return kept, nil
}

func attentionPreferences(ctx context.Context, db reader, r *http.Request) ([]string, bool, error) {
	var raw []byte
	var browser bool
	err := db.QueryRowContext(ctx, `SELECT array_to_json(kinds),browser FROM attention_preferences WHERE organization_id=$1 AND subject=$2`, organization(r), subject(r)).Scan(&raw, &browser)
	if err == sql.ErrNoRows {
		return attentionKinds, false, nil
	}
	kinds := []string{}
	if err == nil {
		err = json.Unmarshal(raw, &kinds)
	}
	return kinds, browser, err
}

// attentionStreamEvery is how often the stream reads the inbox again; attentionStreamFor is how long one request
// lives before the sidebar opens another. Tests shorten both.
var attentionStreamEvery = 10 * time.Second
var attentionStreamFor = 2 * time.Minute

// sessionDB holds the sign-in sessions (the firm pool itself when there is no separate one), for the stream's recheck.
func registerAttention(mux *http.ServeMux, db, sessionDB *sql.DB) {
	// One list per person of what needs them, grouped by close; marks and preferences are theirs alone.
	mux.HandleFunc("GET /v1/closes/attention", func(w http.ResponseWriter, r *http.Request) {
		events, err := attentionEvents(r.Context(), db, r)
		if err != nil {
			problem(w, 503, "Attention inbox unavailable.")
			return
		}
		kinds, browser, err := attentionPreferences(r.Context(), db, r)
		if err != nil {
			problem(w, 503, "Attention inbox unavailable.")
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		jsonReply(w, 200, attentionInbox(events, kinds, browser, r.URL.Query().Get("view"), page, r.URL.Query().Get("order") == "priority"))
	})
	// The inbox as it changes: one long-lived request that says, as NDJSON, when the person's unread items change
	// (their count and keys, or the order and state of what the sidebar shows), read again every attentionStreamEvery;
	// a line every tick keeps the connection alive. It ends after attentionStreamFor, and the sidebar opens it again
	// (features/dashboard/SideNotices.tsx), signing in afresh as any request does.
	mux.HandleFunc("GET /v1/closes/attention/stream", func(w http.ResponseWriter, r *http.Request) {
		// The server's write timeout is for answers; this response lives as long as the stream does.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(attentionStreamFor + time.Minute))
		// The headers go with the first line, so the stream begins, as the request log measures it, when its first
		// reading is ready.
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		send := func(v map[string]any) bool {
			line, _ := json.Marshal(v)
			if _, err := w.Write(append(line, '\n')); err != nil {
				return false
			}
			_ = http.NewResponseController(w).Flush()
			return true
		}
		ticker := time.NewTicker(attentionStreamEvery)
		defer ticker.Stop()
		deadline := time.NewTimer(attentionStreamFor)
		defer deadline.Stop()
		last := ""
		first := true
		// The stream ends with a final document, as a chat's stream does, so the sidebar reads a normal ending.
		defer send(map[string]any{"ended": true})
		// Access is checked again on every read, as it is when a request enters: a membership revoked, a firm
		// suspended, or a server-held sign-in session (AUTH_SESSION=server) revoked while the stream is open ends it
		// with nothing more said.
		sessions, _ := serverSessions()
		verifier := workspaceVerifier()
		still := func() bool {
			if allowed, err := organizationAccess(db, r); err != nil || !allowed {
				return false
			}
			if sessions != nil && r.Context().Value(signedInKey{}) != nil {
				cookie, err := r.Cookie(firmSession.cookie)
				if err != nil {
					return false
				}
				// The session rows are the system's, not a firm's: the lookup runs without this request's actor, as it
				// does when a request enters, on a context of its own.
				lookup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				token, _, status := sessions.accessToken(lookup, sessionDB, firmSession, verifier, cookie.Value)
				cancel()
				// A session refreshed to another token may carry other claims (a role narrowed, a client taken away): the
				// stream ends rather than read on with the claims it entered with, and the sidebar opens it again, which
				// signs in afresh.
				if status != 0 || "Bearer "+token != r.Header.Get("Authorization") {
					return false
				}
			}
			return true
		}
		for {
			if !first && !still() {
				return
			}
			events, err := attentionEvents(r.Context(), db, r)
			var kinds []string
			var browser bool
			if err == nil {
				kinds, browser, err = attentionPreferences(r.Context(), db, r)
			}
			if err != nil {
				send(map[string]any{"type": "error", "message": "Attention inbox unavailable."})
				return
			}
			inbox := attentionInbox(events, kinds, browser, "unread", 0, true)
			keys, _ := inbox["unread_keys"].([]string)
			// What the sidebar shows is the first page of groups in priority order: a change of order, due date or
			// text is a change, not only a key arriving or leaving.
			shown, _ := json.Marshal(inbox["groups"])
			fingerprint := strings.Join(keys, "\n") + "\n" + string(shown)
			if first || fingerprint != last {
				// The version names this reading of the inbox, so the sidebar can tell a change from a repeat.
				version := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint)))[:16]
				if !send(map[string]any{"type": "attention", "unread": len(keys), "unread_keys": keys, "version": version}) {
					return
				}
				last = fingerprint
				first = false
			} else if !send(map[string]any{"type": "tick"}) {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-deadline.C:
				return
			case <-ticker.C:
			}
		}
	})
	mux.HandleFunc("POST /v1/closes/attention", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action string   `json:"action"`
			Keys   []string `json:"keys"`
			Until  string   `json:"until"`
		}
		if !decode(w, r, &in) {
			return
		}
		if subject(r) == "" {
			problem(w, 403, "Sign in to keep your own read and snooze marks.")
			return
		}
		if !slices.Contains([]string{"read", "unread", "snooze", "wake"}, in.Action) || len(in.Keys) == 0 || len(in.Keys) > 200 {
			problem(w, 422, "Mark 1 to 200 items read, unread, snoozed or awake.")
			return
		}
		var until any
		if in.Action == "snooze" {
			t, err := time.Parse(time.RFC3339, in.Until)
			if err != nil || !t.After(time.Now()) || t.After(time.Now().Add(30*24*time.Hour)) {
				problem(w, 422, "Snooze until a time within the next 30 days.")
				return
			}
			until = t
		}
		events, err := attentionEvents(r.Context(), db, r)
		if err != nil {
			problem(w, 503, "Attention inbox unavailable.")
			return
		}
		current := []string{}
		for _, e := range events {
			current = append(current, e.Key)
		}
		for _, k := range in.Keys {
			if !slices.Contains(current, k) {
				problem(w, 409, "Some items changed or are no longer yours to see. Refresh and try again.")
				return
			}
		}
		// Marks for events that no longer exist go, so each person keeps at most their current events' marks.
		_, err = db.ExecContext(r.Context(), `WITH gone AS (DELETE FROM attention_marks WHERE organization_id=$1 AND subject=$2 AND NOT key=ANY($5::text[]))
 INSERT INTO attention_marks(organization_id,subject,key,read_at,snoozed_until) SELECT $1,$2,k,CASE $4 WHEN 'read' THEN now() END,CASE $4 WHEN 'snooze' THEN $6::timestamptz END FROM unnest($3::text[]) k
 ON CONFLICT (organization_id,subject,key) DO UPDATE SET read_at=CASE $4 WHEN 'read' THEN now() WHEN 'unread' THEN NULL ELSE attention_marks.read_at END,snoozed_until=CASE $4 WHEN 'snooze' THEN $6::timestamptz WHEN 'wake' THEN NULL ELSE attention_marks.snoozed_until END`,
			organization(r), subject(r), in.Keys, in.Action, current, until)
		if err != nil {
			problem(w, 503, "Marks could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]int{"marked": len(in.Keys)})
	})
	mux.HandleFunc("POST /v1/closes/attention/preferences", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kinds   []string `json:"kinds"`
			Browser bool     `json:"browser"`
		}
		if !decode(w, r, &in) {
			return
		}
		if subject(r) == "" {
			problem(w, 403, "Sign in to keep your own preferences.")
			return
		}
		if in.Kinds == nil || slices.ContainsFunc(in.Kinds, func(k string) bool { return !slices.Contains(attentionKinds, k) }) {
			problem(w, 422, "Choose kinds from the attention inbox's list.")
			return
		}
		slices.Sort(in.Kinds)
		in.Kinds = slices.Compact(in.Kinds)
		if _, err := db.ExecContext(r.Context(), `INSERT INTO attention_preferences(organization_id,subject,kinds,browser) VALUES($1,$2,$3,$4) ON CONFLICT (organization_id,subject) DO UPDATE SET kinds=EXCLUDED.kinds,browser=EXCLUDED.browser`, organization(r), subject(r), in.Kinds, in.Browser); err != nil {
			problem(w, 503, "Preferences could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]any{"kinds": in.Kinds, "browser": in.Browser})
	})
}

// attentionInbox is the inbox response for events already read: the view's events (all, mine, unread or snoozed) in
// pages of 200, grouped by close, with the keys that are new to the reader. With priority, the reader's own decisions
// come first, then the most pressing close (overdue, then by due date, undated last) and within it the newest event,
// applied before the page boundary so page one holds what matters most; otherwise newest first. Home reads page one
// of every event in the same snapshot as its counts, so the card and the list it opens agree.
func attentionInbox(events []attentionEvent, kinds []string, browser bool, view string, page int, priority bool) map[string]any {
	unread, shown := []string{}, []attentionEvent{}
	for _, e := range events {
		// New, for the badge and a notification, is what the reader can act on; a teammate's decision is shown, not announced.
		if e.SnoozedUntil == nil && !e.Read && e.Yours && slices.Contains(kinds, e.Kind) {
			unread = append(unread, e.Key)
		}
		// New, as a view, is what the count calls new: unread, the reader's to act on, of a kind they count.
		isNew := !e.Read && e.Yours && slices.Contains(kinds, e.Kind)
		if (e.SnoozedUntil != nil) == (view == "snoozed") && (view != "unread" || isNew) && (view != "mine" || e.group.Owner == "mine") {
			shown = append(shown, e)
		}
	}
	// Home asks for the reader's own decisions first, then the most pressing close: overdue closes, then by due date,
	// undated last, and within a close the newest event; the order is applied before the page boundary, so page one
	// holds what matters most.
	if priority {
		today := time.Now().UTC().Format("2006-01-02")
		rank := func(g attentionGroup) int {
			switch {
			case g.DueOn != "" && g.DueOn < today:
				return 0
			case g.DueOn != "":
				return 1
			}
			return 2
		}
		// The reader's own decisions come before a teammate's, so the first page holds what the reader can act on.
		own := func(e attentionEvent) int {
			if e.Yours {
				return 0
			}
			return 1
		}
		slices.SortStableFunc(shown, func(a, b attentionEvent) int {
			if c := own(a) - own(b); c != 0 {
				return c
			}
			if c := rank(a.group) - rank(b.group); c != 0 {
				return c
			}
			if c := strings.Compare(a.group.DueOn, b.group.DueOn); c != 0 {
				return c
			}
			if c := strings.Compare(a.group.CloseID, b.group.CloseID); c != 0 {
				return c
			}
			return b.At.Compare(a.At)
		})
	}
	// Pages of 200 keep every item reachable; the unread keys cover all of them, for the badge and notifications.
	page = min(max(page, 0), max(len(shown)-1, 0)/200)
	start, end := page*200, min(page*200+200, len(shown))
	groups := []*attentionGroup{}
	for _, e := range shown[start:end] {
		i := slices.IndexFunc(groups, func(g *attentionGroup) bool { return g.CloseID == e.group.CloseID })
		if i < 0 {
			g := e.group
			groups, i = append(groups, &g), len(groups)
		}
		groups[i].Events = append(groups[i].Events, e)
	}
	return map[string]any{"groups": groups, "unread": len(unread), "unread_keys": unread, "page": page, "has_more": end < len(shown), "kinds": attentionKinds, "preferences": map[string]any{"kinds": kinds, "browser": browser}}
}
