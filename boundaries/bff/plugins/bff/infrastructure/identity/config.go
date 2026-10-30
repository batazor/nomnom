package identity

import (
	"context"
	"encoding/json"
	"fmt"
)

// Environment of the BFF identity: the identity provider that issues access
// tokens and the key that signs user assertions.
const (
	EnvIdPProviders     = "BFF__IDP_PROVIDERS"
	EnvIdPAudience      = "BFF__IDP_AUDIENCE"
	EnvAssertionKeyFile = "BFF__ASSERTION_KEY_FILE"
	EnvAssertionKeyID   = "BFF__ASSERTION_KEY_ID"
	EnvServiceTokenDir  = "IDENTITY__TOKEN_DIR"
)

type Config struct {
	// IdPProviders is a JSON array of Provider.
	IdPProviders     string
	IdPAudience      string
	AssertionKeyFile string
	AssertionKeyID   string
	ServiceTokenDir  string
}

func ConfigFromEnv(getenv func(string) string) Config {
	return Config{
		IdPProviders:     getenv(EnvIdPProviders),
		IdPAudience:      getenv(EnvIdPAudience),
		AssertionKeyFile: getenv(EnvAssertionKeyFile),
		AssertionKeyID:   getenv(EnvAssertionKeyID),
		ServiceTokenDir:  getenv(EnvServiceTokenDir),
	}
}

// Identity is the assembled BFF identity: the incoming check of access tokens
// and the outgoing credentials of calls to contexts.
type Identity struct {
	AccessTokens *AccessTokenVerifier
	Outgoing     OutgoingCredentials
}

// Load fails on a missing or invalid configuration: a BFF that cannot prove
// customers must not start.
func Load(c Config) (*Identity, error) {
	for name, value := range map[string]string{
		EnvIdPProviders: c.IdPProviders, EnvIdPAudience: c.IdPAudience,
		EnvAssertionKeyFile: c.AssertionKeyFile, EnvAssertionKeyID: c.AssertionKeyID, EnvServiceTokenDir: c.ServiceTokenDir,
	} {
		if value == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	var providers []Provider
	if err := json.Unmarshal([]byte(c.IdPProviders), &providers); err != nil {
		return nil, fmt.Errorf("%s: %w", EnvIdPProviders, err)
	}
	// JWKS URLs are refreshed for the life of the plugin process.
	tokens, err := NewAccessTokenVerifier(context.Background(), c.IdPAudience, providers)
	if err != nil {
		return nil, err
	}
	signer, err := NewAssertionSigner(c.AssertionKeyFile, c.AssertionKeyID)
	if err != nil {
		return nil, err
	}
	return &Identity{
		AccessTokens: tokens,
		Outgoing:     OutgoingCredentials{ServiceTokens: ServiceTokens{Dir: c.ServiceTokenDir}, Signer: signer},
	}, nil
}
