package errorkind_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// cases name every error of the slice an adapter can meet; together they
// produce every kind, so the adapter tables are exercised in full.
var cases = []struct {
	Name string
	Err  error
	Kind application.ErrorKind
}{
	{"invalid input", application.InvalidInput("order_id", "is required"), application.KindInvalidInput},
	{"invalid value", vo.ErrInvalidQuantity, application.KindInvalidInput},
	{"order not found", fmt.Errorf("%w: %q", order.ErrOrderNotFound, "order-1"), application.KindNotFound},
	{"unknown restaurant", placeorder.ErrUnknownRestaurant, application.KindNotFound},
	{"order exists", order.ErrOrderAlreadyExists, application.KindAlreadyExists},
	{"concurrency conflict", application.ErrConcurrencyConflict, application.KindConflict},
	{"invalid transition", fmt.Errorf("apply: %w", order.InvalidTransition{}), application.KindDomainRule},
	{"guard failed", order.GuardFailed{Reason: "occurred_at_required"}, application.KindDomainRule},
	{"not orderable", order.NotOrderable{}, application.KindDomainRule},
	{"cancellation not allowed", order.CancellationNotAllowed{}, application.KindDomainRule},
	{"no items", order.ErrOrderRequiresItems, application.KindDomainRule},
	{"duplicate item", order.ErrDuplicateMenuItem, application.KindDomainRule},
	{"cancellation reason", order.ErrInvalidCancellationReason, application.KindDomainRule},
	{"restaurant closed", placeorder.ErrRestaurantNotAcceptingOrders, application.KindDomainRule},
	{"cancellation refused", fulfillment.ErrCancellationRefused, application.KindDomainRule},
	{"permission denied", application.ErrPermissionDenied, application.KindPermission},
	{"dependency unavailable", application.ErrDependencyUnavailable, application.KindUnavailable},
	{"workflow not found", fulfillment.ErrWorkflowNotFound, application.KindUnavailable},
	{"fail-closed unavailable", application.FailClosed(fmt.Errorf("spicedb: %w", application.ErrDependencyUnavailable)), application.KindUnavailable},
	{"fail-closed other", application.FailClosed(errors.New("boom")), application.KindPermission},
	{"infrastructure", errors.New("connection reset"), application.KindInternal},
}

func TestClassify(t *testing.T) {
	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			assert.Equal(t, tt.Kind, errorkind.Classify(tt.Err))
		})
	}
}

func TestCasesCoverEveryKind(t *testing.T) {
	seen := map[application.ErrorKind]bool{}
	for _, tt := range cases {
		seen[tt.Kind] = true
	}
	for _, kind := range application.ErrorKinds() {
		assert.True(t, seen[kind], kind)
	}
}
