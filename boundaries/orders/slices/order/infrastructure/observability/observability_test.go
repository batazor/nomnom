package observability_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/place"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/observability"
)

func TestClassifySeparatesRejectionsFromFailures(t *testing.T) {
	cases := map[string]struct {
		err  error
		want telemetry.Failure
	}{
		"invalid input":        {usecase.InvalidInput("items", "required"), rejected("invalid_input")},
		"permission denied":    {authz.FailClosed(errors.New("spicedb unavailable")), rejected("permission_denied")},
		"order not found":      {fmt.Errorf("load: %w", order.ErrOrderNotFound), rejected("not_found")},
		"closed restaurant":    {place.ErrRestaurantNotAcceptingOrders, rejected("domain_rule")},
		"already exists":       {order.ErrOrderAlreadyExists, rejected("already_exists")},
		"invalid transition":   {fmt.Errorf("apply: %w", order.InvalidTransition{}), rejected("invalid_transition")},
		"guard failed":         {order.GuardFailed{Reason: "paid"}, rejected("guard_failed")},
		"cancellation refused": {fulfillment.ErrCancellationRefused, rejected("cancellation_refused")},
		"spicedb unavailable":  {authz.FailClosed(fmt.Errorf("check: %w", usecase.ErrDependencyUnavailable)), failed("dependency_unavailable")},
		"concurrency conflict": {usecase.ErrConcurrencyConflict, failed("concurrency_conflict")},
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
