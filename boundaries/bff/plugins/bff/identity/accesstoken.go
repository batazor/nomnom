// Package identity authenticates the customer at the BFF and carries the
// customer to bounded contexts as BFF-signed user assertions (ADR 32).
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
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

// Provider is an identity provider whose access tokens BFF accepts: its
// issuer and where its JWKS is. A JWKS URL is fetched and refreshed (Dex
// rotates its keys, ADR 35); a JWKS file is read once.
type Provider struct {
	Issuer   string `json:"issuer"`
	JWKSURL  string `json:"jwksUrl,omitempty"`
	JWKSFile string `json:"jwksFile,omitempty"`
}

// AccessTokenVerifier checks an access token of a trusted identity provider:
// signature by the JWKS of the provider named by iss, the BFF audience and
// validity. An ID token (nonce) is not an access token and is rejected.
type AccessTokenVerifier struct {
	keys     map[string]jwt.Keyfunc
	audience string
	now      func() time.Time
}

// NewAccessTokenVerifier binds every issuer to its own JWKS: a key of one
// provider never proves a token that names another issuer. ctx bounds the
// background refresh of JWKS URLs.
func NewAccessTokenVerifier(ctx context.Context, audience string, providers []Provider) (*AccessTokenVerifier, error) {
	if audience == "" || len(providers) == 0 {
		return nil, errors.New("identity provider audience and at least one provider are required")
	}
	keys := make(map[string]jwt.Keyfunc, len(providers))
	for _, p := range providers {
		if p.Issuer == "" || (p.JWKSURL == "") == (p.JWKSFile == "") {
			return nil, fmt.Errorf("identity provider %q needs an issuer and exactly one of jwksUrl, jwksFile", p.Issuer)
		}
		if _, ok := keys[p.Issuer]; ok {
			return nil, fmt.Errorf("identity provider %q is listed twice", p.Issuer)
		}
		if p.JWKSFile != "" {
			fileKeys, err := LoadJWKS(p.JWKSFile)
			if err != nil {
				return nil, err
			}
			keys[p.Issuer] = func(token *jwt.Token) (any, error) {
				kid, _ := token.Header["kid"].(string)
				key, ok := fileKeys[kid]
				if !ok {
					return nil, fmt.Errorf("unknown kid %q", kid)
				}
				return key, nil
			}
			continue
		}
		remote, err := keyfunc.NewDefaultCtx(ctx, []string{p.JWKSURL})
		if err != nil {
			return nil, fmt.Errorf("jwks %s: %w", p.JWKSURL, err)
		}
		keys[p.Issuer] = remote.Keyfunc
	}
	return &AccessTokenVerifier{keys: keys, audience: audience, now: time.Now}, nil
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
	Nonce *string `json:"nonce"`
}

func (v *AccessTokenVerifier) Verify(raw string) (User, error) {
	var claims accessClaims
	// Claims are decoded before the key is chosen: iss selects the provider,
	// an unknown issuer has no key.
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		keys, ok := v.keys[claims.Issuer]
		if !ok {
			return nil, fmt.Errorf("unknown issuer %q", claims.Issuer)
		}
		return keys(token)
	},
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return User{}, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	// Dex issues access tokens in the shape of ID tokens, without scope; only
	// nonce tells an ID token apart, so the UI never sends one (ADR 35).
	if claims.Nonce != nil {
		return User{}, fmt.Errorf("%w: id token is not an access token", ErrUnauthenticated)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return User{}, fmt.Errorf("%w: sub is required", ErrUnauthenticated)
	}
	return User{ID: claims.Subject}, nil
}
