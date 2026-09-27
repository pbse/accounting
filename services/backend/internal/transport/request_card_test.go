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

func TestAssistantRequestCardSavesTheWordingItShowed(t *testing.T) {
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
	var alpha, empty, unsourced, clean, older, revision string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Request card fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture'),($1,'user_a','Quiet',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Quiet','2026-09') RETURNING id::text`, org).Scan(&empty))
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	paper := func(close, digest, body string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, close, digest, body).Scan(&id))
		return id
	}
	var evidence string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt','rc-source','Invoice','fixture') RETURNING id::text`, alpha).Scan(&evidence))
	sourced := `"sources":[{"id":"` + evidence + `","role":"invoice","digest":"rc-source"}]`
	unsourced = paper(alpha, "rc-1", `{"input":{"source_reference":"Insurance invoice"},"sources":[],"blockers":[]}`)
	clean = paper(alpha, "rc-2", `{"input":{"source_reference":"Rent"},`+sourced+`,"blockers":[]}`)
	older = paper(alpha, "rc-3", `{"input":{"source_reference":"Loan"},"sources":[],"blockers":[]}`)
	revision = paper(alpha, "rc-4", `{"revises":"`+older+`","input":{"source_reference":"Loan"},`+sourced+`,"blockers":["Confirm the interest rate."]}`)
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
	type candidates struct {
		Workpapers []struct{ ID string } `json:"workpapers"`
		Body       string                `json:"body"`
		Digest     string                `json:"digest"`
	}
	read := func(close string) (c candidates) {
		t.Helper()
		must(json.Unmarshal(call("GET", "/v1/closes/"+close+"/requests/candidates", nil, 200), &c))
		return
	}

	if c := ask(alpha, "Draft the close"); c.Action != "" {
		t.Fatalf("a draft phrase that names no request is not a card: %+v", c)
	}
	first := ask(alpha, "Draft a client request")
	if first.Action != "request" || len(first.Citations) != 1 || first.Citations[0].Href != "/dashboard/closes/"+alpha+"?tab=requests" {
		t.Fatalf("a request phrase answers with a card linked to Requests: %+v", first)
	}
	shown := read(alpha)
	if len(shown.Workpapers) != 2 || shown.Workpapers[0].ID != unsourced || shown.Workpapers[1].ID != revision || !strings.Contains(shown.Body, "Please provide the source documents for Insurance invoice") || !strings.Contains(shown.Body, "Confirm the interest rate.") || strings.Contains(shown.Body, "Rent") {
		t.Fatalf("the card lists only current drafts a request would ask about, and the wording it would save: %+v", shown)
	}
	_ = clean
	call("POST", "/v1/closes/"+alpha+"/chat/"+first.ID+"/confirm", map[string]string{"digest": "stale"}, 409)
	var confirmed struct {
		Messages []card `json:"messages"`
	}
	must(json.Unmarshal(call("POST", "/v1/closes/"+alpha+"/chat/"+first.ID+"/confirm", map[string]string{"digest": shown.Digest}, 200), &confirmed))
	var outcome struct {
		DraftID string `json:"draft_id"`
		Created bool   `json:"created"`
	}
	json.Unmarshal(confirmed.Messages[len(confirmed.Messages)-1].Outcome, &outcome)
	var saved, body string
	must(db.QueryRow(`SELECT id::text,body FROM request_drafts WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, alpha).Scan(&saved, &body))
	if outcome.DraftID != saved || !outcome.Created || body != strings.TrimSpace(shown.Body) {
		t.Fatalf("confirming saves the wording the card showed and records the draft: %+v %s", outcome, saved)
	}
	call("POST", "/v1/closes/"+alpha+"/chat/"+first.ID+"/confirm", map[string]string{"digest": shown.Digest}, 409)
	second := ask(alpha, "draft the request")
	must(json.Unmarshal(call("POST", "/v1/closes/"+alpha+"/chat/"+second.ID+"/confirm", map[string]string{"digest": read(alpha).Digest}, 200), &confirmed))
	json.Unmarshal(confirmed.Messages[len(confirmed.Messages)-1].Outcome, &outcome)
	var drafts int
	must(db.QueryRow(`SELECT count(*) FROM request_drafts WHERE close_id::text=$1`, alpha).Scan(&drafts))
	if outcome.DraftID != saved || outcome.Created || drafts != 1 {
		t.Fatalf("the same wording again finds the saved draft and says so: %+v %d", outcome, drafts)
	}
	quiet := ask(empty, "Draft a client request")
	if c := read(empty); len(c.Workpapers) != 0 {
		t.Fatalf("a close with no draft to ask about lists none: %+v", c)
	}
	call("POST", "/v1/closes/"+empty+"/chat/"+quiet.ID+"/confirm", map[string]string{"digest": read(empty).Digest}, 409)
}
