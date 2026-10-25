package main

import (
	"context"
	"log"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	routerplugin "github.com/wundergraph/cosmo/router-plugin"
	"google.golang.org/grpc"
)

const serviceVersion = "0.1.0"

func main() {
	plugin, err := routerplugin.NewRouterPlugin(func(server *grpc.Server) {
		server.RegisterService(&service.BffService_ServiceDesc, &BffService{})
	}, routerplugin.WithTracing())
	if err != nil {
		log.Fatalf("create router plugin: %v", err)
	}

	plugin.Serve()
}

type BffService struct {
	service.UnimplementedBffServiceServer
}

func (s *BffService) QueryBffInfo(context.Context, *service.QueryBffInfoRequest) (*service.QueryBffInfoResponse, error) {
	return &service.QueryBffInfoResponse{
		BffInfo: &service.BffInfo{
			Name:    "nomnom-bff",
			Version: serviceVersion,
		},
	}, nil
}
