package grpcadapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
)

const fixtures = "../../../../tests/identity/fixtures"

type vectors struct {
	Issuer          string            `json:"issuer"`
	Audience        string            `json:"audience"`
	ServiceAccounts string            `json:"service_accounts"`
	ExpectedService string            `json:"expected_service"`
	Accepted        []string          `json:"accepted"`
	Rejected        []string          `json:"rejected"`
	Tokens          map[string]string `json:"tokens"`
}

func loadVectors(t *testing.T) (vectors, config.IdentityConfig) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, "vectors.json"))
	require.NoError(t, err)
	var v vectors
	require.NoError(t, json.Unmarshal(data, &v))
	return v, config.IdentityConfig{
		Issuer:          v.Issuer,
		Audience:        v.Audience,
		JWKSFile:        filepath.Join(fixtures, "jwks.json"),
		ServiceAccounts: v.ServiceAccounts,
	}
}

func withAuthorization(value string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", value))
}

// AC-459…AC-461: the shared vectors are accepted and rejected like in every context.
func TestServiceTokenResolverSharedVectors(t *testing.T) {
	v, cfg := loadVectors(t)
	resolver, err := NewSubjectResolver(cfg)
	require.NoError(t, err)
	require.IsType(t, &ServiceTokenSubjectResolver{}, resolver)

	for _, name := range v.Accepted {
		t.Run(name, func(t *testing.T) {
			subject, err := resolver.Resolve(withAuthorization("Bearer " + v.Tokens[name]))
			require.NoError(t, err)
			assert.Equal(t, application.Subject{Kind: application.SubjectService, ID: v.ExpectedService}, subject)
		})
	}
	for _, name := range v.Rejected {
		t.Run(name, func(t *testing.T) {
			_, err := resolver.Resolve(withAuthorization("Bearer " + v.Tokens[name]))
			require.ErrorIs(t, err, ErrUnauthenticated)
		})
	}
}

func TestServiceTokenResolverRejectsMalformedAuthorization(t *testing.T) {
	v, cfg := loadVectors(t)
	resolver, err := NewSubjectResolver(cfg)
	require.NoError(t, err)

	for name, ctx := range map[string]context.Context{
		"missing":    context.Background(),
		"basic":      withAuthorization("Basic " + v.Tokens["valid"]),
		"bare token": withAuthorization(v.Tokens["valid"]),
		"empty":      withAuthorization("Bearer "),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolver.Resolve(ctx)
			require.ErrorIs(t, err, ErrUnauthenticated)
		})
	}
}

// AC-462: without identity configuration every caller is rejected.
func TestSubjectResolverFailsClosedWithoutConfiguration(t *testing.T) {
	v, cfg := loadVectors(t)
	cfg.Issuer = ""

	resolver, err := NewSubjectResolver(cfg)
	require.NoError(t, err)

	_, err = resolver.Resolve(withAuthorization("Bearer " + v.Tokens["valid"]))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestSubjectResolverRejectsUnusableJWKS(t *testing.T) {
	_, cfg := loadVectors(t)
	cfg.JWKSFile = filepath.Join(t.TempDir(), "missing.json")
	_, err := NewSubjectResolver(cfg)
	require.Error(t, err)

	empty := filepath.Join(t.TempDir(), "empty.json")
	require.NoError(t, os.WriteFile(empty, []byte(`{"keys":[]}`), 0o600))
	cfg.JWKSFile = empty
	_, err = NewSubjectResolver(cfg)
	require.Error(t, err)
}

func TestLoadJWKSReadsP256Keys(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	raw, err := key.PublicKey.Bytes()
	require.NoError(t, err)
	encode := base64.RawURLEncoding.EncodeToString
	path := filepath.Join(t.TempDir(), "jwks.json")
	jwks := `{"keys":[{"kty":"EC","crv":"P-256","kid":"ec-1","x":"` + encode(raw[1:33]) + `","y":"` + encode(raw[33:]) + `"}]}`
	require.NoError(t, os.WriteFile(path, []byte(jwks), 0o600))

	keys, err := LoadJWKS(path)

	require.NoError(t, err)
	assert.True(t, key.PublicKey.Equal(keys["ec-1"]))
}
