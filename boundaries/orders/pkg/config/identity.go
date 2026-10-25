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

	EnvIdentityIssuer          = "IDENTITY__ISSUER"
	EnvIdentityAudience        = "IDENTITY__AUDIENCE"
	EnvIdentityJWKSFile        = "IDENTITY__JWKS_FILE"
	EnvIdentityServiceAccounts = "IDENTITY__SERVICE_ACCOUNTS"
	EnvIdentityTokenDir        = "IDENTITY__TOKEN_DIR"

	DefaultIdentityAudience = "nomnom.orders"
)

// IdentityConfig verifies inbound service identity (ADR 27) and locates the
// projected tokens this process presents to other contexts.
type IdentityConfig struct {
	Issuer   string `mapstructure:"issuer"`
	Audience string `mapstructure:"audience"`
	JWKSFile string `mapstructure:"jwks_file"`
	// ServiceAccounts is the allowlist "namespace/serviceaccount=service-name,…".
	ServiceAccounts string `mapstructure:"service_accounts"`
	// TokenDir holds one projected token per target audience; empty sends no credentials.
	TokenDir string `mapstructure:"token_dir"`
}

func configureIdentity(loader *viper.Viper) error {
	loader.SetDefault(configKeyIdentityIssuer, "")
	loader.SetDefault(configKeyIdentityAudience, DefaultIdentityAudience)
	loader.SetDefault(configKeyIdentityJWKSFile, "")
	loader.SetDefault(configKeyIdentityServiceAccounts, "")
	loader.SetDefault(configKeyIdentityTokenDir, "")
	for key, env := range map[string]string{
		configKeyIdentityIssuer:          EnvIdentityIssuer,
		configKeyIdentityAudience:        EnvIdentityAudience,
		configKeyIdentityJWKSFile:        EnvIdentityJWKSFile,
		configKeyIdentityServiceAccounts: EnvIdentityServiceAccounts,
		configKeyIdentityTokenDir:        EnvIdentityTokenDir,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

// Verifying reports whether inbound tokens are verified; otherwise every
// caller is rejected.
func (cfg IdentityConfig) Verifying() bool {
	return strings.TrimSpace(cfg.Issuer) != "" && strings.TrimSpace(cfg.Audience) != "" && strings.TrimSpace(cfg.JWKSFile) != ""
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
