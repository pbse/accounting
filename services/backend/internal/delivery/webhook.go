package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

func ValidWebhookSecret(secret string) bool {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	return strings.HasPrefix(secret, "whsec_") && err == nil && len(key) >= 16 && len(key) <= 128
}

// Svix signs the unmodified body and delivery timestamp, not decoded JSON.
func VerifyWebhook(secret, id, timestamp, signature string, body []byte, now time.Time) bool {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || !ValidWebhookSecret(secret) || id == "" || len(id) > 200 || seconds < now.Unix()-300 || seconds > now.Unix()+300 {
		return false
	}
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(body)
	for _, part := range strings.Fields(signature) {
		if !strings.HasPrefix(part, "v1,") {
			continue
		}
		value, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(part, "v1,"))
		if err == nil && hmac.Equal(value, mac.Sum(nil)) {
			return true
		}
	}
	return false
}
