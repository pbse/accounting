package delivery

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPlatformEmailsSay(t *testing.T) {
	welcome, err := platformEmail("welcome", "ana@acme.test", "Acme\nAdvisors\t LLP", "acme.test", "Krith <hello@mail.krith.test>", "https://app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if welcome.Subject != "Welcome to Krith" || welcome.From != "Krith <hello@mail.krith.test>" || len(welcome.To) != 1 || welcome.To[0] != "ana@acme.test" {
		t.Fatalf("the welcome goes to the person from Krith's sender: %+v", welcome)
	}
	for _, want := range []string{"Acme Advisors LLP is set up on Krith, and you are its administrator.", "https://app.example.test/dashboard", "their acme.test address", "https://app.example.test/contact"} {
		if !strings.Contains(welcome.Text, want) {
			t.Fatalf("the welcome says %q:\n%s", want, welcome.Text)
		}
	}
	joined, err := platformEmail("joined", "ben@acme.test", "Acme Advisors", "", "Krith <hello@mail.krith.test>", "https://app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if joined.Subject != "You’ve joined Acme Advisors on Krith" || !strings.Contains(joined.Text, "with your acme.test address") || !strings.Contains(joined.Text, "You can’t open any clients yet.") {
		t.Fatalf("the colleague's note names the firm and says they have no access yet: %+v", joined)
	}
	for _, bad := range [][2]string{{"welcome", "Ana <ana@acme.test>"}, {"welcome", "ana@acme.test\r\nBcc: x@y.test"}, {"invoice", "ana@acme.test"}} {
		if _, err := platformEmail(bad[0], bad[1], "Acme", "acme.test", "Krith <hello@mail.krith.test>", "https://app.example.test"); err == nil {
			t.Fatalf("refused: %q", bad)
		}
	}
}

func TestPlatformEmailsAreSentOnceWithKrithsKey(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	suffix := strings.ToLower(rand.Text()[:8])
	org, domain := "org_PlatformTest"+suffix, "acme"+suffix+".test"
	if _, err := db.Exec(`INSERT INTO organizations(id,name,email_domain) VALUES($1,'Acme Advisors',$2)`, org, domain); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	})
	member := func(who string, granted bool) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,$3,'test','test')`, org, "user_"+who+suffix, granted); err != nil {
			t.Fatal(err)
		}
	}
	// Each person joined the firm by signing in, which queued their email.
	queue := func(kind, who string) (id string) {
		t.Helper()
		member(who, true)
		if err := db.QueryRow(`INSERT INTO platform_emails(kind,organization_id,subject,recipient) VALUES($1,$2,$3,$4) RETURNING id::text`, kind, org, "user_"+who+suffix, who+"@"+domain).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ana, ben, cy, eve, fay := queue("welcome", "ana"), queue("joined", "ben"), queue("joined", "cy"), queue("joined", "eve"), queue("joined", "fay")

	// Resend answers Ana at once and Ben only on the second try; it refuses Cy's, answers Eve's first try without its
	// message ID, and never gives Fay's an ID.
	var mu sync.Mutex
	seen := map[string][]http.Header{}
	bodies := map[string]Email{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var email Email
		json.Unmarshal(raw, &email)
		if len(email.To) != 1 || !strings.HasSuffix(email.To[0], "@"+domain) {
			// Another test's email: accepted, and not this test's concern.
			w.Write([]byte(`{"id":"re_other"}`))
			return
		}
		who, _, _ := strings.Cut(email.To[0], "@")
		mu.Lock()
		seen[who] = append(seen[who], r.Header.Clone())
		bodies[who] = email
		tries := len(seen[who])
		mu.Unlock()
		switch {
		case who == "cy":
			w.WriteHeader(422)
			w.Write([]byte(`{"message":"invalid"}`))
		case who == "ben" && tries == 1:
			w.WriteHeader(500)
		case who == "eve" && tries == 1, who == "fay":
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{"id":"re_` + who + `"}`))
		}
	}))
	t.Cleanup(fake.Close)
	original := resendAPI
	resendAPI = fake.URL
	t.Cleanup(func() { resendAPI = original })

	state := func(id string) (s string, attempts int, provider string) {
		t.Helper()
		if err := db.QueryRow(`SELECT state,attempts,COALESCE(provider_id,'') FROM platform_emails WHERE id=$1`, id).Scan(&s, &attempts, &provider); err != nil {
			t.Fatal(err)
		}
		return
	}
	drain := func() {
		t.Helper()
		for range 20 {
			sent, err := sendPlatformEmail(ctx, db, "re_platform_key", "Krith <hello@mail.krith.test>", "https://app.example.test")
			if err != nil {
				t.Fatal(err)
			}
			if !sent {
				return
			}
		}
	}
	drain()
	if s, n, p := state(ana); s != "sent" || n != 1 || p != "re_ana" {
		t.Fatalf("Ana's welcome is sent once: %s %d %q", s, n, p)
	}
	h := seen["ana"][0]
	if h.Get("Authorization") != "Bearer re_platform_key" || h.Get("Idempotency-Key") != "krith-platform/"+ana {
		t.Fatalf("sent with Krith's key under the email's own idempotency key: %v", h)
	}
	if e := bodies["ana"]; e.Subject != "Welcome to Krith" || e.From != "Krith <hello@mail.krith.test>" || !strings.Contains(e.Text, "Acme Advisors is set up on Krith") || !strings.Contains(e.Text, "their "+domain+" address") {
		t.Fatalf("the welcome names the firm and its domain: %+v", e)
	}
	if s, n, _ := state(ben); s != "queued" || n != 1 {
		t.Fatalf("an unanswered attempt waits to be tried again: %s %d", s, n)
	}
	if s, n, _ := state(cy); s != "failed" || n != 1 {
		t.Fatalf("a refusal is final: %s %d", s, n)
	}
	if s, n, _ := state(eve); s != "queued" || n != 1 {
		t.Fatalf("a success without Resend's message ID is uncertain, so it waits to be tried again: %s %d", s, n)
	}

	// Ben's retry, once due, goes under the same idempotency key, so Resend sends it at most once.
	if _, err := db.Exec(`UPDATE platform_emails SET next_attempt_at=now() WHERE id IN ($1,$2,$3)`, ben, eve, fay); err != nil {
		t.Fatal(err)
	}
	drain()
	if s, n, p := state(ben); s != "sent" || n != 2 || p != "re_ben" || len(seen["ben"]) != 2 || seen["ben"][1].Get("Idempotency-Key") != seen["ben"][0].Get("Idempotency-Key") {
		t.Fatalf("Ben's note is sent on the retry under the same key: %s %d %q %d", s, n, p, len(seen["ben"]))
	}
	if len(seen["cy"]) != 1 {
		t.Fatalf("Cy's refused email is not tried again: %d", len(seen["cy"]))
	}
	if s, _, p := state(eve); s != "sent" || p != "re_eve" || seen["eve"][1].Get("Idempotency-Key") != seen["eve"][0].Get("Idempotency-Key") {
		t.Fatalf("Eve's retry, under the same key, confirms it: %s %q", s, p)
	}
	// Still uncertain after three tries, Fay's is recorded as unknown, not failed: Resend may have sent it.
	for range 2 {
		if _, err := db.Exec(`UPDATE platform_emails SET next_attempt_at=now() WHERE id=$1`, fay); err != nil {
			t.Fatal(err)
		}
		drain()
	}
	if s, n, _ := state(fay); s != "unknown" || n != 3 || len(seen["fay"]) != 3 {
		t.Fatalf("an attempt still unconfirmed after three tries is unknown: %s %d %d", s, n, len(seen["fay"]))
	}

	// Nothing goes to someone the firm removed before their email went out, or to anyone at a suspended firm.
	gus := queue("joined", "gus")
	member("gus", false)
	drain()
	if s, n, _ := state(gus); s != "cancelled" || n != 0 || len(seen["gus"]) != 0 {
		t.Fatalf("a removed person's email is cancelled unsent: %s %d", s, n)
	}
	ivy := queue("joined", "ivy")
	if _, err := db.Exec(`UPDATE organizations SET enabled=false WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	drain()
	if _, err := db.Exec(`UPDATE organizations SET enabled=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	if s, n, _ := state(ivy); s != "cancelled" || n != 0 || len(seen["ivy"]) != 0 {
		t.Fatalf("an email from a suspended firm is cancelled unsent: %s %d", s, n)
	}

	// What waited more than a day is not sent; finished rows go after 30 days.
	old := queue("joined", "dee")
	if _, err := db.Exec(`UPDATE platform_emails SET created_at=now()-interval '24 hours' WHERE id=$1`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_emails SET finished_at=now()-interval '31 days' WHERE id=$1`, ana); err != nil {
		t.Fatal(err)
	}
	if err := tidyPlatformEmails(ctx, db); err != nil {
		t.Fatal(err)
	}
	drain()
	if s, n, _ := state(old); s != "expired" || n != 0 || len(seen["dee"]) != 0 {
		t.Fatalf("an email queued over a day ago expires unsent: %s %d", s, n)
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM platform_emails WHERE id=$1`, ana).Scan(&left); err != nil || left != 0 {
		t.Fatalf("a row finished over 30 days ago is deleted: %d %v", left, err)
	}
}

func TestPlatformEmailsNeedKrithsSender(t *testing.T) {
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test/")
	for _, c := range []struct{ key, from string }{{"", "Krith <hello@mail.krith.test>"}, {"re_key", ""}, {"re_key", "not an address"}, {"re_key", "a@b.test\r\nBcc: c@d.test"}} {
		t.Setenv("PLATFORM_EMAIL_KEY", c.key)
		t.Setenv("PLATFORM_EMAIL_FROM", c.from)
		if _, _, _, ok := platformSender(); ok {
			t.Fatalf("off without a key and a valid sender: %+v", c)
		}
	}
	t.Setenv("PLATFORM_EMAIL_KEY", "re_key")
	t.Setenv("PLATFORM_EMAIL_FROM", "Krith <hello@mail.krith.test>")
	if key, from, app, ok := platformSender(); !ok || key != "re_key" || from != "Krith <hello@mail.krith.test>" || app != "https://app.example.test" {
		t.Fatalf("on with both: %q %q %q %v", key, from, app, ok)
	}
}
