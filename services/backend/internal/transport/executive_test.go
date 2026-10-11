package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestExecutivesReadOnlyTheirClientsPublishedReportsAndCashViews(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Executive" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Executive fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_ceo',true,'Synthetic','fixture'),($1,'user_staff',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_ceo','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	var alpha, beta, publication string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	defer func() {
		for _, q := range []string{`DELETE FROM management_publications WHERE organization_id=$1`, `DELETE FROM budget_versions WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`,
			`DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	pack := `{"entity":"Alpha","period":"2026-09","currency":"USD","basis":"Accrual","note":"","profit_and_loss":{"sections":[],"measures":[{"name":"Net income","amount":"300.00"}]},
 "balance_sheet":{"sections":[]},"working_capital":{"book_cash":{"name":"Book cash","amount":"1500.00"},"working_capital":{"name":"Working capital","amount":"1300.00"}}}`
	must(db.QueryRow(`INSERT INTO management_publications(organization_id,close_id,version,pack,digest,commentary,published_by) VALUES($1,$2,1,$3,'d','Rent held; net income 300.','user_staff') RETURNING id::text`, org, alpha, pack).Scan(&publication))

	store := savedCloses(db, strings.Repeat("e", 32))
	// The executive's role also carries staff permissions: executive access still confines them.
	ceo := []string{"executive:read", "clients:all", "workpapers:review"}
	call := func(who string, permissions []string, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw))).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: who}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s: %d %s", who, method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var overview struct {
		Executive bool
		Closes    []map[string]string
		Published []map[string]any
	}
	must(json.Unmarshal(call("user_ceo", ceo, "GET", "/v1/closes/executive", nil, 200), &overview))
	if !overview.Executive || len(overview.Closes) != 1 || overview.Closes[0]["entity"] != "Alpha" || len(overview.Published) != 1 || overview.Published[0]["net_income"] != "300.00" || overview.Published[0]["book_cash"] != "1500.00" || overview.Published[0]["acknowledged"] != false {
		t.Fatalf("an executive sees their assigned client's newest published pack: %+v", overview)
	}
	// The fixture's published digest is not the live books', so the version is never presented as current (its standing
	// is computed while its month is the latest ended one or later, as Home rechecks sign-offs).
	recent := "2026-09" >= endedMonth(time.Now())
	if state, ok := overview.Published[0]["state"]; recent && state != "needs_correction" && state != "unverified" || !recent && ok {
		t.Fatalf("a recent version the books no longer match is not presented as current; an older one is checked when opened: %v", state)
	}
	base := "/v1/closes/" + alpha
	call("user_ceo", ceo, "GET", base+"/management-pack/publications", nil, 200)
	if body := call("user_ceo", ceo, "GET", base+"/management-pack/publications/"+publication+"/export?format=csv", nil, 200); !strings.Contains(string(body), "Net income,300.00") {
		t.Fatalf("an executive downloads the published version: %s", body)
	}
	if body := call("user_ceo", ceo, "GET", base+"/management-pack/publications/"+publication, nil, 200); !strings.Contains(string(body), `"Rent held; net income 300."`) || !strings.Contains(string(body), `"Net income"`) {
		t.Fatalf("an executive reads the frozen version in the page: %s", body)
	}
	call("user_ceo", ceo, "POST", base+"/management-pack/publications", map[string]any{"action": "acknowledge", "publication_id": publication}, 200)
	call("user_ceo", ceo, "POST", base+"/management-pack/publications", map[string]any{"action": "publish", "digest": "d", "commentary": "x", "confirmed": true}, 403)
	call("user_ceo", ceo, "GET", base+"/cash-plan", nil, 200)
	call("user_ceo", ceo, "GET", base+"/cash-scenarios", nil, 200)
	call("user_ceo", ceo, "GET", base+"/cash-plan/reviews", nil, 200)
	call("user_ceo", ceo, "POST", base+"/cash-plan/reviews", map[string]any{"digest": "d", "assumptions": "Looks right to me", "confirmed": true}, 403)
	_, err = db.Exec(`INSERT INTO budget_versions(organization_id,client_entity,name,company,currency,lines,digest,source,created_by) VALUES($1,'Alpha','Draft plan','quickbooks:123','USD','[{"account":"Salaries","account_id":"","months":{"2026-09":"9000.00"}}]','b','Pasted CSV','user_staff')`, org)
	must(err)
	if body := call("user_ceo", ceo, "GET", base+"/budget", nil, 200); strings.Contains(string(body), "Draft plan") || strings.Contains(string(body), "Salaries") {
		t.Fatalf("an executive sees the comparison, not draft budget versions: %s", body)
	}
	if body := call("user_staff", []string{"clients:all"}, "GET", base+"/budget", nil, 200); !strings.Contains(string(body), "Draft plan") {
		t.Fatalf("staff still see the budget versions: %s", body)
	}
	for _, path := range []string{base + "/management-pack", base + "/workpapers", base, base + "/export", "/v1/closes", "/v1/closes/attention", "/v1/closes/search?q=rent", base + "/cash-obligations"} {
		call("user_ceo", ceo, "GET", path, nil, 403)
	}
	// The conversation is the executive's, firm-wide only; the firm's agent is off here, so the fixed line answers.
	if shown := string(call("user_ceo", ceo, "POST", "/v1/closes/chat", map[string]any{"text": "show me the payroll workpaper"}, 200)); !strings.Contains(shown, executiveOff) {
		t.Fatalf("an executive's question at off gets the fixed line: %s", shown)
	}
	call("user_ceo", ceo, "POST", base+"/chat", map[string]any{"text": "show me the payroll workpaper"}, 403)
	call("user_ceo", ceo, "GET", "/v1/closes/"+beta+"/management-pack/publications", nil, 404)
	must(json.Unmarshal(call("user_ceo", ceo, "GET", "/v1/closes/executive", nil, 200), &overview))
	if overview.Published[0]["acknowledged"] != true {
		t.Fatalf("the acknowledgment is recorded: %+v", overview.Published)
	}

	staff := []string{"clients:all", "workpapers:review"}
	call("user_staff", staff, "GET", base+"/management-pack", nil, 200)
	if body := call("user_staff", staff, "GET", "/v1/closes/executive", nil, 200); !strings.Contains(string(body), `"executive":false`) {
		t.Fatalf("staff are not executives: %s", body)
	}
}

func TestAnExecutivesAgentStaysAnExecutive(t *testing.T) {
	if got := agentActor(auth.Claims{Permissions: []string{"executive:read", "clients:all", "workpapers:review"}}, "prepare").Permissions; len(got) != 1 || got[0] != "executive:read" {
		t.Fatalf("an executive's agent keeps the executive restriction and nothing more: %v", got)
	}
	if got := agentActor(auth.Claims{Permissions: []string{"clients:all"}}, "prepare").Permissions; len(got) != 1 || got[0] != "workpapers:review" {
		t.Fatalf("a member's preparing agent acts under its grant: %v", got)
	}
}
