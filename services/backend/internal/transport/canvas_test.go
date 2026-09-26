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
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestReviewCanvasShowsChangesSinceYourReviewAndBlocksOnOpenComments(t *testing.T) {
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
	org := "org_Canvas" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Canvas fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`, org)
	must(err)
	var closeID, evidence string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Canvas','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1)`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `UPDATE evidence SET superseded_by=NULL WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	content := "Invoice\nAmount: 150.00\nService: September"
	digest := preparation.Digest([]byte(content))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, digest, content).Scan(&evidence))
	draft := func(amount, revises string) string {
		t.Helper()
		paper := map[string]any{"input": map[string]string{"amount": amount}, "sources": []map[string]string{{"id": evidence, "name": "invoice.txt", "digest": digest, "role": "invoice", "location": "line 2"}}}
		if revises != "" {
			paper["revises"] = revises
		}
		raw, _ := json.Marshal(paper)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, "canvas-"+amount, string(raw)).Scan(&id))
		return id
	}
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: []string{"clients:all", "workpapers:review"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	paper := func(id string) string { return "/v1/closes/" + closeID + "/workpapers/" + id }
	decide := func(id, decision string, want int) []byte {
		t.Helper()
		var state struct {
			Digest   string `json:"digest"`
			ReviewID string `json:"review_id"`
		}
		must(json.Unmarshal(call("user_a", "GET", paper(id)+"/reviews", nil, 200), &state))
		return call("user_a", "POST", paper(id)+"/reviews", map[string]string{"decision": decision, "note": "Synthetic review of the amount", "digest": state.Digest, "expected_review_id": state.ReviewID}, want)
	}
	first := draft("100.00", "")
	decide(first, "changes_requested", 200)
	second := draft("120.00", first)
	current := draft("150.00", second)
	type since struct {
		LastReview *struct {
			WorkpaperID string `json:"workpaper_id"`
			Decision    string
		} `json:"last_review"`
		Changes []paperChange
		Reasons []string
	}
	var mine since
	must(json.Unmarshal(call("user_a", "GET", paper(current)+"/since-review", nil, 200), &mine))
	changed := false
	for _, c := range mine.Changes {
		changed = changed || strings.Contains(c.Field, "amount") && strings.Contains(c.Before, "100.00") && strings.Contains(c.After, "150.00")
	}
	if mine.LastReview == nil || mine.LastReview.WorkpaperID != first || mine.LastReview.Decision != "changes_requested" || !changed || len(mine.Reasons) != 1 || mine.Reasons[0] != "Revised since your review: draft #"+first+" → #"+second+" → #"+current+"." {
		t.Fatalf("the member's review two drafts back, what changed and why it reopened: %+v", mine)
	}
	var theirs since
	must(json.Unmarshal(call("user_b", "GET", paper(current)+"/since-review", nil, 200), &theirs))
	if theirs.LastReview != nil || len(theirs.Changes) != 0 {
		t.Fatalf("another member has no review to compare with: %+v", theirs)
	}
	comment := func(id, anchor string, want int) {
		t.Helper()
		call("user_b", "POST", paper(id)+"/comments", map[string]string{"action": "add", "anchor": anchor, "body": "Is the service period right?"}, want)
	}
	comment(second, "general", 409)
	comment(current, "general", 200)
	comment(current, "field:amount", 200)
	comment(current, "field:nope", 422)
	comment(current, "source:"+evidence+":line:2", 200)
	comment(current, "source:"+evidence+":line:9", 422)
	comment(current, "source:1:line:1", 422)
	var unlinked string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'other.txt','canvas-other','Other','fixture') RETURNING id::text`, closeID).Scan(&unlinked))
	comment(current, "source:"+unlinked+":line:1", 422)
	_, err = db.Exec(`INSERT INTO workpaper_comments(workpaper_id,anchor,anchor_label,body,author) VALUES($1,'general','The whole draft','Raised on the earlier draft','user_b')`, second)
	must(err)
	var listed struct {
		Comments []canvasComment
	}
	must(json.Unmarshal(call("user_a", "GET", paper(current)+"/comments", nil, 200), &listed))
	labels := []string{}
	for _, c := range listed.Comments {
		labels = append(labels, c.AnchorLabel+"@"+c.WorkpaperID)
	}
	if strings.Join(labels, "|") != "The whole draft@"+current+"|Input: amount@"+current+"|invoice.txt, line 2@"+current+"|The whole draft@"+second {
		t.Fatalf("the draft's comments and its predecessor's, each with what it is about: %v", labels)
	}
	if body := decide(current, "approved", 409); !strings.Contains(string(body), "4 open comments") {
		t.Fatalf("open comments, including one carried from the earlier draft, keep the draft from approval: %s", body)
	}
	for _, c := range listed.Comments {
		call("user_a", "POST", paper(current)+"/comments", map[string]string{"action": "resolve", "id": c.ID, "resolution": "Checked against the invoice"}, 200)
	}
	call("user_a", "POST", paper(current)+"/comments", map[string]string{"action": "resolve", "id": listed.Comments[0].ID, "resolution": "Again"}, 409)
	decide(current, "approved", 200)
	comment(current, "general", 409)
	var replacement string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice-v2.txt','canvas-v2','Invoice v2','fixture') RETURNING id::text`, closeID).Scan(&replacement))
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$1,replacement_reason='Vendor reissued the invoice' WHERE id::text=$2`, replacement, evidence)
	must(err)
	must(json.Unmarshal(call("user_a", "GET", paper(current)+"/since-review", nil, 200), &mine))
	if len(mine.Reasons) != 1 || mine.Reasons[0] != "Source invoice.txt was replaced by #"+replacement+": Vendor reissued the invoice" || len(mine.Changes) != 0 {
		t.Fatalf("a replaced source is why an approved draft reopened: %+v", mine)
	}
}
