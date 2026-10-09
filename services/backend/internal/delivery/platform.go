package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"close.local/backend/internal/secrets"
)

// Krith's own emails to people at firms (docs/sign-in.md#welcome-emails): a welcome to whoever sets up a firm by
// signing in, a note to each colleague who joins it, and the invitations a firm sends (docs/sign-in.md#invitations).
// Sign-in and the invitation queue them in platform_emails; this loop sends them from Krith's sender with Krith's Resend
// key (PLATFORM_EMAIL_FROM, PLATFORM_EMAIL_KEY), never a firm's connection. Without both, nothing is sent and what was
// queued expires a day later.

func platformSender() (key, from, app string, ok bool) {
	key, from = os.Getenv("PLATFORM_EMAIL_KEY"), strings.TrimSpace(os.Getenv("PLATFORM_EMAIL_FROM"))
	app = strings.TrimRight(os.Getenv("PUBLIC_APP_URL"), "/")
	if key == "" || from == "" || app == "" || strings.ContainsAny(from, "\r\n") {
		return "", "", "", false
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return "", "", "", false
	}
	if u, err := url.Parse(app); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", "", false
	}
	return key, from, app, true
}

// RunPlatformEmails sends what sign-in queued and tidies what is done: anything not sent within a day of its sign-in
// expires, and finished rows are deleted after 30 days.
func RunPlatformEmails(ctx context.Context, db *sql.DB) {
	key, from, app, ok := platformSender()
	if !ok {
		log.Print("Platform emails are off: set PLATFORM_EMAIL_KEY, a valid PLATFORM_EMAIL_FROM and PUBLIC_APP_URL")
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := tidyPlatformEmails(ctx, db); err != nil && ctx.Err() == nil {
			log.Printf("Could not tidy platform emails: %v", err)
		}
		for ok {
			sent, err := sendPlatformEmail(ctx, db, key, from, app)
			if err != nil && ctx.Err() == nil {
				log.Printf("Platform email unavailable: %v", err)
			}
			if err != nil || !sent {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func tidyPlatformEmails(ctx context.Context, db *sql.DB) error {
	// A last attempt the worker stopped holding before Resend answered may have been sent.
	if _, err := db.ExecContext(ctx, `UPDATE platform_emails SET state='unknown',claimed_at=NULL,finished_at=now(),link=NULL,error='The worker stopped before Resend answered the last attempt'
 WHERE state='sending' AND attempts>=3 AND claimed_at<now()-interval '2 minutes'`); err != nil {
		return err
	}
	// Resend keeps an idempotency key for 24 hours, so an attempt is never repeated after that. One never tried expires;
	// one tried before was retried only because Resend may have accepted it, so its outcome is unknown.
	if _, err := db.ExecContext(ctx, `UPDATE platform_emails SET state=CASE WHEN attempts>0 THEN 'unknown' ELSE 'expired' END,claimed_at=NULL,finished_at=now(),link=NULL,
 error=CASE WHEN attempts>0 THEN 'Resend did not confirm it within a day of being queued' ELSE 'Not sent within a day of being queued' END
 WHERE state IN ('queued','sending') AND created_at<now()-interval '23 hours'`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM platform_emails WHERE finished_at<now()-interval '30 days'`)
	return err
}

// sendPlatformEmail sends one queued email, or retries one whose attempt went unanswered, under the same idempotency
// key each time. An attempt whose outcome is uncertain (no answer, a server error, or a success without Resend's message
// ID) is tried twice more and then recorded as unknown, since Resend may have sent it. A refusal is final: failed, or
// unknown when it answers a retry, since the earlier attempt may have been sent. Unknown emails raise a platform alert.
func sendPlatformEmail(ctx context.Context, db *sql.DB, key, from, app string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	var kind, recipient, firm, domain string
	var attempts int
	var current bool
	var invite invitation
	var expires sql.NullTime
	var link []byte
	// A welcome or joined email says the person is in the firm, so it goes only while the firm is enabled and the person
	// still a member; an invitation, only while the firm is enabled and the invitation still open.
	err = tx.QueryRowContext(ctx, `SELECT e.id,e.kind,e.recipient,o.name,COALESCE(o.email_domain,''),e.attempts,
 COALESCE(CASE WHEN e.kind='invitation' THEN o.enabled AND i.state='pending' AND i.expires_at>now()
  ELSE o.enabled AND EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=e.organization_id AND m.subject=e.subject AND m.granted) END,false),
 e.inviter,COALESCE(i.role_name,''),i.expires_at,e.link
 FROM platform_emails e JOIN organizations o ON o.id=e.organization_id LEFT JOIN member_invitations i ON i.id=e.invitation
 WHERE ((e.state='queued' AND e.next_attempt_at<=now()) OR (e.state='sending' AND e.claimed_at<now()-interval '2 minutes')) AND e.attempts<3 AND e.created_at>now()-interval '23 hours'
 ORDER BY e.id FOR UPDATE OF e SKIP LOCKED LIMIT 1`).Scan(&id, &kind, &recipient, &firm, &domain, &attempts, &current, &invite.Inviter, &invite.Role, &expires, &link)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !current {
		// Nothing more is sent. One never tried is cancelled; one tried before may have been sent, so it stays unknown.
		if _, err = tx.ExecContext(ctx, `UPDATE platform_emails SET state=CASE WHEN attempts>0 THEN 'unknown' ELSE 'cancelled' END,claimed_at=NULL,finished_at=now(),link=NULL,
 error=CASE WHEN kind='invitation' THEN CASE WHEN attempts>0 THEN 'The firm was suspended or the invitation closed after an unconfirmed attempt' ELSE 'The firm was suspended or the invitation closed before it was sent' END
  WHEN attempts>0 THEN 'The firm was suspended or the person removed after an unconfirmed attempt' ELSE 'The firm was suspended or the person removed before it was sent' END WHERE id=$1`, id); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE platform_emails SET state='sending',claimed_at=now(),attempts=attempts+1 WHERE id=$1`, id); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	state, providerID, message := "sent", "", ""
	invite.Expires = expires.Time
	if kind == "invitation" {
		var opened []byte
		if opened, err = secrets.Open("platform-email:invitation-link", link); err == nil {
			invite.Link = string(opened)
		}
	}
	var email Email
	if err == nil {
		email, err = platformEmail(kind, recipient, firm, domain, from, app, invite)
	}
	called, status := false, 0
	if err == nil {
		payload, _ := json.Marshal(email)
		called = true
		providerID, status, err = send(ctx, key, payload, "krith-platform/"+strconv.FormatInt(id, 10))
	}
	if err != nil {
		message = err.Error()
		uncertain := called && (status == 0 || status == 408 || status == 409 || status == 429 || status >= 500 || (status >= 200 && status < 300))
		switch {
		case uncertain && attempts+1 < 3:
			state = "queued"
		case uncertain || attempts > 0:
			state = "unknown"
		default:
			state = "failed"
		}
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = db.ExecContext(persist, `UPDATE platform_emails SET state=$2,provider_id=NULLIF($3,''),error=$4,claimed_at=NULL,next_attempt_at=now()+interval '1 minute',
 finished_at=CASE WHEN $2='queued' THEN NULL ELSE now() END,link=CASE WHEN $2='queued' THEN link END WHERE id=$1 AND state='sending'`, id, state, providerID, message)
	return true, err
}

// invitation is what an invitation email says beyond the firm: who sent it, the role it gives, until when, and the Join
// link the API chose for it: Krith's own sign-in with the invitation, or WorkOS's acceptance page.
type invitation struct {
	Inviter, Role, Link string
	Expires             time.Time
}

// oneLine keeps text a firm or person set to one short line.
func oneLine(text string, limit int) string {
	text = strings.Join(strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(text); len(r) > limit {
		text = string(r[:limit])
	}
	return text
}

// platformEmail writes one email in plain text, and an invitation also in simple HTML. The firm's name is as its
// administrator set it, so it is kept to one short line.
func platformEmail(kind, recipient, firm, domain, from, app string, invite invitation) (Email, error) {
	if address, err := mail.ParseAddress(recipient); err != nil || address.Address != recipient {
		return Email{}, fmt.Errorf("The recipient address is not valid.")
	}
	firm = oneLine(firm, 120)
	if domain == "" {
		_, domain, _ = strings.Cut(recipient, "@")
	}
	if firm == "" {
		firm = domain
	}
	var subject, text string
	switch kind {
	case "welcome":
		subject = "Welcome to Krith"
		text = "Welcome to Krith.\n\n" +
			firm + " is set up on Krith, and you are its administrator.\n\n" +
			"Open your workspace: " + app + "/dashboard\n\n" +
			"Colleagues who sign in with their " + domain + " address join " + firm + " with no access until you give them a role or assign them clients.\n\n"
	case "joined":
		subject = "You’ve joined " + firm + " on Krith"
		text = "You’ve joined " + firm + " on Krith with your " + domain + " address.\n\n" +
			"You can’t open any clients yet. Your firm’s administrator decides what you can open, and it appears in your workspace once they do.\n\n" +
			"Open Krith: " + app + "/dashboard\n\n"
	case "invitation":
		return invitationEmail(recipient, firm, from, invite)
	default:
		return Email{}, fmt.Errorf("Unknown platform email %q.", kind)
	}
	text += "Questions? Write to us: " + app + "/contact\n\nKrith\nThe work, prepared.\n"
	return Email{From: from, To: []string{recipient}, Subject: subject, Text: text}, nil
}

// invitationEmail asks the person to join the firm, says what Krith does for them, and gives one button: it signs them
// in with Google and accepts the invitation in the same step.
func invitationEmail(recipient, firm, from string, invite invitation) (Email, error) {
	inviter, role := oneLine(invite.Inviter, 120), oneLine(invite.Role, 80)
	if link, err := url.Parse(invite.Link); err != nil || (link.Scheme != "https" && link.Scheme != "http") || link.Host == "" || invite.Expires.IsZero() || role == "" {
		return Email{}, fmt.Errorf("The invitation's link, role or expiry is missing.")
	}
	// The HTML's heading names the firm, so its first line says only who invited them and as what.
	subject, invited := "You’re invited to join "+firm+" on Krith", "You’re invited"
	if inviter != "" {
		subject, invited = inviter+" invited you to join "+firm+" on Krith", inviter+" invited you"
	}
	until := invite.Expires.UTC().Format("2 January 2006")
	const what = "Krith’s agents prepare the month-end close: reconciliations, schedules and review notes, each figure tied to its source. You review the work, decide and sign."
	text := subject + ", as " + role + ".\n\n" +
		what + "\n\n" +
		"Join " + firm + ": " + invite.Link + "\n\n" +
		"Sign in with Google as " + recipient + ". The invitation is open until " + until + ".\n\n" +
		"Not expecting this? You can ignore it. Nothing happens unless you join.\n\n" +
		"Krith\nThe work, prepared.\n"
	e := html.EscapeString
	page := `<!doctype html><html><body style="margin:0;padding:0;background:#f6f5f2">` +
		`<div style="max-width:520px;margin:0 auto;padding:40px 24px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#1c1b19">` +
		`<div style="font-family:Georgia,'Times New Roman',serif;font-size:26px;line-height:1;padding-bottom:4px;border-bottom:3px double #2f43d6;display:inline-block">krith</div>` +
		`<div style="background:#ffffff;border:1px solid #e4e2dc;border-radius:12px;padding:32px;margin-top:28px">` +
		`<h1 style="font-family:Georgia,'Times New Roman',serif;font-weight:400;font-size:28px;line-height:1.2;margin:0 0 16px">Join ` + e(firm) + ` on Krith</h1>` +
		`<p style="font-size:16px;line-height:1.5;margin:0 0 16px">` + e(invited) + ` to join as <strong>` + e(role) + `</strong>.</p>` +
		`<p style="font-size:15px;line-height:1.55;margin:0 0 28px;color:#55524b">` + e(what) + `</p>` +
		`<a href="` + e(invite.Link) + `" style="display:inline-block;background:#2f43d6;color:#ffffff;text-decoration:none;font-size:16px;font-weight:600;padding:13px 22px;border-radius:999px">Join ` + e(firm) + ` &rarr;</a>` +
		`<p style="font-size:14px;line-height:1.5;margin:24px 0 0;color:#55524b">Sign in with Google as <strong>` + e(recipient) + `</strong>. The invitation is open until ` + e(until) + `.</p>` +
		`</div>` +
		`<p style="font-size:13px;line-height:1.5;margin:24px 0 4px;color:#7a766d">Not expecting this? You can ignore it. Nothing happens unless you join.</p>` +
		`<p style="font-size:13px;line-height:1.5;margin:0;color:#7a766d">Krith · The work, prepared.</p>` +
		`</div></body></html>`
	return Email{From: from, To: []string{recipient}, Subject: subject, Text: text, HTML: page}, nil
}
