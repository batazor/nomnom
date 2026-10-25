package fulfillment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// AC-442, AC-443: a Cancellation Request is admitted until Pickup and while the
// outcome is open; a repeated accepted RequestID stays admitted.
func TestProcessAdmitsCancellation(t *testing.T) {
	request := fulfillment.CancellationRequest{RequestID: "cancel-1", RequestedBy: "user:customer-1"}
	for name, tc := range map[string]struct {
		arrange func(*process)
		request fulfillment.CancellationRequest
		err     error
		message string
	}{
		"before acceptance": {arrange: func(s *process) { s.run() }, request: request},
		"preparing":         {arrange: func(s *process) { s.accepted() }, request: request},
		"without id":        {arrange: func(s *process) { s.run() }, err: fulfillment.ErrRequestIDRequired, message: "request_id is required"},
		"after pickup": {arrange: func(s *process) { s.pickedUp() }, request: request, err: fulfillment.ErrCancellationRefused,
			message: "cancellation refused in stage delivering"},
		"after outcome": {arrange: func(s *process) {
			s.run()
			s.fact(fulfillment.FactRestaurantOrderRejected)
		}, request: request, err: fulfillment.ErrCancellationRefused, message: "cancellation refused in stage waiting_acceptance"},
		"repeated after acceptance": {arrange: func(s *process) {
			s.run()
			require.NoError(t, s.p.RequestCancellation(request))
		}, request: request},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)
			stage := s.p.Stage()

			err := s.p.AdmitCancellation(tc.request)

			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
				assert.EqualError(t, err, tc.message)
			}
			assert.Equal(t, stage, s.p.Stage(), "admission changes nothing")
		})
	}
}

// AC-442: an accepted request cancels the Order once.
func TestProcessAcceptsCancellation(t *testing.T) {
	s := newProcess(t)
	s.run()
	request := fulfillment.CancellationRequest{RequestID: "cancel-1", RequestedBy: "user:customer-1"}

	require.NoError(t, s.p.RequestCancellation(request))
	assert.False(t, s.p.AcceptCancellation(request), "a repeated RequestID changes nothing")

	require.True(t, s.p.Failed())
	outcome, code := s.outcome()
	assert.Equal(t, fulfillment.OutcomeCancellation, outcome)
	assert.Equal(t, fulfillment.ReasonCustomerRequested, code)
	assert.Equal(t, []fulfillment.StepKind{
		fulfillment.StepCancelRestaurantOrder, fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder,
	}, kindsOf(s.p.Compensation()))
}

// AC-443: a refused request does not change the route.
func TestProcessRefusesCancellationAfterPickup(t *testing.T) {
	s := newProcess(t).pickedUp()

	require.ErrorIs(t, s.p.RequestCancellation(fulfillment.CancellationRequest{RequestID: "cancel-1"}), fulfillment.ErrCancellationRefused)

	assert.False(t, s.p.Failed())
	s.fact(fulfillment.FactDeliveryCompleted)
	s.run()
	assert.True(t, s.p.Completed())
}
