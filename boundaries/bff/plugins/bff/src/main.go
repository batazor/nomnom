package main

import (
	"context"
	"log"
	"os"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	routerplugin "github.com/wundergraph/cosmo/router-plugin"
	"google.golang.org/grpc"
)

const serviceVersion = "0.1.0"

func main() {
	// A partial identity configuration stops the plugin: customer operations
	// must not run without a verified customer (ADR 32).
	customers, err := identity.Load(identity.ConfigFromEnv(os.Getenv))
	if err != nil {
		log.Fatalf("load identity: %v", err)
	}

	plugin, err := routerplugin.NewRouterPlugin(func(server *grpc.Server) {
		server.RegisterService(&service.BffService_ServiceDesc, &BffService{identity: customers})
	}, routerplugin.WithTracing())
	if err != nil {
		log.Fatalf("create router plugin: %v", err)
	}

	plugin.Serve()
}

type BffService struct {
	service.UnimplementedBffServiceServer
	// identity is nil until BFF identity is configured; customer resolvers
	// verify the access token and sign user assertions with it.
	identity *identity.Identity
}

func (s *BffService) QueryBffInfo(context.Context, *service.QueryBffInfoRequest) (*service.QueryBffInfoResponse, error) {
	return &service.QueryBffInfoResponse{
		BffInfo: &service.BffInfo{
			Name:    "nomnom-bff",
			Version: serviceVersion,
		},
	}, nil
}
