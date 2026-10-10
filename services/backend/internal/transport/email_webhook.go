package transport

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/delivery"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
)

func emailWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			problem(w, 503, "Email tracking unavailable.")
			return
		}
		var org string
		var encrypted []byte
		err := db.QueryRowContext(r.Context(), `SELECT organization_id,webhook_secret FROM email_connections WHERE id::text=$1 AND webhook_secret IS NOT NULL`, r.PathValue("connection")).Scan(&org, &encrypted)
		if err == sql.ErrNoRows {
			problem(w, 401, "Invalid webhook.")
			return
		}
		if err != nil {
			problem(w, 503, "Email tracking unavailable.")
			return
		}
		secret, err := secrets.Open(org+"/resend-webhook/"+r.PathValue("connection"), encrypted)
		if err != nil {
			problem(w, 503, "Email tracking unavailable.")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			problem(w, 413, "Webhook exceeds the size limit.")
			return
		}
		eventID := r.Header.Get("svix-id")
		if !delivery.VerifyWebhook(string(secret), eventID, r.Header.Get("svix-timestamp"), r.Header.Get("svix-signature"), raw, time.Now()) {
			problem(w, 401, "Invalid webhook signature or timestamp.")
			return
		}
		event, ok := deliveryEvent(w, raw)
		if !ok {
			return
		}
		recordDeliveryEvent(w, r, db, r.PathValue("connection"), eventID, raw, event)
	}
}

// krithEmailWebhook takes delivery events from Krith's own Resend account (CLIENT_EMAIL_WEBHOOK_SECRET), which every
// firm using Krith's sender shares. Each email Krith's sender sent carries its outbox's tag, which finds the firm's
// connection, even before the send's answer is saved; Krith's own welcome and invitation emails carry none and are
// ignored, as is an email whose outbox is gone.
func krithEmailWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret := delivery.KrithWebhookSecret()
		if db == nil || secret == "" {
			problem(w, 503, "Email tracking unavailable.")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			problem(w, 413, "Webhook exceeds the size limit.")
			return
		}
		eventID := r.Header.Get("svix-id")
		if !delivery.VerifyWebhook(secret, eventID, r.Header.Get("svix-timestamp"), r.Header.Get("svix-signature"), raw, time.Now()) {
			problem(w, 401, "Invalid webhook signature or timestamp.")
			return
		}
		event, ok := deliveryEvent(w, raw)
		if !ok {
			return
		}
		outbox := event.Data.Tags[delivery.OutboxTag]
		if outbox == "" {
			w.WriteHeader(204)
			return
		}
		var connection string
		err = db.QueryRowContext(r.Context(), `SELECT m.connection_id::text FROM email_outbox m JOIN email_connections x ON x.id=m.connection_id WHERE m.id=$1 AND x.provider='krith'`, outbox).Scan(&connection)
		if err == sql.ErrNoRows {
			w.WriteHeader(204)
			return
		}
		if err != nil {
			problem(w, 503, "Delivery scope unavailable.")
			return
		}
		recordDeliveryEvent(w, r, db, connection, eventID, raw, event)
	}
}

type emailEvent struct {
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      struct {
		ID   string    `json:"email_id"`
		To   []string  `json:"to"`
		Tags emailTags `json:"tags"`
	} `json:"data"`
}

// emailTags are an email's tags as Resend's events return them: an object of names and values, or a list of them.
type emailTags map[string]string

func (t *emailTags) UnmarshalJSON(raw []byte) error {
	var object map[string]string
	if json.Unmarshal(raw, &object) == nil {
		*t = object
		return nil
	}
	var list []delivery.Tag
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	*t = emailTags{}
	for _, tag := range list {
		(*t)[tag.Name] = tag.Value
	}
	return nil
}

// deliveryEvent is a signed webhook's delivery event, or ok=false once it has answered: 204 for an event type this app
// does not track, and 400 for an invalid one.
func deliveryEvent(w http.ResponseWriter, raw []byte) (event emailEvent, ok bool) {
	if json.Unmarshal(raw, &event) != nil {
		problem(w, 400, "Invalid webhook payload.")
		return event, false
	}
	kind := strings.TrimPrefix(event.Type, "email.")
	if !slices.Contains([]string{"sent", "delivered", "delivery_delayed", "bounced", "complained", "failed", "suppressed"}, kind) {
		w.WriteHeader(204)
		return event, false
	}
	if event.Type != "email."+kind || event.CreatedAt.IsZero() || event.CreatedAt.After(time.Now().Add(5*time.Minute)) || event.Data.ID == "" || len(event.Data.ID) > 200 || len(event.Data.To) != 1 || len(event.Data.To[0]) > 320 || !strings.Contains(event.Data.To[0], "@") {
		problem(w, 400, "Invalid delivery event.")
		return event, false
	}
	return event, true
}

// recordDeliveryEvent keeps one delivery event of a connection's email, linked to its close once one email matches.
func recordDeliveryEvent(w http.ResponseWriter, r *http.Request, db *sql.DB, connection, eventID string, raw []byte, event emailEvent) {
	kind := strings.TrimPrefix(event.Type, "email.")
	var closeID sql.NullString
	err := db.QueryRowContext(r.Context(), `SELECT CASE WHEN count(DISTINCT COALESCE(d.close_id,cm.close_id))=1 THEN min(COALESCE(d.close_id,cm.close_id))::text END
 FROM email_outbox m LEFT JOIN work_requests q ON q.id=m.request_id LEFT JOIN request_drafts d ON d.id=q.draft_id LEFT JOIN collection_messages cm ON cm.id=m.collection_id
 WHERE m.connection_id=$1 AND m.provider_id=$2 AND lower(COALESCE(q.recipient,cm.recipient))=lower($3)`, connection, event.Data.ID, event.Data.To[0]).Scan(&closeID)
	if err != nil {
		problem(w, 503, "Delivery scope unavailable.")
		return
	}
	digest := preparation.Digest(raw)
	// A conflicting duplicate is rejected; identical retries do not alter history.
	var saved string
	err = db.QueryRowContext(r.Context(), `INSERT INTO email_delivery_events(connection_id,event_id,body_digest,provider_id,recipient,kind,occurred_at,close_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(connection_id,event_id) DO UPDATE SET close_id=COALESCE(email_delivery_events.close_id,EXCLUDED.close_id) WHERE email_delivery_events.body_digest=EXCLUDED.body_digest AND (EXCLUDED.close_id IS NULL OR email_delivery_events.close_id IS NULL OR email_delivery_events.close_id=EXCLUDED.close_id) RETURNING body_digest`, connection, eventID, digest, event.Data.ID, event.Data.To[0], kind, event.CreatedAt, closeID).Scan(&saved)
	if err == sql.ErrNoRows {
		problem(w, 409, "Conflicting delivery event.")
		return
	}
	if err != nil {
		problem(w, 503, "Delivery event could not be persisted.")
		return
	}
	w.WriteHeader(204)
}
