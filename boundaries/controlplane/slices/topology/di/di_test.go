package di

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
)

func TestAuthorizerRequiresSpiceDB(t *testing.T) {
	_, _, err := NewAuthorizer(config.SpiceDBConfig{})
	require.ErrorContains(t, err, config.EnvSpiceDBEndpoint)
}
