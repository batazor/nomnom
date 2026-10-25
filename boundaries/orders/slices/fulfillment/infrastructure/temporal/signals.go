package temporal

import (
	"errors"
	"maps"
	"slices"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// factBySignal maps Integration Event Signals to facts of the model.
var factBySignal = map[string]fulfillment.FactKind{
	SignalOrderPlaced:               fulfillment.FactOrderPlaced,
	SignalPaymentAuthorized:         fulfillment.FactPaymentAuthorized,
	SignalPaymentFailed:             fulfillment.FactPaymentFailed,
	SignalRestaurantOrderAccepted:   fulfillment.FactRestaurantOrderAccepted,
	SignalRestaurantOrderRejected:   fulfillment.FactRestaurantOrderRejected,
	SignalPreparationStarted:        fulfillment.FactPreparationStarted,
	SignalPreparationDelayed:        fulfillment.FactPreparationDelayed,
	SignalPreparationFailed:         fulfillment.FactPreparationFailed,
	SignalRestaurantOrderReady:      fulfillment.FactRestaurantOrderReady,
	SignalCourierAssignedToDelivery: fulfillment.FactCourierAssignedToDelivery,
	SignalDeliveryPickedUp:          fulfillment.FactDeliveryPickedUp,
	SignalDeliveryCompleted:         fulfillment.FactDeliveryCompleted,
	SignalDeliveryFailed:            fulfillment.FactDeliveryFailed,
}

func (r *run) listen(ctx workflow.Context) error {
	// Sorted names fix the order of coroutine creation for deterministic replay.
	for _, name := range slices.Sorted(maps.Keys(factBySignal)) {
		channel := workflow.GetSignalChannel(ctx, name)
		kind := factBySignal[name]
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var fact Fact
				channel.Receive(ctx, &fact)
				effect := r.process.Apply(fulfillment.Fact{
					Kind:            kind,
					EventID:         fact.EventID,
					ExpectedReadyAt: fact.ExpectedReadyAt,
				}, workflow.Now(ctx))
				r.receive(ctx, name, fact.EventID, effect)
			}
		})
	}

	decisions := workflow.GetSignalChannel(ctx, SignalOperatorDecision)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var decision OperatorDecision
			decisions.Receive(ctx, &decision)
			effect := r.process.Decide(fulfillment.OperatorDecision{DecisionID: decision.DecisionID, Choice: decision.Choice})
			r.receive(ctx, SignalOperatorDecision, decision.DecisionID, effect)
		}
	})

	return workflow.SetUpdateHandlerWithOptions(ctx, UpdateCancellationRequested, r.onCancellation,
		workflow.UpdateHandlerOptions{Validator: r.validateCancellation})
}

// receive logs every Signal the model did not admit and wakes the main loop
// for an admitted one.
func (r *run) receive(ctx workflow.Context, signal, eventID string, effect fulfillment.Effect) {
	logger := workflow.GetLogger(ctx)
	switch effect.Verdict {
	case fulfillment.MissingEventID:
		logger.Warn("signal without event id ignored", "signal", signal)
	case fulfillment.Duplicate:
		logger.Info("duplicate signal ignored", "signal", signal, "event_id", eventID)
	case fulfillment.Closed:
		logger.Info("signal ignored after outcome chosen", "signal", signal, "event_id", eventID, "stage", r.process.Stage())
	case fulfillment.Refused:
		logger.Warn("signal ignored in current stage", "signal", signal, "event_id", eventID, "stage", r.process.Stage(), "reason", effect.Reason)
	case fulfillment.Applied:
		r.changed = true
	}
}

// validateCancellation must not change the run; the model admits a repeated
// accepted RequestID so retries of the Update succeed.
func (r *run) validateCancellation(_ workflow.Context, request CancellationRequest) error {
	err := r.process.AdmitCancellation(cancellationRequest(request))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fulfillment.ErrCancellationRefused):
		return temporal.NewApplicationError(err.Error(), ErrTypeCancellationRefused)
	default:
		return temporal.NewApplicationError(err.Error(), ErrTypeInvalidInput)
	}
}

func (r *run) onCancellation(ctx workflow.Context, request CancellationRequest) error {
	if !r.process.AcceptCancellation(cancellationRequest(request)) {
		return nil
	}
	workflow.GetLogger(ctx).Info("cancellation accepted", "request_id", request.RequestID, "requested_by", request.RequestedBy)
	r.changed = true
	return nil
}

func cancellationRequest(request CancellationRequest) fulfillment.CancellationRequest {
	return fulfillment.CancellationRequest{RequestID: request.RequestID, RequestedBy: request.RequestedBy, Reason: request.Reason}
}
