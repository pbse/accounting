package transport

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

type searchHit struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Href    string `json:"href"`
	Close   string `json:"close"`
}

// searchSnippet redacts the whole text first, so no cut can separate a secret from its label, then keeps about 200
// characters around the first word.
func searchSnippet(text, word string) string {
	clean, _ := redactSensitive(text)
	clean = strings.Join(strings.Fields(clean), " ")
	runes := []rune(clean)
	at := len([]rune(clean[:max(strings.Index(strings.ToLower(clean), word), 0)]))
	start := max(at-80, 0)
	return string(runes[start:min(start+200, len(runes))])
}

const searchScope = "documents, drafts, the latest QuickBooks transaction capture, procedures and review decisions, up to 8 of each; Xero records are not searched"

// searchWork finds literal matches in the closes the caller can access (one close when closeID is set). It reads only,
// under a statement timeout, and every snippet is redacted like a client reply.
func searchWork(ctx context.Context, db *sql.DB, r *http.Request, closeID, query string) ([]searchHit, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout = '3s'`); err != nil {
		return nil, err
	}
	// Every word must match somewhere in the record; the snippet is taken around the first.
	words := strings.Fields(strings.ToLower(query))
	patterns := []string{}
	for _, word := range words {
		patterns = append(patterns, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(word)+"%")
	}
	closes := `WITH c AS (SELECT id,entity,period FROM client_closes WHERE organization_id=$1 AND ($4='' OR id::text=$4)
 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=client_closes.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(client_closes.entity) AND g.granted))) `
	args := []any{organization(r), firmAccess(r), subject(r), closeID, patterns}
	queries := []struct{ kind, sql string }{
		{"document", closes + `SELECT c.id::text,c.entity||' · '||c.period,e.id::text,e.name,
 COALESCE(e.content,''),
 EXISTS(SELECT 1 FROM work_replies w JOIN work_requests q ON q.id=w.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE w.source_id=e.id AND d.close_id=e.close_id)::text
 FROM c JOIN evidence e ON e.close_id=c.id WHERE e.superseded_by IS NULL AND e.purpose='work' AND (e.name||' '||COALESCE(e.content,'')) ILIKE ALL($5::text[]) ORDER BY e.id DESC LIMIT 8`},
		{"draft", closes + `SELECT c.id::text,c.entity||' · '||c.period,w.id::text,w.kind,COALESCE(w.paper->'input'->>'source_reference',''),''
 FROM c JOIN workpapers w ON w.close_id=c.id WHERE NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text) AND w.paper::text ILIKE ALL($5::text[]) ORDER BY w.id DESC LIMIT 8`},
		{"transaction", closes + `SELECT c.id::text,c.entity||' · '||c.period,t.provider_id,t.kind||' '||t.provider_id||' · '||t.txn_date::text||' · captured '||COALESCE(t.amount,'')||' '||COALESCE(t.currency,''),
 COALESCE(t.record->'VendorRef'->>'name',t.record->'CustomerRef'->>'name',t.record->'EntityRef'->>'name',''),''
 FROM c JOIN LATERAL (SELECT z.id FROM quickbooks_entity_syncs z JOIN evidence v ON v.id=z.evidence_id AND v.close_id=z.close_id AND v.superseded_by IS NULL WHERE z.close_id=c.id AND z.state='completed' AND z.connection_id=COALESCE(` + quickbooks.ReadingConnection("c.id") + `,z.connection_id) ORDER BY z.id DESC LIMIT 1) s ON true
 JOIN quickbooks_transaction_rows t ON t.sync_id=s.id WHERE (t.kind||' '||t.provider_id||' '||t.record::text) ILIKE ALL($5::text[]) ORDER BY t.txn_date DESC,t.provider_id LIMIT 8`},
		{"procedure", closes + `, p AS (SELECT DISTINCT ON (lower(client_entity),kind) id,client_entity,kind,instructions,enabled,created_at FROM procedure_versions WHERE organization_id=$1 ORDER BY lower(client_entity),kind,id DESC)
 SELECT (SELECT max(c.id)::text FROM c WHERE p.client_entity='' OR lower(c.entity)=lower(p.client_entity)),CASE WHEN p.client_entity='' THEN 'All clients' ELSE p.client_entity END,p.id::text,p.kind||' procedure',
 p.instructions,''
 FROM p WHERE p.enabled AND (p.kind||' '||p.instructions) ILIKE ALL($5::text[]) AND EXISTS(SELECT 1 FROM c WHERE p.client_entity='' OR lower(c.entity)=lower(p.client_entity)) ORDER BY p.id DESC LIMIT 8`},
		{"decision", closes + `SELECT c.id::text,c.entity||' · '||c.period,w.id::text,v.decision||' by '||v.actor||' on '||to_char(v.created_at,'YYYY-MM-DD')||': '||w.kind||' draft #'||w.id::text,
 COALESCE(v.note,''),''
 FROM c JOIN workpapers w ON w.close_id=c.id JOIN workpaper_reviews v ON v.workpaper_id=w.id WHERE (v.decision||' '||COALESCE(v.note,'')) ILIKE ALL($5::text[]) ORDER BY v.id DESC LIMIT 8`},
	}
	hits := []searchHit{}
	for _, q := range queries {
		rows, err := tx.QueryContext(ctx, q.sql, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var close, label, id, title, text, reply string
			if err = rows.Scan(&close, &label, &id, &title, &text, &reply); err != nil {
				rows.Close()
				return nil, err
			}
			base := "/dashboard/closes/" + close
			title, _ = redactSensitive(title)
			hit := searchHit{Kind: q.kind, Title: title, Close: label, Snippet: searchSnippet(text, words[0])}
			switch q.kind {
			case "document":
				hit.Href = base + "?tab=evidence#source-" + url.PathEscape(id)
				if reply == "true" {
					hit.Kind = "reply"
				}
			case "draft":
				hit.Title, hit.Href = title+" draft #"+id, base+"/workpapers/"+url.PathEscape(id)
			case "transaction":
				hit.Href = base + "?tab=evidence#quickbooks-ledger"
			case "procedure":
				hit.Href = base + "?tab=settings"
			case "decision":
				hit.Href = base + "/workpapers/" + url.PathEscape(id)
			}
			hits = append(hits, hit)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return hits, nil
}

func registerSearch(mux *http.ServeMux, db *sql.DB) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if words := len(strings.Fields(query)); len(query) < 3 || len(query) > 100 || words == 0 || words > 6 {
			problem(w, 422, "Search for 3–100 characters and up to six words.")
			return
		}
		hits, err := searchWork(r.Context(), db, r, r.PathValue("id"), query)
		if err != nil {
			problem(w, 503, "Search is unavailable right now; try a longer or more specific phrase.")
			return
		}
		jsonReply(w, 200, map[string]any{"query": query, "results": hits, "searched": searchScope})
	}
	mux.HandleFunc("GET /v1/closes/search", handler)
	mux.HandleFunc("GET /v1/closes/{id}/search", handler)
}
