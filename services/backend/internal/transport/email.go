package transport

import (
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

func registerEmail(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/email", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT jsonb_build_object('id',id::text,'sender',sender,'domain_id',domain_id,'verified_at',verified_at,'tracking_configured',webhook_secret IS NOT NULL) FROM email_connections WHERE organization_id=$1 AND enabled),'null'::jsonb)`, organization(r)).Scan(&raw)
		if err != nil {
			problem(w, 503, "Email connection unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"connection": json.RawMessage(raw), "hosted": auth.Actor(r.Context()).Organization != ""})
	})
	mux.HandleFunc("POST /v1/closes/email", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || !firmAccess(r) || !slices.Contains(a.Permissions, "integrations:manage") {
			problem(w, 403, "Connecting email requires a hosted firm administrator with integrations:manage.")
			return
		}
		var in struct {
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
			problem(w, 422, "Confirm that this sender and Resend account belong to your firm.")
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
			result, err := db.ExecContext(r.Context(), `UPDATE email_connections SET webhook_secret=$3 WHERE organization_id=$1 AND id::text=$2 AND enabled`, a.Organization, in.PreviousID, encrypted)
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
		c := delivery.Connection{Key: strings.TrimSpace(in.Key), From: strings.TrimSpace(in.From), DomainID: strings.TrimSpace(in.DomainID)}
		var encrypted []byte
		if !in.Disable {
			raw, _ := json.Marshal(c)
			var err error
			encrypted, err = secrets.Seal(a.Organization+"/resend", raw)
			if err != nil {
				problem(w, 503, err.Error())
				return
			}
			if err = delivery.VerifySender(r.Context(), c); err != nil {
				problem(w, 422, err.Error())
				return
			}
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Email storage unavailable.")
			return
		}
		defer tx.Rollback()
		var org, prior string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, a.Organization).Scan(&org); err != nil {
			problem(w, 403, "Organization unavailable.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM email_connections WHERE organization_id=$1 AND enabled),'')`, org).Scan(&prior); err != nil {
			problem(w, 503, "Current sender unavailable.")
			return
		}
		if prior != in.PreviousID {
			problem(w, 409, "Sender changed. Refresh before changing the connection.")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `UPDATE email_connections SET enabled=false WHERE organization_id=$1 AND enabled`, org); err == nil && !in.Disable {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO email_connections(organization_id,sender,domain_id,credential,actor) VALUES($1,$2,$3,$4,$5)`, org, c.From, c.DomainID, encrypted, a.Subject)
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
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text,sender FROM email_connections WHERE organization_id=$1 AND enabled`, org).Scan(&connection, &sender); err != nil || connection != in.ConnectionID {
			problem(w, 409, "Verify and select the current firm sender before approving delivery.")
			return
		}
		id, token := rand.Text(), rand.Text()
		digest := preparation.Digest([]byte(token))
		message := delivery.Email{From: sender, To: []string{recipient}, Subject: "A secure request from your accounting team", Text: "Your accounting team has requested information. Review the request and reply using this private link:\n\n" + strings.TrimRight(origin.String(), "/") + "/request#" + token + "\n\nThis link expires in seven days. Do not forward it. Contact your accounting team if you were not expecting this request."}
		if in.PreviousID != "" {
			var prior []byte
			err = tx.QueryRowContext(r.Context(), `SELECT m.payload,m.link_digest FROM email_delivery m JOIN client_request_links l ON l.digest=m.link_digest WHERE m.id=$1 AND m.request_id::text=$2 AND m.connection_id::text=$3 AND m.state='accepted' AND m.delivery='delivered' AND l.used_at IS NULL AND l.revoked_at IS NULL AND l.expires_at>now() AND NOT EXISTS(SELECT 1 FROM email_delivery bad WHERE bad.request_id=m.request_id AND bad.delivery IN ('bounced','complained','failed','suppressed')) AND NOT EXISTS(SELECT 1 FROM work_replies WHERE request_id=m.request_id)`, in.PreviousID, r.PathValue("request"), connection).Scan(&prior, &digest)
			if err != nil {
				problem(w, 409, "Reminder blocked: confirm delivery, an unanswered current request, the same sender and an unexpired link first.")
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
		if in.PreviousID == "" {
			if _, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id::text=$1 AND revoked_at IS NULL AND used_at IS NULL`, r.PathValue("request")); err == nil {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO client_request_links(digest,request_id,issued_by) VALUES($1,$2,$3)`, digest, r.PathValue("request"), actor)
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO email_outbox(id,request_id,connection_id,link_digest,payload,approved_by,approved_until,firm_authority,reminder_of) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`, id, r.PathValue("request"), connection, digest, encrypted, actor, until, firmAccess(r), in.PreviousID)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Email approval could not be confirmed. Refresh Work before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	}
	mux.HandleFunc("POST /v1/closes/{id}/work/{job}/requests/{request}/email", send)
	mux.HandleFunc("POST /v1/closes/{id}/requests/{request}/email", send)
}
