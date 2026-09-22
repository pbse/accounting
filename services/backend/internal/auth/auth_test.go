package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifiedOrganizationSession(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := Verifier{ClientID: "client_test", Organization: "org_allowed", Issuer: "https://api.workos.com/", keys: map[string]*rsa.PublicKey{"test": &key.PublicKey}, fetched: time.Now()}
	sign := func(org, issuer string, expiry time.Time) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_test", Issuer: issuer, ExpiresAt: jwt.NewNumericDate(expiry)}, Session: "session_test", Organization: org})
		token.Header["kid"] = "test"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, test := range []struct {
		org, issuer string
		expiry      time.Time
		valid       bool
	}{{"org_allowed", verifier.Issuer, time.Now().Add(time.Minute), true}, {"org_other", verifier.Issuer, time.Now().Add(time.Minute), false}, {"org_allowed", "https://wrong.example", time.Now().Add(time.Minute), false}, {"org_allowed", verifier.Issuer, time.Now().Add(-time.Minute), false}} {
		_, err := verifier.Verify(context.Background(), sign(test.org, test.issuer, test.expiry))
		if (err == nil) != test.valid {
			t.Fatalf("unexpected verification for %s: %v", test.org, err)
		}
	}
	request := httptest.NewRequest("GET", "/v1/closes", nil)
	request.Header.Set("Authorization", "Bearer local-development-token-00000000000")
	if _, err := verifier.Authenticate(request, "local-development-token-00000000000"); err == nil {
		t.Fatal("development token bypassed WorkOS")
	}
}
