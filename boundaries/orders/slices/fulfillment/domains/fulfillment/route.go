package fulfillment

import (
	"strconv"
	"time"
)

// StepKind is one external operation of the route or of the Saga.
type StepKind string

const (
	StepAuthorizePayment           StepKind = "authorize_payment"
	StepCapturePayment             StepKind = "capture_payment"
	StepCancelPaymentAuthorization StepKind = "cancel_payment_authorization"
	StepRefundPayment              StepKind = "refund_payment"
	StepRecordPaymentAuthorization StepKind = "record_payment_authorization"
	StepRequestRestaurantOrder     StepKind = "request_restaurant_order"
	StepCancelRestaurantOrder      StepKind = "cancel_restaurant_order"
	StepCreateDelivery             StepKind = "create_delivery"
	StepReplanDelivery             StepKind = "replan_delivery"
	StepCancelDelivery             StepKind = "cancel_delivery"
	StepCompleteOrder              StepKind = "complete_order"
	StepFailOrCancelOrder          StepKind = "fail_or_cancel_order"

	// Steps of the scheduled route (FR-682, FR-678, FR-679).
	StepConfirmSlotReservation StepKind = "confirm_slot"
	StepPlanSchedule           StepKind = "plan_schedule"
	StepReplanSchedule         StepKind = "replan_schedule"
	StepQueueRestaurantOrder   StepKind = "queue"
	StepReleaseSlotReservation StepKind = "release_slot"
)

// Step is the next operation the process decided on.
type Step struct {
	Kind StepKind
	// Key names the logical step of the Order; it is stable across attempts
	// and builds the idempotency key of the operation. StepFailOrCancelOrder
	// is addressed by the Order itself and has no key.
	Key string
	// Stage is published while the step runs; empty keeps the current Stage.
	Stage Stage
}

// Next is the next due step of the route; ok is false while the process waits
// for a fact, a deadline or the Saga.
func (p *Process) Next() (step Step, ok bool) {
	switch {
	case p.failure != nil || p.final != "":
		return Step{}, false
	case !p.authorized:
		return routeStep(StepAuthorizePayment), true
	case !p.paymentRecorded:
		// The restaurant gets the Order only after the payment is recorded in
		// it: no kitchen fact can precede its step, and a declined payment
		// leaves no Restaurant Order behind.
		return routeStep(StepRecordPaymentAuthorization), true
	case p.schedule != nil && !p.schedule.slotConfirmed:
		return routeStep(StepConfirmSlotReservation), true
	case p.schedule != nil && !p.schedule.planned:
		return routeStep(StepPlanSchedule), true
	case !p.restaurantRequested:
		return routeStep(StepRequestRestaurantOrder), true
	case p.schedule != nil && p.accepted && p.schedule.replanDue:
		return routeStep(StepReplanSchedule), true
	case p.schedule != nil && p.accepted && !p.schedule.queued && p.schedule.startDue:
		return routeStep(StepQueueRestaurantOrder), true
	case p.accepted && !p.deliveryCreated && (p.schedule == nil || p.schedule.courierDue):
		return routeStep(StepCreateDelivery), true
	case p.replanDue && !p.assigned:
		return Step{Kind: StepReplanDelivery, Key: string(StepReplanDelivery) + "_" + strconv.Itoa(p.replans+1)}, true
	case p.pickedUp && !p.captured:
		return routeStep(StepCapturePayment), true
	case p.done:
		return Step{Kind: StepCompleteOrder, Key: string(StepCompleteOrder), Stage: StageCompleting}, true
	default:
		return Step{}, false
	}
}

func routeStep(kind StepKind) Step { return Step{Kind: kind, Key: string(kind)} }

// Confirm records an executed step at the time it finished. Approved is the
// business decision of a payment step and of ConfirmSlotReservation; a decline
// is a negative outcome, other steps are always approved. The plan of a
// Scheduled Order is confirmed by ConfirmPlan.
func (p *Process) Confirm(step Step, approved bool, at time.Time) {
	switch step.Kind {
	case StepConfirmSlotReservation:
		p.confirmSlot(approved)
	case StepQueueRestaurantOrder:
		p.queue(at)
	case StepReleaseSlotReservation:
		p.schedule.slotClosed = true
	case StepAuthorizePayment:
		if !approved {
			p.fail(OutcomeFailure, ReasonPaymentDeclined)
			return
		}
		p.authorized = true
	case StepRecordPaymentAuthorization:
		p.paymentRecorded = true
	case StepRequestRestaurantOrder:
		p.restaurantRequested = true
		p.acceptanceDeadline = at.Add(p.policy.AcceptanceTimeout)
	case StepCreateDelivery:
		p.deliveryCreated = true
		p.assignmentDeadline = at.Add(p.policy.AssignmentTimeout)
	case StepReplanDelivery:
		p.replans++
		p.replanDue = false
		p.assignmentDeadline = at.Add(p.policy.AssignmentTimeout)
	case StepCapturePayment:
		if !approved {
			p.fail(OutcomeFailure, ReasonPaymentCaptureDeclined)
			return
		}
		p.captured = true
	case StepCompleteOrder:
		p.final = StageCompleted
	case StepCancelDelivery:
		p.deliveryClosed = true
	case StepCancelRestaurantOrder:
		p.restaurantClosed = true
	case StepFailOrCancelOrder:
		p.final = StageCancelled
	}
}
