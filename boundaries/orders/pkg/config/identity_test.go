package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadReadsUserIdentityFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvIdentityUserIssuer, "nomnom-bff")
	t.Setenv(EnvIdentityUserJWKSFile, "/etc/nomnom/user-identity/jwks.json")
	t.Setenv(EnvIdentityUserDelegates, " bff, ,admin-api ")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "nomnom-bff", cfg.Identity.UserIssuer)
	assert.Equal(t, "/etc/nomnom/user-identity/jwks.json", cfg.Identity.UserJWKSFile)
	assert.Equal(t, map[string]bool{"bff": true, "admin-api": true}, cfg.Identity.Delegates())
	assert.True(t, cfg.Identity.UserVerifying())
}

func TestUserIdentityIsDisabledByDefault(t *testing.T) {
	clearConfigEnvironment(t)
	for _, name := range []string{EnvIdentityUserIssuer, EnvIdentityUserJWKSFile, EnvIdentityUserDelegates} {
		t.Setenv(name, "")
	}

	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.Identity.UserVerifying())

	full := IdentityConfig{UserIssuer: "nomnom-bff", UserJWKSFile: "jwks.json", UserDelegates: "bff"}
	for name, mutate := range map[string]func(*IdentityConfig){
		"no issuer":    func(c *IdentityConfig) { c.UserIssuer = " " },
		"no jwks":      func(c *IdentityConfig) { c.UserJWKSFile = "" },
		"no delegates": func(c *IdentityConfig) { c.UserDelegates = " , " },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := full
			mutate(&cfg)
			assert.False(t, cfg.UserVerifying())
		})
	}
}
