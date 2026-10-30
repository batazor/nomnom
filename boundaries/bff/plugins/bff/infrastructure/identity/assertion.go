package identity

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AssertionIssuer is the iss every bounded context expects (ADR 32).
	AssertionIssuer = "nomnom-bff"
	// AssertionMetadata carries the assertion next to the service token.
	AssertionMetadata = "x-nomnom-user-assertion"
	// assertionTTL stays below the 60 s contexts accept.
	assertionTTL = 30 * time.Second
)

// AssertionSigner signs one short-lived user assertion per call of a context.
type AssertionSigner struct {
	key   *ecdsa.PrivateKey
	keyID string
	now   func() time.Time
}

// NewAssertionSigner reads a P-256 private key in PEM (PKCS #8 or SEC 1).
func NewAssertionSigner(keyFile, keyID string) (*AssertionSigner, error) {
	if keyID == "" {
		return nil, errors.New("assertion key id is required")
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read assertion key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("assertion key is not PEM")
	}
	key, err := parseECKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &AssertionSigner{key: key, keyID: keyID, now: time.Now}, nil
}

func parseECKey(der []byte) (*ecdsa.PrivateKey, error) {
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return checkCurve(key)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse assertion key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("assertion key must be ECDSA")
	}
	return checkCurve(key)
}

func checkCurve(key *ecdsa.PrivateKey) (*ecdsa.PrivateKey, error) {
	if key.Curve.Params().Name != "P-256" {
		return nil, errors.New("assertion key must use P-256")
	}
	return key, nil
}

// Sign issues an assertion for user towards one context audience, for example
// nomnom.orders. The subject comes only from a verified access token.
func (s *AssertionSigner) Sign(user User, audience string) (string, error) {
	if user.ID == "" || audience == "" {
		return "", errors.New("assertion needs a user and an audience")
	}
	jti, err := newID()
	if err != nil {
		return "", err
	}
	now := s.now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer:    AssertionIssuer,
		Subject:   user.ID,
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(assertionTTL)),
		ID:        jti,
	})
	token.Header["kid"] = s.keyID
	return token.SignedString(s.key)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("assertion id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
