package main

import (
	"context"
	"log"
	"os"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/identity"
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
		// get their own service so Alloy can admit them (wg.is_plugin, alloy/apps.alloy).
		routerplugin.WithTracing(),
		routerplugin.WithServiceName("bff-plugin"),
		routerplugin.WithServiceVersion(serviceVersion),
	)
	if err != nil {
		log.Fatalf("create router plugin: %v", err)
	}

	plugin.Serve()
	// Serve returns when the Router stops the plugin.
	if err := bff.conns.close(); err != nil {
		log.Printf("close connections: %v", err)
	}
}

type BffService struct {
	service.UnimplementedBffServiceServer
	// accessTokens verifies the access token of customer resolvers.
	accessTokens *identity.AccessTokenVerifier
	// conns are the connections to the contexts of the Cell.
	conns       connections
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
