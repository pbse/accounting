package transport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
To have the platform run work for the firm, read plan_catalog and propose a plan with propose_plan, saying why; platform staff approve or reject it and nothing runs before they approve it. list_plan_proposals shows what became of yours.
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
	zip                      bool
	// For the agent: how many rows of the result's main array a model is sent, and where the result names the closes
	// it drew on (tool_results.go). A close-scoped read needs no lineage; its close is the conversation's.
	cap     toolCap
	lineage string
}

func mcpText(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

var mcpCloseID = mcpText("Close ID from list_closes.")

var mcpTools = []mcpTool{
	{name: "list_closes", title: "List client closes", description: "Client/month closes this person can access, newest first, with due date, priority and owner.", method: "GET", path: "/v1/closes", query: map[string]string{"query": "q"}, props: map[string]any{"query": mcpText("Optional client name or YYYY-MM month.")}, readOnly: true, cap: toolCap{"closes", 50}, lineage: "closes[].id|has_more"},
	{name: "get_close", title: "Get a close", description: "One close with its drafts and their review state.", method: "GET", path: "/v1/closes/{close_id}", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "list_work", title: "List preparation work", description: "Preparation jobs across closes, ranked by what needs a person first. Views: active, mine, needs_confirmation, blocked, running, waiting_client, completed, all.", method: "GET", path: "/v1/closes/work", query: map[string]string{"view": "view", "query": "q"}, props: map[string]any{"view": mcpText("Optional view; default active."), "query": mcpText("Optional client name or YYYY-MM month.")}, readOnly: true, cap: toolCap{"items", 60}, lineage: lineageNone},
	{name: "review_queue", title: "Drafts ready for review", description: "Current drafts with evidence and no blockers that a person can approve now, including any approval requests.", method: "GET", path: "/v1/closes/review-queue", query: map[string]string{"client": "client"}, props: map[string]any{"client": mcpText("Optional client name or YYYY-MM month.")}, readOnly: true, cap: toolCap{"items", 60}, lineage: "items[].close_id|count"},
	{name: "list_evidence", title: "List evidence", description: "Documents uploaded to a close, with extraction state and replacements.", method: "GET", path: "/v1/closes/{close_id}/evidence", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true, cap: toolCap{"items", 50}},
	{name: "read_evidence_text", title: "Read evidence text", description: "The extracted text of one document, to cite or check facts against.", method: "GET", path: "/v1/closes/{close_id}/evidence/{evidence_id}/text", props: map[string]any{"close_id": mcpCloseID, "evidence_id": mcpText("Evidence ID from list_evidence.")}, required: []string{"close_id", "evidence_id"}, readOnly: true},
	{name: "get_workpaper", title: "Get a workpaper", description: "One saved draft: inputs, results, cited sources, blockers, questions and review state.", method: "GET", path: "/v1/closes/{close_id}/workpapers/{workpaper_id}?record=1", props: map[string]any{"close_id": mcpCloseID, "workpaper_id": mcpText("Workpaper (draft) ID.")}, required: []string{"close_id", "workpaper_id"}, readOnly: true},
	{name: "find_exceptions", title: "Find exceptions", description: "Possible duplicates, holding-account postings, payments without a payee and payees coded to different accounts in the latest QuickBooks capture, plus Trial Balance accounts opposite their normal side and holding or clearing accounts that still hold a balance in the latest QuickBooks or Xero month-end snapshot. Each comes with the evidence still missing and a client question. Nothing is changed.", method: "GET", path: "/v1/closes/{close_id}/quickbooks/exceptions", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true, cap: toolCap{"items", 40}},
	{name: "morning_brief", title: "Morning brief", description: "Across the firm's outstanding closes (never signed off, reopened, or changed since sign-off): what the agent did in the past 24 hours (month-end snapshots captured, drafts prepared, carried schedules approved under standing approvals), which closes need a person today and why, and which are overdue or due within 7 days.", method: "GET", path: "/v1/closes/morning", props: map[string]any{}, readOnly: true, lineage: lineageNone},
	{name: "agent_track_record", title: "Agent track record by client", description: "For the last 12 months: where the work came from (straight through, after a person confirmed the facts, carried or recurring, other agent drafts, built by people, and failed or unsupported documents), how straight-through drafts were judged, and per client how often people confirmed the model's proposals unchanged, which fields they changed, and how the resulting drafts were judged (approved without revision, revised, sent back).", method: "GET", path: "/v1/closes/track-record", props: map[string]any{}, readOnly: true, lineage: lineageNone},
	{name: "close_brief", title: "Where a close stands", description: "A narrated status of one close: ledger snapshot, preparation, drafts to review, balance sheet tie-out, client questions and sign-off, with a headline of what needs a person.", method: "GET", path: "/v1/closes/{close_id}/brief", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "explain_changes", title: "Explain changes from last month", description: "The largest Profit and Loss movements from last month's close to this one, with the payees behind each (new, missing, changed) and questions about bills that did not recur.", method: "GET", path: "/v1/closes/{close_id}/flux", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true, cap: toolCap{"changes", 60}, lineage: "+last_close_id"},
	{name: "management_pack", title: "Management pack", description: "The month's Profit and Loss, Balance Sheet and working capital from the close's newest captured Trial Balance and Profit and Loss, compared with last month: entity, period, basis, currency, capture time and each month's status (signed off only while the sign-off stands over a QuickBooks capture it binds). Unclassified accounts and differences are shown, never absorbed; no cash-flow statement is derived.", method: "GET", path: "/v1/closes/{close_id}/management-pack", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true, lineage: "+prior.close_id"},
	{name: "open_items", title: "Open invoices and bills", description: "The latest capture's open invoices and bills with what people decided about each: owner, status (following up, promised to pay with a date, disputed, handed to the client's bill-pay tool with a date, on hold) and note, plus those settled in the ledger since a decision. Read only; nothing is sent, paid or written off.", method: "GET", path: "/v1/closes/{close_id}/open-items", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "cash_plan", title: "13-week cash plan", description: "The client's next 13 weeks of book cash from the latest QuickBooks capture: opening bank balances, receipts from open invoices (promised or due dates), payments for open bills (bill-pay or due dates) and approved payroll, tax, debt and other schedules, with each week's opening, receipts, payments, closing and lowest end-of-day balance, plus items left out because no date was agreed. Book cash is not bank-available cash; nothing is paid or collected.", method: "GET", path: "/v1/closes/{close_id}/cash-plan", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "budget_comparison", title: "Budget against actual", description: "The close month's and fiscal year-to-date actuals against the client's approved budget baseline, by mapped account: actual, budget, difference (favorable or not, with a percentage only against a positive budget, flagged material at the firm's materiality), prior month, unbudgeted accounts and unmapped budget lines; plus every budget version (draft or approved). Read only; budgets are imported and approved by people.", method: "GET", path: "/v1/closes/{close_id}/budget", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true, lineage: lineageNone},
	{name: "tie_out", title: "Balance sheet tie-out", description: "Each asset and liability in the newest month-end Trial Balance compared with the balance the close's workpapers support: ties, ties after proposed entries, differs by less than the firm's materiality with no entry pending, differs, posting outcome unknown, or no support yet.", method: "GET", path: "/v1/closes/{close_id}/tie-out", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true, cap: toolCap{"rows", 60}},
	{name: "get_readiness", title: "Close readiness", description: "What still blocks sign-off for a close.", method: "GET", path: "/v1/closes/{close_id}/readiness", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "native_reconciliation", title: "Xero reconciled flags", description: "For Xero books: per bank account, how many captured transactions Xero flags as reconciled, unreconciled or unknown. Xero's own flags, not a completed reconciliation.", method: "GET", path: "/v1/closes/{close_id}/native-reconciliation", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "proposed_state", title: "Proposed-state preview", description: "Production journal proposals awaiting approval or execution (up to 100): each draft's account balances before, the projected balances after, other open proposals on the same accounts, and the combined rehearsal. Built from saved account observations; not a current ledger balance or a financial statement.", method: "GET", path: "/v1/closes/{close_id}/proposed-state", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "workpaper_changes", title: "Changes between draft versions", description: "What changed between a draft and the earlier version it revises.", method: "GET", path: "/v1/closes/{close_id}/workpapers/{workpaper_id}/changes", props: map[string]any{"close_id": mcpCloseID, "workpaper_id": mcpText("Workpaper (draft) ID.")}, required: []string{"close_id", "workpaper_id"}, readOnly: true},
	{name: "list_close_jobs", title: "Preparation jobs of a close", description: "The close's 20 newest preparation jobs with their state, error and the drafts they produced.", method: "GET", path: "/v1/closes/{close_id}/jobs", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "list_client_requests", title: "List client request drafts", description: "Saved client request drafts for a close, newest first; use the latest id as expected_id when drafting.", method: "GET", path: "/v1/closes/{close_id}/requests", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "preview_close_preparation", title: "Preview close preparation", description: "The evidence a close preparation would coordinate, with the inventory_digest prepare_close needs.", method: "GET", path: "/v1/closes/{close_id}/prepare-close", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true},
	{name: "open_close", title: "Open a close", description: "Open or return the close for a client and month. Repeating it returns the same close.", method: "POST", path: "/v1/closes", body: []string{"entity", "period"}, props: map[string]any{"entity": mcpText("Client name."), "period": mcpText("Close month, YYYY-MM.")}, required: []string{"entity", "period"}, idempotent: true},
	{name: "add_evidence", title: "Add a text document", description: "Upload a text or CSV document (for example a bank export) to a close. The agent reads it and proposes work; nothing is approved.", method: "POST", path: "/v1/closes/{close_id}/evidence", upload: true, props: map[string]any{"close_id": mcpCloseID, "name": mcpText("File name ending in .csv or .txt."), "text": mcpText("File contents.")}, required: []string{"close_id", "name", "text"}},
	{name: "export_close_package", title: "Export the close package", description: "The close's review package: its manifest (scope, readiness, workpapers, evidence and postings) and each file's SHA-256, with the ZIP itself attached while it is at most 5 MB. It is not the signed-off binder.", method: "GET", path: "/v1/closes/{close_id}/export", props: map[string]any{"close_id": mcpCloseID}, required: []string{"close_id"}, readOnly: true, zip: true},
	{name: "recheck_close", title: "Recheck failed or pending work", description: "Queue this close's failed or pending proposals, whose documents are current, to be prepared again, using recheck.digest from preview_close_preparation. Open client request links for those jobs are revoked. Nothing is approved.", method: "POST", path: "/v1/closes/{close_id}/prepare-close/recheck", body: []string{"digest", "confirmed"}, props: map[string]any{"close_id": mcpCloseID, "digest": mcpText("recheck.digest from preview_close_preparation."), "confirmed": map[string]any{"type": "boolean", "description": "True to confirm the listed work."}}, required: []string{"close_id", "digest", "confirmed"}},
	{name: "prepare_close", title: "Prepare a close", description: "Coordinate preparation of the evidence listed by preview_close_preparation. Drafts still need a person's approval.", method: "POST", path: "/v1/closes/{close_id}/prepare-close", body: []string{"inventory_digest", "confirmed"}, props: map[string]any{"close_id": mcpCloseID, "inventory_digest": mcpText("From preview_close_preparation."), "confirmed": map[string]any{"type": "boolean", "description": "True to confirm the listed evidence scope."}}, required: []string{"close_id", "inventory_digest", "confirmed"}},
	{name: "draft_client_request", title: "Draft client questions", description: "Save a consolidated client request draft from selected workpapers. Omit body for the suggested text; send body with context_digest to save edited text. It is not sent or approved.", method: "POST", path: "/v1/closes/{close_id}/requests", body: []string{"expected_id", "workpaper_ids", "body", "context_digest"}, props: map[string]any{"close_id": mcpCloseID, "expected_id": mcpText("Latest draft id from list_client_requests, or empty."), "workpaper_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Up to 20 workpaper IDs."}, "body": mcpText("Optional edited request text."), "context_digest": mcpText("Required with body, from list_client_requests.")}, required: []string{"close_id"}},
	{name: "plan_catalog", title: "Plan catalog", description: "What a workflow plan may be built from: the plan format, and every activity with its input schema, permission and effect.", method: "GET", path: "/v1/closes/plans/catalog", props: map[string]any{}, readOnly: true, lineage: lineageNone},
	{name: "list_plan_proposals", title: "List plan proposals", description: "The firm's agent plan proposals, newest first, each proposed, approved (with its run), rejected or archived.", method: "GET", path: "/v1/closes/plans/proposals", query: map[string]string{"before": "before"}, props: map[string]any{"before": mcpText("Optional next_before from the previous page, for older proposals.")}, readOnly: true, lineage: lineageNone},
	{name: "propose_plan", title: "Propose a plan", description: "Propose a workflow plan for the firm, built from plan_catalog, with the reason it should run. Platform staff approve or reject it; nothing runs before they approve it. At most 20 proposals a firm a day.", method: "POST", path: "/v1/closes/plans/proposals", body: []string{"plan", "rationale"}, props: map[string]any{"plan": map[string]any{"type": "object", "description": "The plan, in plan_catalog's format."}, "rationale": mcpText("Why this plan should run, 10–2,000 characters.")}, required: []string{"plan", "rationale"}},
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
		if t.zip && recorder.Code == http.StatusOK {
			closeID, _ := args["close_id"].(string)
			return mcpPackage(recorder.Body.Bytes(), closeID), nil
		}
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

// mcpPackage returns a close package's manifest and checksums, and the ZIP itself while it is small enough to carry;
// a hosted agent has no other way to download it.
func mcpPackage(archive []byte, closeID string) *mcp.CallToolResult {
	const limit = 5 << 20
	files, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return toolProblem("UNAVAILABLE", "The close package could not be read.")
	}
	var manifest map[string]any
	sums := []map[string]string{}
	for _, f := range files.File {
		if f.Name != "manifest.json" && f.Name != "checksums.sha256" {
			continue
		}
		opened, err := f.Open()
		if err != nil {
			return toolProblem("UNAVAILABLE", "The close package could not be read.")
		}
		raw, err := io.ReadAll(opened)
		opened.Close()
		if err != nil {
			return toolProblem("UNAVAILABLE", "The close package could not be read.")
		}
		if f.Name == "manifest.json" {
			if json.Unmarshal(raw, &manifest) != nil {
				return toolProblem("UNAVAILABLE", "The close package has no readable manifest.")
			}
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if line == "" {
				continue
			}
			sum, name, ok := strings.Cut(line, "  ")
			if _, err := hex.DecodeString(sum); !ok || len(sum) != 64 || err != nil || name == "" {
				return toolProblem("UNAVAILABLE", "The close package's checksums could not be read.")
			}
			sums = append(sums, map[string]string{"name": name, "sha256": sum})
		}
	}
	if manifest == nil {
		return toolProblem("UNAVAILABLE", "The close package has no readable manifest.")
	}
	included := len(archive) <= limit
	content := []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("The close package has %d files; the ZIP (%d bytes) is over the 5 MB MCP limit, so a person downloads it from the close page.", len(sums), len(archive))}}
	if included {
		content = []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("The close package has %d files; the ZIP (%d bytes) is attached.", len(sums), len(archive))},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "close-package://" + url.PathEscape(closeID) + ".zip", MIMEType: "application/zip", Blob: archive}}}
	}
	return &mcp.CallToolResult{Content: content, StructuredContent: map[string]any{"manifest": manifest, "files": sums, "archive_bytes": len(archive), "archive_attached": included}}
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

// agentActor is who an OAuth agent acts as. The tool table is the only reachable surface and the grant's permission
// only satisfies its routes; without clients:all the boundary limits the agent to the member's assigned clients. An
// executive's agent stays an executive whatever its grant.
func agentActor(claims auth.Claims, scope string) *auth.Claims {
	granted := claims
	granted.Permissions = nil
	switch {
	case slices.Contains(claims.Permissions, "executive:read"):
		granted.Permissions = []string{"executive:read"}
	case scope == "prepare":
		granted.Permissions = []string{"workpapers:review"}
	}
	return &granted
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
		scope, agent := "prepare", ""
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
				actor, err = agentActor(*claims, scope), nil
				agent = "MCP (client unverified)"
				if claims.ClientID != "" {
					agent = "MCP client " + claims.ClientID
				}
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
		ctx := context.WithValue(auth.WithActor(r.Context(), actor), mcpScope{}, scope)
		if agent != "" {
			ctx = auth.WithAgent(ctx, agent)
		}
		protocol.ServeHTTP(noStore{w}, r.WithContext(ctx))
	})
}
