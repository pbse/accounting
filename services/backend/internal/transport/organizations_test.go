package transport

import (
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
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestOrganizationIsolation(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	suffix := rand.Text()
	orgA, orgB := "org_A"+suffix, "org_B"+suffix
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Firm A'),($2,'Firm B')`, orgA, orgB); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_reviewer',true,'Synthetic enrollment','fixture'),($2,'user_reviewer',true,'Synthetic enrollment','fixture')`, orgA, orgB); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"preparation_jobs", "workpapers", "evidence"} {
			if _, e := db.Exec(`DELETE FROM `+table+` WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id IN ($1,$2))`, orgA, orgB); e != nil {
				t.Error(e)
			}
		}
		db.Exec(`DELETE FROM client_closes WHERE organization_id IN ($1,$2)`, orgA, orgB)
		db.Exec(`DELETE FROM client_access WHERE organization_id IN ($1,$2)`, orgA, orgB)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id IN ($1,$2)`, orgA, orgB)
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, orgA, orgB)
	}()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.workos.com/sso/jwks/client_fixture" {
			t.Fatalf("unexpected provider %s", r.URL.Host)
		}
		body, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{"kid": "fixture", "kty": "RSA", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})
	t.Setenv("WORKOS_CLIENT_ID", "client_fixture")
	t.Setenv("WORKOS_ISSUER", "https://auth.example")
	t.Setenv("WORKOS_ORGANIZATION_ID", "")
	signPermissions := func(org string, permissions []string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_reviewer", Issuer: "https://auth.example", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Session: "session", Organization: org, Permissions: permissions})
		token.Header["kid"] = "fixture"
		raw, e := token.SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	sign := func(org string) string {
		return signPermissions(org, []string{"clients:all", "members:manage", "workpapers:review", "posting:approve"})
	}
	tokenA, tokenB := sign(orgA), sign(orgB)
	h := apiWithStore(db, strings.Repeat("x", 32))
	call := func(token, method, path, body string, want int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	call(sign("org_Unregistered"), "GET", "/v1/closes", "", 403)
	call(sign("local-development"), "GET", "/v1/closes", "", 401)
	call(strings.Repeat("x", 32), "GET", "/v1/closes", "", 401)
	var a, b savedClose
	json.Unmarshal(call(tokenA, "POST", "/v1/closes", `{"entity":"Shared client name","period":"2026-09"}`, 200), &a)
	json.Unmarshal(call(tokenB, "POST", "/v1/closes", `{"entity":"Shared client name","period":"2026-09"}`, 200), &b)
	if a.ID == b.ID {
		t.Fatal("firms shared close identity")
	}
	var staleA, staleB string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','missing-source','{"sources":[{"id":"0","digest":"missing"}]}') RETURNING id::text`, a.ID).Scan(&staleA); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','missing-source','{"sources":[{"id":"0","digest":"missing"}]}') RETURNING id::text`, b.ID).Scan(&staleB); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ token, own, other string }{{tokenA, staleA, staleB}, {tokenB, staleB, staleA}} {
		var dependencies struct {
			Items []struct {
				WorkpaperID string `json:"workpaper_id"`
			} `json:"items"`
		}
		json.Unmarshal(call(scope.token, "GET", "/v1/closes/dependencies", "", 200), &dependencies)
		if len(dependencies.Items) != 1 || dependencies.Items[0].WorkpaperID != scope.own {
			t.Fatalf("dependency review crossed firms or omitted own work: %+v", dependencies)
		}
	}
	for _, test := range []struct {
		token      string
		own, other savedClose
	}{{tokenA, a, b}, {tokenB, b, a}} {
		var list struct {
			Closes []savedClose `json:"closes"`
		}
		json.Unmarshal(call(test.token, "GET", "/v1/closes", "", 200), &list)
		if len(list.Closes) != 1 || list.Closes[0].ID != test.own.ID {
			t.Fatalf("portfolio leaked: %+v", list)
		}
		for _, path := range []string{"", "/export", "/evidence", "/work", "/carry-forward", "/proposed-state", "/workpapers/1", "/evidence/1/download"} {
			call(test.token, "GET", "/v1/closes/"+test.other.ID+path, "", 404)
		}
		for _, path := range []string{"/workpapers", "/evidence", "/carry-forward", "/work/1/answer", "/workpapers/1/reviews"} {
			call(test.token, "POST", "/v1/closes/"+test.other.ID+path, `{}`, 404)
		}
		call(test.token, "GET", "/v1/closes/%"+"32"+test.other.ID, "", 404)
	}
	restricted := signPermissions(orgA, []string{"workpapers:review", "close:signoff"})
	call(restricted, "GET", "/v1/closes/"+a.ID, "", 404)
	var restrictedDependencies struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(call(restricted, "GET", "/v1/closes/dependencies", "", 200), &restrictedDependencies)
	if len(restrictedDependencies.Items) != 0 {
		t.Fatal("unassigned client dependency leaked")
	}
	var access struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call(tokenA, "POST", "/v1/closes/access", `{"subject":"user_reviewer","entity":"Shared client name","granted":true,"reason":"Synthetic assigned accountant","confirmed":true}`, 200), &access)
	call(restricted, "GET", "/v1/closes/"+a.ID, "", 200)
	json.Unmarshal(call(restricted, "GET", "/v1/closes/dependencies", "", 200), &restrictedDependencies)
	if len(restrictedDependencies.Items) != 1 || restrictedDependencies.Items[0]["workpaper_id"] != staleA {
		t.Fatal("assigned client dependency missing")
	}
	call(restricted, "GET", "/v1/closes/"+b.ID, "", 404)
	call(restricted, "POST", "/v1/closes", `{"entity":"Unassigned client","period":"2026-09"}`, 403)
	call(restricted, "POST", "/v1/closes/access", `{}`, 403)
	call(tokenA, "POST", "/v1/closes/access", `{"subject":"user_reviewer","entity":"Shared client name","granted":false,"reason":"Synthetic revocation","confirmed":true,"previous_id":"`+access.ID+`"}`, 200)
	call(restricted, "GET", "/v1/closes/"+a.ID, "", 404)
	// A same-named next close in another firm must not be returned by carry-forward.
	var next string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES('Shared client name','2026-10',$1) RETURNING id::text`, orgB).Scan(&next); err != nil {
		t.Fatal(err)
	}
	var preview struct {
		CloseID string `json:"close_id"`
	}
	json.Unmarshal(call(tokenA, "GET", "/v1/closes/"+a.ID+"/carry-forward", "", 200), &preview)
	if preview.CloseID != "" {
		t.Fatal("carry-forward crossed firms")
	}
	var job string
	if err = db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,'tenant-test','[]','queued','fixture') RETURNING id::text`, b.ID).Scan(&job); err != nil {
		t.Fatal(err)
	}
	var inbox struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(call(tokenA, "GET", "/v1/closes/work", "", 200), &inbox)
	if len(inbox.Items) != 0 {
		t.Fatal("work inbox crossed firms")
	}
	json.Unmarshal(call(tokenB, "GET", "/v1/closes/work", "", 200), &inbox)
	if len(inbox.Items) != 1 || inbox.Items[0]["id"] != job {
		t.Fatal("own work missing")
	}
	call(tokenB, "POST", "/v1/closes/"+b.ID+"/workpapers/1/posting", `{"action":"preview"}`, 403)
	if _, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_reviewer',false,'Synthetic immediate revocation','fixture')`, orgB); err != nil {
		t.Fatal(err)
	}
	call(tokenB, "GET", "/v1/closes", "", 403)
	if _, err = db.Exec(`UPDATE organizations SET enabled=false WHERE id=$1`, orgB); err != nil {
		t.Fatal(err)
	}
	call(tokenB, "GET", "/v1/closes", "", 403)
	if _, err = preparation.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("disabled firm's job ran: %s %v", state, err)
	}
}
