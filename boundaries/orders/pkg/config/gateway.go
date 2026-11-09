package config

// KeyInternalGatewayAddress — адрес internal gateway (ADR 15).
const KeyInternalGatewayAddress = "spec.internalGateway.address"

// InternalGatewayConfig is the address of synchronous business calls to other
// bounded contexts. Every orders process requires it.
type InternalGatewayConfig struct {
	Address string `yaml:"address"`
}
