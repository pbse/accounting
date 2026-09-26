package transport

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"
)

func xeroWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("XERO_WEBHOOK_KEY")
		if key == "" {
			problem(w, 503, "Xero webhooks are not configured.")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			problem(w, 413, "Notification too large.")
			return
		}
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(body)
		signature, err := base64.StdEncoding.DecodeString(r.Header.Get("x-xero-signature"))
		if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			Events []struct {
				TenantID, EventCategory, EventDateUtc string
			} `json:"events"`
		}
		if json.Unmarshal(body, &payload) != nil {
			problem(w, 400, "Expected a Xero events object.")
			return
		}
		tenants, times, now := []string{}, []time.Time{}, time.Now()
		for _, e := range payload.Events {
			at, err := time.Parse(time.RFC3339Nano, e.EventDateUtc)
			if err != nil {
				at, err = time.Parse("2006-01-02T15:04:05.999999999", e.EventDateUtc)
			}
			if err == nil && (e.EventCategory == "INVOICE" || e.EventCategory == "CREDITNOTE") && !at.After(now) {
				tenants, times = append(tenants, e.TenantID), append(times, at)
			}
		}
		if len(tenants) > 0 {
			if _, err = db.ExecContext(r.Context(), `UPDATE xero_connections x SET changed_at=GREATEST(x.changed_at,c.at) FROM (SELECT tenant,max(at) AS at FROM unnest($1::text[],$2::timestamptz[]) v(tenant,at) GROUP BY tenant) c
 WHERE x.tenant_id=c.tenant`, tenants, times); err != nil {
				problem(w, 503, "Change notification could not be recorded.")
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}
