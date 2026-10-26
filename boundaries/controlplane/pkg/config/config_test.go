package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
)

// requiredEnvironment is the minimal valid environment of the process.
func requiredEnvironment(t *testing.T) {
	t.Helper()
	for _, env := range []string{"CONFIG_FILE", "PORT", EnvPort, EnvManagementPort, EnvDrainTimeout, EnvReadinessDelay, EnvIdentityAudience} {
		t.Setenv(env, "")
		require.NoError(t, os.Unsetenv(env))
	}
	for env, value := range map[string]string{
		EnvIdentityIssuer:          "https://kubernetes.default.svc.cluster.local",
		EnvIdentityJWKSFile:        "/etc/nomnom/identity/jwks.json",
		EnvIdentityServiceAccounts: "nomnom-bff/bff=bff,nomnom-bff/demo-seed=demo-seed",
		EnvIdentityUserIssuer:      "nomnom-bff",
		EnvIdentityUserJWKSFile:    "/etc/nomnom/user-identity/jwks.json",
		EnvIdentityUserDelegates:   "demo-seed",
		EnvSpiceDBEndpoint:         "spicedb:50051",
		EnvSpiceDBPresharedKey:     "key",
		database.EnvPostgresDSN:    "postgres://controlplane@db/controlplane",
		EnvRoutesNamespace:         "nomnom-gateway",
		EnvRoutesGateway:           "public",
		EnvRoutesListener:          "api",
	} {
		t.Setenv(env, value)
	}
}

func TestLoadReadsTheRequiredEnvironment(t *testing.T) {
	requiredEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, DefaultPort, cfg.Server.Port)
	assert.Equal(t, DefaultManagementPort, cfg.Management.Port)
	assert.Equal(t, DefaultIdentityAudience, cfg.Identity.Audience)
	assert.Equal(t, RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public", Listener: "api"}, cfg.Routes)
	assert.Equal(t, map[string]bool{"demo-seed": true}, cfg.Identity.Delegates())
	assert.Equal(t, DefaultSpiceDBTimeout, cfg.SpiceDB.Timeout)
}

func TestLoadEnvironmentOverridesConfigFile(t *testing.T) {
	requiredEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  port: 9000\n"), 0o600))
	t.Setenv("CONFIG_FILE", path)
	t.Setenv(EnvPort, "9001")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9001, cfg.Server.Port)
}

func TestLoadReadsDrainTimeoutFromEnvironment(t *testing.T) {
	requiredEnvironment(t)
	t.Setenv(EnvDrainTimeout, "5s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, cfg.Shutdown.DrainTimeout)
}

// Пустой обязательный параметр останавливает старт, а не отключает операции.
func TestLoadRejectsMissingRequiredSettings(t *testing.T) {
	for _, env := range []string{
		EnvIdentityIssuer, EnvIdentityJWKSFile, EnvIdentityServiceAccounts, EnvIdentityUserIssuer,
		EnvIdentityUserJWKSFile, EnvIdentityUserDelegates,
		EnvSpiceDBEndpoint, EnvSpiceDBPresharedKey, database.EnvPostgresDSN,
		EnvRoutesNamespace, EnvRoutesGateway, EnvRoutesListener,
	} {
		t.Run(env, func(t *testing.T) {
			requiredEnvironment(t)
			t.Setenv(env, "")

			_, err := Load()
			require.ErrorContains(t, err, env)
		})
	}
}

func TestLoadRejectsDelegatesOutsideTheServiceAllowlist(t *testing.T) {
	requiredEnvironment(t)
	t.Setenv(EnvIdentityUserDelegates, "demo-seed,grpc-caller")

	_, err := Load()
	require.ErrorContains(t, err, "grpc-caller")
}

func TestLoadRejectsInvalidPorts(t *testing.T) {
	requiredEnvironment(t)
	t.Setenv("PORT", "70000")
	_, err := Load()
	require.Error(t, err)

	requiredEnvironment(t)
	t.Setenv(EnvManagementPort, "8086")
	_, err = Load()
	require.ErrorContains(t, err, "management port")
}
