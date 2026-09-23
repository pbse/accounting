package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestProcedureOverridesPinAndInvalidate(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org, other := "org_Procedure"+rand.Text(), "org_Other"+rand.Text()
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Procedure test'),($2,'Other test')`, org, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture')`, org); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"preparation_jobs", "workpapers", "evidence"} {
			if _, e := db.Exec(`DELETE FROM `+table+` WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id IN ($1,$2))`, org, other); e != nil {
				t.Error(e)
			}
		}
		db.Exec(`DELETE FROM client_closes WHERE organization_id IN ($1,$2)`, org, other)
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id IN ($1,$2)`, org, other)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id IN ($1,$2)`, org, other)
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, org, other)
	}()
	var id, foreign string
	db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Same client','2026-09') RETURNING id::text`, org).Scan(&id)
	db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Same client','2026-09') RETURNING id::text`, other).Scan(&foreign)
	mux := http.NewServeMux()
	registerProcedures(mux, db)
	h := organizationBoundary(db, mux)
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "workpapers:review", "procedures:manage"}}
	call := func(path string, value any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(value)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(auth.WithActor(r.Context(), actor))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	preview := func(kind string) procedureImpact {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+id+"/procedures/impact?kind="+kind, nil)
		r = r.WithContext(auth.WithActor(r.Context(), actor))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("impact: %d %s", w.Code, w.Body.String())
		}
		var impact procedureImpact
		if json.Unmarshal(w.Body.Bytes(), &impact) != nil {
			t.Fatal("invalid impact preview")
		}
		return impact
	}
	publish := func(client, expense, previous string) string {
		t.Helper()
		request := httptest.NewRequest("GET", "/v1/closes/procedures?impact_kind=prepaid&client_entity="+neturl.QueryEscape(client), nil)
		request = request.WithContext(auth.WithActor(request.Context(), actor))
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("publish impact: %d %s", response.Code, response.Body.String())
		}
		var scope struct {
			ImpactDigest string `json:"impact_digest"`
		}
		if json.Unmarshal(response.Body.Bytes(), &scope) != nil || scope.ImpactDigest == "" {
			t.Fatal("invalid publish impact")
		}
		var v struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call("/v1/closes/procedures", map[string]any{"client_entity": client, "kind": "prepaid", "instructions": "Only supported full-month prepaids; verify invoice booking separately.", "expense_account": expense, "prepaid_account": "Prepaid insurance", "enabled": true, "reason": "Synthetic accountant approval reference", "confirmed": true, "previous_id": previous, "impact_digest": scope.ImpactDigest}, 200), &v)
		return v.ID
	}
	publish("", "Firm insurance", "")
	first := publish("Same client", "Client insurance", "")
	call("/v1/closes/procedures", map[string]any{"client_entity": "Same client", "kind": "prepaid", "instructions": "Stale", "reason": "Stale test", "confirmed": true, "enabled": true}, 409)
	ctx := context.Background()
	tx, _ := db.BeginTx(ctx, nil)
	p, e := preparation.CurrentProcedure(ctx, tx, id, "prepaid")
	if e != nil || p.ID != first {
		t.Fatalf("client override missing: %+v %v", p, e)
	}
	if _, e = preparation.CurrentProcedure(ctx, tx, foreign, "prepaid"); e != sql.ErrNoRows {
		t.Fatal("foreign procedure leaked")
	}
	tx.Rollback()
	var source, job string
	tx, _ = db.BeginTx(ctx, nil)
	if err = tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt','invoice','amount: 1200.00\nservice start: 2026-09-01','fixture') RETURNING id::text`, id).Scan(&source); err != nil {
		t.Fatal(err)
	}
	job, err = preparation.StartIntake(ctx, tx, id, source, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := preparation.IntakeSources(ctx, tx, job)
	if err != nil {
		t.Fatal(err)
	}
	proposed := preparation.Propose(sources)
	found := false
	for _, f := range proposed.Facts {
		if f.Field == "expense_account" && f.Value == "Client insurance" && len(f.Candidates) == 1 {
			found = true
		}
	}
	if !found || len(sources) != 2 {
		t.Fatalf("approved account mapping was not cited: %+v", proposed)
	}
	inventory, _, err := preparation.AgentInventory(ctx, tx, id, job)
	if err != nil {
		t.Fatal(err)
	}
	bound := false
	for _, s := range inventory {
		if s.Role == "procedure" {
			bound = true
		}
	}
	if !bound {
		t.Fatal("agent inventory lost procedure context")
	}
	raw, _ := json.Marshal(map[string]any{"sources": proposed.Sources, "blockers": []string{}})
	var paper string
	if err = tx.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','procedure-test',$2) RETURNING id::text`, id, string(raw)).Scan(&paper); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	second := publish("Same client", "Updated client insurance", first)
	tx, _ = db.BeginTx(ctx, nil)
	latest, _ := preparation.CurrentProcedure(ctx, tx, id, "prepaid")
	if _, err = preparation.AttachProcedure(ctx, tx, id, latest, false, "fixture"); err != nil {
		t.Fatal(err)
	}
	var pinned string
	if err = tx.QueryRow(`SELECT procedure_id::text FROM evidence WHERE close_id=$1 AND procedure_id IS NOT NULL AND superseded_by IS NULL`, id).Scan(&pinned); err != nil || pinned != first {
		t.Fatal("publication silently replaced pinned context")
	}
	tx.Rollback()
	call("/v1/closes/"+id+"/procedures/apply", map[string]any{"kind": "prepaid", "version_id": first, "confirmed": true}, 409)
	impact := preview("prepaid")
	if impact.VersionID != second || impact.SourceID == "" || !bytes.Contains(impact.Affected, []byte(paper)) {
		t.Fatalf("affected review was not previewed: %+v", impact)
	}
	apply := map[string]any{"kind": "prepaid", "version_id": second, "impact_digest": impact.ImpactDigest, "confirmed": true}
	call("/v1/closes/"+id+"/procedures/apply", apply, 200)
	call("/v1/closes/"+id+"/procedures/apply", apply, 409)
	apply["impact_digest"] = preview("prepaid").ImpactDigest
	call("/v1/closes/"+id+"/procedures/apply", apply, 200)
	var state string
	if err = db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE id=$1`, paper).Scan(&state); err != nil || state != "source_changed" {
		t.Fatalf("procedure change retained review: %s %v", state, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM evidence WHERE close_id=$1 AND procedure_id IS NOT NULL`, id).Scan(&count); err != nil || count != 2 {
		t.Fatal("reapply duplicated procedure evidence")
	}
	actor.Permissions = []string{"workpapers:review"}
	call("/v1/closes/procedures", map[string]any{}, 403)
}
