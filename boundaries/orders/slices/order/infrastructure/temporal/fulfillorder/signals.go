package fulfillorder

import (
	"fmt"
	"maps"
	"slices"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// applyFact changes the run for an accepted Fact; a non-empty result is the
// diagnostic reason why the current state does not admit the Fact.
type applyFact func(ctx workflow.Context, fact Fact) string

func (r *run) listen(ctx workflow.Context) error {
	facts := map[string]applyFact{
		// Confirmations: the workflow already knows the result synchronously.
		SignalOrderPlaced:       confirmation,
		SignalPaymentAuthorized: confirmation,
		SignalPaymentFailed:     confirmation,

		SignalRestaurantOrderAccepted:   r.onAccepted,
		SignalRestaurantOrderRejected:   r.onRejected,
		SignalPreparationStarted:        r.onPreparationStarted,
		SignalPreparationDelayed:        r.onPreparationDelayed,
		SignalPreparationFailed:         r.onPreparationFailed,
		SignalRestaurantOrderReady:      r.onReady,
		SignalCourierAssignedToDelivery: r.onAssigned,
		SignalDeliveryPickedUp:          r.onPickedUp,
		SignalDeliveryCompleted:         r.onDelivered,
		SignalDeliveryFailed:            r.onDeliveryFailed,
	}
	// Sorted names fix the order of coroutine creation for deterministic replay.
	for _, name := range slices.Sorted(maps.Keys(facts)) {
		channel := workflow.GetSignalChannel(ctx, name)
		apply := facts[name]
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var fact Fact
				channel.Receive(ctx, &fact)
				r.receive(ctx, name, fact.EventID, func() string { return apply(ctx, fact) })
			}
		})
	}

	decisions := workflow.GetSignalChannel(ctx, SignalOperatorDecision)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var decision OperatorDecision
			decisions.Receive(ctx, &decision)
			r.receive(ctx, SignalOperatorDecision, decision.DecisionID, func() string { return r.onOperatorDecision(decision) })
		}
	})

	return workflow.SetUpdateHandlerWithOptions(ctx, UpdateCancellationRequested, r.onCancellation,
		workflow.UpdateHandlerOptions{Validator: r.validateCancellation})
}

// receive deduplicates by event id, ignores Signals once the outcome is chosen
// and logs every Fact the current state does not admit.
func (r *run) receive(ctx workflow.Context, signal, eventID string, apply func() string) {
	logger := workflow.GetLogger(ctx)
	if eventID == "" {
		logger.Warn("signal without event id ignored", "signal", signal)
		return
	}
	if r.processed[eventID] {
		logger.Info("duplicate signal ignored", "signal", signal, "event_id", eventID)
		return
	}
	r.processed[eventID] = true
	if r.closing() {
		logger.Info("signal ignored after outcome chosen", "signal", signal, "event_id", eventID, "stage", r.stage())
		return
	}
	if reason := apply(); reason != "" {
		logger.Warn("signal ignored in current stage", "signal", signal, "event_id", eventID, "stage", r.stage(), "reason", reason)
		return
	}
	r.changed = true
}

func confirmation(workflow.Context, Fact) string { return "" }

func (r *run) onAccepted(ctx workflow.Context, _ Fact) string {
	switch {
	case !r.restaurantRequested:
		return "restaurant order not requested"
	case r.accepted:
		return "already accepted"
	}
	r.accepted = true
	r.preparationDeadline = workflow.Now(ctx).Add(r.in.Policy.PreparationTimeout)
	r.preparationLimit = r.preparationDeadline.Add(r.in.Policy.MaxPreparationExtension)
	return ""
}

func (r *run) onRejected(workflow.Context, Fact) string {
	if r.pickedUp {
		return "rejected after pickup"
	}
	r.restaurantClosed = true
	r.fail(OutcomeFailure, ReasonRestaurantRejected)
	return ""
}

func (r *run) onPreparationStarted(workflow.Context, Fact) string {
	if !r.accepted {
		return "not accepted"
	}
	return ""
}

func (r *run) onPreparationDelayed(_ workflow.Context, fact Fact) string {
	switch {
	case !r.accepted || r.ready:
		return "not preparing"
	case !fact.ExpectedReadyAt.After(r.preparationDeadline):
		return "no extension"
	}
	r.preparationDeadline = fact.ExpectedReadyAt
	if r.preparationDeadline.After(r.preparationLimit) {
		r.preparationDeadline = r.preparationLimit
	}
	return ""
}

func (r *run) onPreparationFailed(workflow.Context, Fact) string {
	if r.ready {
		return "already ready"
	}
	r.restaurantClosed = true
	r.fail(OutcomeFailure, ReasonPreparationFailed)
	return ""
}

func (r *run) onReady(ctx workflow.Context, _ Fact) string {
	switch {
	case !r.accepted:
		return "not accepted"
	case r.ready:
		return "already ready"
	}
	r.ready = true
	r.armPickup(ctx)
	return ""
}

func (r *run) onAssigned(ctx workflow.Context, _ Fact) string {
	switch {
	case !r.deliveryCreated:
		return "delivery not created"
	case r.assigned:
		return "already assigned"
	}
	r.assigned = true
	r.escalated = false
	r.replanDue = false
	r.armPickup(ctx)
	return ""
}

func (r *run) armPickup(ctx workflow.Context) {
	if r.ready && r.assigned {
		r.pickupDeadline = workflow.Now(ctx).Add(r.in.Policy.PickupTimeout)
	}
}

func (r *run) onPickedUp(ctx workflow.Context, _ Fact) string {
	switch {
	case !r.assigned:
		return "courier not assigned"
	case r.pickedUp:
		return "already picked up"
	}
	r.pickedUp = true
	r.deliveryDeadline = workflow.Now(ctx).Add(r.in.Policy.DeliveryTimeout)
	return ""
}

func (r *run) onDelivered(workflow.Context, Fact) string {
	if !r.pickedUp {
		return "not picked up"
	}
	r.done = true
	return ""
}

func (r *run) onDeliveryFailed(workflow.Context, Fact) string {
	if !r.deliveryCreated {
		return "delivery not created"
	}
	r.deliveryClosed = true
	r.fail(OutcomeFailure, ReasonDeliveryFailed)
	return ""
}

func (r *run) onOperatorDecision(decision OperatorDecision) string {
	if !r.escalated {
		return "no escalation"
	}
	switch decision.Choice {
	case OperatorRetryAssignment:
		r.escalated = false
		r.replanDue = true
	case OperatorCancel:
		r.fail(OutcomeCancellation, ReasonOperatorDecision)
	default:
		return fmt.Sprintf("unknown choice %q", decision.Choice)
	}
	return ""
}

// validateCancellation must not change the run. A repeated accepted
// RequestID is admitted so retries of the Update succeed.
func (r *run) validateCancellation(_ workflow.Context, request CancellationRequest) error {
	switch {
	case request.RequestID == "":
		return temporal.NewApplicationError("request_id is required", ErrTypeInvalidInput)
	case r.cancellations[request.RequestID]:
		return nil
	case r.closing() || r.pickedUp:
		return temporal.NewApplicationError(fmt.Sprintf("cancellation refused in stage %s", r.stage()), ErrTypeCancellationRefused)
	}
	return nil
}

func (r *run) onCancellation(ctx workflow.Context, request CancellationRequest) error {
	if r.cancellations[request.RequestID] {
		return nil
	}
	r.cancellations[request.RequestID] = true
	workflow.GetLogger(ctx).Info("cancellation accepted", "request_id", request.RequestID, "requested_by", request.RequestedBy)
	r.fail(OutcomeCancellation, ReasonCustomerRequested)
	r.changed = true
	return nil
}
