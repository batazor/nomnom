// Package gateway connects orders to the business APIs of neighbouring
// contexts through the internal gateway (ADR 15) with the projected
// ServiceAccount token of the target context (ADR 27). Both the fulfillment
// Activities and the Place Order catalog use it; gen holds their clients.
package gateway

import (
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial opens a lazy connection to the internal gateway; no I/O happens here.
// With a non-empty tokenDir every call carries the projected token of the
// target context (ADR 27).
func Dial(address, tokenDir string) (*grpc.ClientConn, error) {
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Client-side метрики зависимостей через internal gateway (глобальный MeterProvider).
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
	if tokenDir != "" {
		options = append(options, grpc.WithUnaryInterceptor(ServiceToken(tokenDir)))
	}
	conn, err := grpc.NewClient(address, options...)
	if err != nil {
		return nil, fmt.Errorf("dial internal gateway %s: %w", address, err)
	}
	return conn, nil
}
