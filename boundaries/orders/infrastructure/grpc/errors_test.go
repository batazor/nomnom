package grpcadapter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// AC-635: the API table answers every application.ErrorKind.
func TestTransportTableCoversEveryKind(t *testing.T) {
	for _, kind := range application.ErrorKinds() {
		answer, ok := transportByKind[kind]
		assert.True(t, ok, kind)
		assert.NotEmpty(t, answer.reason, kind)
	}
}

// The API error table of the README: codes and reasons stay as published.
func TestTransportErrorKeepsPublishedCodesAndReasons(t *testing.T) {
	tests := []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{ErrUnauthenticated, codes.Unauthenticated, "AUTHENTICATION_REQUIRED"},
		{application.ErrDependencyUnavailable, codes.Unavailable, "DEPENDENCY_UNAVAILABLE"},
		{fulfillment.ErrWorkflowNotFound, codes.Unavailable, "DEPENDENCY_UNAVAILABLE"},
		{application.FailClosed(fmt.Errorf("spicedb: %w", application.ErrDependencyUnavailable)), codes.Unavailable, "DEPENDENCY_UNAVAILABLE"},
		{placeorder.ErrUnknownRestaurant, codes.NotFound, "RESTAURANT_NOT_FOUND"},
		{placeorder.ErrRestaurantNotAcceptingOrders, codes.FailedPrecondition, "RESTAURANT_NOT_ACCEPTING_ORDERS"},
		{placeorder.ErrPremiumUnavailable, codes.FailedPrecondition, "PREMIUM_UNAVAILABLE"},
		{application.InvalidInput("order_id", "is required"), codes.InvalidArgument, "INVALID_ARGUMENT"},
		{order.NotOrderable{}, codes.FailedPrecondition, "ORDER_NOT_ORDERABLE"},
		{vo.ErrInvalidQuantity, codes.InvalidArgument, "INVALID_ORDER"},
		{order.ErrOrderRequiresItems, codes.InvalidArgument, "INVALID_ORDER"},
		{order.ErrDuplicateMenuItem, codes.InvalidArgument, "INVALID_ORDER"},
		{order.CancellationNotAllowed{Reasons: []order.PolicyReason{{Code: "cancellation_window_expired"}}}, codes.FailedPrecondition, "CANCELLATION_WINDOW_EXPIRED"},
		{order.InvalidTransition{}, codes.FailedPrecondition, "ORDER_STATE_CONFLICT"},
		{fulfillment.ErrCancellationRefused, codes.FailedPrecondition, "ORDER_STATE_CONFLICT"},
		{order.GuardFailed{Reason: "occurred_at_required"}, codes.FailedPrecondition, "ORDER_STATE_CONFLICT"},
		{application.ErrPermissionDenied, codes.PermissionDenied, "OPERATION_NOT_ALLOWED"},
		{order.ErrOrderNotFound, codes.NotFound, "ORDER_NOT_FOUND"},
		{order.ErrOrderAlreadyExists, codes.AlreadyExists, "ORDER_ALREADY_EXISTS"},
		{application.ErrConcurrencyConflict, codes.Aborted, "CONCURRENCY_CONFLICT"},
		{errors.New("connection reset"), codes.Internal, "INTERNAL_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.reason+"/"+tt.err.Error(), func(t *testing.T) {
			st := status.Convert(transportError(tt.err))
			assert.Equal(t, tt.code, st.Code())
			require.Len(t, st.Details(), 1)
			info, ok := st.Details()[0].(*errdetails.ErrorInfo)
			require.True(t, ok)
			assert.Equal(t, tt.reason, info.GetReason())
			assert.Equal(t, errorDomain, info.GetDomain())
		})
	}
}

// Internal and unavailable failures do not leak their cause to the caller.
func TestTransportErrorHidesTechnicalCauses(t *testing.T) {
	assert.Equal(t, "internal error", status.Convert(transportError(errors.New("pq: password=secret"))).Message())
	assert.Equal(t, "dependency unavailable", status.Convert(transportError(fmt.Errorf("dial 10.0.0.1: %w", application.ErrDependencyUnavailable))).Message())
}
