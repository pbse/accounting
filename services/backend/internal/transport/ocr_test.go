package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestFirmSettingsShowHowTheWorkerReadsPDFs(t *testing.T) {
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
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Reading firm')`, org); err != nil {
		t.Fatal(err)
	}
	db.Exec(`DELETE FROM worker_heartbeats WHERE loop='worker'`)
	t.Cleanup(func() { db.Exec(`DELETE FROM worker_heartbeats WHERE loop='worker'`) })
	// Firm settings are served as the app serves them: a non-superuser through the tenant connection, so row-level
	// security applies to the signed-in member's firm.
	if _, err = db.Exec(`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='close_rls_probe') THEN CREATE ROLE close_rls_probe LOGIN PASSWORD 'probe-only' NOSUPERUSER NOBYPASSRLS; END IF; END $$;
		GRANT USAGE ON SCHEMA public TO close_rls_probe; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO close_rls_probe; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO close_rls_probe`); err != nil {
		t.Fatal(err)
	}
	probeURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	probeURL.User = url.UserPassword("close_rls_probe", "probe-only")
	tenant, err := OpenTenantDB(probeURL.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	mux := http.NewServeMux()
	registerIndependence(mux, tenant)
	read := func() string {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/organization/policy", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"clients:all"}}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var policy struct {
			OCR string `json:"ocr_provider"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &policy) != nil {
			t.Fatalf("policy: %d %s", w.Code, w.Body.String())
		}
		return policy.OCR
	}
	if got := read(); !strings.HasPrefix(got, "Not reported") {
		t.Fatalf("before the worker reports, Firm settings say so: %q", got)
	}

	// The worker's heartbeat reports how it reads PDFs, keeping what earlier beats recorded (a panic, say).
	if _, err = db.Exec(`INSERT INTO worker_heartbeats(loop,detail) VALUES('worker','{"panic":"earlier"}')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		RunHeartbeat(ctx, db, "worker", map[string]any{"ocr": map[string]string{"provider": "gemini", "model": "gemini-3.8-flash"}})
		close(stopped)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(read(), "Google Gemini (gemini-3.8-flash) reads every PDF page") {
		if time.Now().After(deadline) {
			t.Fatalf("the worker's report reaches Firm settings: %q", read())
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-stopped
	var panicked string
	if err = db.QueryRow(`SELECT detail->>'panic' FROM worker_heartbeats WHERE loop='worker'`).Scan(&panicked); err != nil || panicked != "earlier" {
		t.Fatalf("a heartbeat keeps the detail earlier beats recorded: %q %v", panicked, err)
	}
	member := auth.WithActor(t.Context(), &auth.Claims{Organization: org})
	if provider, model := workerOCR(member, tenant); provider != "gemini" || model != "gemini-3.8-flash" {
		t.Fatalf("a member's request reads the worker's OCR setting despite its firm's row-level security: %q %q", provider, model)
	}
	var visible int
	if err = tenant.QueryRowContext(member, `SELECT count(*) FROM worker_heartbeats`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("the member's own context still cannot read platform heartbeats: %d %v", visible, err)
	}

	// A worker without a key reads locally; one that has stopped reporting is not vouched for.
	if _, err = db.Exec(`UPDATE worker_heartbeats SET detail=detail||'{"ocr":{"provider":"tesseract"}}' WHERE loop='worker'`); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.HasPrefix(got, "Local only:") {
		t.Fatalf("without a key, PDFs are read locally: %q", got)
	}
	// A key whose paid service the operator has not confirmed sends nothing, and Firm settings say why.
	if _, err = db.Exec(`UPDATE worker_heartbeats SET detail=detail||'{"ocr":{"provider":"tesseract","held":"paid service not confirmed"}}' WHERE loop='worker'`); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.Contains(got, "paid-service confirmation") {
		t.Fatalf("a held key is shown as held: %q", got)
	}
	if _, err = db.Exec(`UPDATE worker_heartbeats SET detail=detail||'{"ocr":{"provider":"tesseract","held":"model name not valid"}}' WHERE loop='worker'`); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.Contains(got, "GEMINI_OCR_MODEL is not a valid model name") {
		t.Fatalf("a key held for a malformed model says so: %q", got)
	}
	if _, err = db.Exec(`UPDATE worker_heartbeats SET beat_at=now()-interval '10 minutes' WHERE loop='worker'`); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.HasPrefix(got, "Not reported") {
		t.Fatalf("a silent worker is not vouched for: %q", got)
	}
}
