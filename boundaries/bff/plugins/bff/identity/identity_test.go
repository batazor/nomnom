package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	verifier, err := NewAccessTokenVerifier(context.Background(), v.Audience, testProviders(v.Issuer))
	require.NoError(t, err)
	return verifier
}

func testProviders(issuer string) []Provider {
	return []Provider{{Issuer: issuer, JWKSFile: filepath.Join(fixtures, "idp-jwks.json")}}
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

// ADR 35: a provider with a JWKS URL (Dex rotates its keys) verifies tokens of
// its issuer only; the key set of one issuer never proves another issuer.
func TestAccessTokenVerifierFetchesJWKSByIssuer(t *testing.T) {
	v := loadIdPVectors(t)
	jwks, err := os.ReadFile(filepath.Join(fixtures, "idp-jwks.json"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwks)
	}))
	defer server.Close()

	verifier, err := NewAccessTokenVerifier(t.Context(), v.Audience, []Provider{
		{Issuer: v.Issuer, JWKSURL: server.URL},
		{Issuer: "https://other.nomnom.test", JWKSFile: filepath.Join(fixtures, "bff-jwks.json")},
	})
	require.NoError(t, err)
	user, err := verifier.Verify(v.Tokens["valid"])
	require.NoError(t, err)
	assert.Equal(t, User{ID: v.ExpectedSubject}, user)

	other, err := NewAccessTokenVerifier(t.Context(), v.Audience, []Provider{
		{Issuer: v.Issuer, JWKSFile: filepath.Join(fixtures, "bff-jwks.json")},
		{Issuer: "https://other.nomnom.test", JWKSURL: server.URL},
	})
	require.NoError(t, err)
	_, err = other.Verify(v.Tokens["valid"])
	assert.ErrorIs(t, err, ErrUnauthenticated)
}

func rsaJWK(kid string, key *rsa.PrivateKey) map[string]string {
	return map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

// ADR 35: Dex signs with a new key as soon as it rotates; the first requests
// after a rotation arrive together and all of them pass on one JWKS refresh.
func TestAccessTokenVerifierSharesRefreshAfterKeyRotation(t *testing.T) {
	const issuer = "https://dex.nomnom.test"
	before, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	after, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var published atomic.Value
	published.Store([]map[string]string{rsaJWK("before", before)})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": published.Load()})
	}))
	defer server.Close()
	verifier, err := NewAccessTokenVerifier(t.Context(), "nomnom-bff", []Provider{{Issuer: issuer, JWKSURL: server.URL}})
	require.NoError(t, err)
	sign := func(kid string, key *rsa.PrivateKey) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": issuer, "aud": []string{"nomnom-bff"}, "sub": "customer-1",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		token.Header["kid"] = kid
		signed, err := token.SignedString(key)
		require.NoError(t, err)
		return signed
	}
	_, err = verifier.Verify(sign("before", before))
	require.NoError(t, err)

	// Rotation: the old key stays for verification, the new one signs.
	published.Store([]map[string]string{rsaJWK("before", before), rsaJWK("after", after)})
	token := sign("after", after)
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = verifier.Verify(token)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	_, err = verifier.Verify(sign("before", before))
	require.NoError(t, err, "a token signed before the rotation stays valid")
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
	_, err := Load(Config{})
	assert.Error(t, err)

	_, err = Load(Config{IdPAudience: "nomnom-bff"})
	assert.Error(t, err)

	v := loadIdPVectors(t)
	providers, err := json.Marshal(testProviders(v.Issuer))
	require.NoError(t, err)

	full, err := Load(Config{
		IdPProviders: string(providers), IdPAudience: v.Audience,
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
