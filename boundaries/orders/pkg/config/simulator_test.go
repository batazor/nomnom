package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadReadsSimulator(t *testing.T) {
	s := spec()
	s["simulator"] = map[string]any{
		"interval": "5s", "minDelay": "30s", "maxDelay": "2m", "customerId": "customer-1", "marketIds": "berlin, hamburg",
	}
	useConfig(t, document(RoleAPI, s))

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	require.NoError(t, cfg.Simulator.Validate())
	assert.Equal(t, SimulatorConfig{Interval: 5 * time.Second, MinDelay: 30 * time.Second, MaxDelay: 2 * time.Minute, CustomerID: "customer-1", MarketIDs: "berlin, hamburg"}, cfg.Simulator)
	assert.Equal(t, []string{"berlin", "hamburg"}, cfg.Simulator.Markets())
}

func TestSimulatorRejectsInvalidConfig(t *testing.T) {
	valid := SimulatorConfig{Interval: 5 * time.Second, MinDelay: 30 * time.Second, MaxDelay: 2 * time.Minute, CustomerID: "customer-1", MarketIDs: "berlin, hamburg"}
	require.NoError(t, valid.Validate())
	for name, mutate := range map[string]func(*SimulatorConfig){
		"AC-765 min above max": func(c *SimulatorConfig) { c.MinDelay, c.MaxDelay = 3*time.Minute, time.Minute },
		"missing interval":     func(c *SimulatorConfig) { c.Interval = 0 },
		"missing min delay":    func(c *SimulatorConfig) { c.MinDelay = 0 },
		"missing max delay":    func(c *SimulatorConfig) { c.MaxDelay = 0 },
		"missing customer":     func(c *SimulatorConfig) { c.CustomerID = " " },
		"missing markets":      func(c *SimulatorConfig) { c.MarketIDs = "" },
		"malformed market":     func(c *SimulatorConfig) { c.MarketIDs = "berlin,Hamburg" },
		"empty market entry":   func(c *SimulatorConfig) { c.MarketIDs = "berlin," },
		"repeated market":      func(c *SimulatorConfig) { c.MarketIDs = "berlin,berlin" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), "simulator")
		})
	}
}

func TestLoadRejectsUnparsableSimulatorDelay(t *testing.T) {
	s := spec()
	s["simulator"] = map[string]any{"minDelay": "soon"}
	useConfig(t, document(RoleAPI, s))

	_, err := Load(RoleAPI)
	require.Error(t, err)
}
