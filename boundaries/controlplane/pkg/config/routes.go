package config

import (
	"fmt"
	"strings"
)

// RoutesConfig is where the control plane publishes the routes of the public
// gateway (ADR 41): the namespace of its routes, the Gateway and the listener
// of client traffic. A process without them could not route a declared Market,
// so it does not start.
type RoutesConfig struct {
	Namespace string `yaml:"namespace"`
	Gateway   string `yaml:"gateway"`
	Listener  string `yaml:"listener"`
}

func (cfg RoutesConfig) Validate() error {
	for _, setting := range []struct{ key, value string }{
		{"spec.routes.namespace", cfg.Namespace}, {"spec.routes.gateway", cfg.Gateway}, {"spec.routes.listener", cfg.Listener},
	} {
		if strings.TrimSpace(setting.value) == "" {
			return fmt.Errorf("%s is required: controlplane publishes the routes of the public gateway", setting.key)
		}
	}
	return nil
}
