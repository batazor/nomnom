package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUserVerifyingRequiresEverySetting(t *testing.T) {
	full := IdentityConfig{UserIssuer: "nomnom-bff", UserJWKSFile: "jwks.json", UserDelegates: "bff"}
	assert.True(t, full.UserVerifying())
	for name, mutate := range map[string]func(*IdentityConfig){
		"no settings":  func(c *IdentityConfig) { *c = IdentityConfig{} },
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
