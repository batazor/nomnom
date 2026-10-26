package infrastructure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// orderCouriers answers GetOrderCourier and records whether the call carried
// the customer as a user assertion.
type orderCouriers struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	response *deliveryv1.GetOrderCourierResponse
	err      error
	asserted bool
}

func (s *orderCouriers) GetOrderCourier(ctx context.Context, _ *deliveryv1.GetOrderCourierRequest) (*deliveryv1.GetOrderCourierResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.asserted = len(md.Get(identity.AssertionMetadata)) == 1
	return s.response, s.err
}

func couriersOver(t *testing.T, server *orderCouriers) *Couriers {
	t.Helper()
	return NewCouriers(connect(t, "nomnom.delivery", func(s *grpc.Server) {
		deliveryv1.RegisterDeliveryServiceServer(s, server)
	}))
}

// AC-795: the courier of the Order is read on behalf of the customer.
func TestCourierOfTheOrderIsReadForTheCustomer(t *testing.T) {
	server := &orderCouriers{response: &deliveryv1.GetOrderCourierResponse{CourierId: "courier-1", Name: "Тимур"}}

	courier, err := couriersOver(t, server).CourierOf(customerContext(), "order-1")

	require.NoError(t, err)
	assert.Equal(t, &applications.Courier{Name: "Тимур"}, courier)
	assert.True(t, server.asserted)

	server.response = &deliveryv1.GetOrderCourierResponse{}
	courier, err = couriersOver(t, server).CourierOf(customerContext(), "order-1")
	require.NoError(t, err)
	assert.Nil(t, courier, "an empty answer is no courier")

	server.err = status.Error(codes.Unavailable, "down")
	_, err = couriersOver(t, server).CourierOf(customerContext(), "order-1")
	assert.Error(t, err)
}
