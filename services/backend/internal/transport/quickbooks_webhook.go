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
	"regexp"
	"time"

	"close.local/backend/internal/quickbooks"
)

var intuitEventType = regexp.MustCompile(`^qbo\.[a-z]+\.[a-z]+\.v[0-9]+$`)

// quickbooksWebhook records Intuit's change notifications as hints. It makes no provider call;
// the worker's scheduler refreshes the affected closes.
func quickbooksWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier := os.Getenv("QBO_WEBHOOK_VERIFIER")
		if verifier == "" {
			problem(w, 503, "QuickBooks webhooks are not configured.")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			problem(w, 413, "Notification too large.")
			return
		}
		mac := hmac.New(sha256.New, []byte(verifier))
		mac.Write(body)
		signature, err := base64.StdEncoding.DecodeString(r.Header.Get("intuit-signature"))
		if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
			problem(w, 401, "Invalid QuickBooks signature.")
			return
		}
		var events []struct {
			SpecVersion string    `json:"specversion"`
			ID          string    `json:"id"`
			Type        string    `json:"type"`
			Time        time.Time `json:"time"`
			Realm       string    `json:"intuitaccountid"`
		}
		if json.Unmarshal(body, &events) != nil {
			problem(w, 400, "Expected a CloudEvents array.")
			return
		}
		// The change time, not the delivery time, is recorded, so a replayed or retried event
		// does not refresh a close whose capture started after the change.
		realms, times := []string{}, []time.Time{}
		for _, e := range events {
			if e.SpecVersion == "1.0" && e.ID != "" && intuitEventType.MatchString(e.Type) && !e.Time.IsZero() && quickbooks.ValidRealm(e.Realm) {
				realms, times = append(realms, e.Realm), append(times, e.Time)
			}
		}
		if len(realms) == 0 {
			problem(w, 400, "No valid QuickBooks change event.")
			return
		}
		if _, err = db.ExecContext(r.Context(), `UPDATE quickbooks_connections x SET changed_at=GREATEST(x.changed_at,c.at) FROM (SELECT realm,LEAST(max(at),now()) AS at FROM unnest($1::text[],$2::timestamptz[]) v(realm,at) GROUP BY realm) c
 WHERE x.realm=c.realm AND x.state='active' AND x.environment='sandbox'`, realms, times); err != nil {
			problem(w, 503, "Change notification could not be recorded.")
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
