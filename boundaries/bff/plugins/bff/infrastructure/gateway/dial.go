// Package gateway connects the slices of BFF to the gRPC APIs of bounded
// contexts through the internal gateway (ADR 15).
package gateway

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
)

// Dial opens a lazy connection to the internal gateway for one target context;
// every call carries the service token of BFF for audience and, when the
// request has a customer, a user assertion (ADR 32). Every call gets a deadline
// of callTimeout at most: the Router gives the plugin a request without one, and
// the gateway only bounds a call by its own upper limit (ADR 15). The client
// span continues the trace of the GraphQL request: the router plugin installs
// the global TracerProvider and W3C propagator, otelgrpc injects traceparent.
// No I/O happens here.
func Dial(gateway, audience string, callTimeout time.Duration, credentials identity.OutgoingCredentials) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(gateway,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			Deadline(callTimeout),
			identity.UnaryClientInterceptor(audience, credentials.ServiceTokens, credentials.Signer),
		),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial internal gateway %s for %s: %w", gateway, audience, err)
	}
	return conn, nil
}

// Deadline bounds every call by timeout; an earlier deadline of the caller
// stays. gRPC sends the deadline as grpc-timeout, so the gateway and the
// context stop the call as well.
func Deadline(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
