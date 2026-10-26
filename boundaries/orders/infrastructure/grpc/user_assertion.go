package grpcadapter

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"nomnom/orders/pkg/config"
)

// userAssertionVerifier checks user assertions signed by BFF (ADR 32) and
// accepts them only from an allowed delegate service.
type userAssertionVerifier struct {
	keys      jwks
	issuer    string
	delegates map[string]bool
}

// newUserAssertionVerifier requires the BFF issuer, JWKS and delegates; a
// missing setting or an unusable BFF JWKS fails startup.
func newUserAssertionVerifier(cfg config.IdentityConfig) (*userAssertionVerifier, error) {
	if !cfg.UserVerifying() {
		return nil, fmt.Errorf("%s, %s and %s are required",
			config.EnvIdentityUserIssuer, config.EnvIdentityUserJWKSFile, config.EnvIdentityUserDelegates)
	}
	keys, err := LoadUserJWKS(cfg.UserJWKSFile)
	if err != nil {
		return nil, err
	}
	return &userAssertionVerifier{keys: keys, issuer: cfg.UserIssuer, delegates: cfg.Delegates()}, nil
}

// accepts reports whether the service may assert a user.
func (v *userAssertionVerifier) accepts(delegate string) bool { return v.delegates[delegate] }

// verify checks an ES256 assertion: kid in the BFF JWKS, issuer, audience,
// exp/nbf with leeway, iat, lifetime, sub and jti. Claims are returned even on
// failure so that the caller can log the unverified jti.
func (v *userAssertionVerifier) verify(raw, audience string, now func() time.Time) (jwt.RegisteredClaims, error) {
	claims := jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, &claims, v.keys.key,
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(now),
	)
	switch {
	case err != nil:
		return claims, fmt.Errorf("verify assertion: %w", err)
	case claims.IssuedAt == nil:
		return claims, errors.New("iat is required")
	case claims.ExpiresAt.Sub(claims.IssuedAt.Time) > maxAssertionLifetime:
		return claims, fmt.Errorf("lifetime exceeds %s", maxAssertionLifetime)
	case strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.Subject) != claims.Subject:
		return claims, errors.New("sub must be a non-blank string without surrounding spaces")
	case claims.ID == "":
		return claims, errors.New("jti is required")
	}
	return claims, nil
}
