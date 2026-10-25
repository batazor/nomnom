// Package identity authenticates the customer at the BFF and carries the
// customer to bounded contexts as BFF-signed user assertions (ADR 32).
package identity

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
)

// clockLeeway tolerates skew for exp and nbf, as in ADR 27 and ADR 32.
const clockLeeway = 30 * time.Second

// ErrUnauthenticated hides why a token was rejected from the client.
var ErrUnauthenticated = errors.New("unauthenticated")

// User is the customer proven by a verified access token.
type User struct {
	ID string
}

// AccessTokenVerifier checks an access token of the identity provider:
// signature by its JWKS, issuer, the BFF audience and validity. An ID token
// (nonce, no scope) is not an access token and is rejected.
type AccessTokenVerifier struct {
	keys     map[string]crypto.PublicKey
	issuer   string
	audience string
	now      func() time.Time
}

func NewAccessTokenVerifier(issuer, audience, jwksFile string) (*AccessTokenVerifier, error) {
	if issuer == "" || audience == "" {
		return nil, errors.New("identity provider issuer and audience are required")
	}
	keys, err := LoadJWKS(jwksFile)
	if err != nil {
		return nil, err
	}
	return &AccessTokenVerifier{keys: keys, issuer: issuer, audience: audience, now: time.Now}, nil
}

// FromIncomingContext verifies the access token the Router forwards as
// metadata authorization. Client headers that name a user are never read.
func (v *AccessTokenVerifier) FromIncomingContext(ctx context.Context) (User, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	if len(values) != 1 {
		return User{}, fmt.Errorf("%w: authorization metadata must be present once", ErrUnauthenticated)
	}
	raw, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || raw == "" {
		return User{}, fmt.Errorf("%w: authorization scheme must be Bearer", ErrUnauthenticated)
	}
	return v.Verify(raw)
}

type accessClaims struct {
	jwt.RegisteredClaims
	Scope *string `json:"scope"`
	Nonce *string `json:"nonce"`
}

func (v *AccessTokenVerifier) Verify(raw string) (User, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		key, ok := v.keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown kid %q", kid)
		}
		return key, nil
	},
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return User{}, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	if claims.Nonce != nil || claims.Scope == nil {
		return User{}, fmt.Errorf("%w: id token is not an access token", ErrUnauthenticated)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return User{}, fmt.Errorf("%w: sub is required", ErrUnauthenticated)
	}
	return User{ID: claims.Subject}, nil
}
