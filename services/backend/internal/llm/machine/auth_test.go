package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fakeCodexAuth() string {
	claims := `{"email":"test@example.invalid","https://api.openai.com/auth":{"chatgpt_account_id":"test-account","chatgpt_plan_type":"pro"}}`
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "last_refresh": time.Now().UTC().Format(time.RFC3339), "tokens": map[string]string{"access_token": "upstream-secret", "refresh_token": "fake-refresh", "account_id": "test-account", "id_token": "e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".fake"}})
	return string(raw)
}

func TestPersonalAuthValidationAndSelection(t *testing.T) {
	cfg := Config{CodexModel: "fixed", CodexAuthJSON: fakeCodexAuth()}
	got, err := cfg.agent("codex")
	if err != nil || got.mode != "chatgpt" {
		t.Fatal("personal auth unavailable")
	}
	cfg.OpenAIKey = "api-key"
	got, err = cfg.agent("codex")
	if err != nil || got.mode != "api_key" {
		t.Fatal("auto priority changed")
	}
	cfg.CodexAuth = "chatgpt"
	got, err = cfg.agent("codex")
	if err != nil || got.mode != "chatgpt" {
		t.Fatal("explicit personal mode ignored")
	}
	for _, raw := range []string{"", `{}`, `{"auth_mode":"api_key"}`, strings.Repeat("x", maxAuthBytes+1), strings.Replace(fakeCodexAuth(), "fake-refresh", "", 1)} {
		cfg.CodexAuthJSON = raw
		if _, err = cfg.agent("codex"); err == nil {
			t.Fatal("invalid auth accepted")
		}
	}
}

func TestPersonalAuthRestoresAndPersistsRotatedTokens(t *testing.T) {
	current := fakeCodexAuth()
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer service-token" || len(r.Header.Get("X-Auth-Seed")) != 64 || r.Header.Get("X-Auth-Lease") == "" {
			t.Error("missing storage authentication")
		}
		if r.Method == "POST" {
			io.WriteString(w, current)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		current = string(raw)
		puts++
	}))
	defer server.Close()
	cfg := Config{CodexAuthJSON: current, CodexAuthStateURL: server.URL, Token: "service-token"}
	session, err := restoreCodexAuth(context.Background(), cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(session.file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("auth permissions")
	}
	updated := strings.Replace(current, "fake-refresh", "rotated-refresh", 1)
	if err = os.WriteFile(session.file, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err = session.save(true); err != nil || current != updated || puts != 1 {
		t.Fatal("refresh not persisted")
	}
	next, err := restoreCodexAuth(context.Background(), cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := next.read()
	if err != nil || loaded.Tokens.Refresh != "rotated-refresh" {
		t.Fatal("restart reverted refreshed credential")
	}
}

func TestPersonalProxyDoesNotSendInferenceWhenCredentialSaveFails(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret detail", 503) }))
	defer store.Close()
	file := t.TempDir() + "/auth.json"
	if err := os.WriteFile(file, []byte(fakeCodexAuth()), 0600); err != nil {
		t.Fatal(err)
	}
	sent := false
	p := &modelProxy{ctx: context.Background(), token: "per-run", agent: "codex", model: "fixed", effort: "medium", authMode: "chatgpt", limit: 2, tokens: 100, personal: &codexAuthSession{file: file, endpoint: store.URL, client: store.Client()}, client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { sent = true; return nil, nil })}}
	for _, header := range []string{"", "wrong", "per-run"} {
		req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
		req.Header.Set("X-Context-Machine-Run", header)
		response := httptest.NewRecorder()
		p.ServeHTTP(response, req)
		want := 401
		if header == "per-run" {
			want = 503
		}
		if response.Code != want || sent || strings.Contains(response.Body.String(), "secret detail") {
			t.Fatal("failed storage allowed inference or leaked details")
		}
	}
}

func TestFailedCLIStillPersistsRefreshedLogin(t *testing.T) {
	seed := fakeCodexAuth()
	rotated := strings.Replace(seed, "fake-refresh", "rotated-refresh", 1)
	saved, released := "", false
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			io.WriteString(w, seed)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		saved = string(raw)
		released = r.Header.Get("X-Auth-Release") == "true"
	}))
	defer store.Close()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' '" + rotated + "' > \"$CODEX_HOME/auth.json\"\nexit 1\n"
	if err := os.WriteFile(bin+"/codex", []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	s := &server{cfg: Config{CodexAuthJSON: seed, CodexAuthStateURL: store.URL}, agents: map[string]agentSettings{"codex": {model: "fixed", mode: "chatgpt", effort: "medium"}}}
	if _, err := s.execute(context.Background(), runRequest{Model: "codex"}); err == nil {
		t.Fatal("failed CLI succeeded")
	}
	if saved != rotated || !released {
		t.Fatal("failed run lost refreshed login or retained its lease")
	}
}

func TestRestoreFailureReleasesLeaseWithoutChangingCredentials(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		released := false
		store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "DELETE" {
				released = true
				w.WriteHeader(200)
				return
			}
			if lostResponse {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			io.WriteString(w, fakeCodexAuth())
		}))
		// Missing directory makes the file write fail after an otherwise good restore.
		_, err := restoreCodexAuth(context.Background(), Config{CodexAuthJSON: fakeCodexAuth(), CodexAuthStateURL: store.URL}, t.TempDir()+"/missing")
		store.Close()
		if err == nil || !released {
			t.Fatal("restore failure did not release its lease")
		}
	}
}
