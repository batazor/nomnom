package config

import (
	"fmt"

	"github.com/spf13/viper"
)

const (
	configKeyInternalGatewayAddress = "internal_gateway.address"
	// EnvInternalGatewayAddr is the shared name used by every context (ADR 15).
	EnvInternalGatewayAddr = "INTERNAL_GATEWAY_ADDR"
)

// InternalGatewayConfig is the address of synchronous business calls to other
// bounded contexts. It is required only by processes that call them.
type InternalGatewayConfig struct {
	Address string `mapstructure:"address"`
}

func configureInternalGateway(loader *viper.Viper) error {
	loader.SetDefault(configKeyInternalGatewayAddress, "")
	if err := loader.BindEnv(configKeyInternalGatewayAddress, EnvInternalGatewayAddr); err != nil {
		return fmt.Errorf("bind %s: %w", EnvInternalGatewayAddr, err)
	}
	return nil
}
