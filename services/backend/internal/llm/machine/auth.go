package machine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const maxAuthBytes = 32 << 10

var errCodexAuth = errors.New("personal Codex login unavailable; reseed managed auth through the secure setup instructions")

type codexAuth struct {
	Mode   string `json:"auth_mode"`
	Tokens struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		ID      string `json:"id_token"`
		Account string `json:"account_id"`
	} `json:"tokens"`
}

func parseCodexAuth(raw []byte) (codexAuth, error) {
	var a codexAuth
	if len(raw) > maxAuthBytes || json.Unmarshal(raw, &a) != nil || a.Mode != "chatgpt" || a.Tokens.Access == "" || a.Tokens.Refresh == "" || a.Tokens.ID == "" || a.Tokens.Account == "" {
		return a, errCodexAuth
	}
	return a, nil
}

// Only auth.json survives a run, in the private Durable Object. Workspaces and
// conversations stay ephemeral. The CLI owns OAuth refresh; this code never does.
type codexAuthSession struct {
	file, endpoint, token, seed, lease string
	client                             *http.Client
}

func restoreCodexAuth(ctx context.Context, cfg Config, dir string) (*codexAuthSession, error) {
	sum := sha256.Sum256([]byte(cfg.CodexAuthJSON))
	a := &codexAuthSession{file: filepath.Join(dir, "auth.json"), endpoint: cfg.CodexAuthStateURL, token: cfg.Token, seed: hex.EncodeToString(sum[:]), lease: rand.Text(), client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	raw, err := a.request(ctx, "POST", nil, false)
	if err != nil {
		return nil, err
	}
	if _, err = parseCodexAuth(raw); err != nil {
		return nil, err
	}
	if err = os.WriteFile(a.file, raw, 0600); err != nil {
		return nil, errCodexAuth
	}
	return a, nil
}

func (a *codexAuthSession) read() (codexAuth, error) {
	raw, err := a.bytes()
	if err != nil {
		return codexAuth{}, err
	}
	return parseCodexAuth(raw)
}
func (a *codexAuthSession) bytes() ([]byte, error) {
	f, err := os.Open(a.file)
	if err != nil {
		return nil, errCodexAuth
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxAuthBytes+1))
	if err != nil {
		return nil, errCodexAuth
	}
	if _, err = parseCodexAuth(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
func (a *codexAuthSession) save(release bool) error {
	raw, err := a.bytes()
	if err != nil {
		return err
	}
	// A disconnected caller must not prevent saving a rotated refresh token.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = a.request(ctx, "PUT", raw, release)
	return err
}
func (a *codexAuthSession) request(ctx context.Context, method string, body []byte, release bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errCodexAuth
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("X-Auth-Seed", a.seed)
	req.Header.Set("X-Auth-Lease", a.lease)
	if release {
		req.Header.Set("X-Auth-Release", "true")
	}
	res, err := a.client.Do(req)
	if err != nil {
		return nil, errCodexAuth
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errCodexAuth
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxAuthBytes+1))
	if err != nil || len(raw) > maxAuthBytes {
		return nil, errCodexAuth
	}
	return raw, nil
}
