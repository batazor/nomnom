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

// clockLeeway tolerates skew between nodes for exp and nbf (ADR 27).
const clockLeeway = 30 * time.Second

// ServiceTokenSubjectResolver authenticates a projected ServiceAccount token
// (ADR 27): signature by the cluster JWKS, issuer, audience, validity and an
// explicit ServiceAccount allowlist. It yields only service subjects.
type ServiceTokenSubjectResolver struct {
	keys     map[string]crypto.PublicKey
	issuer   string
	audience string
	accounts map[string]string
	now      func() time.Time
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
	return &ServiceTokenSubjectResolver{
		keys: keys, issuer: cfg.Issuer, audience: cfg.Audience, accounts: accounts, now: time.Now,
	}, nil
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
	values := md.Get("authorization")
	if len(values) != 1 {
		return application.Subject{}, errors.New("authorization metadata must be present once")
	}
	raw, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || raw == "" {
		return application.Subject{}, errors.New("authorization scheme must be Bearer")
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
		return application.Subject{}, fmt.Errorf("verify token: %w", err)
	}
	service, ok := r.accounts[claims.Subject]
	if !ok {
		return application.Subject{}, fmt.Errorf("service account %q is not allowed", claims.Subject)
	}
	return application.Subject{Kind: application.SubjectService, ID: service}, nil
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

func decodeInt(value string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("invalid base64url integer")
	}
	return new(big.Int).SetBytes(raw), nil
}
