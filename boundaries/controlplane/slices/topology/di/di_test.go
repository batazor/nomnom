package di

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
)

func TestManagerRequiresTheRoutesConfig(t *testing.T) {
	_, err := NewManager(&rest.Config{}, config.RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public"}, config.ShutdownConfig{})
	require.ErrorContains(t, err, config.EnvRoutesListener)
}

func TestRouteApplierRequiresTheRoutesConfig(t *testing.T) {
	_, err := NewRouteApplier(&rest.Config{}, config.RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public"})
	require.ErrorContains(t, err, config.EnvRoutesListener)
}
