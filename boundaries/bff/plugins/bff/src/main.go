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

	plugin.Serve()
}

type BffService struct {
	service.UnimplementedBffServiceServer
	// accessTokens is nil until BFF identity is configured; customer resolvers
	// verify the access token with it.
	accessTokens *identity.AccessTokenVerifier
	// tracking, catalog, checkout and management are nil until identity and
	// the internal gateway are configured.
	tracking   *orderTracking
	catalog    *restaurantCatalog
	checkout   *checkout
	management *restaurantManagement
}

func (s *BffService) QueryBffInfo(context.Context, *service.QueryBffInfoRequest) (*service.QueryBffInfoResponse, error) {
	return &service.QueryBffInfoResponse{
		BffInfo: &service.BffInfo{
			Name:    "nomnom-bff",
			Version: serviceVersion,
		},
	}, nil
}
