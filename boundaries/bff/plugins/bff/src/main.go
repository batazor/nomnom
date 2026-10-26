package main

import (
	"context"
	"log"
	"os"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/gateway"
	routerplugin "github.com/wundergraph/cosmo/router-plugin"
	"google.golang.org/grpc"
)

const serviceVersion = "0.1.0"

func main() {
	bff, err := newBffService(configFromEnv(os.Getenv), gateway.Dial)
	if err != nil {
		log.Fatalf("assemble BFF: %v", err)
	}

	plugin, err := routerplugin.NewRouterPlugin(func(server *grpc.Server) {
		server.RegisterService(&service.BffService_ServiceDesc, bff)
	},
		// Exporters come from the Router (telemetry.tracing); spans of the plugin
		// get their own service so Alloy can admit them (wg.is_plugin, alloy.yaml).
		routerplugin.WithTracing(),
		routerplugin.WithServiceName("bff-plugin"),
		routerplugin.WithServiceVersion(serviceVersion),
	)
	if err != nil {
		log.Fatalf("create router plugin: %v", err)
	}

	bff.cells.start()
	plugin.Serve()
	// Serve returns when the Router stops the plugin.
	if err := bff.cells.stop(); err != nil {
		log.Printf("stop cell routing: %v", err)
	}
}

type BffService struct {
	service.UnimplementedBffServiceServer
	// accessTokens verifies the access token of customer resolvers.
	accessTokens *identity.AccessTokenVerifier
	// cells routes calls of the slices to the Cell of the request Market.
	cells       *cellRouting
	listMarkets listMarketsHandler
	tracking    *orderTracking
	catalog     *restaurantCatalog
	checkout    *checkout
	management  *restaurantManagement
	kitchen     *kitchen
	courier     *courier
	courierPool *courierPool
	simulator   *simulator
}

func (s *BffService) QueryBffInfo(context.Context, *service.QueryBffInfoRequest) (*service.QueryBffInfoResponse, error) {
	return &service.QueryBffInfoResponse{
		BffInfo: &service.BffInfo{
			Name:    "nomnom-bff",
			Version: serviceVersion,
		},
	}, nil
}
