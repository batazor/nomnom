package fulfillorder

import (
	"strconv"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Stage is the Workflow Stage: orchestration progress, never the Order State.
type Stage string

const (
	StageAuthorizingPayment Stage = "authorizing_payment"
	StageWaitingAcceptance  Stage = "waiting_acceptance"
	StagePreparing          Stage = "preparing"
	StageWaitingOperator    Stage = "waiting_operator"
	StageWaitingPickup      Stage = "waiting_pickup"
	StageDelivering         Stage = "delivering"
	StageCompleting         Stage = "completing"
	StageCompensating       Stage = "compensating"
	StageCompleted          Stage = "completed"
	StageCancelled          Stage = "cancelled"
)

// StageSearchAttribute publishes the low-cardinality Workflow Stage.
var StageSearchAttribute = temporal.NewSearchAttributeKeyKeyword("OrderFulfillmentStage")

// Workflow is the Order Fulfillment Workflow. Signal and Update handlers only
// change serializable state; the main loop performs every Activity, so the
// sequence of commands is deterministic.
func Workflow(ctx workflow.Context, in Input) (Result, error) {
	if in.OrderID == "" {
		return Result{}, temporal.NewNonRetryableApplicationError("order_id is required", ErrTypeInvalidInput, nil)
	}
	r := &run{
		in:            in,
		log:           workflow.GetLogger(ctx),
		processed:     map[string]bool{},
		cancellations: map[string]bool{},
	}
	if err := r.listen(ctx); err != nil {
		return Result{}, err
	}
	return r.drive(ctx)
}

type negative struct {
	outcome Outcome
	code    string
}

type run struct {
	in  Input
	log log.Logger

	processed     map[string]bool
	cancellations map[string]bool
	changed       bool
	published     Stage
	final         Stage

	// Journal of confirmed steps and owner-side closures for the Saga.
	authorized          bool
	captured            bool
	restaurantRequested bool
	restaurantClosed    bool
	deliveryCreated     bool
	deliveryClosed      bool

	accepted bool
	ready    bool
	assigned bool
	pickedUp bool
	done     bool

	replans   int
	replanDue bool
	escalated bool

	acceptanceDeadline  time.Time
	assignmentDeadline  time.Time
	operatorDeadline    time.Time
	preparationDeadline time.Time
	preparationLimit    time.Time
	pickupDeadline      time.Time
	deliveryDeadline    time.Time

	failure *negative
}

func (r *run) drive(ctx workflow.Context) (Result, error) {
	for {
		if r.failure != nil {
			return r.compensate(ctx)
		}
		progressed, err := r.step(ctx)
		if err != nil {
			return Result{}, err
		}
		if r.final == StageCompleted {
			return Result{Outcome: OutcomeCompleted}, nil
		}
		if progressed {
			continue
		}
		if err := r.publish(ctx, r.stage()); err != nil {
			return Result{}, err
		}

		next := r.nextDeadline()
		now := workflow.Now(ctx)
		if !next.IsZero() && !next.After(now) {
			r.expire(now)
			continue
		}
		r.changed = false
		if next.IsZero() {
			if err := workflow.Await(ctx, func() bool { return r.changed }); err != nil {
				return Result{}, err
			}
			continue
		}
		if _, err := workflow.AwaitWithTimeout(ctx, next.Sub(now), func() bool { return r.changed }); err != nil {
			return Result{}, err
		}
	}
}

// step performs the next due Activity of the route and reports whether it did.
func (r *run) step(ctx workflow.Context) (bool, error) {
	switch {
	case !r.authorized:
		var decision PaymentDecision
		if err := r.execute(ctx, ActivityAuthorizePayment, "authorize_payment", &decision); err != nil {
			return false, err
		}
		if !decision.Approved {
			r.fail(OutcomeFailure, ReasonPaymentDeclined)
			return true, nil
		}
		r.authorized = true
	case !r.restaurantRequested:
		if err := r.execute(ctx, ActivityRequestRestaurantOrder, "request_restaurant_order", nil); err != nil {
			return false, err
		}
		r.restaurantRequested = true
		r.acceptanceDeadline = workflow.Now(ctx).Add(r.in.Policy.AcceptanceTimeout)
	case r.accepted && !r.deliveryCreated:
		if err := r.execute(ctx, ActivityCreateDelivery, "create_delivery", nil); err != nil {
			return false, err
		}
		r.deliveryCreated = true
		r.assignmentDeadline = workflow.Now(ctx).Add(r.in.Policy.AssignmentTimeout)
	case r.replanDue && !r.assigned:
		r.replans++
		if err := r.execute(ctx, ActivityReplanDelivery, "replan_delivery_"+strconv.Itoa(r.replans), nil); err != nil {
			return false, err
		}
		r.replanDue = false
		r.assignmentDeadline = workflow.Now(ctx).Add(r.in.Policy.AssignmentTimeout)
	case r.pickedUp && !r.captured:
		var decision PaymentDecision
		if err := r.execute(ctx, ActivityCapturePayment, "capture_payment", &decision); err != nil {
			return false, err
		}
		if !decision.Approved {
			r.fail(OutcomeFailure, ReasonPaymentCaptureDeclined)
			return true, nil
		}
		r.captured = true
	case r.done:
		if err := r.publish(ctx, StageCompleting); err != nil {
			return false, err
		}
		if err := r.execute(ctx, ActivityCompleteOrder, "complete_order", nil); err != nil {
			return false, err
		}
		r.final = StageCompleted
		return true, r.publish(ctx, StageCompleted)
	default:
		return false, nil
	}
	return true, nil
}

// expire turns the passed deadlines into business branches.
func (r *run) expire(now time.Time) {
	passed := func(deadline time.Time) bool { return !deadline.IsZero() && !deadline.After(now) }
	switch {
	case r.restaurantRequested && !r.accepted && passed(r.acceptanceDeadline):
		r.fail(OutcomeFailure, ReasonRestaurantAcceptanceTimeout)
	case r.escalated && passed(r.operatorDeadline):
		r.fail(OutcomeFailure, ReasonCourierAssignmentExhausted)
	case r.awaitingAssignment() && passed(r.assignmentDeadline):
		if r.replans < r.in.Policy.MaxReplans {
			r.replanDue = true
			return
		}
		r.escalated = true
		r.operatorDeadline = now.Add(r.in.Policy.OperatorDecisionTimeout)
		r.log.Warn("courier assignment exhausted, waiting for operator decision", "replans", r.replans)
	case r.accepted && !r.ready && passed(r.preparationDeadline):
		r.fail(OutcomeFailure, ReasonPreparationTimeout)
	case r.ready && r.assigned && !r.pickedUp && passed(r.pickupDeadline):
		r.fail(OutcomeFailure, ReasonPickupTimeout)
	case r.pickedUp && !r.done && passed(r.deliveryDeadline):
		r.fail(OutcomeFailure, ReasonDeliveryTimeout)
	}
}

func (r *run) awaitingAssignment() bool {
	return r.deliveryCreated && !r.assigned && !r.escalated && !r.replanDue
}

func (r *run) nextDeadline() time.Time {
	var next time.Time
	consider := func(active bool, deadline time.Time) {
		if active && !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
			next = deadline
		}
	}
	consider(r.restaurantRequested && !r.accepted, r.acceptanceDeadline)
	consider(r.awaitingAssignment(), r.assignmentDeadline)
	consider(r.escalated, r.operatorDeadline)
	consider(r.accepted && !r.ready, r.preparationDeadline)
	consider(r.ready && r.assigned && !r.pickedUp, r.pickupDeadline)
	consider(r.pickedUp && !r.done, r.deliveryDeadline)
	return next
}

func (r *run) stage() Stage {
	switch {
	case r.final != "":
		return r.final
	case !r.authorized:
		return StageAuthorizingPayment
	case !r.accepted:
		return StageWaitingAcceptance
	case r.escalated:
		return StageWaitingOperator
	case !r.ready || !r.assigned:
		return StagePreparing
	case !r.pickedUp:
		return StageWaitingPickup
	default:
		return StageDelivering
	}
}

func (r *run) publish(ctx workflow.Context, stage Stage) error {
	if stage == r.published {
		return nil
	}
	r.published = stage
	if !r.in.Policy.PublishStage {
		return nil
	}
	return workflow.UpsertTypedSearchAttributes(ctx, StageSearchAttribute.ValueSet(string(stage)))
}

// closing reports that the outcome is chosen and Signals no longer change the route.
func (r *run) closing() bool {
	return r.failure != nil || r.done || r.final != ""
}

func (r *run) fail(outcome Outcome, code string) {
	if r.failure != nil {
		return
	}
	r.failure = &negative{outcome: outcome, code: code}
}

// compensate runs the Saga over the journal of confirmed steps in reverse
// order and records the negative outcome of the Order.
func (r *run) compensate(ctx workflow.Context) (Result, error) {
	r.final = StageCompensating
	if err := r.publish(ctx, StageCompensating); err != nil {
		return Result{}, err
	}
	if r.deliveryCreated && !r.deliveryClosed {
		if err := r.execute(ctx, ActivityCancelDelivery, "cancel_delivery", nil); err != nil {
			return Result{}, err
		}
	}
	if r.restaurantRequested && !r.restaurantClosed && !r.pickedUp {
		if err := r.execute(ctx, ActivityCancelRestaurantOrder, "cancel_restaurant_order", nil); err != nil {
			return Result{}, err
		}
	}
	switch {
	case r.captured:
		if err := r.execute(ctx, ActivityRefundPayment, "refund_payment", nil); err != nil {
			return Result{}, err
		}
	case r.authorized:
		if err := r.execute(ctx, ActivityCancelPaymentAuthorization, "cancel_payment_authorization", nil); err != nil {
			return Result{}, err
		}
	}

	finalize := FinalizeInput{
		OrderID:    r.in.OrderID,
		Outcome:    r.failure.outcome,
		ReasonCode: r.failure.code,
		DecidedAt:  workflow.Now(ctx),
	}
	if err := workflow.ExecuteActivity(r.activityContext(ctx), ActivityFailOrCancelOrder, finalize).Get(ctx, nil); err != nil {
		return Result{}, err
	}
	r.final = StageCancelled
	if err := r.publish(ctx, StageCancelled); err != nil {
		return Result{}, err
	}
	return Result{Outcome: r.failure.outcome, ReasonCode: r.failure.code}, nil
}

func (r *run) execute(ctx workflow.Context, activity, step string, result any) error {
	input := StepInput{OrderID: r.in.OrderID, IdempotencyKey: WorkflowID(r.in.OrderID) + ":" + step}
	return workflow.ExecuteActivity(r.activityContext(ctx), activity, input).Get(ctx, result)
}

func (r *run) activityContext(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    time.Minute,
		},
	})
}
