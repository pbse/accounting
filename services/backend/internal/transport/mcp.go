package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"close.local/backend/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpInstructions = `Month-end close preparation for accounting firms. Find work with list_work and review_queue, read evidence and workpapers, prepare drafts, draft client questions, and ask a person to approve with request_approval.
You cannot approve, sign off, post to QuickBooks or send client email; people do that in the app, and every call is limited to what the signed-in person may see.
Failed calls return {"code","message"}: SOURCE_STALE means re-read and re-propose; CONFLICT means re-read before deciding; EXECUTION_UNKNOWN means never repeat a write.`

// mcpTool is one existing close route offered to agents; the route's own authorization applies.
type mcpTool struct {
	name, title, description string
	method, path             string
	query                    map[string]string
	body                     []string
	props                    map[string]any
	required                 []string
	readOnly, idempotent     bool
	upload                   bool
}

func mcpText(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

var mcpCloseID = mcpText("Close ID from list_closes.")

var mcpTools = []mcpTool{
	{name: "list_closes", title: "List client closes", description: "Client/month closes this person can access, newest first, with due date, priority and owner.", method: "GET", path: "/v1/closes", query: map[string]string{"query": "q"}, props: map[string]any{"query": mcpText("Optional client name or YYYY-MM month.")}, readOnly: true},
	{name: "get_close", title: "Get a close", description: "One close with its drafts and their review state.", method: "GET", path: "/v1/closes/{close_id}", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "list_work", title: "List preparation work", description: "Preparation jobs across closes, ranked by what needs a person first. Views: active, mine, needs_confirmation, blocked, running, waiting_client, completed, all.", method: "GET", path: "/v1/closes/work", query: map[string]string{"view": "view", "query": "q"}, props: map[string]any{"view": mcpText("Optional view; default active."), "query": mcpText("Optional client name or YYYY-MM month.")}, readOnly: true},
	{name: "review_queue", title: "Drafts ready for review", description: "Current drafts with evidence and no blockers that a person can approve now, including any approval requests.", method: "GET", path: "/v1/closes/review-queue", query: map[string]string{"client": "client"}, props: map[string]any{"client": mcpText("Optional client name or YYYY-MM month.")}, readOnly: true},
	{name: "list_evidence", title: "List evidence", description: "Documents uploaded to a close, with extraction state and replacements.", method: "GET", path: "/v1/closes/{close_id}/evidence", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "read_evidence_text", title: "Read evidence text", description: "The extracted text of one document, to cite or check facts against.", method: "GET", path: "/v1/closes/{close_id}/evidence/{evidence_id}/text", props: map[string]any{"close_id": mcpCloseID, "evidence_id": mcpText("Evidence ID from list_evidence.")}, required: []string{"close_id", "evidence_id"}, readOnly: true},
	{name: "get_workpaper", title: "Get a workpaper", description: "One saved draft: inputs, results, cited sources, blockers, questions and review state.", method: "GET", path: "/v1/closes/{close_id}/workpapers/{workpaper_id}?record=1", props: map[string]any{"close_id": mcpCloseID, "workpaper_id": mcpText("Workpaper (draft) ID.")}, required: []string{"close_id", "workpaper_id"}, readOnly: true},
	{name: "find_exceptions", title: "Find exceptions", description: "Possible duplicates, holding-account postings, payments without a payee and payees coded to different accounts in the latest QuickBooks capture, plus Trial Balance accounts opposite their normal side in the latest period snapshot. Each comes with the evidence still missing and a client question. Nothing is changed.", method: "GET", path: "/v1/closes/{close_id}/quickbooks/exceptions", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "get_readiness", title: "Close readiness", description: "What still blocks sign-off for a close.", method: "GET", path: "/v1/closes/{close_id}/readiness", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "list_client_requests", title: "List client request drafts", description: "Saved client request drafts for a close, newest first; use the latest id as expected_id when drafting.", method: "GET", path: "/v1/closes/{close_id}/requests", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "preview_close_preparation", title: "Preview close preparation", description: "The evidence a close preparation would coordinate, with the inventory_digest prepare_close needs.", method: "GET", path: "/v1/closes/{close_id}/prepare-close", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "open_close", title: "Open a close", description: "Open or return the close for a client and month. Repeating it returns the same close.", method: "POST", path: "/v1/closes", body: []string{"entity", "period"}, props: map[string]any{"entity": mcpText("Client name."), "period": mcpText("Close month, YYYY-MM.")}, required: []string{"entity", "period"}, idempotent: true},
	{name: "add_evidence", title: "Add a text document", description: "Upload a text or CSV document (for example a bank export) to a close. The agent reads it and proposes work; nothing is approved.", method: "POST", path: "/v1/closes/{close_id}/evidence", upload: true, props: map[string]any{"close_id": mcpCloseID, "name": mcpText("File name ending in .csv or .txt."), "text": mcpText("File contents.")}, required: []string{"close_id", "name", "text"}},
	{name: "prepare_close", title: "Prepare a close", description: "Coordinate preparation of the evidence listed by preview_close_preparation. Drafts still need a person's approval.", method: "POST", path: "/v1/closes/{close_id}/prepare-close", body: []string{"inventory_digest", "confirmed"}, props: map[string]any{"close_id": mcpCloseID, "inventory_digest": mcpText("From preview_close_preparation."), "confirmed": map[string]any{"type": "boolean", "description": "True to confirm the listed evidence scope."}}, required: []string{"close_id", "inventory_digest", "confirmed"}},
	{name: "draft_client_request", title: "Draft client questions", description: "Save a consolidated client request draft from selected workpapers. Omit body for the suggested text; send body with context_digest to save edited text. It is not sent or approved.", method: "POST", path: "/v1/closes/{close_id}/requests", body: []string{"expected_id", "workpaper_ids", "body", "context_digest"}, props: map[string]any{"close_id": mcpCloseID, "expected_id": mcpText("Latest draft id from list_client_requests, or empty."), "workpaper_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Up to 20 workpaper IDs."}, "body": mcpText("Optional edited request text."), "context_digest": mcpText("Required with body, from list_client_requests.")}, required: []string{"close_id"}},
	{name: "request_approval", title: "Request approval", description: "Ask a person to review a draft, saying what to check. This is not an approval; the draft appears as requested in the review queue.", method: "POST", path: "/v1/closes/{close_id}/workpapers/{workpaper_id}/approval-request", body: []string{"note"}, props: map[string]any{"close_id": mcpCloseID, "workpaper_id": mcpText("Draft awaiting review."), "note": mcpText("What the reviewer should check (5–1,000 characters).")}, required: []string{"close_id", "workpaper_id", "note"}},
}

func (t mcpTool) handle(store http.Handler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 && json.Unmarshal(req.Params.Arguments, &args) != nil {
			return toolProblem("INVALID_REQUEST", "Arguments must be a JSON object."), nil
		}
		for _, key := range t.required {
			if value, ok := args[key]; !ok || value == "" || value == nil {
				return toolProblem("INVALID_REQUEST", key+" is required."), nil
			}
		}
		path, fixed, _ := strings.Cut(t.path, "?")
		for key := range t.props {
			if value, ok := args[key].(string); ok && strings.Contains(path, "{"+key+"}") {
				path = strings.Replace(path, "{"+key+"}", url.PathEscape(value), 1)
			}
		}
		query, _ := url.ParseQuery(fixed)
		for arg, param := range t.query {
			if value, ok := args[arg].(string); ok && value != "" {
				query.Set(param, value)
			}
		}
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		var body io.Reader
		contentType := "application/json"
		if t.upload {
			name, _ := args["name"].(string)
			content, _ := args["text"].(string)
			var form bytes.Buffer
			writer := multipart.NewWriter(&form)
			part, _ := writer.CreateFormFile("file", name)
			part.Write([]byte(content))
			writer.Close()
			body, contentType = &form, writer.FormDataContentType()
		} else if t.body != nil {
			fields := map[string]any{}
			for _, key := range t.body {
				if value, ok := args[key]; ok {
					fields[key] = value
				}
			}
			raw, _ := json.Marshal(fields)
			body = bytes.NewReader(raw)
		}
		call, err := http.NewRequestWithContext(ctx, t.method, path, body)
		if err != nil {
			return toolProblem("INVALID_REQUEST", "Arguments do not form a valid request."), nil
		}
		call.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		store.ServeHTTP(recorder, call)
		result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: recorder.Body.String()}}, IsError: recorder.Code >= 400}
		var value any
		if json.Unmarshal(recorder.Body.Bytes(), &value) == nil {
			if _, object := value.(map[string]any); !object {
				value = map[string]any{"items": value}
			}
			result.StructuredContent = value
		}
		return result, nil
	}
}

// noStore keeps client data out of caches; the SDK sets its own Cache-Control.
type noStore struct{ http.ResponseWriter }

func (w noStore) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(code)
}

func (w noStore) Write(b []byte) (int, error) {
	w.Header().Set("Cache-Control", "no-store")
	return w.ResponseWriter.Write(b)
}

func toolProblem(code, message string) *mcp.CallToolResult {
	value := map[string]any{"code": code, "message": message}
	raw, _ := json.Marshal(value)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: value, IsError: true}
}

type mcpScope struct{}

// mcpOAuth verifies hosted MCP sign-in through WorkOS AuthKit; nil unless configured.
func mcpOAuth() *auth.Verifier {
	domain, resource := strings.TrimRight(os.Getenv("WORKOS_AUTHKIT_DOMAIN"), "/"), os.Getenv("MCP_RESOURCE_URL")
	if !strings.HasPrefix(domain, "https://") || !strings.HasPrefix(resource, "https://") {
		return nil
	}
	return &auth.Verifier{Issuer: domain, JWKSURL: domain + "/oauth2/jwks", Audience: resource, Organization: os.Getenv("WORKOS_ORGANIZATION_ID")}
}

// mcpMetadataURL is the resource's protected resource metadata (RFC 9728).
func mcpMetadataURL(resource string) string {
	u, err := url.Parse(resource)
	if err != nil {
		return ""
	}
	u.Path = "/.well-known/oauth-protected-resource" + u.Path
	return u.String()
}

func mcpServer(store http.Handler, include func(mcpTool) bool) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "close-preparation", Title: "Close preparation", Version: "1"}, &mcp.ServerOptions{Instructions: mcpInstructions})
	for _, t := range mcpTools {
		if !include(t) {
			continue
		}
		schema := map[string]any{"type": "object", "properties": t.props, "additionalProperties": false}
		if t.required != nil {
			schema["required"] = t.required
		}
		destructive := false
		server.AddTool(&mcp.Tool{Name: t.name, Title: t.title, Description: t.description, InputSchema: schema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: t.readOnly, IdempotentHint: t.readOnly || t.idempotent, DestructiveHint: &destructive}}, t.handle(store))
	}
	return server
}

// mcpHandler serves MCP (current and earlier protocol revisions). The workspace token and session tokens
// sign in as for the API; hosted OAuth tokens act only under the member's approved agent access.
func mcpHandler(store http.Handler, token string, db *sql.DB) http.Handler {
	full := mcpServer(store, func(mcpTool) bool { return true })
	read := mcpServer(store, func(t mcpTool) bool { return t.readOnly })
	protocol := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if r.Context().Value(mcpScope{}) == "read" {
			return read
		}
		return full
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 8 << 20})
	oauth := mcpOAuth()
	origins := []string{}
	for _, origin := range strings.Split(os.Getenv("MCP_ALLOWED_ORIGINS")+","+os.Getenv("FRONTEND_ORIGIN"), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	verifier := workspaceVerifier()
	// ponytail: per-process window; move to Postgres when the API runs as several instances.
	var mu sync.Mutex
	window, calls := time.Now(), map[[32]byte]int{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" {
			if !slices.Contains(origins, origin) {
				problem(w, 403, "This origin may not use MCP. Add it to MCP_ALLOWED_ORIGINS.")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if len(token) < 32 && verifier.ClientID == "" && oauth == nil {
			problem(w, 503, "MCP requires configured workspace authentication.")
			return
		}
		actor, err := verifier.Authenticate(r, token)
		scope := "prepare"
		if err != nil && oauth != nil {
			raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if claims, e := oauth.Verify(r.Context(), raw); e == nil {
				if scope, e = agentGrant(r.Context(), db, claims.Organization, claims.Subject); e != nil {
					problem(w, 503, "Agent access unavailable.")
					return
				}
				if scope == "" {
					problem(w, 403, "Ask a firm administrator to approve agent access for your account under Firm settings.")
					return
				}
				// The tool table is the only reachable surface; this permission only satisfies its routes.
				// Without clients:all the boundary limits the agent to the member's assigned clients.
				granted := *claims
				granted.Permissions = nil
				if scope == "prepare" {
					granted.Permissions = []string{"workpapers:review"}
				}
				actor, err = &granted, nil
			}
		}
		if err != nil {
			challenge := "Bearer"
			if oauth != nil {
				challenge += ` resource_metadata="` + mcpMetadataURL(oauth.Audience) + `"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
			problem(w, 401, "Sign in to connect an agent: the workspace token locally, or your firm's agent sign-in.")
			return
		}
		key := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		mu.Lock()
		if time.Since(window) > time.Minute {
			window, calls = time.Now(), map[[32]byte]int{}
		}
		calls[key]++
		over := calls[key] > 120
		mu.Unlock()
		if over {
			problem(w, 429, "Too many MCP requests. Wait a minute and retry.")
			return
		}
		protocol.ServeHTTP(noStore{w}, r.WithContext(context.WithValue(auth.WithActor(r.Context(), actor), mcpScope{}, scope)))
	})
}
