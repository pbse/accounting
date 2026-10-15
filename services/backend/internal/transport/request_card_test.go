package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// The chat's request card drafts the close's client request through the same route as the Requests tab, so it is the
// request the goal would draft: a document's client question is asked, and a reviewer's note stays with the firm.
func TestAssistantRequestCardDraftsTheClosesRequest(t *testing.T) {
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
	org := "org_RequestCard" + rand.Text()
	var alpha, empty string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Request card fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture'),($1,'user_a','Quiet',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Quiet','2026-09') RETURNING id::text`, org).Scan(&empty))
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1)`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	// A document whose reading asks the client something, and a draft a reviewer returned with an internal note.
	var source, job, paper string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'insurance invoice.pdf',$2,'Invoice','fixture') RETURNING id::text`, alpha, rand.Text()).Scan(&source))
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, alpha, rand.Text()).Scan(&job))
	_, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,'{"kind":"prepaid","questions":["Please send the signed policy schedule showing the coverage dates."]}')`, job, source)
	must(err)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','rc-returned','{"input":{"source_reference":"Rent"},"sources":[],"blockers":[]}') RETURNING id::text`, alpha).Scan(&paper))
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'rc-returned','changes_requested','Our partner thinks the rent was double-booked','fixture')`, paper)
	must(err)
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type card struct {
		ID        string          `json:"id"`
		Action    string          `json:"action"`
		Outcome   json.RawMessage `json:"outcome"`
		Citations []chatCitation  `json:"citations"`
	}
	ask := func(close, text string) card {
		t.Helper()
		var out struct {
			Messages []card `json:"messages"`
		}
		must(json.Unmarshal(call("POST", "/v1/closes/"+close+"/chat", map[string]string{"text": text}, 200), &out))
		return out.Messages[len(out.Messages)-1]
	}
	confirm := func(close, id string) (outcome struct {
		DraftID   string   `json:"draft_id"`
		Questions int      `json:"questions"`
		Kept      []string `json:"kept"`
	}) {
		t.Helper()
		var confirmed struct {
			Messages []card `json:"messages"`
		}
		must(json.Unmarshal(call("POST", "/v1/closes/"+close+"/chat/"+id+"/confirm", map[string]string{}, 200), &confirmed))
		must(json.Unmarshal(confirmed.Messages[len(confirmed.Messages)-1].Outcome, &outcome))
		return
	}

	if c := ask(alpha, "Draft the close"); c.Action != "" {
		t.Fatalf("a draft phrase that names no request is not a card: %+v", c)
	}
	first := ask(alpha, "Draft a client request")
	if first.Action != "request" || len(first.Citations) != 1 || first.Citations[0].Href != "/dashboard/closes/"+alpha+"?tab=requests" {
		t.Fatalf("a request phrase answers with a card linked to Requests: %+v", first)
	}
	outcome := confirm(alpha, first.ID)
	var proposed struct {
		Proposal *struct {
			ID        string
			Questions []proposedQuestion
		}
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+alpha+"/requests/proposed", nil, 200), &proposed))
	if p := proposed.Proposal; p == nil || outcome.DraftID != p.ID || outcome.Questions != 1 || len(p.Questions) != 1 || !strings.Contains(p.Questions[0].Question, "signed policy schedule") {
		t.Fatalf("confirming drafts the close's request, the one Requests shows: %+v %+v", outcome, proposed.Proposal)
	}
	var body string
	must(db.QueryRow(`SELECT body FROM request_drafts WHERE id::text=$1`, outcome.DraftID).Scan(&body))
	if strings.Contains(body, "double-booked") || strings.Contains(body, "Rent") {
		t.Fatalf("a reviewer's note stays with the firm: %q", body)
	}
	call("POST", "/v1/closes/"+alpha+"/chat/"+first.ID+"/confirm", map[string]string{}, 409)
	// Asked again, the card finds the same draft.
	if again := confirm(alpha, ask(alpha, "draft the request").ID); again.DraftID != outcome.DraftID {
		t.Fatalf("the same request again is the same draft: %+v %+v", again, outcome)
	}
	if quiet := confirm(empty, ask(empty, "Draft a client request").ID); quiet.DraftID != "" || quiet.Questions != 0 || quiet.Kept == nil {
		t.Fatalf("a close with nothing for the client drafts nothing and says so: %+v", quiet)
	}
}
