package transport

import (
	"archive/zip"
	"bytes"
	"close.local/backend/internal/prepaids"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWorkspaceAccessBoundary(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, test := range []struct {
		token, auth string
		status      int
	}{{"", "", 503}, {strings.Repeat("x", 32), "", 401}, {strings.Repeat("x", 32), "Bearer wrong", 401}} {
		handler := savedCloses(db, test.token)
		r := httptest.NewRequest("GET", "/v1/closes", nil)
		r.Header.Set("Authorization", test.auth)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("access boundary: got %d, want %d", w.Code, test.status)
		}
	}
}

// Run against a disposable, migrated local PostgreSQL database only.
func TestSavedCloseRoundTrip(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	call := func(method, path, body string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var close savedClose
	json.Unmarshal(call("POST", "/v1/closes", `{"entity":"Round trip fixture","period":"2026-09"}`, 200), &close)
	defer func() {
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, close.ID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, close.ID)
	}()
	input := `{"entity":"Round trip fixture","source_reference":"Invoice page 1","amount":"12000.00","service_start":"2026-09-01","service_end":"2027-08-31","close_period":"2026-09","currency":"USD","basis":"accrual","expense_account":"Expense","prepaid_account":"Prepaid","method":"equal_full_months_v1","method_confirmed":true,"booking_confirmed":true}`
	body := `{"kind":"prepaid","input":` + input + `}`
	first := call("POST", "/v1/closes/"+close.ID+"/workpapers", body, 200)
	second := call("POST", "/v1/closes/"+close.ID+"/workpapers", body, 200)
	if string(first) != string(second) {
		t.Fatal("repeat save changed identity or calculation")
	}
	call("POST", "/v1/closes/"+close.ID+"/workpapers", strings.Replace(body, "Round trip fixture", "Wrong client", 1), 409)
	var result struct {
		Workpapers []savedPaper `json:"workpapers"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+close.ID, "", 200), &result)
	var restored prepaids.Workpaper
	if len(result.Workpapers) != 1 {
		t.Fatalf("expected one saved draft, got %d", len(result.Workpapers))
	}
	if json.Unmarshal(result.Workpapers[0].Paper, &restored) != nil || restored.PrepaidBalance != "11000.00" {
		t.Fatalf("readback failed: %+v", result)
	}
	// Export must include records beyond the dashboard's 100-row display limit.
	_, err = db.Exec(`INSERT INTO workpapers(close_id,kind,digest,paper) SELECT $1,'prepaid','export-'||n,$2::jsonb FROM generate_series(1,100) n`, close.ID, string(result.Workpapers[0].Paper))
	if err != nil {
		t.Fatal(err)
	}
	exported := call("GET", "/v1/closes/"+close.ID+"/export", "", 200)
	archive, err := zip.NewReader(bytes.NewReader(exported), int64(len(exported)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range archive.File {
		if file.Name == "manifest.json" {
			reader, _ := file.Open()
			raw, _ := io.ReadAll(reader)
			reader.Close()
			var manifest closePackage
			if json.Unmarshal(raw, &manifest) != nil || len(manifest.Workpapers) != 101 || manifest.Close.ID != close.ID || manifest.DataCutAt.IsZero() {
				t.Fatal("export truncated or mixed the close snapshot")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("manifest missing")
	}
	request := httptest.NewRequest("GET", "/v1/closes/"+close.ID+"/export", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("unauthenticated export was allowed")
	}

}
