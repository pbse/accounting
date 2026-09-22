package delivery

import (
	"testing"
	"time"
)

func TestWebhookOfficialVector(t *testing.T) {
	secret := "whsec_plJ3nmyCDGBKInavdOK15jsl"
	body := []byte(`{"event_type":"ping","data":{"success":true}}`)
	verify := func(value []byte, now time.Time) bool {
		return VerifyWebhook(secret, "msg_loFOjxBNrRLzqYUf", "1731705121", "v1,rAvfW3dJ/X/qxhsaXPOyyCGmRKsaKWcsNccKXlIktD0=", value, now)
	}
	now := time.Unix(1731705121, 0)
	if !verify(body, now) || verify(append(body, ' '), now) || verify(body, now.Add(301*time.Second)) {
		t.Fatal("Svix signature or freshness verification failed")
	}
}
