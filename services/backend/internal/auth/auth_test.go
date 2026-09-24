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

func TestOAuthResourceTokensMustNameTheResource(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := Verifier{Issuer: "https://auth.example.com", JWKSURL: "https://auth.example.com/oauth2/jwks", Audience: "https://api.example.com/mcp", keys: map[string]*rsa.PublicKey{"test": &key.PublicKey}, fetched: time.Now()}
	sign := func(audience []string, issuer, session string) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_test", Issuer: issuer, Audience: audience, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}, Session: session, Organization: "org_firm"})
		token.Header["kid"] = "test"
		raw, _ := token.SignedString(key)
		return raw
	}
	for _, c := range []struct {
		audience        []string
		issuer, session string
		valid           bool
	}{
		{[]string{"https://api.example.com/mcp"}, verifier.Issuer, "consent_1", true},
		{nil, verifier.Issuer, "consent_1", false},
		{[]string{"client_other"}, verifier.Issuer, "consent_1", false},
		{[]string{"https://api.example.com/mcp"}, "https://api.workos.com/", "consent_1", false},
		{[]string{"https://api.example.com/mcp"}, verifier.Issuer, "", false},
	} {
		if _, err := verifier.Verify(context.Background(), sign(c.audience, c.issuer, c.session)); (err == nil) != c.valid {
			t.Fatalf("audience %v issuer %s session %q: %v", c.audience, c.issuer, c.session, err)
		}
	}
}
