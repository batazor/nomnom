package neighbours_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/neighbours"
)

func TestUnavailableCatalogFailsClosedAsDependencyUnavailable(t *testing.T) {
	_, err := neighbours.UnavailableCatalog{}.Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.ErrorIs(t, err, application.ErrDependencyUnavailable)
	require.ErrorIs(t, err, neighbours.ErrNotImplemented)
}
