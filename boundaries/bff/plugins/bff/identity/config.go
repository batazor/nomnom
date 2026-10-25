package identity

import "fmt"

// Environment of the BFF identity: the identity provider that issues access
// tokens and the key that signs user assertions.
const (
	EnvIdPIssuer        = "BFF__IDP_ISSUER"
	EnvIdPAudience      = "BFF__IDP_AUDIENCE"
	EnvIdPJWKSFile      = "BFF__IDP_JWKS_FILE"
	EnvAssertionKeyFile = "BFF__ASSERTION_KEY_FILE"
	EnvAssertionKeyID   = "BFF__ASSERTION_KEY_ID"
	EnvServiceTokenDir  = "IDENTITY__TOKEN_DIR"
)

type Config struct {
	IdPIssuer        string
	IdPAudience      string
	IdPJWKSFile      string
	AssertionKeyFile string
	AssertionKeyID   string
	ServiceTokenDir  string
}

func ConfigFromEnv(getenv func(string) string) Config {
	return Config{
		IdPIssuer:        getenv(EnvIdPIssuer),
		IdPAudience:      getenv(EnvIdPAudience),
		IdPJWKSFile:      getenv(EnvIdPJWKSFile),
		AssertionKeyFile: getenv(EnvAssertionKeyFile),
		AssertionKeyID:   getenv(EnvAssertionKeyID),
		ServiceTokenDir:  getenv(EnvServiceTokenDir),
	}
}

// Enabled reports whether any identity setting is present. Without settings
// BFF serves only operations that need no customer.
func (c Config) Enabled() bool {
	return c != Config{}
}

// Identity is the assembled BFF identity.
type Identity struct {
	AccessTokens  *AccessTokenVerifier
	Assertions    *AssertionSigner
	ServiceTokens ServiceTokens
}

// Load fails on a partial or invalid configuration: a BFF that cannot prove
// customers must not start half-configured.
func Load(c Config) (*Identity, error) {
	if !c.Enabled() {
		return nil, nil
	}
	for name, value := range map[string]string{
		EnvIdPIssuer: c.IdPIssuer, EnvIdPAudience: c.IdPAudience, EnvIdPJWKSFile: c.IdPJWKSFile,
		EnvAssertionKeyFile: c.AssertionKeyFile, EnvAssertionKeyID: c.AssertionKeyID, EnvServiceTokenDir: c.ServiceTokenDir,
	} {
		if value == "" {
			return nil, fmt.Errorf("%s is required when BFF identity is configured", name)
		}
	}
	tokens, err := NewAccessTokenVerifier(c.IdPIssuer, c.IdPAudience, c.IdPJWKSFile)
	if err != nil {
		return nil, err
	}
	signer, err := NewAssertionSigner(c.AssertionKeyFile, c.AssertionKeyID)
	if err != nil {
		return nil, err
	}
	return &Identity{AccessTokens: tokens, Assertions: signer, ServiceTokens: ServiceTokens{Dir: c.ServiceTokenDir}}, nil
}
