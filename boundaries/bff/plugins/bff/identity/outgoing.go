package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OutgoingCredentials authenticate BFF calls to bounded contexts: the service
// token of BFF for every call and, for a verified customer, a user assertion.
type OutgoingCredentials struct {
	ServiceTokens TokenSource
	Signer        Signer
}

// Signer issues a user assertion for one context audience (ADR 32).
type Signer interface {
	Sign(user User, audience string) (string, error)
}

var _ Signer = (*AssertionSigner)(nil)

// ServiceTokens reads the projected ServiceAccount token of an audience on
// every call: kubelet rotates the file (ADR 27).
type ServiceTokens struct {
	Dir string
}

func (t ServiceTokens) Token(audience string) (string, error) {
	data, err := os.ReadFile(filepath.Join(t.Dir, audience))
	if err != nil {
		return "", fmt.Errorf("read service token for %s: %w", audience, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("service token for %s is empty", audience)
	}
	return token, nil
}

type TokenSource interface {
	Token(audience string) (string, error)
}
