package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setSimulatorEnvironment(t *testing.T, env map[string]string) {
	t.Helper()
	for key, value := range env {
		t.Setenv(key, value)
	}
}

func validSimulatorEnvironment() map[string]string {
	return map[string]string{
		EnvSimulatorInterval:   "5s",
		EnvSimulatorMinDelay:   "30s",
		EnvSimulatorMaxDelay:   "2m",
		EnvSimulatorCustomerID: "customer-1",
	}
}

func TestLoadReadsSimulatorFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	setSimulatorEnvironment(t, validSimulatorEnvironment())

	cfg, err := Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Simulator.Validate())
	assert.Equal(t, SimulatorConfig{Interval: 5 * time.Second, MinDelay: 30 * time.Second, MaxDelay: 2 * time.Minute, CustomerID: "customer-1"}, cfg.Simulator)
}

func TestSimulatorRejectsInvalidConfig(t *testing.T) {
	for name, override := range map[string]map[string]string{
		"AC-765 min above max": {EnvSimulatorMinDelay: "3m", EnvSimulatorMaxDelay: "1m"},
		"missing interval":     {EnvSimulatorInterval: ""},
		"missing min delay":    {EnvSimulatorMinDelay: ""},
		"missing max delay":    {EnvSimulatorMaxDelay: ""},
		"missing customer":     {EnvSimulatorCustomerID: ""},
		"zero interval":        {EnvSimulatorInterval: "0s"},
	} {
		t.Run(name, func(t *testing.T) {
			clearConfigEnvironment(t)
			env := validSimulatorEnvironment()
			for key, value := range override {
				env[key] = value
			}
			setSimulatorEnvironment(t, env)

			cfg, err := Load()
			require.NoError(t, err)
			require.ErrorContains(t, cfg.Simulator.Validate(), "simulator")
		})
	}
}

func TestLoadRejectsUnparsableSimulatorDelay(t *testing.T) {
	clearConfigEnvironment(t)
	env := validSimulatorEnvironment()
	env[EnvSimulatorMinDelay] = "soon"
	setSimulatorEnvironment(t, env)

	_, err := Load()
	require.Error(t, err)
}
