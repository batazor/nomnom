// Package infrastructure adapts the Market directory to ListMarkets of
// controlplane.v1.ControlPlaneService, called through the internal gateway
// with the service token of BFF and without a user assertion. The control
// plane is global: the call is not routed to a Cell.
package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	controlplanev1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/controlplane/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/markets/applications"
)

// ControlPlane implements applications.MarketDirectory.
type ControlPlane struct {
	client controlplanev1.ControlPlaneServiceClient
}

func NewControlPlane(conn grpc.ClientConnInterface) *ControlPlane {
	return &ControlPlane{client: controlplanev1.NewControlPlaneServiceClient(conn)}
}

var _ applications.MarketDirectory = (*ControlPlane)(nil)

func (c *ControlPlane) Markets(ctx context.Context) ([]applications.Market, error) {
	response, err := c.client.ListMarkets(identity.WithoutUser(ctx), &controlplanev1.ListMarketsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list markets: %w", err)
	}
	markets := make([]applications.Market, 0, len(response.GetMarkets()))
	for _, market := range response.GetMarkets() {
		markets = append(markets, applications.Market{
			ID:       market.GetMarketId(),
			Name:     market.GetName(),
			Currency: market.GetCurrency(),
		})
	}
	return markets, nil
}
