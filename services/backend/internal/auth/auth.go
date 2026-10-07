package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	jwt.RegisteredClaims
	Organization string   `json:"org_id"`
	Session      string   `json:"sid"`
	Permissions  []string `json:"permissions"`
	ClientID     string   `json:"client_id"`
}
type contextKey struct{}
type agentKey struct{}

// WithAgent marks a request as made by an MCP client under a member's agent access; Agent returns it, or "".
func WithAgent(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, agentKey{}, agent)
}
func Agent(ctx context.Context) string { agent, _ := ctx.Value(agentKey{}).(string); return agent }

func Actor(ctx context.Context) *Claims { actor, _ := ctx.Value(contextKey{}).(*Claims); return actor }
func WithActor(ctx context.Context, actor *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, actor)
}

// ErrSigningKeys means the issuer's signing keys could not be fetched or do not yet include the token's key, which can
// pass once they are fetched again; any other verification error is final.
var ErrSigningKeys = errors.New("signing keys unavailable")

type Verifier struct {
	ClientID, Organization, Issuer string
	// AnyOrganization accepts a session in any organization or in none (staff by email domain, docs/admin-panel.md).
	AnyOrganization bool
	// JWKSURL and Audience configure an OAuth resource (MCP): its own key set, and an audience the token must name.
	JWKSURL, Audience string
	mu                sync.Mutex
	keys              map[string]*rsa.PublicKey
	fetched           time.Time
}

func (v *Verifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	if len(raw) > 16384 || (v.ClientID == "" && v.JWKSURL == "") || v.Issuer == "" {
		return nil, errors.New("authentication unavailable")
	}
	claims := new(Claims)
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing signing key")
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		if time.Since(v.fetched) > time.Minute {
			jwks := v.JWKSURL
			if jwks == "" {
				jwks = "https://api.workos.com/sso/jwks/" + v.ClientID
			}
			req, err := http.NewRequestWithContext(ctx, "GET", jwks, nil)
			if err != nil {
				return nil, err
			}
			response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				return nil, ErrSigningKeys
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				return nil, ErrSigningKeys
			}
			var set struct {
				Keys []struct{ Kid, Kty, Use, Alg, N, E string }
			}
			if json.NewDecoder(io.LimitReader(response.Body, 128*1024)).Decode(&set) != nil {
				return nil, fmt.Errorf("%w: invalid key set", ErrSigningKeys)
			}
			keys := map[string]*rsa.PublicKey{}
			for _, key := range set.Keys {
				if key.Kty != "RSA" || (key.Use != "" && key.Use != "sig") || (key.Alg != "" && key.Alg != "RS256") {
					continue
				}
				n, err := base64.RawURLEncoding.DecodeString(key.N)
				if err != nil {
					continue
				}
				e, err := base64.RawURLEncoding.DecodeString(key.E)
				if err != nil || len(e) > 4 {
					continue
				}
				exponent := new(big.Int).SetBytes(e).Int64()
				if exponent < 3 || len(n) < 256 {
					continue
				}
				keys[key.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent)}
			}
			v.keys = keys
			v.fetched = time.Now()
		}
		key := v.keys[kid]
		if key == nil {
			return nil, fmt.Errorf("%w: unknown signing key", ErrSigningKeys)
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.Issuer), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if errors.Is(err, ErrSigningKeys) {
		return nil, ErrSigningKeys
	}
	if err != nil || claims.Subject == "" || claims.Session == "" || (claims.Organization == "" && !v.AnyOrganization) || claims.Organization == "local-development" || (v.Organization != "" && claims.Organization != v.Organization) {
		return nil, errors.New("invalid session or organization")
	}
	if v.Audience != "" {
		if !slices.Contains(claims.Audience, v.Audience) {
			return nil, errors.New("wrong audience")
		}
	} else if len(claims.Audience) > 0 && !slices.Contains(claims.Audience, v.ClientID) {
		return nil, errors.New("wrong audience")
	}
	return claims, nil
}
func (v *Verifier) Authenticate(r *http.Request, developmentToken string) (*Claims, error) {
	value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, errors.New("sign in to access this workspace")
	}
	if v.ClientID != "" {
		return v.Verify(r.Context(), value)
	}
	if len(developmentToken) < 32 {
		return nil, errors.New("workspace authentication is not configured")
	}
	got := sha256.Sum256([]byte(value))
	want := sha256.Sum256([]byte(developmentToken))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return nil, errors.New("invalid workspace token")
	}
	return &Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "local-development"}}, nil
}
