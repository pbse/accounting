package transport

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A member's chats with the agent, listed newest first, with the scope each one is in; a chat is renamed or deleted
// here. The list holds every scope (firm-wide and each close) of the member's current role, and leaves out a close's
// chat once the close cannot be opened.
func registerChatSessions(mux *http.ServeMux, db *sql.DB, scopeOf func(*http.Request) string) {
	type session struct {
		ID       string    `json:"id"`
		Title    string    `json:"title"`
		CloseID  string    `json:"close_id"`
		Close    string    `json:"close"`
		LastAt   time.Time `json:"last_at"`
		Messages int       `json:"messages"`
	}
	mux.HandleFunc("GET /v1/closes/chat/sessions", func(w http.ResponseWriter, r *http.Request) {
		// Firm-wide turns that touched a close the member can no longer open are pruned first, and a chat left without
		// turns (pruned, expired) is removed, so nothing listed fails when opened.
		if err := pruneConversation(r.Context(), db, organization(r), subject(r), "", firmAccess(r), executive(r)); err != nil {
			problem(w, 503, "Your chats are unavailable.")
			return
		}
		if _, err := db.ExecContext(r.Context(), `DELETE FROM chat_sessions WHERE organization_id=$1 AND author=$2 AND NOT EXISTS(SELECT 1 FROM chat_messages m WHERE m.session_id=chat_sessions.id)`, organization(r), subject(r)); err != nil {
			problem(w, 503, "Your chats are unavailable.")
			return
		}
		// Pages of up to 50, newest first; has_more says whether an older page follows at the next offset.
		limit, offset := 50, 0
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n < limit {
			limit = n
		}
		if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
			offset = n
		}
		// A filter matches the title or the close's name, whatever page the chat is on.
		q := strings.Join(strings.Fields(r.URL.Query().Get("q")), " ")
		if runes := []rune(q); len(runes) > 100 {
			q = string(runes[:100])
		}
		// The text is matched as typed: a % or _ in it is not a wildcard.
		q = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q)
		rows, err := db.QueryContext(r.Context(), `SELECT s.id::text,s.title,COALESCE(s.close_id::text,''),COALESCE(c.entity||' · '||c.period,''),s.last_at,(SELECT count(*) FROM chat_messages m WHERE m.session_id=s.id)
 FROM chat_sessions s LEFT JOIN client_closes c ON c.id=s.close_id
 WHERE s.organization_id=$1 AND s.author=$2 AND s.executive=$3 AND (s.close_id IS NULL OR $4 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$2 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND ($7='' OR s.title ILIKE '%'||$7||'%' OR COALESCE(c.entity,'') ILIKE '%'||$7||'%')
 ORDER BY s.last_at DESC,s.id DESC LIMIT $5 OFFSET $6`, organization(r), subject(r), executive(r), firmAccess(r), limit+1, offset, q)
		if err != nil {
			problem(w, 503, "Your chats are unavailable.")
			return
		}
		defer rows.Close()
		sessions := []session{}
		for rows.Next() {
			var s session
			if err = rows.Scan(&s.ID, &s.Title, &s.CloseID, &s.Close, &s.LastAt, &s.Messages); err != nil {
				problem(w, 503, "Your chats are unavailable.")
				return
			}
			sessions = append(sessions, s)
		}
		if rows.Err() != nil {
			problem(w, 503, "Your chats are unavailable.")
			return
		}
		more := len(sessions) > limit
		if more {
			sessions = sessions[:limit]
		}
		jsonReply(w, 200, map[string]any{"sessions": sessions, "has_more": more, "next_offset": offset + len(sessions)})
	})
	mux.HandleFunc("POST /v1/closes/chat/sessions/{session}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action string `json:"action"`
			Title  string `json:"title"`
		}
		if !decode(w, r, &in) {
			return
		}
		id := r.PathValue("session")
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			problem(w, 404, "That chat is no longer yours, or no longer exists.")
			return
		}
		// The member's own chat in their current role, whatever scope it is in.
		own := `organization_id=$1 AND author=$2 AND executive=$3 AND id=$4::bigint`
		var result sql.Result
		var err error
		switch in.Action {
		case "rename":
			title := strings.Join(strings.Fields(in.Title), " ")
			if n := len([]rune(title)); n < 1 || n > 80 {
				problem(w, 422, "Name the chat in 1–80 characters.")
				return
			}
			result, err = db.ExecContext(r.Context(), `UPDATE chat_sessions SET title=$5,renamed=true WHERE `+own, organization(r), subject(r), executive(r), id, title)
		case "delete":
			result, err = db.ExecContext(r.Context(), `DELETE FROM chat_sessions WHERE `+own, organization(r), subject(r), executive(r), id)
		default:
			problem(w, 422, "Say whether to rename or delete the chat.")
			return
		}
		if err != nil {
			problem(w, 503, "The chat could not be changed.")
			return
		}
		if n, _ := result.RowsAffected(); n == 0 {
			problem(w, 404, "That chat is no longer yours, or no longer exists.")
			return
		}
		jsonReply(w, 200, map[string]any{"ok": true})
	})
	_ = scopeOf
}
