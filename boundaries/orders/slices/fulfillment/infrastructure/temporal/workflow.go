package temporal

import (
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// StageSearchAttribute publishes the low-cardinality Workflow Stage.
var StageSearchAttribute = temporal.NewSearchAttributeKeyKeyword("OrderFulfillmentStage")

// activityByStep maps the steps decided by the model to registered Activities.
var activityByStep = map[fulfillment.StepKind]string{
	fulfillment.StepAuthorizePayment:           ActivityAuthorizePayment,
	fulfillment.StepCapturePayment:             ActivityCapturePayment,
	fulfillment.StepCancelPaymentAuthorization: ActivityCancelPaymentAuthorization,
	fulfillment.StepRefundPayment:              ActivityRefundPayment,
	fulfillment.StepRecordPaymentAuthorization: ActivityRecordPaymentAuthorization,
	fulfillment.StepRequestRestaurantOrder:     ActivityRequestRestaurantOrder,
	fulfillment.StepCancelRestaurantOrder:      ActivityCancelRestaurantOrder,
	fulfillment.StepCreateDelivery:             ActivityCreateDelivery,
	fulfillment.StepReplanDelivery:             ActivityReplanDelivery,
	fulfillment.StepCancelDelivery:             ActivityCancelDelivery,
	fulfillment.StepCompleteOrder:              ActivityCompleteOrder,
	fulfillment.StepFailOrCancelOrder:          ActivityFailOrCancelOrder,
	fulfillment.StepConfirmSlotReservation:     ActivityConfirmSlotReservation,
	fulfillment.StepPlanSchedule:               ActivityPlanSchedule,
	fulfillment.StepReplanSchedule:             ActivityPlanSchedule,
	fulfillment.StepQueueRestaurantOrder:       ActivityQueueRestaurantOrder,
	fulfillment.StepReleaseSlotReservation:     ActivityReleaseSlotReservation,
}

// Workflow is the Order Fulfillment Workflow. Signal and Update handlers only
// change the process state; the main loop performs every Activity decided by
// the model, so the sequence of commands is deterministic.
func Workflow(ctx workflow.Context, in Input) (Result, error) {
	if in.OrderID == "" {
		return Result{}, temporal.NewNonRetryableApplicationError("order_id is required", ErrTypeInvalidInput, nil)
	}
	r := &run{
		in:      in,
		log:     workflow.GetLogger(ctx),
		process: newProcess(ctx, in),
	}
	if err := r.listen(ctx); err != nil {
		return Result{}, err
	}
	return r.drive(ctx)
}

// newProcess chooses the route by the Delivery Window of the input. The
// scheduled route exists only in histories that record ScheduledRouteChangeID;
// an ASAP input records nothing new, so histories started before the change
// replay unchanged.
func newProcess(ctx workflow.Context, in Input) *fulfillment.Process {
	if in.DeliveryWindow != nil &&
		workflow.GetVersion(ctx, ScheduledRouteChangeID, workflow.DefaultVersion, ScheduledRouteVersion) == ScheduledRouteVersion {
		return fulfillment.NewScheduled(in.Policy.Policy)
	}
	return fulfillment.New(in.Policy.Policy)
}

type run struct {
	in      Input
	log     log.Logger
	process *fulfillment.Process

	// restaurantOrderID is returned by RequestRestaurantOrder of a Scheduled
	// Order and addresses QueueRestaurantOrder.
	restaurantOrderID string

	changed   bool
	published Stage
}

func (r *run) drive(ctx workflow.Context) (Result, error) {
	for {
		if r.process.Failed() {
			return r.compensate(ctx)
		}
		if step, ok := r.process.Next(); ok {
			if err := r.perform(ctx, step); err != nil {
				return Result{}, err
			}
			if r.process.Completed() {
				if err := r.publish(ctx, r.process.Stage()); err != nil {
					return Result{}, err
				}
				return Result{Outcome: OutcomeCompleted}, nil
			}
			continue
		}
		if err := r.publish(ctx, r.process.Stage()); err != nil {
			return Result{}, err
		}

		next := r.process.NextDeadline()
		now := workflow.Now(ctx)
		if !next.IsZero() && !next.After(now) {
			if r.process.Expire(now) == fulfillment.ExpiryEscalation {
				r.log.Warn("courier assignment exhausted, waiting for operator decision", "replans", r.process.Replans())
			}
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

// perform executes a route step as its Activity and confirms it in the model
// at the time it finished; a payment Activity and ConfirmSlotReservation
// return the business decision, PlanSchedule the plan.
func (r *run) perform(ctx workflow.Context, step fulfillment.Step) error {
	if step.Stage != "" {
		if err := r.publish(ctx, step.Stage); err != nil {
			return err
		}
	}
	input := r.stepInput(ctx, step)
	activity := activityByStep[step.Kind]
	switch step.Kind {
	case fulfillment.StepAuthorizePayment, fulfillment.StepCapturePayment:
		var decision PaymentDecision
		if err := r.executeStep(ctx, activity, input, &decision); err != nil {
			return err
		}
		r.process.Confirm(step, decision.Approved, workflow.Now(ctx))
	case fulfillment.StepConfirmSlotReservation:
		var decision SlotDecision
		if err := r.executeStep(ctx, activity, input, &decision); err != nil {
			return err
		}
		r.process.Confirm(step, decision.Confirmed, workflow.Now(ctx))
	case fulfillment.StepPlanSchedule, fulfillment.StepReplanSchedule:
		var plan Plan
		if err := r.executeStep(ctx, activity, input, &plan); err != nil {
			return err
		}
		r.process.ConfirmPlan(step, plan, workflow.Now(ctx))
	case fulfillment.StepRequestRestaurantOrder:
		// Only the scheduled route needs the restaurant_order_id: the ASAP
		// route reads no result, as before the scheduled route existed.
		var requested RestaurantOrderRequested
		var result any
		if r.process.Scheduled() {
			result = &requested
		}
		if err := r.executeStep(ctx, activity, input, result); err != nil {
			return err
		}
		r.restaurantOrderID = requested.RestaurantOrderID
		r.process.Confirm(step, true, workflow.Now(ctx))
	default:
		if err := r.executeStep(ctx, activity, input, nil); err != nil {
			return err
		}
		r.process.Confirm(step, true, workflow.Now(ctx))
	}
	return nil
}

// stepInput addresses the step by its idempotency key order:<order_id>:<key>
// and carries the workflow data the Activity needs.
func (r *run) stepInput(ctx workflow.Context, step fulfillment.Step) StepInput {
	input := StepInput{OrderID: r.in.OrderID, IdempotencyKey: WorkflowID(r.in.OrderID) + ":" + step.Key}
	switch step.Kind {
	case fulfillment.StepCompleteOrder, fulfillment.StepRecordPaymentAuthorization:
		input.DecidedAt = workflow.Now(ctx)
	case fulfillment.StepRequestRestaurantOrder, fulfillment.StepReplanSchedule:
		input.PlannedReadyAt = r.process.Plan().PlannedReadyAt
	case fulfillment.StepQueueRestaurantOrder:
		input.RestaurantOrderID = r.restaurantOrderID
	}
	return input
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

// compensate runs the Saga planned by the model over the journal of confirmed
// steps and records the negative outcome of the Order.
func (r *run) compensate(ctx workflow.Context) (Result, error) {
	plan := r.process.Compensate()
	if err := r.publish(ctx, r.process.Stage()); err != nil {
		return Result{}, err
	}
	outcome, code := r.process.Outcome()
	for _, step := range plan {
		if step.Kind != fulfillment.StepFailOrCancelOrder {
			if err := r.executeStep(ctx, activityByStep[step.Kind], r.stepInput(ctx, step), nil); err != nil {
				return Result{}, err
			}
			r.process.Confirm(step, true, workflow.Now(ctx))
			continue
		}
		finalize := FinalizeInput{
			OrderID:    r.in.OrderID,
			Outcome:    outcome,
			ReasonCode: code,
			DecidedAt:  workflow.Now(ctx),
		}
		if err := workflow.ExecuteActivity(r.activityContext(ctx), ActivityFailOrCancelOrder, finalize).Get(ctx, nil); err != nil {
			return Result{}, err
		}
		r.process.Confirm(step, true, workflow.Now(ctx))
	}
	if err := r.publish(ctx, r.process.Stage()); err != nil {
		return Result{}, err
	}
	return Result{Outcome: outcome, ReasonCode: code}, nil
}

func (r *run) executeStep(ctx workflow.Context, activity string, input StepInput, result any) error {
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
