package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	configKeyRoutesNamespace = "routes.namespace"
	configKeyRoutesGateway   = "routes.gateway"
	configKeyRoutesListener  = "routes.listener"

	EnvRoutesNamespace = "ROUTES__NAMESPACE"
	EnvRoutesGateway   = "ROUTES__GATEWAY"
	EnvRoutesListener  = "ROUTES__LISTENER"
)

// RoutesConfig is where the control plane publishes the routes of the public
// gateway (ADR 41): the namespace of its routes, the Gateway and the listener
// of client traffic. A process without them could not route a declared Market,
// so it does not start.
type RoutesConfig struct {
	Namespace string `mapstructure:"namespace"`
	Gateway   string `mapstructure:"gateway"`
	Listener  string `mapstructure:"listener"`
}

func configureRoutes(loader *viper.Viper) error {
	loader.SetDefault(configKeyRoutesNamespace, "")
	loader.SetDefault(configKeyRoutesGateway, "")
	loader.SetDefault(configKeyRoutesListener, "")
	for key, env := range map[string]string{
		configKeyRoutesNamespace: EnvRoutesNamespace,
		configKeyRoutesGateway:   EnvRoutesGateway,
		configKeyRoutesListener:  EnvRoutesListener,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg RoutesConfig) Validate() error {
	for _, setting := range []struct{ env, value string }{
		{EnvRoutesNamespace, cfg.Namespace}, {EnvRoutesGateway, cfg.Gateway}, {EnvRoutesListener, cfg.Listener},
	} {
		if strings.TrimSpace(setting.value) == "" {
			return fmt.Errorf("%s is required: controlplane publishes the routes of the public gateway", setting.env)
		}
	}
	return nil
}
