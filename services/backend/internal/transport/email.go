package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/delivery"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
)

// reminderStatus is SQL for request q: the reminders its firm's rule still allows, when the next is due (null until
// the latest email is delivered) and when that email's link expires.
const reminderStatus = `(SELECT jsonb_build_object('left',o.reminder_max-(SELECT count(*) FROM email_outbox WHERE request_id=q.id AND reminder_of IS NOT NULL),
 'due_at',(SELECT min(e.occurred_at) FROM email_delivery_events e WHERE e.connection_id=m.connection_id AND e.provider_id=m.provider_id AND lower(e.recipient)=lower(q.recipient) AND e.kind='delivered')+make_interval(days=>o.reminder_after_days),
 'expires_at',(SELECT l.expires_at FROM client_request_links l WHERE l.digest=m.link_digest))
 FROM request_drafts rd JOIN client_closes rc ON rc.id=rd.close_id JOIN organizations o ON o.id=rc.organization_id
 LEFT JOIN LATERAL (SELECT connection_id,provider_id,link_digest FROM email_outbox WHERE request_id=q.id ORDER BY email_outbox.created_at DESC,email_outbox.id DESC LIMIT 1) m ON true WHERE rd.id=q.draft_id)`

// reminderFollow is SQL for waiting request q, in Work's order: exhausted once the firm's rule allows no more reminders,
// expiring once the link lapsed or lapses before the next is due, and due while a reminder could be approved now, as the
// approval checks it; ” before any email was sent, or while the next reminder is not due.
const reminderFollow = `(SELECT CASE
 WHEN m.id IS NULL THEN ''
 WHEN (s->>'left')::int<=0 THEN 'exhausted'
 WHEN (s->>'expires_at')::timestamptz<=now() OR (s->>'due_at')::timestamptz>=(s->>'expires_at')::timestamptz THEN 'expiring'
 WHEN (s->>'due_at')::timestamptz<=now() AND m.state='accepted' AND m.delivery='delivered'
  AND EXISTS(SELECT 1 FROM client_request_links l WHERE l.digest=m.link_digest AND l.used_at IS NULL AND l.revoked_at IS NULL AND l.expires_at>now())
  AND NOT EXISTS(SELECT 1 FROM email_delivery bad WHERE bad.request_id=q.id AND bad.delivery IN ('bounced','complained','failed','suppressed'))
  AND NOT EXISTS(SELECT 1 FROM work_replies WHERE request_id=q.id)
  AND m.connection_id=(SELECT e.id FROM email_connections e JOIN request_drafts rd ON rd.id=q.draft_id JOIN client_closes rc ON rc.id=rd.close_id WHERE e.organization_id=rc.organization_id AND e.enabled) THEN 'due'
 ELSE '' END
 FROM (SELECT ` + reminderStatus + ` AS s) x LEFT JOIN LATERAL (SELECT * FROM email_delivery m WHERE m.request_id=q.id ORDER BY m.created_at DESC,m.id DESC LIMIT 1) m ON true)`

// replyAddress is where a client's reply to the outbox's email comes back into its close, or "" when this server
// takes no email replies (EMAIL_INTAKE_ADDRESS and EMAIL_INTAKE_KEY).
func replyAddress(outbox string) string {
	intake := strings.Split(os.Getenv("EMAIL_INTAKE_ADDRESS"), "@")
	if len(intake) != 2 || intake[0] == "" || intake[1] == "" || len(os.Getenv("EMAIL_INTAKE_KEY")) < 32 {
		return ""
	}
	return intake[0] + "+reply-" + strings.ToLower(outbox) + "@" + intake[1]
}

// currentSender is the firm's current sender in a transaction of its own (firmSender).
func currentSender(ctx context.Context, db *sql.DB, org string) (id, sender, provider string, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", err
	}
	defer tx.Rollback()
	// A sender stopped for want of Krith's address stays stopped even though the firm then has none.
	if id, sender, provider, err = firmSender(ctx, tx, org); err == nil || err == sql.ErrNoRows {
		if e := tx.Commit(); e != nil {
			return "", "", "", e
		}
	}
	return id, sender, provider, err
}

func registerEmail(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/email", func(w http.ResponseWriter, r *http.Request) {
		// The firm's current sender, Krith's own unless it connected one, and the integrations it may connect instead.
		raw := []byte("null")
		id, _, _, err := currentSender(r.Context(), db, organization(r))
		if err == nil {
			err = db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('id',id::text,'provider',provider,'sender',sender,'domain_id',domain_id,'verified_at',verified_at,
 'tracking_configured',CASE WHEN provider='krith' THEN $3 ELSE webhook_secret IS NOT NULL END) FROM email_connections WHERE organization_id=$1 AND id::text=$2`, organization(r), id, delivery.KrithWebhookSecret() != "").Scan(&raw)
		}
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Email connection unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"connection": json.RawMessage(raw), "hosted": auth.Actor(r.Context()).Organization != "", "providers": delivery.Providers, "krith": delivery.KrithAddress() != ""})
	})
	mux.HandleFunc("POST /v1/closes/email", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || !firmAccess(r) || !slices.Contains(a.Permissions, "integrations:manage") {
			problem(w, 403, "Connecting email requires a hosted firm administrator with integrations:manage.")
			return
		}
		var in struct {
			Provider string            `json:"provider"`
			Fields   map[string]string `json:"fields"`
			// A Resend connection's fields, as pages before integrations sent them.
			Key           string `json:"key"`
			From          string `json:"from"`
			DomainID      string `json:"domain_id"`
			PreviousID    string `json:"previous_id"`
			Confirmed     bool   `json:"confirmed"`
			Disable       bool   `json:"disable"`
			WebhookSecret string `json:"webhook_secret"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm that this sender and its account belong to your firm.")
			return
		}
		if in.WebhookSecret != "" {
			if !delivery.ValidWebhookSecret(in.WebhookSecret) {
				problem(w, 422, "Enter the Resend webhook signing secret.")
				return
			}
			encrypted, err := secrets.Seal(a.Organization+"/resend-webhook/"+in.PreviousID, []byte(in.WebhookSecret))
			if err != nil {
				problem(w, 503, "Webhook encryption unavailable.")
				return
			}
			// Krith's own sender is tracked by Krith's webhook, never a firm's.
			result, err := db.ExecContext(r.Context(), `UPDATE email_connections SET webhook_secret=$3 WHERE organization_id=$1 AND id::text=$2 AND enabled AND provider<>'krith'`, a.Organization, in.PreviousID, encrypted)
			if err != nil {
				problem(w, 503, "Webhook configuration unavailable.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Sender changed. Refresh before configuring tracking.")
				return
			}
			jsonReply(w, 200, map[string]bool{"saved": true})
			return
		}
		// The firm's own integration, checked with its provider before anything is saved.
		var provider delivery.Provider
		var sender, reference string
		var encrypted []byte
		if !in.Disable {
			if in.Provider == "" && in.Fields == nil {
				in.Provider, in.Fields = "resend", map[string]string{"key": in.Key, "from": in.From, "domain_id": in.DomainID}
			}
			var ok bool
			if provider, ok = delivery.Lookup(in.Provider); !ok {
				problem(w, 422, "Choose an email integration this server supports.")
				return
			}
			scope := delivery.SettingsScope(a.Organization, provider.Name)
			if _, err := secrets.Seal(scope, nil); err != nil {
				problem(w, 503, err.Error())
				return
			}
			var settings []byte
			var err error
			if sender, reference, settings, err = provider.Connect(r.Context(), in.Fields); err != nil {
				problem(w, 422, err.Error())
				return
			}
			if encrypted, err = secrets.Seal(scope, settings); err != nil {
				problem(w, 503, err.Error())
				return
			}
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Email storage unavailable.")
			return
		}
		defer tx.Rollback()
		var org, prior, priorProvider string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, a.Organization).Scan(&org); err != nil {
			problem(w, 403, "Organization unavailable.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM email_connections WHERE organization_id=$1 AND enabled),''),COALESCE((SELECT provider FROM email_connections WHERE organization_id=$1 AND enabled),'')`, org).Scan(&prior, &priorProvider); err != nil {
			problem(w, 503, "Current sender unavailable.")
			return
		}
		if prior != in.PreviousID {
			problem(w, 409, "Sender changed. Refresh before changing the connection.")
			return
		}
		if in.Disable && priorProvider == delivery.Krith {
			problem(w, 409, "Krith's sender is the firm's default. Connect the firm's own sender to replace it.")
			return
		}
		// Disconnecting the firm's own sender brings back Krith's, where this server has it.
		if _, err = tx.ExecContext(r.Context(), `UPDATE email_connections SET enabled=false WHERE organization_id=$1 AND enabled`, org); err == nil && !in.Disable {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO email_connections(organization_id,provider,sender,domain_id,credential,actor) VALUES($1,$2,$3,$4,$5,$6)`, org, provider.Name, sender, reference, encrypted, a.Subject)
		} else if err == nil {
			if _, _, _, err = firmSender(r.Context(), tx, org); err == sql.ErrNoRows {
				err = nil
			}
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Email connection save could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
	})
	send := func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || !slices.Contains(a.Permissions, "requests:send") {
			problem(w, 403, "Email delivery requires a hosted session with requests:send.")
			return
		}
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Confirmed    bool   `json:"confirmed"`
			Revision     int    `json:"revision"`
			ConnectionID string `json:"connection_id"`
			PreviousID   string `json:"previous_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the recipient, sender and replacement of any existing private link before sending.")
			return
		}
		origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
		if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
			problem(w, 503, "Configure PUBLIC_APP_URL as the exact HTTPS application origin before sending links.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Email outbox unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, org string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,organization_id FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID, &org); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var recipient, connection, sender, existing string
		var grouped bool
		err = tx.QueryRowContext(r.Context(), `SELECT q.recipient,q.coverage_scope IS NOT NULL FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id::text=$1 AND d.close_id=$2`, r.PathValue("request"), closeID).Scan(&recipient, &grouped)
		if err != nil || grouped != (r.PathValue("job") == "") {
			problem(w, 404, "Approved request not found.")
			return
		}
		if in.PreviousID == "" {
			err = tx.QueryRowContext(r.Context(), `SELECT id FROM email_outbox WHERE request_id::text=$1 AND reminder_of IS NULL`, r.PathValue("request")).Scan(&existing)
		} else {
			err = tx.QueryRowContext(r.Context(), `SELECT id FROM email_outbox WHERE request_id::text=$1 AND reminder_of=$2`, r.PathValue("request"), in.PreviousID).Scan(&existing)
		}
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Email history unavailable.")
			return
		}
		if existing != "" {
			// A reminder reuses the original link and passcode, so only a first approval's retry can lose one.
			guarded := false
			if in.PreviousID == "" {
				err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT l.passcode_digest IS NOT NULL FROM email_outbox m JOIN client_request_links l ON l.digest=m.link_digest WHERE m.id=$1),false)`, existing).Scan(&guarded)
			}
			if err != nil {
				problem(w, 503, "Email history unavailable.")
				return
			}
			if guarded {
				problem(w, 409, "This email was already approved, and its link's passcode was shown only then. If it was not recorded, create a new private reply link, which replaces the emailed one, and give the client that link and its passcode.")
				return
			}
			jsonReply(w, 200, map[string]string{"id": existing})
			return
		}
		err = nil
		if !grouped {
			var state string
			var revision int
			err = tx.QueryRowContext(r.Context(), `SELECT j.state,i.revision FROM work_requests q JOIN preparation_jobs j ON j.id=q.job_id JOIN preparation_intakes i ON i.job_id=j.id WHERE q.id::text=$1 AND j.id::text=$2 AND j.close_id=$3 AND q.id=(SELECT max(id) FROM work_requests WHERE job_id=j.id) FOR UPDATE OF j,i`, r.PathValue("request"), r.PathValue("job"), closeID).Scan(&state, &revision)
			if err != nil || state != "waiting_client" || revision != in.Revision {
				problem(w, 409, "Request changed or is no longer awaiting a client reply.")
				return
			}
		}
		var unanswered bool
		err = tx.QueryRowContext(r.Context(), `SELECT NOT EXISTS(SELECT 1 FROM work_replies WHERE request_id=$1) AND ($2::boolean=false OR EXISTS(SELECT 1 FROM client_request_links WHERE request_id=$1 AND revoked_at IS NULL AND used_at IS NULL AND expires_at>now()))`, r.PathValue("request"), grouped).Scan(&unanswered)
		if err != nil || !unanswered {
			problem(w, 409, "Request was answered, withdrawn or expired. Review before issuing a new request.")
			return
		}
		if err = preparation.FreshClientRequest(r.Context(), tx, closeID, r.PathValue("request")); err != nil {
			problem(w, 409, err.Error())
			return
		}
		var provider string
		if connection, sender, provider, err = firmSender(r.Context(), tx, org); err != nil || connection != in.ConnectionID {
			problem(w, 409, "Verify and select the current firm sender before approving delivery.")
			return
		}
		// Krith's sending address takes no replies: a client who replies must reach the close through the reply address.
		if provider == delivery.Krith && replyAddress("") == "" {
			problem(w, 409, "Krith's sender needs this server's reply address (EMAIL_INTAKE_ADDRESS), or a client's reply would reach no one. Share the private link by hand, or connect the firm's own sender.")
			return
		}
		id, token := rand.Text(), rand.Text()
		digest := preparation.Digest([]byte(token))
		passcode := ""
		if in.PreviousID == "" {
			// A reminder reuses the original link and its passcode.
			if _, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id::text=$1 AND revoked_at IS NULL AND used_at IS NULL`, r.PathValue("request")); err == nil {
				passcode, err = issueLink(r.Context(), tx, r.PathValue("request"), actor, token)
			}
			if err != nil {
				problem(w, 503, "Email approval could not be confirmed. Refresh Work before retrying.")
				return
			}
		}
		message := delivery.Email{From: sender, To: []string{recipient}, Subject: "A secure request from your accounting team", Text: "Your accounting team has requested information. Review the request and reply using this private link:\n\n" + strings.TrimRight(origin.String(), "/") + "/request#" + token + "\n\nThis link expires in seven days. Do not forward it. Contact your accounting team if you were not expecting this request."}
		if message.ReplyTo = replyAddress(id); message.ReplyTo != "" {
			message.Text += "\n\nYou may also reply to this email with text. Your accounting team will review the sender and answer before using it; use the secure link for attachments."
		}
		if passcode != "" {
			message.Text += "\n\nYou will also need the passcode your accounting team gives you by phone or in person. It is never sent by email."
		}
		if in.PreviousID != "" {
			var prior []byte
			err = tx.QueryRowContext(r.Context(), `SELECT m.payload,m.link_digest FROM email_delivery m JOIN client_request_links l ON l.digest=m.link_digest WHERE m.id=$1 AND m.request_id::text=$2 AND m.connection_id::text=$3 AND m.state='accepted' AND m.delivery='delivered' AND l.used_at IS NULL AND l.revoked_at IS NULL AND l.expires_at>now() AND NOT EXISTS(SELECT 1 FROM email_delivery bad WHERE bad.request_id=m.request_id AND bad.delivery IN ('bounced','complained','failed','suppressed')) AND NOT EXISTS(SELECT 1 FROM work_replies WHERE request_id=m.request_id)`, in.PreviousID, r.PathValue("request"), connection).Scan(&prior, &digest)
			if err != nil {
				problem(w, 409, "Reminder blocked: confirm delivery, an unanswered current request, the same sender and an unexpired link first.")
				return
			}
			// The firm's rule bounds reminders: every approved one counts, and the next is due some days after delivery.
			// A predecessor without a reminder is the request's latest email.
			var left int
			var due time.Time
			if err = tx.QueryRowContext(r.Context(), `SELECT (s->>'left')::int,(s->>'due_at')::timestamptz FROM (SELECT `+reminderStatus+` AS s FROM work_requests q WHERE q.id::text=$1) x`, r.PathValue("request")).Scan(&left, &due); err != nil {
				problem(w, 503, "The firm's reminder rule is unavailable.")
				return
			}
			if left <= 0 {
				problem(w, 409, "This request is unanswered after all the reminders the firm's rule allows. Contact the client another way.")
				return
			}
			if due.After(time.Now()) {
				problem(w, 409, "Under the firm's rule the next reminder is due "+due.UTC().Format("2 January 2006 15:04 MST")+".")
				return
			}
			plain, e := secrets.Open(org+"/email/"+in.PreviousID, prior)
			if e != nil || json.Unmarshal(plain, &message) != nil {
				problem(w, 503, "Approved message unavailable.")
				return
			}
			message.Subject = "Reminder: a secure request from your accounting team"
			message.Text = strings.ReplaceAll(message.Text, "This link expires in seven days.", "This link keeps its original expiry.")

		}
		payload, _ := json.Marshal(message)
		encrypted, err := secrets.Seal(org+"/email/"+id, payload)
		if err != nil {
			problem(w, 503, err.Error())
			return
		}
		until := time.Now().Add(30 * time.Minute)
		if a.ExpiresAt == nil {
			problem(w, 403, "A current expiring user session is required.")
			return
		}
		if a.ExpiresAt.Time.Before(until) {
			until = a.ExpiresAt.Time
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO email_outbox(id,request_id,connection_id,link_digest,payload,approved_by,approved_until,firm_authority,reminder_of) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`, id, r.PathValue("request"), connection, digest, encrypted, actor, until, firmAccess(r), in.PreviousID)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Email approval could not be confirmed. Refresh Work before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "passcode": passcode})
	}
	mux.HandleFunc("POST /v1/closes/{id}/work/{job}/requests/{request}/email", send)
	mux.HandleFunc("POST /v1/closes/{id}/requests/{request}/email", send)
}
