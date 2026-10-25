package grpcadapter

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"

	"github.com/golang-jwt/jwt/v5"
)

// jwks is a set of signing keys by kid.
type jwks map[string]crypto.PublicKey

// key is the jwt.Keyfunc of the set: the token names its signing key by kid.
func (k jwks) key(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, errors.New("key id is required")
	}
	key, ok := k[kid]
	if !ok {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	return key, nil
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
