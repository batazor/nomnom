package errorkind_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/place"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// cases name every error of the slice an adapter can meet; together they
// produce every kind, so the adapter tables are exercised in full.
var cases = []struct {
	Name string
	Err  error
	Kind usecase.ErrorKind
}{
	{"invalid input", usecase.InvalidInput("order_id", "is required"), usecase.KindInvalidInput},
	{"invalid value", vo.ErrInvalidQuantity, usecase.KindInvalidInput},
	{"order not found", fmt.Errorf("%w: %q", order.ErrOrderNotFound, "order-1"), usecase.KindNotFound},
	{"unknown restaurant", place.ErrUnknownRestaurant, usecase.KindNotFound},
	{"order exists", order.ErrOrderAlreadyExists, usecase.KindAlreadyExists},
	{"concurrency conflict", usecase.ErrConcurrencyConflict, usecase.KindConflict},
	{"invalid transition", fmt.Errorf("apply: %w", order.InvalidTransition{}), usecase.KindDomainRule},
	{"guard failed", order.GuardFailed{Reason: "occurred_at_required"}, usecase.KindDomainRule},
	{"not orderable", order.NotOrderable{}, usecase.KindDomainRule},
	{"cancellation not allowed", order.CancellationNotAllowed{}, usecase.KindDomainRule},
	{"no items", order.ErrOrderRequiresItems, usecase.KindDomainRule},
	{"duplicate item", order.ErrDuplicateMenuItem, usecase.KindDomainRule},
	{"cancellation reason", order.ErrInvalidCancellationReason, usecase.KindDomainRule},
	{"restaurant closed", place.ErrRestaurantNotAcceptingOrders, usecase.KindDomainRule},
	{"cancellation refused", fulfillment.ErrCancellationRefused, usecase.KindDomainRule},
	{"permission denied", authz.ErrPermissionDenied, usecase.KindPermission},
	{"dependency unavailable", usecase.ErrDependencyUnavailable, usecase.KindUnavailable},
	{"workflow not found", fulfillment.ErrWorkflowNotFound, usecase.KindUnavailable},
	{"fail-closed unavailable", authz.FailClosed(fmt.Errorf("spicedb: %w", usecase.ErrDependencyUnavailable)), usecase.KindUnavailable},
	{"fail-closed other", authz.FailClosed(errors.New("boom")), usecase.KindPermission},
	{"infrastructure", errors.New("connection reset"), usecase.KindInternal},
}

func TestClassify(t *testing.T) {
	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			assert.Equal(t, tt.Kind, errorkind.Classify(tt.Err))
		})
	}
}

func TestCasesCoverEveryKind(t *testing.T) {
	seen := map[usecase.ErrorKind]bool{}
	for _, tt := range cases {
		seen[tt.Kind] = true
	}
	for _, kind := range usecase.ErrorKinds() {
		assert.True(t, seen[kind], kind)
	}
}
