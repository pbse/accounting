package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestDriveFilesAreListedAndHandedBackOnlyWithAGrant(t *testing.T) {
	mux := http.NewServeMux()
	registerDrive(mux)
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_01"}, Organization: "org_01"}
	call := func(method, path string, want int) (string, http.Header) {
		t.Helper()
		r := httptest.NewRequest(method, path, nil).WithContext(auth.WithActor(context.Background(), actor))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String(), w.Header()
	}
	t.Setenv("WORKOS_API_KEY", "")
	if body, _ := call("GET", "/v1/closes/7/drive", 200); !strings.Contains(body, `"configured":false`) {
		t.Fatalf("without a WorkOS key Drive is off: %s", body)
	}

	t.Setenv("WORKOS_API_KEY", "sk_test_synthetic")
	t.Setenv("PUBLIC_APP_URL", "https://app.example")
	grant := `{"active":false,"error":"not_installed"}`
	var sent map[string]string
	var query string
	csv := "date,amount\n2026-09-01,10.00\n"
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.URL.Host == "api.workos.com" && r.URL.Path == "/data-integrations/google-drive/token":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &sent)
			body = grant
		case r.URL.Host == "api.workos.com" && r.URL.Path == "/data-integrations/google-drive/authorize":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &sent)
			body = `{"url":"https://accounts.google.com/o/oauth2/auth?x=1"}`
		case r.URL.Host == "www.googleapis.com" && r.URL.Path == "/drive/v3/files":
			if r.Header.Get("Authorization") != "Bearer ya29.synthetic" {
				t.Fatalf("Drive is called with the person's token: %v", r.Header)
			}
			query = r.URL.Query().Get("q")
			body = `{"files":[{"id":"f1","name":"Bank statement","mimeType":"text/csv","size":"30"},{"id":"g1","name":"Notes","mimeType":"application/vnd.google-apps.document"}]}`
		case r.URL.Path == "/drive/v3/files/f1" && r.URL.Query().Get("alt") == "media":
			body = csv
		case r.URL.Path == "/drive/v3/files/f1":
			body = `{"name":"Bank statement","mimeType":"text/csv"}`
		case r.URL.Path == "/drive/v3/files/g1":
			body = `{"name":"Notes","mimeType":"application/vnd.google-apps.document"}`
		default:
			t.Fatalf("unexpected call %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	if body, _ := call("GET", "/v1/closes/7/drive", 200); !strings.Contains(body, `"connected":false`) || !strings.Contains(body, "Connect Google Drive") || sent["user_id"] != "user_01" || sent["organization_id"] != "org_01" {
		t.Fatalf("a person who has not connected is asked to: %s %v", body, sent)
	}
	grant = `{"active":true,"access_token":{"object":"access_token","access_token":"ya29.synthetic","scopes":[],"missing_scopes":["https://www.googleapis.com/auth/drive.readonly"]}}`
	if body, _ := call("GET", "/v1/closes/7/drive", 200); !strings.Contains(body, "drive.readonly") {
		t.Fatalf("a missing scope asks for reconnection: %s", body)
	}
	grant = `{"active":true,"access_token":{"object":"access_token","access_token":"ya29.synthetic","scopes":["https://www.googleapis.com/auth/drive.readonly"],"missing_scopes":[]}}`
	body, _ := call("GET", `/v1/closes/7/drive?q=O'Neil\x`, 200)
	if !strings.Contains(query, `name contains 'O\'Neil\\x'`) || !strings.Contains(query, "(mimeType='application/pdf' or mimeType='text/plain' or mimeType='text/csv')") || !strings.Contains(body, `"filename":"Bank statement.csv"`) || strings.Contains(body, "g1") || strings.Contains(body, "ya29") {
		t.Fatalf("the search is escaped, only supported files are listed and the token stays server-side: %s | %s", query, body)
	}
	if body, header := call("GET", "/v1/closes/7/drive/files/f1", 200); body != csv || !strings.Contains(header.Get("Content-Disposition"), "Bank%20statement.csv") {
		t.Fatalf("a CSV's bytes are handed back for the ordinary upload: %q %v", body, header)
	}
	if body, _ := call("GET", "/v1/closes/7/drive/files/g1", 422); !strings.Contains(body, "Google Docs") {
		t.Fatalf("a Google Doc is refused by its type before any download: %s", body)
	}
	csv = strings.Repeat("a,1\n", 140000)
	call("GET", "/v1/closes/7/drive/files/f1", 413)
	if body, _ := call("POST", "/v1/closes/7/drive/connect", 200); !strings.Contains(body, "accounts.google.com") || sent["return_to"] != "https://app.example/dashboard/closes/7?tab=evidence" {
		t.Fatalf("connecting returns to this close's evidence: %s %v", body, sent)
	}
	t.Setenv("PUBLIC_APP_URL", "http://app.example")
	call("POST", "/v1/closes/7/drive/connect", 409)
}
