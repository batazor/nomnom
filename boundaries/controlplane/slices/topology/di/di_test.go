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

func TestRouteApplierRequiresTheRoutesConfig(t *testing.T) {
	_, err := NewRouteApplier(config.RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public"})
	require.ErrorContains(t, err, config.EnvRoutesListener)
}

func TestRouteApplierRunsInTheCluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	_, err := NewRouteApplier(config.RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public", Listener: "api"})
	require.ErrorContains(t, err, "in-cluster")
}
