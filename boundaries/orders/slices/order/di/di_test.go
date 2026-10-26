package di

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
)

func TestNewAuthorizerRequiresEndpoint(t *testing.T) {
	_, _, err := NewAuthorizer(config.SpiceDBConfig{}, nil)
	require.ErrorContains(t, err, config.EnvSpiceDBEndpoint)
}
