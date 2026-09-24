package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type authkitJWKS struct {
	key  *rsa.PublicKey
	next http.RoundTripper
}

func (a authkitJWKS) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "auth.test" {
		return a.next.RoundTrip(r)
	}
	body, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": "agent", "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(a.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(a.key.E)).Bytes())}}})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
}

func TestAgentsActOnlyUnderAnApprovedMemberGrant(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_" + rand.Text()
	member, admin := "user_agent"+rand.Text(), "user_admin"+rand.Text()
	mine, other := "Assigned client "+rand.Text(), "Other client "+rand.Text()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Agent firm')`, org)
	must(err)
	var mineID, otherID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES($1,'2026-09',$2) RETURNING id::text`, mine, org).Scan(&mineID))
	must(db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES($1,'2026-09',$2) RETURNING id::text`, other, org).Scan(&otherID))
	membership := func(subject string, granted bool) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,$3,'Synthetic membership','fixture')`, org, subject, granted)
		must(e)
	}
	membership(admin, true)
	membership(member, true)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,$3,true,'Synthetic assignment','fixture')`, org, member, mine)
	must(err)
	defer func() {
		db.Exec(`DELETE FROM agent_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM client_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM client_closes WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	must(err)
	original := http.DefaultTransport
	http.DefaultTransport = authkitJWKS{key: &key.PublicKey, next: original}
	defer func() { http.DefaultTransport = original }()
	t.Setenv("WORKOS_AUTHKIT_DOMAIN", "https://auth.test")
	t.Setenv("MCP_RESOURCE_URL", "https://api.test/mcp")
	sign := func(audience string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: member, Issuer: "https://auth.test", Audience: jwt.ClaimStrings{audience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}, Session: "consent_1", Organization: org})
		token.Header["kid"] = "agent"
		raw, e := token.SignedString(key)
		must(e)
		return raw
	}
	agentToken := sign("https://api.test/mcp")
	server := httptest.NewServer(apiWithStore(db, strings.Repeat("a", 32)))
	defer server.Close()

	grants := http.NewServeMux()
	registerAgentAccess(grants, db)
	decide := func(subject string, permissions []string, body map[string]any, want int) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/closes/agent-access", bytes.NewReader(raw)).WithContext(auth.WithActor(context.Background(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		grants.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body.String())
		}
		var out struct{ ID string }
		json.Unmarshal(w.Body.Bytes(), &out)
		return out.ID
	}
	adminPerms := []string{"clients:all", "members:manage"}
	initialize := func(token string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"agent","version":"1"}}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Authorization", "Bearer "+token)
		response, e := http.DefaultClient.Do(r)
		must(e)
		return response
	}
	if response := initialize(agentToken); response.StatusCode != 403 {
		t.Fatalf("an agent without an approved grant must be refused: %d", response.StatusCode)
	}
	if response := initialize(sign("https://other.test/mcp")); response.StatusCode != 401 || !strings.Contains(response.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("a token for another resource must be refused with the metadata pointer: %d %s", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}

	decide(member, nil, map[string]any{"action": "request", "scope": "read", "all_clients": true, "reason": "Connect my assistant", "confirmed": true}, 422)
	request := decide(member, nil, map[string]any{"action": "request", "scope": "read", "reason": "Connect my assistant", "confirmed": true}, 200)
	if response := initialize(agentToken); response.StatusCode != 403 {
		t.Fatalf("a pending request is not access: %d", response.StatusCode)
	}
	decide(member, nil, map[string]any{"action": "approve", "subject": member, "scope": "read", "previous_id": request, "reason": "Self approval attempt", "confirmed": true}, 403)
	decide(admin, adminPerms, map[string]any{"action": "approve", "subject": member, "scope": "prepare", "previous_id": request, "reason": "Widening attempt", "confirmed": true}, 422)
	approved := decide(admin, adminPerms, map[string]any{"action": "approve", "subject": member, "scope": "read", "previous_id": request, "reason": "Approved for read-only use", "confirmed": true}, 200)

	connect := func() *mcp.ClientSession {
		t.Helper()
		session, e := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{agentToken}}}, nil)
		must(e)
		return session
	}
	names := func(session *mcp.ClientSession) []string {
		t.Helper()
		tools, e := session.ListTools(context.Background(), nil)
		must(e)
		out := []string{}
		for _, tool := range tools.Tools {
			out = append(out, tool.Name)
		}
		return out
	}
	session := connect()
	if list := names(session); slices.Contains(list, "prepare_close") || slices.Contains(list, "request_approval") || !slices.Contains(list, "list_closes") {
		t.Fatalf("a read grant must offer only read tools: %v", list)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_closes", Arguments: map[string]any{}})
	must(err)
	text := result.Content[0].(*mcp.TextContent).Text
	if result.IsError || !strings.Contains(text, mineID) || strings.Contains(text, otherID) {
		t.Fatalf("the agent must see only the member's assigned client: %s", text)
	}
	session.Close()

	request = decide(member, nil, map[string]any{"action": "request", "scope": "prepare", "previous_id": approved, "reason": "Let my assistant prepare work", "confirmed": true}, 200)
	if response := initialize(agentToken); response.StatusCode != 200 {
		t.Fatalf("a pending upgrade must not suspend approved read access: %d", response.StatusCode)
	}
	decide(admin, adminPerms, map[string]any{"action": "approve", "subject": member, "scope": "prepare", "previous_id": request, "reason": "Approved preparation", "confirmed": true}, 200)
	session = connect()
	if list := names(session); !slices.Contains(list, "prepare_close") {
		t.Fatalf("a prepare grant must offer preparation tools: %v", list)
	}
	session.Close()

	membership(member, false)
	membership(member, true)
	if response := initialize(agentToken); response.StatusCode != 403 {
		t.Fatalf("re-enrollment must not revive an earlier agent grant: %d", response.StatusCode)
	}
	metadata, err := http.Get(server.URL + "/.well-known/oauth-protected-resource/mcp")
	must(err)
	var described struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
	}
	json.NewDecoder(metadata.Body).Decode(&described)
	if described.Resource != "https://api.test/mcp" || len(described.Servers) != 1 || described.Servers[0] != "https://auth.test" {
		t.Fatalf("protected resource metadata: %+v", described)
	}
}
