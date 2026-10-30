package temporal

import (
	"errors"
	"maps"
	"slices"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// kindBySignal maps Integration Event Signals to kinds of the model.
var kindBySignal = map[string]fulfillment.SignalKind{
	SignalOrderPlaced:               fulfillment.SignalOrderPlaced,
	SignalPaymentAuthorized:         fulfillment.SignalPaymentAuthorized,
	SignalPaymentFailed:             fulfillment.SignalPaymentFailed,
	SignalRestaurantOrderAccepted:   fulfillment.SignalRestaurantOrderAccepted,
	SignalRestaurantOrderRejected:   fulfillment.SignalRestaurantOrderRejected,
	SignalPreparationStarted:        fulfillment.SignalPreparationStarted,
	SignalPreparationDelayed:        fulfillment.SignalPreparationDelayed,
	SignalPreparationFailed:         fulfillment.SignalPreparationFailed,
	SignalRestaurantOrderReady:      fulfillment.SignalRestaurantOrderReady,
	SignalCourierAssignedToDelivery: fulfillment.SignalCourierAssignedToDelivery,
	SignalDeliveryPickedUp:          fulfillment.SignalDeliveryPickedUp,
	SignalDeliveryCompleted:         fulfillment.SignalDeliveryCompleted,
	SignalDeliveryFailed:            fulfillment.SignalDeliveryFailed,
}

func (r *run) listen(ctx workflow.Context) error {
	// Sorted names fix the order of coroutine creation for deterministic replay.
	for _, name := range slices.Sorted(maps.Keys(kindBySignal)) {
		channel := workflow.GetSignalChannel(ctx, name)
		kind := kindBySignal[name]
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var payload SignalPayload
				channel.Receive(ctx, &payload)
				effect := r.process.Apply(fulfillment.Signal{
					Kind:            kind,
					EventID:         payload.EventID,
					ExpectedReadyAt: payload.ExpectedReadyAt,
				}, workflow.Now(ctx))
				r.receive(ctx, name, payload.EventID, effect)
			}
		})
	}

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
