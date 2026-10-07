package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Krith's own emails to people at firms (docs/sign-in.md#welcome-emails): a welcome to whoever sets up a firm by
// signing in, and a note to each colleague who joins it. Sign-in queues them in platform_emails; this loop sends them
// from Krith's sender with Krith's Resend key (PLATFORM_EMAIL_FROM, PLATFORM_EMAIL_KEY), never a firm's connection.
// Without both, nothing is sent and what was queued expires a day after the sign-in.

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
	if _, err := db.ExecContext(ctx, `UPDATE platform_emails SET state='unknown',claimed_at=NULL,finished_at=now(),error='The worker stopped before Resend answered the last attempt'
 WHERE state='sending' AND attempts>=3 AND claimed_at<now()-interval '2 minutes'`); err != nil {
		return err
	}
	// Resend keeps an idempotency key for 24 hours, so an attempt is never repeated after that. One never tried expires;
	// one tried before was retried only because Resend may have accepted it, so its outcome is unknown.
	if _, err := db.ExecContext(ctx, `UPDATE platform_emails SET state=CASE WHEN attempts>0 THEN 'unknown' ELSE 'expired' END,claimed_at=NULL,finished_at=now(),
 error=CASE WHEN attempts>0 THEN 'Resend did not confirm it within a day of the sign-in' ELSE 'Not sent within a day of the sign-in' END
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
	// The email says the person is in the firm, so it goes only while the firm is enabled and the person still a member.
	err = tx.QueryRowContext(ctx, `SELECT e.id,e.kind,e.recipient,o.name,COALESCE(o.email_domain,''),e.attempts,
 o.enabled AND EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=e.organization_id AND m.subject=e.subject AND m.granted)
 FROM platform_emails e JOIN organizations o ON o.id=e.organization_id
 WHERE ((e.state='queued' AND e.next_attempt_at<=now()) OR (e.state='sending' AND e.claimed_at<now()-interval '2 minutes')) AND e.attempts<3 AND e.created_at>now()-interval '23 hours'
 ORDER BY e.id FOR UPDATE OF e SKIP LOCKED LIMIT 1`).Scan(&id, &kind, &recipient, &firm, &domain, &attempts, &current)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !current {
		if _, err = tx.ExecContext(ctx, `UPDATE platform_emails SET state='cancelled',claimed_at=NULL,finished_at=now(),error='The firm was suspended or the person removed before it was sent' WHERE id=$1`, id); err != nil {
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
	email, err := platformEmail(kind, recipient, firm, domain, from, app)
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
 finished_at=CASE WHEN $2='queued' THEN NULL ELSE now() END WHERE id=$1 AND state='sending'`, id, state, providerID, message)
	return true, err
}

// platformEmail writes one email in plain text. The firm's name is as its administrator set it, so it is kept to one
// short line.
func platformEmail(kind, recipient, firm, domain, from, app string) (Email, error) {
	if address, err := mail.ParseAddress(recipient); err != nil || address.Address != recipient {
		return Email{}, fmt.Errorf("The recipient address is not valid.")
	}
	firm = strings.Join(strings.FieldsFunc(firm, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(firm); len(r) > 120 {
		firm = string(r[:120])
	}
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
	default:
		return Email{}, fmt.Errorf("Unknown platform email %q.", kind)
	}
	text += "Questions? Write to us: " + app + "/contact\n\nKrith\nThe work, prepared.\n"
	return Email{From: from, To: []string{recipient}, Subject: subject, Text: text}, nil
}
