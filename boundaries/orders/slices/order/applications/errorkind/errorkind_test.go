package errorkind_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/failure"
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
	Kind errorkind.Kind
}{
	{"invalid input", failure.InvalidInput("order_id", "is required"), errorkind.InvalidInput},
	{"invalid value", vo.ErrInvalidQuantity, errorkind.InvalidInput},
	{"order not found", fmt.Errorf("%w: %q", order.ErrOrderNotFound, "order-1"), errorkind.NotFound},
	{"unknown restaurant", place.ErrUnknownRestaurant, errorkind.NotFound},
	{"order exists", order.ErrOrderAlreadyExists, errorkind.AlreadyExists},
	{"concurrency conflict", failure.ErrConcurrencyConflict, errorkind.Conflict},
	{"invalid transition", fmt.Errorf("apply: %w", order.InvalidTransition{}), errorkind.DomainRule},
	{"guard failed", order.GuardFailed{Reason: "occurred_at_required"}, errorkind.DomainRule},
	{"not orderable", order.NotOrderable{}, errorkind.DomainRule},
	{"cancellation not allowed", order.CancellationNotAllowed{}, errorkind.DomainRule},
	{"no items", order.ErrOrderRequiresItems, errorkind.DomainRule},
	{"duplicate item", order.ErrDuplicateMenuItem, errorkind.DomainRule},
	{"cancellation reason", order.ErrInvalidCancellationReason, errorkind.DomainRule},
	{"restaurant closed", place.ErrRestaurantNotAcceptingOrders, errorkind.DomainRule},
	{"cancellation refused", fulfillment.ErrCancellationRefused, errorkind.DomainRule},
	{"permission denied", authz.ErrPermissionDenied, errorkind.Permission},
	{"dependency unavailable", failure.ErrDependencyUnavailable, errorkind.Unavailable},
	{"workflow not found", fulfillment.ErrWorkflowNotFound, errorkind.Unavailable},
	{"fail-closed unavailable", authz.FailClosed(fmt.Errorf("spicedb: %w", failure.ErrDependencyUnavailable)), errorkind.Unavailable},
	{"fail-closed other", authz.FailClosed(errors.New("boom")), errorkind.Permission},
	{"infrastructure", errors.New("connection reset"), errorkind.Internal},
}

func TestClassify(t *testing.T) {
	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			assert.Equal(t, tt.Kind, errorkind.Classify(tt.Err))
		})
	}
}

func TestCasesCoverEveryKind(t *testing.T) {
	seen := map[errorkind.Kind]bool{}
	for _, tt := range cases {
		seen[tt.Kind] = true
	}
	for _, kind := range errorkind.All() {
		assert.True(t, seen[kind], kind)
	}
}
