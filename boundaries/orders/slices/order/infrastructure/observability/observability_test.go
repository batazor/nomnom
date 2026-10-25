package observability_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/observability"
)

func TestClassifySeparatesRejectionsFromFailures(t *testing.T) {
	cases := map[string]struct {
		err  error
		want telemetry.Failure
	}{
		"invalid input":        {application.InvalidInput("items", "required"), rejected("invalid_input")},
		"permission denied":    {application.FailClosed(errors.New("spicedb unavailable")), rejected("permission_denied")},
		"order not found":      {fmt.Errorf("load: %w", order.ErrOrderNotFound), rejected("not_found")},
		"unknown restaurant":   {placeorder.ErrUnknownRestaurant, rejected("not_found")},
		"closed restaurant":    {placeorder.ErrRestaurantNotAcceptingOrders, rejected("domain_rule")},
		"already exists":       {order.ErrOrderAlreadyExists, rejected("already_exists")},
		"invalid transition":   {fmt.Errorf("apply: %w", order.InvalidTransition{}), rejected("invalid_transition")},
		"guard failed":         {order.GuardFailed{Reason: "paid"}, rejected("guard_failed")},
		"domain rule":          {order.ErrDuplicateMenuItem, rejected("domain_rule")},
		"cancellation refused": {fulfillment.ErrCancellationRefused, rejected("cancellation_refused")},
		"concurrency conflict": {application.ErrConcurrencyConflict, failed("concurrency_conflict")},
		"corrupted snapshot":   {order.ErrInvalidSnapshot, failed("internal")},
		"unknown":              {errors.New("connection reset"), failed("internal")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, observability.Classify(tc.err))
		})
	}
}

func rejected(errorType string) telemetry.Failure {
	return telemetry.Failure{Outcome: telemetry.OutcomeRejected, ErrorType: errorType}
}

func failed(errorType string) telemetry.Failure {
	return telemetry.Failure{Outcome: telemetry.OutcomeError, ErrorType: errorType}
}
