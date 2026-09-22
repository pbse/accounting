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
		var event struct {
			Type      string    `json:"type"`
			CreatedAt time.Time `json:"created_at"`
			Data      struct {
				ID string   `json:"email_id"`
				To []string `json:"to"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &event) != nil {
			problem(w, 400, "Invalid webhook payload.")
			return
		}
		kind := strings.TrimPrefix(event.Type, "email.")
		if !slices.Contains([]string{"sent", "delivered", "delivery_delayed", "bounced", "complained", "failed", "suppressed"}, kind) {
			w.WriteHeader(204)
			return
		}
		if event.Type != "email."+kind || event.CreatedAt.IsZero() || event.CreatedAt.After(time.Now().Add(5*time.Minute)) || event.Data.ID == "" || len(event.Data.ID) > 200 || len(event.Data.To) != 1 || len(event.Data.To[0]) > 320 || !strings.Contains(event.Data.To[0], "@") {
			problem(w, 400, "Invalid delivery event.")
			return
		}
		digest := preparation.Digest(raw)
		// A conflicting duplicate is rejected; identical retries do not alter history.
		var saved string
		err = db.QueryRowContext(r.Context(), `INSERT INTO email_delivery_events(connection_id,event_id,body_digest,provider_id,recipient,kind,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(connection_id,event_id) DO UPDATE SET event_id=EXCLUDED.event_id RETURNING body_digest`, r.PathValue("connection"), eventID, digest, event.Data.ID, event.Data.To[0], kind, event.CreatedAt).Scan(&saved)
		if err != nil {
			problem(w, 503, "Delivery event could not be persisted.")
			return
		}
		if saved != digest {
			problem(w, 409, "Conflicting delivery event.")
			return
		}
		w.WriteHeader(204)
	}
}
