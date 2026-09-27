package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// workosPages answers WorkOS list requests by path and cursor.
type workosPages struct {
	next  http.RoundTripper
	pages map[string]string
}

func (w workosPages) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.workos.com" {
		return w.next.RoundTrip(r)
	}
	body, ok := w.pages[r.URL.Path+"?after="+r.URL.Query().Get("after")]
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
}

func TestHandADocumentToATeammate(t *testing.T) {
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
	ctx := context.Background()
	org := "org_Hand" + rand.Text()
	// Partners review every client; reviewers only those they are assigned to; viewers review nothing. Cy is a viewer
	// who also holds the reviewer role.
	t.Setenv("WORKOS_API_KEY", "sk_test_synthetic")
	original := http.DefaultTransport
	http.DefaultTransport = workosPages{next: original, pages: map[string]string{
		"/authorization/organizations/" + org + "/roles?after=": `{"data":[{"slug":"partner","permissions":["workpapers:review","clients:all"]},{"slug":"reviewer","permissions":["workpapers:review"]},{"slug":"viewer","permissions":[]}]}`,
		"/user_management/organization_memberships?after=":      `{"data":[{"user_id":"user_a","status":"active","role":{"slug":"partner"}},{"user_id":"user_b","status":"active","role":{"slug":"partner"},"user":{"first_name":"Bea","last_name":"Brown","email":"bea@firm.test"}},{"user_id":"user_c","status":"active","role":{"slug":"viewer"},"roles":[{"slug":"viewer"},{"slug":"reviewer"}],"user":{"first_name":"","last_name":"","email":"cy@firm.test"}}],"list_metadata":{"after":"om_3"}}`,
		"/user_management/organization_memberships?after=om_3":  `{"data":[{"user_id":"user_d","status":"active","role":{"slug":"viewer"}},{"user_id":"user_e","status":"active","role":{"slug":"reviewer"}},{"user_id":"user_f","status":"active","role":{"slug":"partner"}}],"list_metadata":{"after":null}}`,
	}}
	defer func() { http.DefaultTransport = original }()
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Hand-off fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,s<>'user_f','Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b','user_c','user_d','user_e','user_f']) s`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Alpha',true,'Synthetic','fixture'),($1,'user_d','Alpha',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	var alpha string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	// Each document has a fact the agent could not read, so it needs a person.
	document := func(name string) string {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		must(err)
		defer tx.Rollback()
		var source string
		must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, alpha, name, preparation.Digest([]byte(name)), "Invoice "+name).Scan(&source))
		job, err := preparation.StartIntake(ctx, tx, alpha, source, "fixture")
		must(err)
		must(tx.Commit())
		_, err = db.Exec(`UPDATE preparation_intakes SET proposal='{"kind":"prepaid","questions":["Which months does the policy cover?"],"facts":[]}' WHERE job_id::text=$1`, job)
		must(err)
		_, err = db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`, job)
		must(err)
		return job
	}
	invoice, lease := document("invoice.txt"), document("lease.txt")
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	partner, reviewer := []string{"workpapers:review", "clients:all"}, []string{"workpapers:review"}
	hand := func(job, to string, want int) string {
		t.Helper()
		return string(call("user_a", partner, "POST", "/v1/closes/"+alpha+"/goal/handoffs", map[string]string{"job_id": job, "assignee": to, "note": "Check the service dates with the client"}, want))
	}
	type status struct {
		Goal *struct {
			ID     string `json:"id"`
			Events []struct {
				Text string `json:"text"`
			} `json:"events"`
		} `json:"goal"`
		Handoffs []goalHandoff `json:"handoffs"`
	}
	read := func(subject string, permissions []string) status {
		t.Helper()
		var s status
		must(json.Unmarshal(call(subject, permissions, "GET", "/v1/closes/"+alpha+"/goal", nil, 200), &s))
		return s
	}
	last := func() string {
		s := read("user_a", partner)
		return s.Goal.Events[len(s.Goal.Events)-1].Text
	}
	handed := func(subject string, permissions []string) []string {
		t.Helper()
		var inbox struct {
			Groups []struct {
				Events []struct {
					Kind  string `json:"kind"`
					Text  string `json:"text"`
					JobID string `json:"job_id"`
				} `json:"events"`
			} `json:"groups"`
		}
		must(json.Unmarshal(call(subject, permissions, "GET", "/v1/closes/attention", nil, 200), &inbox))
		out := []string{}
		for _, g := range inbox.Groups {
			for _, e := range g.Events {
				if e.Kind == "handoff" {
					out = append(out, e.JobID+" "+e.Text)
				}
			}
		}
		return out
	}

	// Only teammates who can review Alpha's work are offered: not the requester, a viewer even when assigned, a reviewer
	// without the assignment, or a member the firm removed.
	if body := hand(invoice, "user_b", 409); !strings.Contains(body, "Hand the close to the agent first") {
		t.Fatalf("a document is handed as part of a delegated close: %s", body)
	}
	call("user_a", partner, "POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true}, 200)
	goal := read("user_a", partner).Goal.ID
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, alpha)
	must(err)
	if body := hand(invoice, "user_b", 409); !strings.Contains(body, "signed off") {
		t.Fatalf("a signed-off close is reopened before work is handed out: %s", body)
	}
	_, err = db.Exec(`DELETE FROM close_signoffs WHERE close_id::text=$1`, alpha)
	must(err)
	var people struct {
		People []teammate `json:"people"`
	}
	must(json.Unmarshal(call("user_a", partner, "GET", "/v1/closes/"+alpha+"/people", nil, 200), &people))
	listed := []string{}
	for _, p := range people.People {
		listed = append(listed, p.Subject+"="+p.Name)
	}
	if strings.Join(listed, ",") != "user_b=Bea Brown,user_c=cy@firm.test" {
		t.Fatalf("teammates: %v", listed)
	}
	hand(invoice, "user_e", 422)

	// A handed document shows who has it and reaches only their attention list, linked to the document.
	hand(invoice, "user_c", 200)
	if body := hand(invoice, "user_b", 409); !strings.Contains(body, "cy@firm.test already has invoice.txt") {
		t.Fatalf("a document is with one teammate at a time: %s", body)
	}
	if s := read("user_c", reviewer); len(s.Handoffs) != 1 || s.Handoffs[0].JobID != invoice || !s.Handoffs[0].Mine || s.Handoffs[0].Name != "cy@firm.test" {
		t.Fatalf("the close shows the hand-off to its teammate: %+v", s.Handoffs)
	}
	if got := handed("user_c", reviewer); len(got) != 1 || got[0] != invoice+" user_a handed you invoice.txt: Check the service dates with the client" {
		t.Fatalf("the teammate is told: %v", got)
	}
	if got := handed("user_b", partner); len(got) != 0 {
		t.Fatalf("nobody else is told: %v", got)
	}

	// Losing the assignment to the client ends the hand-off.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Alpha',false,'Moved to another client','fixture')`, org)
	must(err)
	must(stepGoal(ctx, db, goal))
	if text := last(); text != "cy@firm.test can no longer act for this client, so I took invoice.txt back." || len(read("user_a", partner).Handoffs) != 0 {
		t.Fatalf("a lost assignment ends the hand-off: %q", text)
	}

	// Once the document is prepared nobody needs to look into it, so the agent closes the hand-off.
	hand(invoice, "user_b", 200)
	var paper string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,'{}') RETURNING id::text`, alpha, "handoff-"+rand.Text()).Scan(&paper))
	_, err = db.Exec(`UPDATE preparation_jobs SET state='completed',workpaper_ids=jsonb_build_array($2::text),finished_at=now() WHERE id::text=$1`, invoice, paper)
	must(err)
	must(stepGoal(ctx, db, goal))
	if text := last(); text != "invoice.txt no longer needs anyone, so Bea Brown's hand-off is closed." {
		t.Fatalf("the agent closes a resolved hand-off: %q", text)
	}
	hand(invoice, "user_b", 422)

	// The teammate can hand it back; stopping the agent closes what is still handed.
	hand(lease, "user_b", 200)
	id := read("user_a", partner).Handoffs[0].ID
	call("user_b", partner, "POST", "/v1/closes/"+alpha+"/goal/handoffs/"+id+"/take-back", nil, 200)
	call("user_b", partner, "POST", "/v1/closes/"+alpha+"/goal/handoffs/"+id+"/take-back", nil, 409)
	if text := last(); text != "Bea Brown handed lease.txt back." {
		t.Fatalf("handing back is recorded: %q", text)
	}
	hand(lease, "user_b", 200)
	call("user_a", partner, "POST", "/v1/closes/"+alpha+"/goal/stop", map[string]string{"reason": "Doing it myself"}, 200)
	var open int
	must(db.QueryRow(`SELECT count(*) FROM goal_handoffs WHERE goal_id::text=$1 AND closed_at IS NULL`, goal).Scan(&open))
	if open != 0 {
		t.Fatal("stopping the agent closes its hand-offs")
	}

	// Without WorkOS a hosted firm offers nobody rather than an unchecked list.
	t.Setenv("WORKOS_API_KEY", "")
	call("user_a", partner, "GET", "/v1/closes/"+alpha+"/people", nil, 502)
}
