package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	configKeyIdentityIssuer          = "identity.issuer"
	configKeyIdentityAudience        = "identity.audience"
	configKeyIdentityJWKSFile        = "identity.jwks_file"
	configKeyIdentityServiceAccounts = "identity.service_accounts"
	configKeyIdentityTokenDir        = "identity.token_dir"
	configKeyIdentityUserIssuer      = "identity.user_issuer"
	configKeyIdentityUserJWKSFile    = "identity.user_jwks_file"
	configKeyIdentityUserDelegates   = "identity.user_delegates"

	EnvIdentityIssuer          = "IDENTITY__ISSUER"
	EnvIdentityAudience        = "IDENTITY__AUDIENCE"
	EnvIdentityJWKSFile        = "IDENTITY__JWKS_FILE"
	EnvIdentityServiceAccounts = "IDENTITY__SERVICE_ACCOUNTS"
	EnvIdentityTokenDir        = "IDENTITY__TOKEN_DIR"
	EnvIdentityUserIssuer      = "IDENTITY__USER_ISSUER"
	EnvIdentityUserJWKSFile    = "IDENTITY__USER_JWKS_FILE"
	EnvIdentityUserDelegates   = "IDENTITY__USER_DELEGATES"

	DefaultIdentityAudience = "nomnom.orders"
)

// IdentityConfig verifies inbound service identity (ADR 27) and user
// assertions signed by BFF (ADR 32), and locates the projected tokens this
// process presents to other contexts.
type IdentityConfig struct {
	Issuer   string `mapstructure:"issuer"`
	Audience string `mapstructure:"audience"`
	JWKSFile string `mapstructure:"jwks_file"`
	// ServiceAccounts is the allowlist "namespace/serviceaccount=service-name,…".
	ServiceAccounts string `mapstructure:"service_accounts"`
	// TokenDir holds one projected token per target audience; empty sends no credentials.
	TokenDir string `mapstructure:"token_dir"`
	// UserIssuer is the expected iss of user assertions; empty rejects every assertion.
	UserIssuer string `mapstructure:"user_issuer"`
	// UserJWKSFile holds the BFF assertion keys (EC P-256).
	UserJWKSFile string `mapstructure:"user_jwks_file"`
	// UserDelegates lists services allowed to pass a user assertion: "bff,…".
	UserDelegates string `mapstructure:"user_delegates"`
}

func configureIdentity(loader *viper.Viper) error {
	loader.SetDefault(configKeyIdentityIssuer, "")
	loader.SetDefault(configKeyIdentityAudience, DefaultIdentityAudience)
	loader.SetDefault(configKeyIdentityJWKSFile, "")
	loader.SetDefault(configKeyIdentityServiceAccounts, "")
	loader.SetDefault(configKeyIdentityTokenDir, "")
	loader.SetDefault(configKeyIdentityUserIssuer, "")
	loader.SetDefault(configKeyIdentityUserJWKSFile, "")
	loader.SetDefault(configKeyIdentityUserDelegates, "")
	for key, env := range map[string]string{
		configKeyIdentityIssuer:          EnvIdentityIssuer,
		configKeyIdentityAudience:        EnvIdentityAudience,
		configKeyIdentityJWKSFile:        EnvIdentityJWKSFile,
		configKeyIdentityServiceAccounts: EnvIdentityServiceAccounts,
		configKeyIdentityTokenDir:        EnvIdentityTokenDir,
		configKeyIdentityUserIssuer:      EnvIdentityUserIssuer,
		configKeyIdentityUserJWKSFile:    EnvIdentityUserJWKSFile,
		configKeyIdentityUserDelegates:   EnvIdentityUserDelegates,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

// Verifying reports whether the inbound token settings are complete.
func (cfg IdentityConfig) Verifying() bool {
	return strings.TrimSpace(cfg.Issuer) != "" && strings.TrimSpace(cfg.Audience) != "" && strings.TrimSpace(cfg.JWKSFile) != ""
}

// UserVerifying reports whether user assertions are accepted; otherwise every
// call carrying one is rejected.
func (cfg IdentityConfig) UserVerifying() bool {
	return strings.TrimSpace(cfg.UserIssuer) != "" && strings.TrimSpace(cfg.UserJWKSFile) != "" && len(cfg.Delegates()) > 0
}

// Delegates parses the comma-separated services allowed to pass user assertions.
func (cfg IdentityConfig) Delegates() map[string]bool {
	delegates := map[string]bool{}
	for _, service := range strings.Split(cfg.UserDelegates, ",") {
		if service = strings.TrimSpace(service); service != "" {
			delegates[service] = true
		}
	}
	return delegates
}

// Accounts parses the ServiceAccount allowlist into "namespace:name" → service.
func (cfg IdentityConfig) Accounts() (map[string]string, error) {
	accounts := map[string]string{}
	for _, entry := range strings.Split(cfg.ServiceAccounts, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		account, service, ok := strings.Cut(entry, "=")
		namespace, name, okAccount := strings.Cut(strings.TrimSpace(account), "/")
		service = strings.TrimSpace(service)
		if !ok || !okAccount || namespace == "" || name == "" || service == "" {
			return nil, fmt.Errorf("identity service account %q must be namespace/serviceaccount=service-name", entry)
		}
		accounts["system:serviceaccount:"+namespace+":"+name] = service
	}
	return accounts, nil
}

func (cfg IdentityConfig) Validate() error {
	accounts, err := cfg.Accounts()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Issuer) != "" && len(accounts) == 0 {
		return fmt.Errorf("%s must not be empty when %s is set", EnvIdentityServiceAccounts, EnvIdentityIssuer)
	}
	return nil
}
