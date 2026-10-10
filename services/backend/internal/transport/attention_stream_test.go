package transport

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// The attention stream says what is unread when it opens, keeps the connection alive, says again when that changes,
// and ends when its time is up.
func TestAttentionStreamFollowsTheInbox(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_Stream" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Stream fixture')`, org); err != nil {
		t.Fatal(err)
	}
	// The stream checks firm access on every read, as a request does on entry: the member is enabled.
	if _, err := db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM attention_marks WHERE organization_id=$1`, `DELETE FROM attention_preferences WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	every, long := attentionStreamEvery, attentionStreamFor
	attentionStreamEvery, attentionStreamFor = 20*time.Millisecond, 400*time.Millisecond
	defer func() { attentionStreamEvery, attentionStreamFor = every, long }()
	mux := http.NewServeMux()
	registerAttention(mux, db, db)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", "/v1/closes/attention/stream", nil).WithContext(auth.WithActor(ctx, &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org}))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, r)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream did not end at its deadline")
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type %q", ct)
	}
	var kinds []string
	ended := false
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("line %q: %v", scanner.Text(), err)
		}
		if ended {
			t.Fatalf("a line after the ending: %v", line)
		}
		if line["ended"] == true {
			ended = true
			continue
		}
		kinds = append(kinds, line["type"].(string))
		if line["type"] == "attention" {
			if _, ok := line["unread"].(float64); !ok {
				t.Fatalf("attention line without a count: %v", line)
			}
		}
	}
	if len(kinds) < 2 || kinds[0] != "attention" {
		t.Fatalf("the stream should open with the inbox and keep ticking, got %v", kinds)
	}
	if !ended {
		t.Fatal("the stream should end with its final document")
	}
	for _, k := range kinds[1:] {
		if k != "tick" && k != "attention" {
			t.Fatalf("unexpected line %q in %v", k, kinds)
		}
	}
}
