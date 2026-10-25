package grpcadapter

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"

	"nomnom/orders/pkg/config"
)

// serviceTokenVerifier authenticates a projected ServiceAccount token (ADR 27):
// signature by the cluster JWKS, issuer, audience, validity and an explicit
// ServiceAccount allowlist.
type serviceTokenVerifier struct {
	keys     jwks
	issuer   string
	audience string
	accounts map[string]string
}

func newServiceTokenVerifier(cfg config.IdentityConfig) (serviceTokenVerifier, error) {
	accounts, err := cfg.Accounts()
	if err != nil {
		return serviceTokenVerifier{}, err
	}
	keys, err := LoadJWKS(cfg.JWKSFile)
	if err != nil {
		return serviceTokenVerifier{}, err
	}
	return serviceTokenVerifier{keys: keys, issuer: cfg.Issuer, audience: cfg.Audience, accounts: accounts}, nil
}

// verify checks the Bearer token of the call and returns the allowed service
// name.
func (v serviceTokenVerifier) verify(md metadata.MD, now func() time.Time) (string, error) {
	values := md.Get("authorization")
	if len(values) != 1 {
		return "", errors.New("authorization metadata must be present once")
	}
	raw, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || raw == "" {
		return "", errors.New("authorization scheme must be Bearer")
	}

	claims := jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, &claims, v.keys.key,
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(now),
	)
	if err != nil {
		return "", fmt.Errorf("verify token: %w", err)
	}
	service, ok := v.accounts[claims.Subject]
	if !ok {
		return "", fmt.Errorf("service account %q is not allowed", claims.Subject)
	}
	return service, nil
}
