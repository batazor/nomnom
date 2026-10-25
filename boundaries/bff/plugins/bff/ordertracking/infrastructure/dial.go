// Package infrastructure adapts the order tracking ports to the gRPC APIs of
// orders and restaurants, called through the internal gateway (ADR 15).
package infrastructure

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
)

// Dial opens a lazy connection to the internal gateway for one target context;
// every call carries the service token of BFF for audience and, when the
// request has a customer, a user assertion (ADR 32). No I/O happens here.
func Dial(gateway, audience string, customers *identity.Identity) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(gateway,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor(audience, customers.ServiceTokens, customers.Assertions)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial internal gateway %s for %s: %w", gateway, audience, err)
	}
	return conn, nil
}
