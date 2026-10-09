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
	"time"

	"close.local/backend/internal/secrets"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPlatformEmailsSay(t *testing.T) {
	welcome, err := platformEmail("welcome", "ana@acme.test", "Acme\nAdvisors\t LLP", "acme.test", "Krith <hello@mail.krith.test>", "https://app.example.test", invitation{})
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
	joined, err := platformEmail("joined", "ben@acme.test", "Acme Advisors", "", "Krith <hello@mail.krith.test>", "https://app.example.test", invitation{})
	if err != nil {
		t.Fatal(err)
	}
	if joined.Subject != "You’ve joined Acme Advisors on Krith" || !strings.Contains(joined.Text, "with your acme.test address") || !strings.Contains(joined.Text, "You can’t open any clients yet.") {
		t.Fatalf("the colleague's note names the firm and says they have no access yet: %+v", joined)
	}
	for _, bad := range [][2]string{{"welcome", "Ana <ana@acme.test>"}, {"welcome", "ana@acme.test\r\nBcc: x@y.test"}, {"invoice", "ana@acme.test"}} {
		if _, err := platformEmail(bad[0], bad[1], "Acme", "acme.test", "Krith <hello@mail.krith.test>", "https://app.example.test", invitation{}); err == nil {
			t.Fatalf("refused: %q", bad)
		}
	}
}

func TestInvitationEmailsSay(t *testing.T) {
	until := time.Date(2026, 10, 15, 18, 0, 0, 0, time.UTC)
	invite := invitation{Inviter: "Prashant\nBansal", Role: "Firm admin", Link: "https://app.example.test/v1/auth/login?invitation=tok_1", Expires: until}
	e, err := platformEmail("invitation", "nia@acme.test", "Acme <Advisors>", "acme.test", "Krith <hello@mail.krith.test>", "https://app.example.test", invite)
	if err != nil {
		t.Fatal(err)
	}
	if e.Subject != "Prashant Bansal invited you to join Acme <Advisors> on Krith" || e.From != "Krith <hello@mail.krith.test>" || len(e.To) != 1 || e.To[0] != "nia@acme.test" {
		t.Fatalf("the invitation names who sent it and the firm, from Krith's sender: %+v", e)
	}
	for _, want := range []string{"Prashant Bansal invited you to join Acme <Advisors> on Krith, as Firm admin.", "Krith’s agents prepare the month-end close", "Join Acme <Advisors>: https://app.example.test/v1/auth/login?invitation=tok_1", "Sign in with Google as nia@acme.test. The invitation is open until 15 October 2026.", "Nothing happens unless you join.", "Krith\nThe work, prepared."} {
		if !strings.Contains(e.Text, want) {
			t.Fatalf("the invitation says %q:\n%s", want, e.Text)
		}
	}
	for _, want := range []string{`href="https://app.example.test/v1/auth/login?invitation=tok_1"`, "Join Acme &lt;Advisors&gt; on Krith</h1>", "Prashant Bansal invited you to join as <strong>Firm admin</strong>.", "<strong>nia@acme.test</strong>", "15 October 2026"} {
		if !strings.Contains(e.HTML, want) {
			t.Fatalf("the invitation's HTML has %q, with the firm's name escaped:\n%s", want, e.HTML)
		}
	}
	if strings.Contains(e.HTML, "<Advisors>") {
		t.Fatalf("the firm's name is escaped in HTML:\n%s", e.HTML)
	}
	invite.Inviter = ""
	if e, err = platformEmail("invitation", "nia@acme.test", "Acme", "", "Krith <hello@mail.krith.test>", "https://app.example.test", invite); err != nil || e.Subject != "You’re invited to join Acme on Krith" {
		t.Fatalf("without the inviter's name it still invites: %q %v", e.Subject, err)
	}
	// WorkOS's own acceptance page, with browser sessions, is a Join link too.
	invite.Link = "https://auth.example.test/invite?token=tok_1"
	if e, err = platformEmail("invitation", "nia@acme.test", "Acme", "", "Krith <hello@mail.krith.test>", "https://app.example.test", invite); err != nil || !strings.Contains(e.Text, "Join Acme: https://auth.example.test/invite?token=tok_1") {
		t.Fatalf("WorkOS's acceptance page is the Join link with browser sessions: %v %s", err, e.Text)
	}
	for _, bad := range []invitation{{Role: "Member", Expires: until}, {Role: "Member", Link: "javascript:alert(1)", Expires: until}, {Role: "Member", Link: invite.Link}, {Link: invite.Link, Expires: until}} {
		if _, err := platformEmail("invitation", "nia@acme.test", "Acme", "", "Krith <hello@mail.krith.test>", "https://app.example.test", bad); err == nil {
			t.Fatalf("an invitation needs its link to Krith, its role and its expiry: %+v", bad)
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
		case who == "ben" && tries == 1, who == "kim" && tries == 1, who == "max":
			w.WriteHeader(500)
		case who == "kim":
			w.WriteHeader(422)
			w.Write([]byte(`{"message":"invalid"}`))
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
	// Removed after an unanswered attempt, nothing more is sent, but the outcome stays unknown: it may have gone.
	maxine := queue("joined", "max")
	drain()
	member("max", false)
	if _, err := db.Exec(`UPDATE platform_emails SET next_attempt_at=now() WHERE id=$1`, maxine); err != nil {
		t.Fatal(err)
	}
	drain()
	if s, n, _ := state(maxine); s != "unknown" || n != 1 || len(seen["max"]) != 1 {
		t.Fatalf("a removed person's email tried once stays unknown, unsent again: %s %d %d", s, n, len(seen["max"]))
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

	// A refusal that answers a retry leaves the outcome unknown: the unanswered attempt before it may have been sent.
	kim := queue("joined", "kim")
	drain()
	if _, err := db.Exec(`UPDATE platform_emails SET next_attempt_at=now() WHERE id=$1`, kim); err != nil {
		t.Fatal(err)
	}
	drain()
	if s, n, _ := state(kim); s != "unknown" || n != 2 {
		t.Fatalf("a refusal after an unanswered attempt is unknown, not failed: %s %d", s, n)
	}
	// So is a last attempt the worker stopped holding before Resend answered, and one still unconfirmed after a day.
	jo, lee := queue("joined", "jo"), queue("joined", "lee")
	if _, err := db.Exec(`UPDATE platform_emails SET state='sending',attempts=3,claimed_at=now()-interval '5 minutes' WHERE id=$1`, jo); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_emails SET attempts=1,next_attempt_at=now()+interval '1 hour',created_at=now()-interval '24 hours' WHERE id=$1`, lee); err != nil {
		t.Fatal(err)
	}
	if err := tidyPlatformEmails(ctx, db); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := state(jo); s != "unknown" {
		t.Fatalf("an abandoned last attempt is unknown: %s", s)
	}
	if s, _, _ := state(lee); s != "unknown" {
		t.Fatalf("an email tried and unconfirmed within a day is unknown, not expired: %s", s)
	}

	// An invitation goes with its link while it is open; its sealed token is gone once it is sent. One the firm revoked
	// before it went is cancelled unsent.
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	t.Cleanup(func() { db.Exec(`DELETE FROM member_invitations WHERE organization_id=$1`, org) })
	invite := func(who, state string) (id string) {
		t.Helper()
		var invitation string
		if err := db.QueryRow(`INSERT INTO member_invitations(organization_id,email,role_slug,role_name,workos_id,state,invited_by,reason,expires_at) VALUES($1,$2,'member','Member',$3,$4,'user_ana','Joining the close team',now()+interval '7 days') RETURNING id::text`,
			org, who+"@"+domain, "invitation_"+who+suffix, state).Scan(&invitation); err != nil {
			t.Fatal(err)
		}
		sealed, err := secrets.Seal(org+"/platform-email:invitation-link", []byte("https://app.example.test/v1/auth/login?invitation=tok_"+who))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`INSERT INTO platform_emails(kind,organization_id,recipient,invitation,inviter,link) VALUES('invitation',$1,$2,$3::bigint,'Ana Diaz',$4) RETURNING id::text`, org, who+"@"+domain, invitation, sealed).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	nia, oz := invite("nia", "pending"), invite("oz", "revoked")
	drain()
	var kept int
	if err := db.QueryRow(`SELECT count(*) FROM platform_emails WHERE id IN ($1,$2) AND link IS NOT NULL`, nia, oz).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("an invitation's token is not kept once its email is done: %d %v", kept, err)
	}
	if s, n, p := state(nia); s != "sent" || n != 1 || p != "re_nia" {
		t.Fatalf("an open invitation's email is sent: %s %d %q", s, n, p)
	}
	if e := bodies["nia"]; e.Subject != "Ana Diaz invited you to join Acme Advisors on Krith" || !strings.Contains(e.Text, "https://app.example.test/v1/auth/login?invitation=tok_nia") || !strings.Contains(e.Text, ", as Member.") || e.HTML == "" {
		t.Fatalf("the invitation names its sender and role, and links to sign in with its token: %+v", e)
	}
	if s, n, _ := state(oz); s != "cancelled" || n != 0 || len(seen["oz"]) != 0 {
		t.Fatalf("a revoked invitation's email is cancelled unsent: %s %d", s, n)
	}
	// A resend's email replaces one whose attempt went unanswered: the older is never tried again, and only the newer goes.
	pia := invite("pia", "pending")
	if _, err := db.Exec(`UPDATE platform_emails SET state='sending',attempts=1,claimed_at=now()-interval '5 minutes' WHERE id=$1`, pia); err != nil {
		t.Fatal(err)
	}
	var resent string
	if err := db.QueryRow(`INSERT INTO platform_emails(kind,organization_id,recipient,invitation,inviter,link) SELECT kind,organization_id,recipient,invitation,inviter,link FROM platform_emails WHERE id=$1 RETURNING id::text`, pia).Scan(&resent); err != nil {
		t.Fatal(err)
	}
	drain()
	if s, n, _ := state(pia); s != "unknown" || n != 1 {
		t.Fatalf("an unconfirmed email a resend replaced is not tried again, and stays unknown: %s %d", s, n)
	}
	if s, _, _ := state(resent); s != "sent" || len(seen["pia"]) != 1 {
		t.Fatalf("only the resend's email goes: %s %d", s, len(seen["pia"]))
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
