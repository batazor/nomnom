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
	fulfillment.StepRequestRestaurantOrder:     ActivityRequestRestaurantOrder,
	fulfillment.StepCancelRestaurantOrder:      ActivityCancelRestaurantOrder,
	fulfillment.StepCreateDelivery:             ActivityCreateDelivery,
	fulfillment.StepReplanDelivery:             ActivityReplanDelivery,
	fulfillment.StepCancelDelivery:             ActivityCancelDelivery,
	fulfillment.StepCompleteOrder:              ActivityCompleteOrder,
	fulfillment.StepFailOrCancelOrder:          ActivityFailOrCancelOrder,
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
		process: fulfillment.New(in.Policy.Policy),
	}
	if err := r.listen(ctx); err != nil {
		return Result{}, err
	}
	return r.drive(ctx)
}

type run struct {
	in      Input
	log     log.Logger
	process *fulfillment.Process

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
// at the time it finished; a payment Activity returns the business decision.
func (r *run) perform(ctx workflow.Context, step fulfillment.Step) error {
	if step.Stage != "" {
		if err := r.publish(ctx, step.Stage); err != nil {
			return err
		}
	}
	decision := PaymentDecision{Approved: true}
	var result any
	if step.Kind == fulfillment.StepAuthorizePayment || step.Kind == fulfillment.StepCapturePayment {
		decision = PaymentDecision{}
		result = &decision
	}
	if err := r.execute(ctx, activityByStep[step.Kind], step.Key, result); err != nil {
		return err
	}
	r.process.Confirm(step, decision.Approved, workflow.Now(ctx))
	return nil
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
			if err := r.execute(ctx, activityByStep[step.Kind], step.Key, nil); err != nil {
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

func (r *run) execute(ctx workflow.Context, activity, step string, result any) error {
	input := StepInput{OrderID: r.in.OrderID, IdempotencyKey: WorkflowID(r.in.OrderID) + ":" + step}
	if activity == ActivityCompleteOrder {
		input.DecidedAt = workflow.Now(ctx)
	}
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
