package documents

import (
	"close.local/backend/internal/llm"
	"crypto/rand"
	"database/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"os"
	"strings"
	"testing"
)

func TestDocumentImageEligibilityUsesCurrentOwnerAndClientGrants(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org, owner := "org_OCR"+rand.Text(), "user_ocr_owner"
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Image route test')`, org)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"client_access", "organization_member_access", "organizations"} {
			column := "organization_id"
			if table == "organizations" {
				column = "id"
			}
			db.Exec("DELETE FROM "+table+" WHERE "+column+"=$1", org)
		}
	}()
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'OCR test','user_admin')`, org, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,'Alpha',true,'OCR test','user_admin')`, org, owner)
	if err != nil {
		t.Fatal(err)
	}
	client, err := llm.New(llm.Config{Provider: "context_machine", ContextMachineToken: strings.Repeat("x", 32), ContextMachineModel: "codex", CodexModel: "gpt-6.1-sol", Profiles: []llm.Scope{{Organization: org, Subject: owner, AccessProfile: "own"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := llm.WithIdentity(llm.WithClient(t.Context(), client), org, owner)
	if !pageReadingAllowed(ctx, db, org, "Alpha") || pageReadingAllowed(ctx, db, org, "Beta") || pageReadingAllowed(llm.WithIdentity(ctx, org, "user_other"), db, org, "Alpha") || pageReadingAllowed(llm.WithIdentity(ctx, "org_other", owner), db, org, "Alpha") {
		t.Fatal("wrong owner/client eligibility")
	}
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,'Alpha',false,'Revoked for OCR test','user_admin')`, org, owner)
	if err != nil {
		t.Fatal(err)
	}
	if pageReadingAllowed(ctx, db, org, "Alpha") {
		t.Fatal("revoked client grant sent image")
	}
}
