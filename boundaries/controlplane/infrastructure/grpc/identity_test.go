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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
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
		ServiceAccounts: v.ServiceAccounts + ",nomnom-bff/bff=bff",
		UserIssuer:      "nomnom-bff",
		UserJWKSFile:    filepath.Join(fixtures, "bff-jwks.json"),
		UserDelegates:   "bff",
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
			assert.Equal(t, application.Subject{Kind: application.SubjectService, ID: v.ExpectedService, Service: v.ExpectedService}, subject)
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
func TestSubjectResolverRejectsMissingOrUnusableJWKS(t *testing.T) {
	_, cfg := loadVectors(t)
	unset := cfg
	unset.Issuer = ""
	_, err := NewSubjectResolver(unset)
	require.ErrorContains(t, err, config.EnvIdentityIssuer+" is required")

	cfg.JWKSFile = filepath.Join(t.TempDir(), "missing.json")
	_, err = NewSubjectResolver(cfg)
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

type userVectors struct {
	Issuer           string            `json:"issuer"`
	Audience         string            `json:"audience"`
	Now              int64             `json:"now"`
	Delegates        string            `json:"delegates"`
	ServiceIssuer    string            `json:"service_issuer"`
	ServiceAccounts  string            `json:"service_accounts"`
	DelegateToken    string            `json:"delegate_token"`
	NonDelegateToken string            `json:"non_delegate_token"`
	ExpectedSubject  string            `json:"expected_subject"`
	Accepted         []string          `json:"accepted"`
	Rejected         []string          `json:"rejected"`
	Tokens           map[string]string `json:"tokens"`
}

func loadUserVectors(t *testing.T) (userVectors, config.IdentityConfig) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, "user-vectors.json"))
	require.NoError(t, err)
	var v userVectors
	require.NoError(t, json.Unmarshal(data, &v))
	return v, config.IdentityConfig{
		Issuer:          v.ServiceIssuer,
		Audience:        v.Audience,
		JWKSFile:        filepath.Join(fixtures, "jwks.json"),
		ServiceAccounts: v.ServiceAccounts,
		UserIssuer:      v.Issuer,
		UserJWKSFile:    filepath.Join(fixtures, "bff-jwks.json"),
		UserDelegates:   v.Delegates,
	}
}

// newUserResolver pins the clock to the vectors' now: assertions live at most 60 s.
func newUserResolver(t *testing.T, v userVectors, cfg config.IdentityConfig) SubjectResolver {
	t.Helper()
	resolver, err := NewSubjectResolver(cfg)
	require.NoError(t, err)
	require.IsType(t, &ServiceTokenSubjectResolver{}, resolver)
	resolver.(*ServiceTokenSubjectResolver).now = func() time.Time { return time.Unix(v.Now, 0) }
	return resolver
}

func withAssertion(serviceToken string, assertions ...string) context.Context {
	md := metadata.Pairs("authorization", "Bearer "+serviceToken)
	for _, assertion := range assertions {
		md.Append(UserAssertionMetadata, assertion)
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

// AC-525, AC-527, AC-528: the shared user vectors are accepted and rejected like in every context.
func TestUserAssertionSharedVectors(t *testing.T) {
	v, cfg := loadUserVectors(t)
	resolver := newUserResolver(t, v, cfg)

	for _, name := range v.Accepted {
		t.Run(name, func(t *testing.T) {
			subject, err := resolver.Resolve(withAssertion(v.DelegateToken, v.Tokens[name]))
			require.NoError(t, err)
			assert.Equal(t, application.Subject{Kind: application.SubjectUser, ID: v.ExpectedSubject, Service: "bff"}, subject)
		})
	}
	for _, name := range v.Rejected {
		t.Run(name, func(t *testing.T) {
			_, err := resolver.Resolve(withAssertion(v.DelegateToken, v.Tokens[name]))
			require.ErrorIs(t, err, ErrUnauthenticated)
		})
	}
}

// AC-526: a valid assertion from a service outside the delegates is rejected.
func TestUserAssertionRequiresDelegate(t *testing.T) {
	v, cfg := loadUserVectors(t)
	resolver := newUserResolver(t, v, cfg)

	_, err := resolver.Resolve(withAssertion(v.NonDelegateToken, v.Tokens["valid"]))
	require.ErrorIs(t, err, ErrUnauthenticated)

	subject, err := resolver.Resolve(withAssertion(v.NonDelegateToken))
	require.NoError(t, err)
	assert.Equal(t, application.Subject{Kind: application.SubjectService, ID: "orders-worker", Service: "orders-worker"}, subject)
}

func TestUserAssertionRequiresServiceToken(t *testing.T) {
	v, cfg := loadUserVectors(t)
	resolver := newUserResolver(t, v, cfg)

	md := metadata.Pairs(UserAssertionMetadata, v.Tokens["valid"])
	_, err := resolver.Resolve(metadata.NewIncomingContext(context.Background(), md))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestUserAssertionMustBePresentOnce(t *testing.T) {
	v, cfg := loadUserVectors(t)
	resolver := newUserResolver(t, v, cfg)

	for name, ctx := range map[string]context.Context{
		"empty":    withAssertion(v.DelegateToken, ""),
		"repeated": withAssertion(v.DelegateToken, v.Tokens["valid"], v.Tokens["valid"]),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolver.Resolve(ctx)
			require.ErrorIs(t, err, ErrUnauthenticated)
		})
	}
}

// AC-529: user assertion settings are required: a missing one stops the start.
func TestSubjectResolverRequiresUserSettings(t *testing.T) {
	_, full := loadUserVectors(t)
	for name, cfg := range map[string]config.IdentityConfig{
		config.EnvIdentityUserJWKSFile:  func() config.IdentityConfig { c := full; c.UserJWKSFile = ""; return c }(),
		config.EnvIdentityUserIssuer:    func() config.IdentityConfig { c := full; c.UserIssuer = " "; return c }(),
		config.EnvIdentityUserDelegates: func() config.IdentityConfig { c := full; c.UserDelegates = " , "; return c }(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewSubjectResolver(cfg)
			require.ErrorContains(t, err, name+" is required")
		})
	}
}

func TestSubjectResolverRejectsUnusableUserJWKS(t *testing.T) {
	_, cfg := loadUserVectors(t)
	dir := t.TempDir()
	for name, content := range map[string]string{
		"unparseable": `{"keys":`,
		"empty":       `{"keys":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".json")
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			cfg.UserJWKSFile = path
			_, err := NewSubjectResolver(cfg)
			require.Error(t, err)
		})
	}

	t.Run("rsa key", func(t *testing.T) {
		cfg.UserJWKSFile = filepath.Join(fixtures, "jwks.json")
		_, err := NewSubjectResolver(cfg)
		require.ErrorContains(t, err, "must be EC P-256")
	})
	t.Run("missing file", func(t *testing.T) {
		cfg.UserJWKSFile = filepath.Join(dir, "missing.json")
		_, err := NewSubjectResolver(cfg)
		require.Error(t, err)
	})
}

// withExtraKey writes the JWKS fixture with one more key added.
func withExtraKey(t *testing.T, fixture string, extra map[string]any) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, fixture))
	require.NoError(t, err)
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(data, &set))
	set.Keys = append(set.Keys, extra)
	out, err := json.Marshal(set)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "jwks.json")
	require.NoError(t, os.WriteFile(path, out, 0o600))
	return path
}

func TestLoadJWKSRejectsUnsupportedKeys(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixtures, "jwks.json"))
	require.NoError(t, err)
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(data, &set))
	clone := func(change func(map[string]any)) map[string]any {
		key := map[string]any{}
		for name, value := range set.Keys[0] {
			key[name] = value
		}
		change(key)
		return key
	}
	for name, extra := range map[string]map[string]any{
		"oct":       {"kty": "oct", "kid": "hmac", "k": "c2VjcmV0"},
		"enc":       clone(func(k map[string]any) { k["kid"], k["use"] = "encryption", "enc" }),
		"no kid":    clone(func(k map[string]any) { delete(k, "kid") }),
		"wrong alg": clone(func(k map[string]any) { k["kid"], k["alg"] = "rs512", "RS512" }),
		"duplicate": clone(func(map[string]any) {}),
	} {
		_, err := LoadJWKS(withExtraKey(t, "jwks.json", extra))
		require.Error(t, err, name)
	}
}
