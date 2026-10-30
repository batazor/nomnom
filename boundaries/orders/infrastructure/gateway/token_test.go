package gateway_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"nomnom/orders/infrastructure/gateway"
)

// AC-463: every call carries the current projected token of the target audience.
func TestServiceTokenFollowsAudienceAndRotation(t *testing.T) {
	dir := t.TempDir()
	write := func(audience, token string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, audience), []byte(token+"\n"), 0o600))
	}
	write("nomnom.payments", "payments-token-1")
	write("nomnom.restaurants", "restaurants-token")

	var seen []string
	capture := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ grpc.UnaryInvoker, _ ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		seen = append(seen, md.Get("authorization")...)
		return nil
	}
	call := func(method string) error {
		return gateway.ServiceToken(dir)(context.Background(), method, nil, nil, nil,
			func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				return capture(ctx, method, req, reply, cc, nil, opts...)
			})
	}

	require.NoError(t, call("/nomnom.payments.v1.PaymentsService/AuthorizePayment"))
	write("nomnom.payments", "payments-token-2")
	require.NoError(t, call("/nomnom.payments.v1.PaymentsService/CapturePayment"))
	require.NoError(t, call("/nomnom.restaurants.v1.RestaurantsService/GetRestaurant"))
	require.Error(t, call("/nomnom.delivery.v1.DeliveryService/CreateDelivery"), "missing token file")
	require.Error(t, call("/other.v1.Service/Method"))

	assert.Equal(t, []string{"Bearer payments-token-1", "Bearer payments-token-2", "Bearer restaurants-token"}, seen)
}
