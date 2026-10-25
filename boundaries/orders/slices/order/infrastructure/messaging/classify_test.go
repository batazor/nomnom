package messaging

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/domains/order"
)

// AC-635: the consumer table classifies every application.ErrorKind.
func TestClassTableCoversEveryKind(t *testing.T) {
	for _, kind := range application.ErrorKinds() {
		_, ok := classByKind[kind]
		assert.True(t, ok, kind)
	}
}

func TestClassifyApplicationErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want messaging.Class
	}{
		{"invalid input", application.InvalidInput("order_id", "is required"), messaging.ClassPermanent},
		{"unknown order", fmt.Errorf("%w: %q", order.ErrOrderNotFound, "order-1"), messaging.ClassPermanent},
		{"permission denied", application.ErrPermissionDenied, messaging.ClassPermanent},
		{"invalid transition", fmt.Errorf("apply: %w", order.InvalidTransition{}), messaging.ClassBusiness},
		{"guard failed", order.GuardFailed{Reason: "x"}, messaging.ClassBusiness},
		{"order exists", order.ErrOrderAlreadyExists, messaging.ClassBusiness},
		{"concurrency conflict", application.ErrConcurrencyConflict, messaging.ClassTransient},
		{"dependency unavailable", application.ErrDependencyUnavailable, messaging.ClassTransient},
		{"infrastructure", errors.New("connection reset"), messaging.ClassTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, messaging.Classify(classify(tt.err)))
		})
	}
	assert.NoError(t, classify(nil))
}
