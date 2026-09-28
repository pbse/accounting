package transport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCPToolsUseTheSignedInPersonsAccess(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other := "org_" + rand.Text()
	var mine, theirs string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "MCP client "+rand.Text()).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, other); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES($1,'2026-09',$2) RETURNING id::text`, "Other client "+rand.Text(), other).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, id := range []string{mine, theirs} {
			db.Exec(`DELETE FROM approval_requests WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM preparation_intakes WHERE job_id IN (SELECT id FROM preparation_jobs WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organizations WHERE id=$1`, other)
	}()
	content := "Invoice " + rand.Text()
	var evidenceID, paperID string
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, mine, preparation.Digest([]byte(content)), content).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	sources := fmt.Sprintf(`{"sources":[{"id":"%s","digest":"%s","role":"invoice","name":"invoice.txt","location":"line 1"}],"blockers":[]}`, evidenceID, preparation.Digest([]byte(content)))
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'prepaid','mcp',$2,'fixture') RETURNING id::text`, mine, sources).Scan(&paperID); err != nil {
		t.Fatal(err)
	}
	var blockedID string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'prepaid','mcp-blocked',$2,'fixture') RETURNING id::text`, mine, strings.Replace(sources, `"blockers":[]`, `"blockers":["Service end date missing"]`, 1)).Scan(&blockedID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://agent.example, https://other.example")
	token := strings.Repeat("m", 32)
	server := httptest.NewServer(apiWithStore(db, token))
	defer server.Close()
	ctx := context.Background()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if version := session.InitializeResult().ProtocolVersion; version != "2026-07-28" {
		t.Fatalf("a current client must get the current protocol, got %s", version)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"list_closes", "get_workpaper", "find_exceptions", "close_brief", "tie_out", "explain_changes", "agent_track_record", "morning_brief", "draft_client_request", "request_approval", "export_close_package", "recheck_close", "prepare_close"} {
		if !slices.Contains(names, want) {
			t.Fatalf("missing tool %s in %v", want, names)
		}
	}
	for _, name := range names {
		if strings.Contains(name, "approve") && name != "request_approval" || strings.Contains(name, "post") || strings.Contains(name, "sign") {
			t.Fatalf("agents must not approve, sign off or post: %s", name)
		}
	}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	text := func(result *mcp.CallToolResult) string { return result.Content[0].(*mcp.TextContent).Text }
	if listed := call("list_closes", map[string]any{"query": "MCP client"}); listed.IsError || !strings.Contains(text(listed), `"id":"`+mine+`"`) {
		t.Fatalf("list_closes must return the person's closes: %s", text(listed))
	}
	if denied := call("get_close", map[string]any{"close_id": theirs}); !denied.IsError || !strings.Contains(text(denied), "NOT_FOUND") {
		t.Fatalf("another firm's close must stay hidden: %s", text(denied))
	}
	if added := call("add_evidence", map[string]any{"close_id": mine, "name": "bank.csv", "text": "id,date,amount\nB1,2026-09-02,10.00\n"}); added.IsError {
		t.Fatalf("add_evidence: %s", text(added))
	}
	var jobs int
	if err = db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id=$1`, mine).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("an added document must start intake: %d %v", jobs, err)
	}
	if blocked := call("request_approval", map[string]any{"close_id": mine, "workpaper_id": blockedID, "note": "Check the service dates"}); !blocked.IsError || !strings.Contains(text(blocked), "CONFLICT") {
		t.Fatalf("a draft the queue hides must not take an approval request: %s", text(blocked))
	}
	if requested := call("request_approval", map[string]any{"close_id": mine, "workpaper_id": paperID, "note": "Check the service dates"}); requested.IsError {
		t.Fatalf("request_approval: %s", text(requested))
	}
	if queue := call("review_queue", map[string]any{"client": "MCP client"}); !strings.Contains(text(queue), `"approval_note":"Check the service dates"`) {
		t.Fatalf("the request must reach the review queue: %s", text(queue))
	}
	var reviews int
	if err = db.QueryRow(`SELECT count(*) FROM workpaper_reviews WHERE workpaper_id=$1`, paperID).Scan(&reviews); err != nil || reviews != 0 {
		t.Fatalf("an approval request must not approve: %d %v", reviews, err)
	}
	// The review package comes back with its manifest, each file's checksum and the ZIP itself.
	exported := call("export_close_package", map[string]any{"close_id": mine})
	pack, _ := exported.StructuredContent.(map[string]any)
	manifest, _ := pack["manifest"].(map[string]any)
	if exported.IsError || manifest["version"] != "close_package_v2" || pack["archive_attached"] != true || len(exported.Content) != 2 {
		t.Fatalf("export_close_package returns the manifest and the ZIP: %v %s", exported.IsError, text(exported))
	}
	attached := exported.Content[1].(*mcp.EmbeddedResource).Resource
	archive, err := zip.NewReader(bytes.NewReader(attached.Blob), int64(len(attached.Blob)))
	if err != nil || attached.MIMEType != "application/zip" {
		t.Fatalf("the attachment is the package ZIP: %v %s", err, attached.MIMEType)
	}
	sums := map[string]string{}
	for _, f := range pack["files"].([]any) {
		entry := f.(map[string]any)
		sums[entry["name"].(string)] = entry["sha256"].(string)
	}
	for _, f := range archive.File {
		if f.Name != "manifest.json" {
			continue
		}
		opened, _ := f.Open()
		raw, _ := io.ReadAll(opened)
		if sums["manifest.json"] != fmt.Sprintf("%x", sha256.Sum256(raw)) {
			t.Fatalf("each file's checksum is the one in the ZIP: %v", sums)
		}
	}
	if len(sums) < 2 || sums["manifest.json"] == "" {
		t.Fatalf("the files and their checksums are listed: %v", sums)
	}
	if foreign := call("export_close_package", map[string]any{"close_id": theirs}); !foreign.IsError || !strings.Contains(text(foreign), "NOT_FOUND") {
		t.Fatalf("another firm's package stays hidden: %s", text(foreign))
	}
	// A failed proposal is queued again with the preview's digest; a stale digest changes nothing.
	if _, err = db.Exec(`UPDATE evidence SET extraction_state='ready' WHERE close_id=$1`, mine); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE preparation_jobs SET state='failed',error='Synthetic failure' WHERE close_id=$1`, mine); err != nil {
		t.Fatal(err)
	}
	preview, _ := call("preview_close_preparation", map[string]any{"close_id": mine}).StructuredContent.(map[string]any)
	recheck, _ := preview["recheck"].(map[string]any)
	if stale := call("recheck_close", map[string]any{"close_id": mine, "digest": "stale", "confirmed": true}); !stale.IsError || !strings.Contains(text(stale), "CONFLICT") {
		t.Fatalf("a stale digest rechecks nothing: %s", text(stale))
	}
	if rechecked := call("recheck_close", map[string]any{"close_id": mine, "digest": recheck["digest"], "confirmed": true}); rechecked.IsError || !strings.Contains(text(rechecked), `"count":1`) {
		t.Fatalf("recheck_close queues the failed proposal again: %s", text(rechecked))
	}

	post := func(body string, headers map[string]string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	auth := map[string]string{"Authorization": "Bearer " + token}
	legacy := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"legacy","version":"1"}}}`, auth)
	var initialized struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	json.NewDecoder(legacy.Body).Decode(&initialized)
	if legacy.StatusCode != 200 || initialized.Result.ProtocolVersion != "2025-06-18" || legacy.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("an initialize-era client must be served: %d %+v", legacy.StatusCode, initialized)
	}
	mismatch := post(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"modern","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`, map[string]string{"Authorization": "Bearer " + token, "MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call"})
	if mismatch.StatusCode != 400 {
		t.Fatalf("mismatched MCP headers must be rejected: %d", mismatch.StatusCode)
	}
	if anonymous := post(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, nil); anonymous.StatusCode != 401 || anonymous.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("MCP requires the same sign-in as the API: %d", anonymous.StatusCode)
	}
	if trusted := post(`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"browser","version":"1"}}}`, map[string]string{"Authorization": "Bearer " + token, "Origin": "https://other.example"}); trusted.StatusCode != 200 || trusted.Header.Get("Access-Control-Allow-Origin") != "https://other.example" {
		t.Fatalf("a configured browser origin must be served: %d", trusted.StatusCode)
	}
	if foreign := post(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + token, "Origin": "https://attacker.example"}); foreign.StatusCode != 403 {
		t.Fatalf("an untrusted browser origin must be refused: %d", foreign.StatusCode)
	}
}

func TestMCPPackageRefusesAnUnreadablePackage(t *testing.T) {
	pack := func(files map[string]string) []byte {
		var buffer bytes.Buffer
		archive := zip.NewWriter(&buffer)
		for name, body := range files {
			w, _ := archive.Create(name)
			w.Write([]byte(body))
		}
		archive.Close()
		return buffer.Bytes()
	}
	sum := strings.Repeat("a", 64)
	if good := mcpPackage(pack(map[string]string{"manifest.json": `{"version":"close_package_v2"}`, "checksums.sha256": sum + "  manifest.json\n"}), "1"); good.IsError {
		t.Fatalf("a complete package is read: %v", good.StructuredContent)
	}
	for name, files := range map[string]map[string]string{
		"a manifest that is not an object":     {"manifest.json": `"oops"`, "checksums.sha256": sum + "  manifest.json\n"},
		"a truncated checksum line":            {"manifest.json": `{"version":"close_package_v2"}`, "checksums.sha256": sum + "  manifest.json\n" + sum[:20]},
		"a whitespace-only checksum line":      {"manifest.json": `{"version":"close_package_v2"}`, "checksums.sha256": "   \n" + sum + "  manifest.json\n"},
		"a carriage-return-only checksum line": {"manifest.json": `{"version":"close_package_v2"}`, "checksums.sha256": "\r\n" + sum + "  manifest.json\n"},
		"not a ZIP":                            nil,
	} {
		archive := []byte("not a zip")
		if files != nil {
			archive = pack(files)
		}
		if result := mcpPackage(archive, "1"); !result.IsError {
			t.Fatalf("%s is a tool error: %v", name, result.StructuredContent)
		}
	}
}
