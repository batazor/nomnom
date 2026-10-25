package grpcadapter

import (
	"crypto/ecdsa"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWKSKeyByKid(t *testing.T) {
	key := &ecdsa.PublicKey{}
	set := jwks{"kid-1": key}

	found, err := set.key(&jwt.Token{Header: map[string]any{"kid": "kid-1"}})
	require.NoError(t, err)
	assert.Same(t, key, found)

	_, err = set.key(&jwt.Token{Header: map[string]any{"kid": "kid-2"}})
	require.ErrorContains(t, err, "unknown key id")
	_, err = set.key(&jwt.Token{Header: map[string]any{}})
	require.ErrorContains(t, err, "key id is required")
}
