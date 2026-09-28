package transport

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
)

func adminDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// signer serves a test key set as WorkOS's and signs session tokens for any organization.
func signer(t *testing.T) func(org string, permissions ...string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{"kid": "fixture", "kty": "RSA", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})
	t.Setenv("WORKOS_CLIENT_ID", "client_fixture")
	t.Setenv("WORKOS_ISSUER", "https://auth.example")
	t.Setenv("WORKOS_ORGANIZATION_ID", "")
	return func(org string, permissions ...string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_staff", Issuer: "https://auth.example", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())},
			Organization: org, Session: "session_fixture", Permissions: permissions})
		token.Header["kid"] = "fixture"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
}

func adminCall(t *testing.T, h http.Handler, token, method, path string, want int) []byte {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestStaffAndFirmSessionsNeverCross(t *testing.T) {
	db := adminDB(t)
	sign := signer(t)
	staffOrg, firmOrg := "org_Staff"+rand.Text(), "org_Firm"+rand.Text()
	t.Setenv("PLATFORM_ORGANIZATION_ID", staffOrg)
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Admin test firm')`, firmOrg); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM organizations WHERE id=$1`, firmOrg)
	h := apiWithStore(db, strings.Repeat("x", 32))

	var me staffMember
	json.Unmarshal(adminCall(t, h, sign(staffOrg, "platform:read", "clients:all"), "GET", "/v1/admin/me", 200), &me)
	if me.Subject != "user_staff" || !slices.Equal(me.Permissions, []string{"platform:read"}) || me.Mode != "workos" {
		t.Fatalf("staff identity %+v: only platform permissions count", me)
	}
	var firms struct{ Firms []adminFirmRow }
	json.Unmarshal(adminCall(t, h, sign(staffOrg, "platform:read"), "GET", "/v1/admin/firms?q="+firmOrg, 200), &firms)
	if len(firms.Firms) != 1 || firms.Firms[0].ID != firmOrg {
		t.Fatalf("firms %+v", firms)
	}
	adminCall(t, h, sign(staffOrg, "platform:content"), "GET", "/v1/admin/firms", 403)
	// A firm's session is refused even if its role claims a platform permission.
	adminCall(t, h, sign(firmOrg, "platform:read", "clients:all"), "GET", "/v1/admin/firms", 401)
	// A staff session cannot use the firm routes: the staff organization is never enrolled.
	adminCall(t, h, sign(staffOrg, "platform:read", "clients:all"), "GET", "/v1/closes", 403)

	t.Setenv("PLATFORM_ORGANIZATION_ID", "")
	adminCall(t, apiWithStore(db, strings.Repeat("x", 32)), sign(staffOrg, "platform:read"), "GET", "/v1/admin/me", 503)
}

func TestLocalDevelopmentStaffAccessUsesTheWorkspaceToken(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("d", 32)
	h := apiWithStore(db, token)
	var me staffMember
	json.Unmarshal(adminCall(t, h, token, "GET", "/v1/admin/me", 200), &me)
	if me.Mode != "local-development" || len(me.Permissions) != 4 {
		t.Fatalf("%+v", me)
	}
	adminCall(t, h, strings.Repeat("e", 32), "GET", "/v1/admin/me", 401)
	var overview map[string]any
	json.Unmarshal(adminCall(t, h, token, "GET", "/v1/admin/overview", 200), &overview)
	if _, ok := overview["heartbeats"]; !ok {
		t.Fatalf("overview %v", overview)
	}
}

func TestAdminReadsCannotWrite(t *testing.T) {
	db := adminDB(t)
	err := adminRead(context.Background(), db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO worker_heartbeats(loop) VALUES('admin-read-test')`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a read must not be able to write: %v", err)
	}
}

func TestAdminActionsRunAsTheFirmAndAreAudited(t *testing.T) {
	db := adminDB(t)
	firm := "org_Acted" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Acted firm')`, firm); err != nil {
		t.Fatal(err)
	}
	// The audit log is append-only, so this firm and its audit rows stay behind, as they would in production.
	ctx := context.WithValue(context.Background(), staffKey{}, &staffMember{Subject: "user_staff", Permissions: platformPermissions})
	var actingAs, agent string
	err := adminAct(ctx, db, adminAction{Firm: firm, Action: "test.touch", TargetType: "organization", TargetID: firm, Reason: "support ticket 42", Request: map[string]string{"note": "touch"}},
		func(ctx context.Context, tx *sql.Tx) error {
			actingAs, agent = auth.Actor(ctx).Organization, auth.Agent(ctx)
			_, err := tx.ExecContext(ctx, `UPDATE organizations SET reminder_max=reminder_max WHERE id=$1`, firm)
			return err
		})
	if err != nil || actingAs != firm || agent != "staff:user_staff" {
		t.Fatalf("a staff action runs as the firm, attributed to the staff member: %v %q %q", err, actingAs, agent)
	}
	refused := errors.New("the firm is already suspended")
	if err = adminAct(ctx, db, adminAction{Firm: firm, Action: "test.refused", Reason: "try"}, func(context.Context, *sql.Tx) error { return refused }); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if err = adminAct(ctx, db, adminAction{Firm: firm, Action: "test.silent"}, func(context.Context, *sql.Tx) error { return nil }); !errors.Is(err, errReasonRequired) {
		t.Fatalf("a staff action without a reason: %v", err)
	}
	rows, err := db.Query(`SELECT action,result,staff_user,reason FROM platform_audit WHERE organization_id=$1 ORDER BY id`, firm)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var log []string
	for rows.Next() {
		var action, result, staff, reason string
		rows.Scan(&action, &result, &staff, &reason)
		log = append(log, action+" "+result+" "+staff+" "+reason)
	}
	if want := []string{"test.touch ok user_staff support ticket 42", "test.refused refused user_staff try"}; !slices.Equal(log, want) {
		t.Fatalf("audit %v, want %v", log, want)
	}
}

func TestFirmDetailShowsRunsAndTheLatestHealthCheck(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("d", 32)
	firm := "org_Detail" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Detail firm')`, firm); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM organizations WHERE id=$1`, firm)
	defer db.Exec(`DELETE FROM workflow_runs WHERE organization_id=$1`, firm)
	if _, err := db.Exec(`INSERT INTO workflow_runs(organization_id,workflow,idempotency_key,started_by,state,output,finished_at) VALUES($1,'firm_health_check','x','schedule','completed','{"failed_preparation":2}',now())`, firm); err != nil {
		t.Fatal(err)
	}
	h := apiWithStore(db, token)
	var detail struct {
		Firm   adminFirmRow
		Runs   []map[string]any
		Health map[string]any
	}
	json.Unmarshal(adminCall(t, h, token, "GET", "/v1/admin/firms/"+firm, 200), &detail)
	if detail.Firm.Name != "Detail firm" || len(detail.Runs) != 1 || detail.Health == nil || !strings.Contains(string(mustJSON(detail.Health["report"])), "failed_preparation") {
		t.Fatalf("detail %+v", detail)
	}
	adminCall(t, h, token, "GET", "/v1/admin/firms/org_Missing"+rand.Text(), 404)
}

func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

func TestTheAuditLogCannotBeChangedOrErased(t *testing.T) {
	db := adminDB(t)
	firm := "org_Audited" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Audited firm')`, firm); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), staffKey{}, &staffMember{Subject: "user_staff", Permissions: platformPermissions})
	if err := adminAct(ctx, db, adminAction{Firm: firm, Action: "test.recorded", Reason: "for the record"}, func(context.Context, *sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE platform_audit SET reason='rewritten' WHERE organization_id=$1`, `DELETE FROM platform_audit WHERE organization_id=$1`} {
		if _, err := db.Exec(q, firm); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Exec(`TRUNCATE platform_audit`); err == nil {
		t.Fatal("the audit log must not be truncated")
	}
}

func TestARefusedActionThatLockedTheFirmIsAuditedPromptly(t *testing.T) {
	db := adminDB(t)
	firm := "org_Locked" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Locked firm')`, firm); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), staffKey{}, &staffMember{Subject: "user_staff", Permissions: platformPermissions}), 10*time.Second)
	defer cancel()
	refused := errors.New("refused after locking")
	err := adminAct(ctx, db, adminAction{Firm: firm, Action: "test.locked", Reason: "check the lock"}, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, firm); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) || ctx.Err() != nil {
		t.Fatalf("the refusal must be recorded without waiting on the action's own lock: %v %v", err, ctx.Err())
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM platform_audit WHERE organization_id=$1 AND result='refused'`, firm).Scan(&n)
	if n != 1 {
		t.Fatalf("refusals recorded: %d", n)
	}
}

func TestTheFirmsListPagesInsteadOfTruncating(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("d", 32)
	prefix := "org_Page" + rand.Text()[:8]
	for i := 0; i < 101; i++ {
		if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Paged firm')`, fmt.Sprintf("%s%03d", prefix, i)); err != nil {
			t.Fatal(err)
		}
	}
	defer db.Exec(`DELETE FROM organizations WHERE id LIKE $1`, prefix+"%")
	h := apiWithStore(db, token)
	var page struct {
		Firms      []adminFirmRow
		HasMore    bool `json:"has_more"`
		NextOffset int  `json:"next_offset"`
	}
	json.Unmarshal(adminCall(t, h, token, "GET", "/v1/admin/firms?q="+prefix, 200), &page)
	if len(page.Firms) != 100 || !page.HasMore || page.NextOffset != 100 {
		t.Fatalf("first page: %d firms, more %v, next %d", len(page.Firms), page.HasMore, page.NextOffset)
	}
	json.Unmarshal(adminCall(t, h, token, "GET", fmt.Sprintf("/v1/admin/firms?q=%s&offset=%d", prefix, page.NextOffset), 200), &page)
	if len(page.Firms) != 1 || page.HasMore {
		t.Fatalf("second page: %d firms, more %v", len(page.Firms), page.HasMore)
	}
	adminCall(t, h, token, "GET", "/v1/admin/firms?offset=-1", 422)
}
