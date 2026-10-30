package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requiredEnvironment is the minimal valid environment of the process.
func requiredEnvironment(t *testing.T) {
	t.Helper()
	for _, env := range []string{"CONFIG_FILE", EnvManagementPort, EnvDrainTimeout, EnvReadinessDelay} {
		t.Setenv(env, "")
		require.NoError(t, os.Unsetenv(env))
	}
	for env, value := range map[string]string{
		EnvRoutesNamespace: "nomnom-gateway",
		EnvRoutesGateway:   "public",
		EnvRoutesListener:  "api",
	} {
		t.Setenv(env, value)
	}
}

func TestLoadReadsTheRequiredEnvironment(t *testing.T) {
	requiredEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, DefaultManagementPort, cfg.Management.Port)
	assert.Equal(t, RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public", Listener: "api"}, cfg.Routes)
}

func TestLoadEnvironmentOverridesConfigFile(t *testing.T) {
	requiredEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("management:\n  port: 9000\n"), 0o600))
	t.Setenv("CONFIG_FILE", path)
	t.Setenv(EnvManagementPort, "9001")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9001, cfg.Management.Port)
}

func TestLoadReadsDrainTimeoutFromEnvironment(t *testing.T) {
	requiredEnvironment(t)
	t.Setenv(EnvDrainTimeout, "5s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, cfg.Shutdown.DrainTimeout)
}

// AC-810: пустой обязательный параметр останавливает старт, а не отключает операции.
func TestLoadRejectsMissingRequiredSettings(t *testing.T) {
	for _, env := range []string{EnvRoutesNamespace, EnvRoutesGateway, EnvRoutesListener} {
		t.Run(env, func(t *testing.T) {
			requiredEnvironment(t)
			t.Setenv(env, "")

			_, err := Load()
			require.ErrorContains(t, err, env)
		})
	}
}

// AC-1014: конфигурации PostgreSQL, SpiceDB и identity у процесса нет.
func TestLoadRejectsSectionsOfTheRemovedAPI(t *testing.T) {
	for _, section := range []string{"postgres", "spicedb", "identity", "server"} {
		t.Run(section, func(t *testing.T) {
			requiredEnvironment(t)
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(section+":\n  value: x\n"), 0o600))
			t.Setenv("CONFIG_FILE", path)

			_, err := Load()
			require.ErrorContains(t, err, section)
		})
	}
}

func TestLoadRejectsInvalidPorts(t *testing.T) {
	requiredEnvironment(t)
	t.Setenv(EnvManagementPort, "70000")

	_, err := Load()
	require.ErrorContains(t, err, "management port")
}
