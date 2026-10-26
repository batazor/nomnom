package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	configKeyIdentityIssuer           = "identity.issuer"
	configKeyIdentityAudience         = "identity.audience"
	configKeyIdentityJWKSFile         = "identity.jwks_file"
	configKeyIdentityServiceAccounts  = "identity.service_accounts"
	configKeyIdentityUserIssuer       = "identity.user_issuer"
	configKeyIdentityUserJWKSFile     = "identity.user_jwks_file"
	configKeyIdentityUserDelegates    = "identity.user_delegates"
	configKeyIdentityPlacementReaders = "identity.placement_readers"

	EnvIdentityIssuer           = "IDENTITY__ISSUER"
	EnvIdentityAudience         = "IDENTITY__AUDIENCE"
	EnvIdentityJWKSFile         = "IDENTITY__JWKS_FILE"
	EnvIdentityServiceAccounts  = "IDENTITY__SERVICE_ACCOUNTS"
	EnvIdentityUserIssuer       = "IDENTITY__USER_ISSUER"
	EnvIdentityUserJWKSFile     = "IDENTITY__USER_JWKS_FILE"
	EnvIdentityUserDelegates    = "IDENTITY__USER_DELEGATES"
	EnvIdentityPlacementReaders = "IDENTITY__PLACEMENT_READERS"

	DefaultIdentityAudience = "nomnom.controlplane"
)

// IdentityConfig verifies inbound service identity (ADR 27) and user
// assertions signed by BFF (ADR 32). Every setting is required: the control
// plane has no anonymous operations.
type IdentityConfig struct {
	Issuer   string `mapstructure:"issuer"`
	Audience string `mapstructure:"audience"`
	JWKSFile string `mapstructure:"jwks_file"`
	// ServiceAccounts is the allowlist "namespace/serviceaccount=service-name,…".
	ServiceAccounts string `mapstructure:"service_accounts"`
	// UserIssuer is the expected iss of user assertions.
	UserIssuer string `mapstructure:"user_issuer"`
	// UserJWKSFile holds the BFF assertion keys (EC P-256).
	UserJWKSFile string `mapstructure:"user_jwks_file"`
	// UserDelegates lists services allowed to pass a user assertion: "bff,…".
	UserDelegates string `mapstructure:"user_delegates"`
	// PlacementReaders lists services allowed to read Markets and the Placement
	// Map (ListMarkets, GetPlacementMap): "bff".
	PlacementReaders string `mapstructure:"placement_readers"`
}

func configureIdentity(loader *viper.Viper) error {
	loader.SetDefault(configKeyIdentityIssuer, "")
	loader.SetDefault(configKeyIdentityAudience, DefaultIdentityAudience)
	loader.SetDefault(configKeyIdentityJWKSFile, "")
	loader.SetDefault(configKeyIdentityServiceAccounts, "")
	loader.SetDefault(configKeyIdentityUserIssuer, "")
	loader.SetDefault(configKeyIdentityUserJWKSFile, "")
	loader.SetDefault(configKeyIdentityUserDelegates, "")
	loader.SetDefault(configKeyIdentityPlacementReaders, "")
	for key, env := range map[string]string{
		configKeyIdentityIssuer:           EnvIdentityIssuer,
		configKeyIdentityAudience:         EnvIdentityAudience,
		configKeyIdentityJWKSFile:         EnvIdentityJWKSFile,
		configKeyIdentityServiceAccounts:  EnvIdentityServiceAccounts,
		configKeyIdentityUserIssuer:       EnvIdentityUserIssuer,
		configKeyIdentityUserJWKSFile:     EnvIdentityUserJWKSFile,
		configKeyIdentityUserDelegates:    EnvIdentityUserDelegates,
		configKeyIdentityPlacementReaders: EnvIdentityPlacementReaders,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

// Delegates parses the comma-separated services allowed to pass user assertions.
func (cfg IdentityConfig) Delegates() map[string]bool { return services(cfg.UserDelegates) }

// Readers parses the comma-separated services allowed to read the topology.
func (cfg IdentityConfig) Readers() map[string]bool { return services(cfg.PlacementReaders) }

func services(list string) map[string]bool {
	set := map[string]bool{}
	for _, service := range strings.Split(list, ",") {
		if service = strings.TrimSpace(service); service != "" {
			set[service] = true
		}
	}
	return set
}

// Accounts parses the ServiceAccount allowlist into "system:serviceaccount:namespace:name" → service.
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

// Validate requires every identity setting: a process without them would
// reject every call, so it does not start.
func (cfg IdentityConfig) Validate() error {
	for env, value := range map[string]string{
		EnvIdentityIssuer:       cfg.Issuer,
		EnvIdentityAudience:     cfg.Audience,
		EnvIdentityJWKSFile:     cfg.JWKSFile,
		EnvIdentityUserIssuer:   cfg.UserIssuer,
		EnvIdentityUserJWKSFile: cfg.UserJWKSFile,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", env)
		}
	}
	accounts, err := cfg.Accounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return fmt.Errorf("%s is required", EnvIdentityServiceAccounts)
	}
	if len(cfg.Delegates()) == 0 {
		return fmt.Errorf("%s is required", EnvIdentityUserDelegates)
	}
	if len(cfg.Readers()) == 0 {
		return fmt.Errorf("%s is required", EnvIdentityPlacementReaders)
	}
	allowed := map[string]bool{}
	for _, service := range accounts {
		allowed[service] = true
	}
	for name, list := range map[string]map[string]bool{EnvIdentityUserDelegates: cfg.Delegates(), EnvIdentityPlacementReaders: cfg.Readers()} {
		for service := range list {
			if !allowed[service] {
				return fmt.Errorf("%s names %s, which %s does not allow", name, service, EnvIdentityServiceAccounts)
			}
		}
	}
	return nil
}
