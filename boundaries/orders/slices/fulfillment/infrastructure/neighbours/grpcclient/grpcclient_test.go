package grpcclient_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"nomnom/orders/pkg/application"
	deliveryv1 "nomnom/orders/pkg/gateway/gen/nomnom/delivery/v1"
	paymentsv1 "nomnom/orders/pkg/gateway/gen/nomnom/payments/v1"
	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/fulfillment/applications/neighbours"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours/grpcclient"
)

// serve runs the mocked neighbours behind one in-process endpoint, like the internal gateway.
func serve(t *testing.T) (*grpc.ClientConn, *MockPaymentsServiceServer, *MockRestaurantsServiceServer, *MockDeliveryServiceServer) {
	t.Helper()
	payments := NewMockPaymentsServiceServer(t)
	restaurants := NewMockRestaurantsServiceServer(t)
	delivery := NewMockDeliveryServiceServer(t)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	paymentsv1.RegisterPaymentsServiceServer(server, payments)
	restaurantsv1.RegisterRestaurantsServiceServer(server, restaurants)
	deliveryv1.RegisterDeliveryServiceServer(server, delivery)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///gateway",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, payments, restaurants, delivery
}

var payment = neighbours.PaymentRequest{
	OrderID: "order-1", MarketID: "berlin", Amount: "20", Currency: "EUR", IdempotencyKey: "order:order-1:authorize_payment",
}

// createPayment is the CreatePayment request every payment operation sends first.
var createPayment = &paymentsv1.CreatePaymentRequest{
	OrderId: "order-1", Amount: &paymentsv1.Money{Amount: "20", Currency: "EUR"}, MarketId: "berlin",
}

func TestPaymentsAuthorizeCreatesPaymentAndUsesStepKey(t *testing.T) {
	conn, payments, _, _ := serve(t)
	payments.EXPECT().CreatePayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.CreatePaymentRequest) bool {
		// The Payment is in the Market of the Order.
		return proto.Equal(r, createPayment)
	})).Return(&paymentsv1.CreatePaymentResponse{PaymentId: "payment-1"}, nil).Once()
	payments.EXPECT().AuthorizePayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.AuthorizePaymentRequest) bool {
		return proto.Equal(r, &paymentsv1.AuthorizePaymentRequest{PaymentId: "payment-1", IdempotencyKey: payment.IdempotencyKey})
	})).Return(&paymentsv1.AuthorizePaymentResponse{Operation: &paymentsv1.PaymentOperation{PaymentId: "payment-1", Status: paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_SUCCEEDED, DeclineReason: "card_declined"}}, nil).Once()

	d, err := grpcclient.NewPayments(conn).Authorize(context.Background(), payment)

	require.NoError(t, err)
	assert.True(t, d.Approved)
}

// AC-457: a provider decline is a business decision, not an error.
func TestPaymentsDeclineIsDecision(t *testing.T) {
	conn, payments, _, _ := serve(t)
	payments.EXPECT().CreatePayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.CreatePaymentRequest) bool {
		return proto.Equal(r, createPayment)
	})).
		Return(&paymentsv1.CreatePaymentResponse{PaymentId: "payment-1"}, nil).Once()
	payments.EXPECT().CapturePayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.CapturePaymentRequest) bool {
		return proto.Equal(r, &paymentsv1.CapturePaymentRequest{PaymentId: "payment-1", IdempotencyKey: payment.IdempotencyKey})
	})).
		Return(&paymentsv1.CapturePaymentResponse{Operation: &paymentsv1.PaymentOperation{PaymentId: "payment-1", Status: paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_DECLINED, DeclineReason: "card_declined"}}, nil).Once()

	d, err := grpcclient.NewPayments(conn).Capture(context.Background(), payment)

	require.NoError(t, err)
	assert.Equal(t, neighbours.PaymentDecision{Reason: "card_declined"}, d)
}

// AC-458: a pending operation is retried by the Activity with the same key.
func TestPaymentsPendingIsRetryable(t *testing.T) {
	conn, payments, _, _ := serve(t)
	payments.EXPECT().CreatePayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.CreatePaymentRequest) bool {
		return proto.Equal(r, createPayment)
	})).
		Return(&paymentsv1.CreatePaymentResponse{PaymentId: "payment-1"}, nil).Once()
	payments.EXPECT().AuthorizePayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.AuthorizePaymentRequest) bool {
		return proto.Equal(r, &paymentsv1.AuthorizePaymentRequest{PaymentId: "payment-1", IdempotencyKey: payment.IdempotencyKey})
	})).
		Return(&paymentsv1.AuthorizePaymentResponse{Operation: &paymentsv1.PaymentOperation{PaymentId: "payment-1", Status: paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_PENDING, DeclineReason: "card_declined"}}, nil).Once()

	_, err := grpcclient.NewPayments(conn).Authorize(context.Background(), payment)

	require.ErrorIs(t, err, grpcclient.ErrOperationPending)
	assert.NotErrorIs(t, err, application.ErrInvalidInput)
}

func TestPaymentsDeclinedCompensationIsError(t *testing.T) {
	conn, payments, _, _ := serve(t)
	payments.EXPECT().CreatePayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.CreatePaymentRequest) bool {
		return proto.Equal(r, createPayment)
	})).
		Return(&paymentsv1.CreatePaymentResponse{PaymentId: "payment-1"}, nil).Times(2)
	payments.EXPECT().RefundPayment(mock.Anything, mock.MatchedBy(func(r *paymentsv1.RefundPaymentRequest) bool {
		return proto.Equal(r, &paymentsv1.RefundPaymentRequest{PaymentId: "payment-1", IdempotencyKey: payment.IdempotencyKey})
	})).
		Return(&paymentsv1.RefundPaymentResponse{Operation: &paymentsv1.PaymentOperation{PaymentId: "payment-1", Status: paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_DECLINED, DeclineReason: "card_declined"}}, nil).Once()
	payments.EXPECT().CancelAuthorization(mock.Anything, mock.MatchedBy(func(r *paymentsv1.CancelAuthorizationRequest) bool {
		return proto.Equal(r, &paymentsv1.CancelAuthorizationRequest{PaymentId: "payment-1", IdempotencyKey: payment.IdempotencyKey})
	})).
		Return(&paymentsv1.CancelAuthorizationResponse{Operation: &paymentsv1.PaymentOperation{PaymentId: "payment-1", Status: paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_SUCCEEDED, DeclineReason: "card_declined"}}, nil).Once()

	require.Error(t, grpcclient.NewPayments(conn).Refund(context.Background(), payment))
	require.NoError(t, grpcclient.NewPayments(conn).CancelAuthorization(context.Background(), payment))
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
			conn, _, _, delivery := serve(t)
			delivery.EXPECT().CancelDelivery(mock.Anything, mock.MatchedBy(func(r *deliveryv1.CancelDeliveryRequest) bool {
				return proto.Equal(r, &deliveryv1.CancelDeliveryRequest{OrderId: "order-1"})
			})).Return(nil, status.Error(tt.code, "boom")).Once()

			err := grpcclient.NewDelivery(conn).Cancel(context.Background(), neighbours.DeliveryRequest{OrderID: "order-1"})

			require.Error(t, err)
			assert.Equal(t, tt.permanent, errors.Is(err, application.ErrInvalidInput))
		})
	}
}

func TestRestaurantsRequestAndCancel(t *testing.T) {
	conn, _, server, _ := serve(t)
	server.EXPECT().ReceiveOrderRequest(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.ReceiveOrderRequestRequest) bool {
		return proto.Equal(r, &restaurantsv1.ReceiveOrderRequestRequest{
			RestaurantId: "restaurant-1",
			OrderId:      "order-1",
			Items:        []*restaurantsv1.OrderRequestItem{{MenuItemId: "menu-1", Quantity: 2}},
			Priority:     restaurantsv1.OrderPriority_ORDER_PRIORITY_PREMIUM, // AC-857
		})
	})).Return(&restaurantsv1.ReceiveOrderRequestResponse{RestaurantOrderId: "ro-1", Created: true}, nil).Once()
	server.EXPECT().CancelRestaurantOrder(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.CancelRestaurantOrderRequest) bool {
		return proto.Equal(r, &restaurantsv1.CancelRestaurantOrderRequest{RestaurantId: "restaurant-1", OrderId: "order-1"})
	})).
		Return(&restaurantsv1.CancelRestaurantOrderResponse{Cancelled: true}, nil).Once()
	server.EXPECT().CancelRestaurantOrder(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.CancelRestaurantOrderRequest) bool {
		return proto.Equal(r, &restaurantsv1.CancelRestaurantOrderRequest{RestaurantId: "restaurant-1", OrderId: "order-1"})
	})).
		Return(&restaurantsv1.CancelRestaurantOrderResponse{RejectionReason: "already_ready"}, nil).Once()
	restaurants := grpcclient.NewRestaurants(conn)
	request := neighbours.RestaurantOrderRequest{
		OrderID: "order-1", RestaurantID: "restaurant-1",
		Items:    []neighbours.RestaurantOrderItem{{MenuItemID: "menu-1", Quantity: 2}},
		Priority: neighbours.PriorityPremium,
	}

	id, err := restaurants.RequestOrder(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, "ro-1", id)
	require.NoError(t, restaurants.CancelOrder(context.Background(), request))
	require.NoError(t, restaurants.CancelOrder(context.Background(), request), "a rejection by stage is done")
}

func TestDeliveryCreateSendsTheOrder(t *testing.T) {
	conn, _, _, delivery := serve(t)
	delivery.EXPECT().CreateDelivery(mock.Anything, mock.MatchedBy(func(r *deliveryv1.CreateDeliveryRequest) bool {
		// The Delivery is in the Market of the Order.
		return proto.Equal(r, &deliveryv1.CreateDeliveryRequest{OrderId: "order-1", MarketId: "berlin"})
	})).Return(&deliveryv1.CreateDeliveryResponse{DeliveryId: "d-1", Created: true}, nil).Once()
	delivery.EXPECT().CancelDelivery(mock.Anything, mock.MatchedBy(func(r *deliveryv1.CancelDeliveryRequest) bool {
		return proto.Equal(r, &deliveryv1.CancelDeliveryRequest{OrderId: "order-1"})
	})).
		Return(&deliveryv1.CancelDeliveryResponse{DeliveryId: "d-1", RejectionReason: "already_picked_up"}, nil).Once()
	d := grpcclient.NewDelivery(conn)

	require.NoError(t, d.Create(context.Background(), neighbours.DeliveryRequest{OrderID: "order-1", MarketID: "berlin"}))
	require.NoError(t, d.Cancel(context.Background(), neighbours.DeliveryRequest{OrderID: "order-1", MarketID: "berlin"}))
}

// FR-683: a Scheduled Order carries its window and planned readiness.
func TestRestaurantsRequestSendsTheScheduleOfAScheduledOrder(t *testing.T) {
	conn, _, server, _ := serve(t)
	windowStart := time.Date(2026, time.October, 25, 19, 0, 0, 0, time.UTC)
	plannedReadyAt := windowStart.Add(-20 * time.Minute)
	server.EXPECT().ReceiveOrderRequest(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.ReceiveOrderRequestRequest) bool {
		return proto.Equal(r, &restaurantsv1.ReceiveOrderRequestRequest{
			RestaurantId: "restaurant-1",
			OrderId:      "order-1",
			Items:        []*restaurantsv1.OrderRequestItem{{MenuItemId: "menu-1", Quantity: 1}},
			Priority:     restaurantsv1.OrderPriority_ORDER_PRIORITY_STANDARD,
			Schedule: &restaurantsv1.OrderSchedule{
				WindowStart:    timestamppb.New(windowStart),
				WindowEnd:      timestamppb.New(windowStart.Add(15 * time.Minute)),
				PlannedReadyAt: timestamppb.New(plannedReadyAt),
			},
		})
	})).Return(&restaurantsv1.ReceiveOrderRequestResponse{RestaurantOrderId: "ro-1", Created: true}, nil).Once()

	id, err := grpcclient.NewRestaurants(conn).RequestOrder(context.Background(), neighbours.RestaurantOrderRequest{
		OrderID: "order-1", RestaurantID: "restaurant-1",
		Items:    []neighbours.RestaurantOrderItem{{MenuItemID: "menu-1", Quantity: 1}},
		Priority: neighbours.PriorityStandard,
		Schedule: &neighbours.RestaurantOrderSchedule{
			WindowStart: windowStart, WindowEnd: windowStart.Add(15 * time.Minute), PlannedReadyAt: plannedReadyAt,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "ro-1", id)
}

func TestRestaurantsQueueConfirmAndReleaseSlot(t *testing.T) {
	conn, _, server, _ := serve(t)
	server.EXPECT().QueueRestaurantOrder(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.QueueRestaurantOrderRequest) bool {
		return proto.Equal(r, &restaurantsv1.QueueRestaurantOrderRequest{RestaurantOrderId: "ro-1"})
	})).Return(&restaurantsv1.QueueRestaurantOrderResponse{}, nil).Once()
	server.EXPECT().ConfirmScheduledSlot(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.ConfirmScheduledSlotRequest) bool {
		return proto.Equal(r, &restaurantsv1.ConfirmScheduledSlotRequest{OrderId: "order-1"})
	})).Return(&restaurantsv1.ConfirmScheduledSlotResponse{
		Result: &restaurantsv1.ConfirmScheduledSlotResponse_Confirmed{Confirmed: &restaurantsv1.DeliveryWindow{}},
	}, nil).Once()
	server.EXPECT().ConfirmScheduledSlot(mock.Anything, mock.Anything).Return(&restaurantsv1.ConfirmScheduledSlotResponse{
		Result: &restaurantsv1.ConfirmScheduledSlotResponse_SlotUnavailable{SlotUnavailable: &restaurantsv1.SlotUnavailable{}},
	}, nil).Once()
	server.EXPECT().ReleaseScheduledSlot(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.ReleaseScheduledSlotRequest) bool {
		return proto.Equal(r, &restaurantsv1.ReleaseScheduledSlotRequest{OrderId: "order-1"})
	})).Return(&restaurantsv1.ReleaseScheduledSlotResponse{}, nil).Once()
	restaurants := grpcclient.NewRestaurants(conn)
	slot := neighbours.SlotRequest{OrderID: "order-1", IdempotencyKey: "order:order-1:confirm_slot"}

	require.NoError(t, restaurants.QueueOrder(context.Background(), neighbours.QueueRequest{OrderID: "order-1", RestaurantOrderID: "ro-1"}))
	confirmed, err := restaurants.ConfirmSlot(context.Background(), slot)
	require.NoError(t, err)
	assert.True(t, confirmed.Confirmed)
	unavailable, err := restaurants.ConfirmSlot(context.Background(), slot)
	require.NoError(t, err)
	assert.False(t, unavailable.Confirmed, "a full window is a decision, not an error")
	require.NoError(t, restaurants.ReleaseSlot(context.Background(), slot))
}
