package identity

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const fixtures = "../../../../../tests/identity/fixtures"

type idpVectors struct {
	Issuer          string            `json:"issuer"`
	Audience        string            `json:"audience"`
	ExpectedSubject string            `json:"expected_subject"`
	Accepted        []string          `json:"accepted"`
	Rejected        []string          `json:"rejected"`
	Tokens          map[string]string `json:"tokens"`
}

func loadIdPVectors(t *testing.T) idpVectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, "idp-vectors.json"))
	require.NoError(t, err)
	var v idpVectors
	require.NoError(t, json.Unmarshal(data, &v))
	return v
}

func newVerifier(t *testing.T, v idpVectors) *AccessTokenVerifier {
	t.Helper()
	verifier, err := NewAccessTokenVerifier(v.Issuer, v.Audience, filepath.Join(fixtures, "idp-jwks.json"))
	require.NoError(t, err)
	return verifier
}

func newSigner(t *testing.T) *AssertionSigner {
	t.Helper()
	signer, err := NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	return signer
}

// AC-531: only a valid access token of the identity provider proves a user;
// an ID token is not an access token.
func TestAccessTokenVerifierOnSharedVectors(t *testing.T) {
	v := loadIdPVectors(t)
	verifier := newVerifier(t, v)
	for _, name := range v.Accepted {
		user, err := verifier.Verify(v.Tokens[name])
		require.NoError(t, err, name)
		assert.Equal(t, User{ID: v.ExpectedSubject}, user, name)
	}
	for _, name := range v.Rejected {
		_, err := verifier.Verify(v.Tokens[name])
		assert.ErrorIs(t, err, ErrUnauthenticated, name)
	}
}

func TestAccessTokenFromIncomingContextRequiresBearer(t *testing.T) {
	v := loadIdPVectors(t)
	verifier := newVerifier(t, v)
	for name, md := range map[string]metadata.MD{
		"missing":      {},
		"not bearer":   metadata.Pairs("authorization", "Basic abc"),
		"twice":        metadata.Pairs("authorization", "Bearer a", "authorization", "Bearer b"),
		"only user id": metadata.Pairs("x-user-id", "customer-2"),
	} {
		_, err := verifier.FromIncomingContext(metadata.NewIncomingContext(context.Background(), md))
		assert.ErrorIs(t, err, ErrUnauthenticated, name)
	}
}

type userVectors struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
}

// FR-474, AC-530: the assertion carries the subject of the verified token,
// not a client header, and passes the rules contexts apply.
func TestAssertionCarriesVerifiedSubject(t *testing.T) {
	v := loadIdPVectors(t)
	md := metadata.Pairs("authorization", "Bearer "+v.Tokens["valid"], "x-user-id", "customer-2")
	user, err := newVerifier(t, v).FromIncomingContext(metadata.NewIncomingContext(context.Background(), md))
	require.NoError(t, err)

	signed, err := newSigner(t).Sign(user, "nomnom.orders")
	require.NoError(t, err)

	keys, err := LoadJWKS(filepath.Join(fixtures, "bff-jwks.json"))
	require.NoError(t, err)
	var claims jwt.RegisteredClaims
	token, err := jwt.ParseWithClaims(signed, &claims, func(token *jwt.Token) (any, error) {
		return keys[token.Header["kid"].(string)], nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer(AssertionIssuer), jwt.WithAudience("nomnom.orders"))
	require.NoError(t, err)
	assert.Equal(t, "nomnom-bff-test-key", token.Header["kid"])
	assert.Equal(t, "customer-1", claims.Subject)
	assert.NotEmpty(t, claims.ID)
	assert.Equal(t, assertionTTL, claims.ExpiresAt.Sub(claims.IssuedAt.Time))
}

func TestAssertionSignerRejectsUnusableKey(t *testing.T) {
	_, err := NewAssertionSigner(filepath.Join(fixtures, "idp-signing-key.pem"), "kid")
	assert.Error(t, err, "RSA key")
	_, err = NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "")
	assert.Error(t, err, "empty kid")
	_, err = newSigner(t).Sign(User{}, "nomnom.orders")
	assert.Error(t, err, "no user")
}

type fixedTokens map[string]string

func (f fixedTokens) Token(audience string) (string, error) {
	token, ok := f[audience]
	if !ok {
		return "", errors.New("no token")
	}
	return token, nil
}

func invoke(t *testing.T, ctx context.Context, tokens TokenSource) (metadata.MD, error) {
	t.Helper()
	var sent metadata.MD
	interceptor := UnaryClientInterceptor("nomnom.orders", tokens, newSigner(t))
	err := interceptor(ctx, "/nomnom.orders.v1.OrdersService/GetOrder", nil, nil, nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			sent, _ = metadata.FromOutgoingContext(ctx)
			return nil
		})
	return sent, err
}

func TestInterceptorSendsServiceTokenAndAssertionForUser(t *testing.T) {
	tokens := fixedTokens{"nomnom.orders": "service-token"}

	withUser, err := invoke(t, WithUser(context.Background(), User{ID: "customer-1"}), tokens)
	require.NoError(t, err)
	assert.Equal(t, []string{"Bearer service-token"}, withUser.Get("authorization"))
	require.Len(t, withUser.Get(AssertionMetadata), 1)

	withoutUser, err := invoke(t, context.Background(), tokens)
	require.NoError(t, err)
	assert.Equal(t, []string{"Bearer service-token"}, withoutUser.Get("authorization"))
	assert.Empty(t, withoutUser.Get(AssertionMetadata))

	_, err = invoke(t, context.Background(), fixedTokens{})
	assert.Error(t, err)
}

type fakeSigner struct{ audience string }

func (f *fakeSigner) Sign(user User, audience string) (string, error) {
	f.audience = audience
	return "assertion-of-" + user.ID, nil
}

// The interceptor depends on the Signer interface, not on a key.
func TestInterceptorUsesAnySigner(t *testing.T) {
	signer := &fakeSigner{}
	interceptor := UnaryClientInterceptor("nomnom.orders", fixedTokens{"nomnom.orders": "service-token"}, signer)
	var sent metadata.MD
	err := interceptor(WithUser(context.Background(), User{ID: "customer-1"}), "/m", nil, nil, nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			sent, _ = metadata.FromOutgoingContext(ctx)
			return nil
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"assertion-of-customer-1"}, sent.Get(AssertionMetadata))
	assert.Equal(t, "nomnom.orders", signer.audience)
}

func TestServiceTokensReadFilePerAudience(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nomnom.orders"), []byte("token\n"), 0o600))
	token, err := ServiceTokens{Dir: dir}.Token("nomnom.orders")
	require.NoError(t, err)
	assert.Equal(t, "token", token)
	_, err = ServiceTokens{Dir: dir}.Token("nomnom.payments")
	assert.Error(t, err)
}

func TestLoadRequiresCompleteConfig(t *testing.T) {
	none, err := Load(Config{})
	require.NoError(t, err)
	assert.Nil(t, none)

	_, err = Load(Config{IdPIssuer: "https://idp.nomnom.test"})
	assert.Error(t, err)

	v := loadIdPVectors(t)
	full, err := Load(Config{
		IdPIssuer: v.Issuer, IdPAudience: v.Audience, IdPJWKSFile: filepath.Join(fixtures, "idp-jwks.json"),
		AssertionKeyFile: filepath.Join(fixtures, "bff-signing-key.pem"), AssertionKeyID: "nomnom-bff-test-key",
		ServiceTokenDir: t.TempDir(),
	})
	require.NoError(t, err)
	assert.NotNil(t, full.AccessTokens)
	assert.IsType(t, ServiceTokens{}, full.Outgoing.ServiceTokens)
	signer, ok := full.Outgoing.Signer.(*AssertionSigner)
	require.True(t, ok)
	assert.WithinDuration(t, time.Now(), signer.now(), time.Second)
}
