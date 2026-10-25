package grpcadapter

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
)

const (
	// clockLeeway tolerates skew between nodes for exp and nbf (ADR 27).
	clockLeeway = 30 * time.Second
	// maxAssertionLifetime caps exp - iat of a user assertion (ADR 32).
	maxAssertionLifetime = 60 * time.Second

	// UserAssertionMetadata carries the user assertion signed by BFF (ADR 32).
	UserAssertionMetadata = "x-nomnom-user-assertion"
)

// ServiceTokenSubjectResolver authenticates a projected ServiceAccount token
// (ADR 27): signature by the cluster JWKS, issuer, audience, validity and an
// explicit ServiceAccount allowlist. A call that also carries a user assertion
// from an allowed delegate yields the asserted user (ADR 32); otherwise it
// yields the service subject.
type ServiceTokenSubjectResolver struct {
	keys     map[string]crypto.PublicKey
	issuer   string
	audience string
	accounts map[string]string
	// user is nil when user assertions are not configured: every one is rejected.
	user *userAssertionVerifier
	now  func() time.Time
}

// userAssertionVerifier checks user assertions signed by BFF (ADR 32).
type userAssertionVerifier struct {
	keys      map[string]crypto.PublicKey
	issuer    string
	delegates map[string]bool
}

// NewSubjectResolver returns the token resolver when identity is configured
// and the rejecting resolver otherwise (fail closed).
func NewSubjectResolver(cfg config.IdentityConfig) (SubjectResolver, error) {
	if !cfg.Verifying() {
		log.Printf("WARNING: %s, %s or %s is not set: every caller is rejected",
			config.EnvIdentityIssuer, config.EnvIdentityAudience, config.EnvIdentityJWKSFile)
		return NewRejectingSubjectResolver(), nil
	}
	accounts, err := cfg.Accounts()
	if err != nil {
		return nil, err
	}
	keys, err := LoadJWKS(cfg.JWKSFile)
	if err != nil {
		return nil, err
	}
	user, err := newUserAssertionVerifier(cfg)
	if err != nil {
		return nil, err
	}
	return &ServiceTokenSubjectResolver{
		keys: keys, issuer: cfg.Issuer, audience: cfg.Audience, accounts: accounts, user: user, now: time.Now,
	}, nil
}

// newUserAssertionVerifier returns nil when user assertions are not fully
// configured. A configured but unusable BFF JWKS fails startup.
func newUserAssertionVerifier(cfg config.IdentityConfig) (*userAssertionVerifier, error) {
	var keys map[string]crypto.PublicKey
	if strings.TrimSpace(cfg.UserJWKSFile) != "" {
		var err error
		if keys, err = LoadUserJWKS(cfg.UserJWKSFile); err != nil {
			return nil, err
		}
	}
	if !cfg.UserVerifying() {
		log.Printf("%s, %s or %s is not set: user assertions are rejected",
			config.EnvIdentityUserIssuer, config.EnvIdentityUserJWKSFile, config.EnvIdentityUserDelegates)
		return nil, nil
	}
	return &userAssertionVerifier{keys: keys, issuer: cfg.UserIssuer, delegates: cfg.Delegates()}, nil
}

func (r *ServiceTokenSubjectResolver) Resolve(ctx context.Context) (application.Subject, error) {
	subject, err := r.resolve(ctx)
	if err != nil {
		log.Printf("caller identity rejected: %v", err)
		return application.Subject{}, ErrUnauthenticated
	}
	return subject, nil
}

func (r *ServiceTokenSubjectResolver) resolve(ctx context.Context) (application.Subject, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	service, err := r.authenticateService(md)
	if err != nil {
		return application.Subject{}, err
	}
	assertions := md.Get(UserAssertionMetadata)
	if len(assertions) == 0 {
		return application.Subject{Kind: application.SubjectService, ID: service}, nil
	}
	return r.resolveUser(service, assertions)
}

// resolveUser accepts the asserted user only from an allowed delegate. The
// delegate and jti are logged for audit; the assertion itself never is.
func (r *ServiceTokenSubjectResolver) resolveUser(delegate string, assertions []string) (application.Subject, error) {
	if len(assertions) != 1 || assertions[0] == "" {
		return application.Subject{}, fmt.Errorf("%s metadata must be present once", UserAssertionMetadata)
	}
	if r.user == nil {
		return application.Subject{}, fmt.Errorf("user assertion from %s: user assertions are not configured", delegate)
	}
	if !r.user.delegates[delegate] {
		return application.Subject{}, fmt.Errorf("user assertion from %s: service is not an allowed delegate", delegate)
	}
	claims, err := r.user.verify(assertions[0], r.audience, r.now)
	if err != nil {
		return application.Subject{}, fmt.Errorf("user assertion from %s (jti %q): %w", delegate, claims.ID, err)
	}
	log.Printf("user assertion accepted: delegate=%s jti=%s", delegate, claims.ID)
	return application.Subject{Kind: application.SubjectUser, ID: claims.Subject}, nil
}

// verify checks an ES256 assertion: kid in the BFF JWKS, issuer, audience,
// exp/nbf with leeway, iat, lifetime, sub and jti. Claims are returned even on
// failure so that the caller can log the unverified jti.
func (v *userAssertionVerifier) verify(raw, audience string, now func() time.Time) (jwt.RegisteredClaims, error) {
	claims := jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("key id is required")
		}
		key, ok := v.keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown key id %q", kid)
		}
		return key, nil
	},
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

// authenticateService verifies the projected ServiceAccount token and returns
// the allowed service name.
func (r *ServiceTokenSubjectResolver) authenticateService(md metadata.MD) (string, error) {
	values := md.Get("authorization")
	if len(values) != 1 {
		return "", errors.New("authorization metadata must be present once")
	}
	raw, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || raw == "" {
		return "", errors.New("authorization scheme must be Bearer")
	}

	claims := jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		key, ok := r.keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown key id %q", kid)
		}
		return key, nil
	},
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
		jwt.WithIssuer(r.issuer),
		jwt.WithAudience(r.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(r.now),
	)
	if err != nil {
		return "", fmt.Errorf("verify token: %w", err)
	}
	service, ok := r.accounts[claims.Subject]
	if !ok {
		return "", fmt.Errorf("service account %q is not allowed", claims.Subject)
	}
	return service, nil
}

// LoadJWKS reads RSA and P-256 signing keys by kid; an unusable file fails startup.
func LoadJWKS(path string) (map[string]crypto.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read jwks %s: %w", path, err)
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parse jwks %s: %w", path, err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		switch k.Kty {
		case "RSA":
			n, errN := decodeInt(k.N)
			e, errE := decodeInt(k.E)
			if errN != nil || errE != nil || !e.IsInt64() {
				return nil, fmt.Errorf("jwks %s: invalid RSA key %q", path, k.Kid)
			}
			keys[k.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
		case "EC":
			x, errX := base64.RawURLEncoding.DecodeString(k.X)
			y, errY := base64.RawURLEncoding.DecodeString(k.Y)
			if k.Crv != "P-256" || errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
				return nil, fmt.Errorf("jwks %s: invalid EC key %q", path, k.Kid)
			}
			key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
			if err != nil {
				return nil, fmt.Errorf("jwks %s: invalid EC key %q: %w", path, k.Kid, err)
			}
			keys[k.Kid] = key
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("jwks %s has no signing keys", path)
	}
	return keys, nil
}

// LoadUserJWKS reads the BFF assertion keys; only EC P-256 keys are allowed.
func LoadUserJWKS(path string) (map[string]crypto.PublicKey, error) {
	keys, err := LoadJWKS(path)
	if err != nil {
		return nil, err
	}
	for kid, key := range keys {
		if _, ok := key.(*ecdsa.PublicKey); !ok {
			return nil, fmt.Errorf("jwks %s: key %q must be EC P-256", path, kid)
		}
	}
	return keys, nil
}

func decodeInt(value string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("invalid base64url integer")
	}
	return new(big.Int).SetBytes(raw), nil
}
