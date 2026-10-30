package identity

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
)

// LoadJWKS reads the signing keys of a JWKS file by kid. Only RSA (RS256)
// and P-256 (ES256) signing keys are supported; any other key, a key without
// kid or a file without keys is an error.
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
			Alg string `json:"alg"`
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
		if k.Kid == "" {
			return nil, fmt.Errorf("jwks %s: key without kid", path)
		}
		if k.Use != "" && k.Use != "sig" {
			return nil, fmt.Errorf("jwks %s: key %q is not a signing key", path, k.Kid)
		}
		if _, ok := keys[k.Kid]; ok {
			return nil, fmt.Errorf("jwks %s: duplicate key %q", path, k.Kid)
		}
		switch k.Kty {
		case "RSA":
			if k.Alg != "" && k.Alg != "RS256" {
				return nil, fmt.Errorf("jwks %s: RSA key %q declares alg %q", path, k.Kid, k.Alg)
			}
			n, errN := decodeInt(k.N)
			e, errE := decodeInt(k.E)
			if errN != nil || errE != nil || !e.IsInt64() {
				return nil, fmt.Errorf("jwks %s: invalid RSA key %q", path, k.Kid)
			}
			keys[k.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
		case "EC":
			if k.Alg != "" && k.Alg != "ES256" {
				return nil, fmt.Errorf("jwks %s: EC key %q declares alg %q", path, k.Kid, k.Alg)
			}
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
		default:
			return nil, fmt.Errorf("jwks %s: key %q has unsupported kty %q", path, k.Kid, k.Kty)
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
