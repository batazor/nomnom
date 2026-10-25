package grpcclient_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"nomnom/orders/pkg/application"
	deliveryv1 "nomnom/orders/pkg/gateway/gen/nomnom/delivery/v1"
	paymentsv1 "nomnom/orders/pkg/gateway/gen/nomnom/payments/v1"
	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours/grpcclient"
	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

type fakePayments struct {
	paymentsv1.UnimplementedPaymentsServiceServer
	created   []*paymentsv1.CreatePaymentRequest
	authorize []*paymentsv1.AuthorizePaymentRequest
	status    paymentsv1.PaymentOperationStatus
	err       error
}

func (f *fakePayments) CreatePayment(_ context.Context, r *paymentsv1.CreatePaymentRequest) (*paymentsv1.CreatePaymentResponse, error) {
	f.created = append(f.created, r)
	return &paymentsv1.CreatePaymentResponse{PaymentId: "payment-1"}, nil
}

func (f *fakePayments) operation() *paymentsv1.PaymentOperation {
	return &paymentsv1.PaymentOperation{PaymentId: "payment-1", Status: f.status, DeclineReason: "card_declined"}
}

func (f *fakePayments) AuthorizePayment(_ context.Context, r *paymentsv1.AuthorizePaymentRequest) (*paymentsv1.AuthorizePaymentResponse, error) {
	f.authorize = append(f.authorize, r)
	return &paymentsv1.AuthorizePaymentResponse{Operation: f.operation()}, f.err
}

func (f *fakePayments) CapturePayment(context.Context, *paymentsv1.CapturePaymentRequest) (*paymentsv1.CapturePaymentResponse, error) {
	return &paymentsv1.CapturePaymentResponse{Operation: f.operation()}, f.err
}

func (f *fakePayments) CancelAuthorization(context.Context, *paymentsv1.CancelAuthorizationRequest) (*paymentsv1.CancelAuthorizationResponse, error) {
	return &paymentsv1.CancelAuthorizationResponse{Operation: f.operation()}, f.err
}

func (f *fakePayments) RefundPayment(context.Context, *paymentsv1.RefundPaymentRequest) (*paymentsv1.RefundPaymentResponse, error) {
	return &paymentsv1.RefundPaymentResponse{Operation: f.operation()}, f.err
}

type fakeRestaurants struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	received  []*restaurantsv1.ReceiveOrderRequestRequest
	address   string
	cancelled bool
}

func (f *fakeRestaurants) GetRestaurant(_ context.Context, r *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: r.GetRestaurantId(), Address: f.address}}, nil
}

func (f *fakeRestaurants) ReceiveOrderRequest(_ context.Context, r *restaurantsv1.ReceiveOrderRequestRequest) (*restaurantsv1.ReceiveOrderRequestResponse, error) {
	f.received = append(f.received, r)
	return &restaurantsv1.ReceiveOrderRequestResponse{RestaurantOrderId: "ro-1", Created: true}, nil
}

func (f *fakeRestaurants) CancelRestaurantOrder(context.Context, *restaurantsv1.CancelRestaurantOrderRequest) (*restaurantsv1.CancelRestaurantOrderResponse, error) {
	if f.cancelled {
		return &restaurantsv1.CancelRestaurantOrderResponse{Cancelled: true}, nil
	}
	return &restaurantsv1.CancelRestaurantOrderResponse{RejectionReason: "already_ready"}, nil
}

type fakeDelivery struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	created []*deliveryv1.CreateDeliveryRequest
	err     error
}

func (f *fakeDelivery) CreateDelivery(_ context.Context, r *deliveryv1.CreateDeliveryRequest) (*deliveryv1.CreateDeliveryResponse, error) {
	f.created = append(f.created, r)
	return &deliveryv1.CreateDeliveryResponse{DeliveryId: "d-1", Created: true}, f.err
}

func (f *fakeDelivery) ReplanDelivery(context.Context, *deliveryv1.ReplanDeliveryRequest) (*deliveryv1.ReplanDeliveryResponse, error) {
	return &deliveryv1.ReplanDeliveryResponse{DeliveryId: "d-1"}, f.err
}

func (f *fakeDelivery) CancelDelivery(context.Context, *deliveryv1.CancelDeliveryRequest) (*deliveryv1.CancelDeliveryResponse, error) {
	return &deliveryv1.CancelDeliveryResponse{DeliveryId: "d-1", RejectionReason: "already_picked_up"}, f.err
}

type fakes struct {
	payments    *fakePayments
	restaurants *fakeRestaurants
	delivery    *fakeDelivery
	conn        *grpc.ClientConn
}

// serve runs the fake neighbours behind one in-process endpoint, like the internal gateway.
func serve(t *testing.T) *fakes {
	t.Helper()
	f := &fakes{
		payments:    &fakePayments{status: paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_SUCCEEDED},
		restaurants: &fakeRestaurants{address: "Kitchen street 1", cancelled: true},
		delivery:    &fakeDelivery{},
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	paymentsv1.RegisterPaymentsServiceServer(server, f.payments)
	restaurantsv1.RegisterRestaurantsServiceServer(server, f.restaurants)
	deliveryv1.RegisterDeliveryServiceServer(server, f.delivery)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///gateway",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	f.conn = conn
	return f
}

var payment = temporal.PaymentRequest{
	OrderID: "order-1", Amount: "20", Currency: "EUR", IdempotencyKey: "order:order-1:authorize_payment",
}

func TestPaymentsAuthorizeCreatesPaymentAndUsesStepKey(t *testing.T) {
	f := serve(t)

	d, err := grpcclient.NewPayments(f.conn).Authorize(context.Background(), payment)

	require.NoError(t, err)
	assert.True(t, d.Approved)
	require.Len(t, f.payments.created, 1)
	assert.Equal(t, "order-1", f.payments.created[0].GetOrderId())
	assert.Equal(t, "20", f.payments.created[0].GetAmount().GetAmount())
	require.Len(t, f.payments.authorize, 1)
	assert.Equal(t, "payment-1", f.payments.authorize[0].GetPaymentId())
	assert.Equal(t, payment.IdempotencyKey, f.payments.authorize[0].GetIdempotencyKey())
}

// AC-457: a provider decline is a business decision, not an error.
func TestPaymentsDeclineIsDecision(t *testing.T) {
	f := serve(t)
	f.payments.status = paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_DECLINED

	d, err := grpcclient.NewPayments(f.conn).Capture(context.Background(), payment)

	require.NoError(t, err)
	assert.Equal(t, temporal.PaymentDecision{Reason: "card_declined"}, d)
}

// AC-458: a pending operation is retried by the Activity with the same key.
func TestPaymentsPendingIsRetryable(t *testing.T) {
	f := serve(t)
	f.payments.status = paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_PENDING

	_, err := grpcclient.NewPayments(f.conn).Authorize(context.Background(), payment)

	require.ErrorIs(t, err, grpcclient.ErrOperationPending)
	assert.NotErrorIs(t, err, application.ErrInvalidInput)
}

func TestPaymentsDeclinedCompensationIsError(t *testing.T) {
	f := serve(t)
	f.payments.status = paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_DECLINED

	require.Error(t, grpcclient.NewPayments(f.conn).Refund(context.Background(), payment))
	f.payments.status = paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_SUCCEEDED
	require.NoError(t, grpcclient.NewPayments(f.conn).CancelAuthorization(context.Background(), payment))
}

func TestErrorsMapToRetryability(t *testing.T) {
	tests := []struct {
		code      codes.Code
		permanent bool
	}{
		{codes.InvalidArgument, true},
		{codes.NotFound, true},
		{codes.Unavailable, false},
		{codes.Unauthenticated, false},
		{codes.DeadlineExceeded, false},
	}
	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			f := serve(t)
			f.delivery.err = status.Error(tt.code, "boom")

			err := grpcclient.NewDelivery(f.conn).Cancel(context.Background(), temporal.DeliveryRequest{OrderID: "order-1"})

			require.Error(t, err)
			assert.Equal(t, tt.permanent, errors.Is(err, application.ErrInvalidInput))
		})
	}
}

func TestRestaurantsRequestAndCancel(t *testing.T) {
	f := serve(t)
	restaurants := grpcclient.NewRestaurants(f.conn)
	request := temporal.RestaurantOrderRequest{
		OrderID: "order-1", RestaurantID: "restaurant-1",
		Items: []temporal.RestaurantOrderItem{{MenuItemID: "menu-1", Quantity: 2}},
	}

	require.NoError(t, restaurants.RequestOrder(context.Background(), request))
	require.NoError(t, restaurants.CancelOrder(context.Background(), request))
	f.restaurants.cancelled = false
	require.NoError(t, restaurants.CancelOrder(context.Background(), request), "a rejection by stage is done")

	require.Len(t, f.restaurants.received, 1)
	assert.Equal(t, "menu-1", f.restaurants.received[0].GetItems()[0].GetMenuItemId())
	assert.Equal(t, int32(2), f.restaurants.received[0].GetItems()[0].GetQuantity())
}

func TestDeliveryCreateUsesRestaurantAddress(t *testing.T) {
	f := serve(t)
	d := grpcclient.NewDelivery(f.conn)

	require.NoError(t, d.Create(context.Background(), temporal.DeliveryRequest{
		OrderID: "order-1", RestaurantID: "restaurant-1", DeliveryAddress: "Main street 1",
	}))
	require.NoError(t, d.Cancel(context.Background(), temporal.DeliveryRequest{OrderID: "order-1"}))

	require.Len(t, f.delivery.created, 1)
	assert.Equal(t, "Kitchen street 1", f.delivery.created[0].GetPickupAddress())
	assert.Equal(t, "Main street 1", f.delivery.created[0].GetDropoffAddress())
}

func TestDeliveryCreateWithoutRestaurantAddressIsPermanent(t *testing.T) {
	f := serve(t)
	f.restaurants.address = ""

	err := grpcclient.NewDelivery(f.conn).Create(context.Background(), temporal.DeliveryRequest{OrderID: "order-1", RestaurantID: "r"})

	require.ErrorIs(t, err, application.ErrInvalidInput)
	assert.Empty(t, f.delivery.created)
}

func TestDeliveryReplanPreconditionIsSkipped(t *testing.T) {
	f := serve(t)
	f.delivery.err = status.Error(codes.FailedPrecondition, "candidate busy")

	require.NoError(t, grpcclient.NewDelivery(f.conn).Replan(context.Background(), temporal.DeliveryRequest{OrderID: "order-1"}))
}
