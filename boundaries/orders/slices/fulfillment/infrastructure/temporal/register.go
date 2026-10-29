package temporal

import (
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

// Registry is the subset of worker.Worker used for registration.
type Registry interface {
	RegisterWorkflowWithOptions(w any, options workflow.RegisterOptions)
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}

// Register adds the workflow and its Activities under stable names to a worker
// polling task queue TaskQueue. The workflow is Pinned (ADR 5): a run finishes
// on the Worker Deployment Version it started on, so its code is not patched.
// Every Activity must have a priority key; one without it stops the worker
// at start.
func Register(registry Registry, activities *Activities) {
	registry.RegisterWorkflowWithOptions(Workflow, workflow.RegisterOptions{
		Name:               WorkflowName,
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})
	for name, fn := range map[string]any{
		ActivityAuthorizePayment:           activities.AuthorizePayment,
		ActivityCapturePayment:             activities.CapturePayment,
		ActivityCancelPaymentAuthorization: activities.CancelPaymentAuthorization,
		ActivityRefundPayment:              activities.RefundPayment,
		ActivityRecordPaymentAuthorization: activities.RecordPaymentAuthorization,
		ActivityRequestRestaurantOrder:     activities.RequestRestaurantOrder,
		ActivityCancelRestaurantOrder:      activities.CancelRestaurantOrder,
		ActivityCreateDelivery:             activities.CreateDelivery,
		ActivityReplanDelivery:             activities.ReplanDelivery,
		ActivityCancelDelivery:             activities.CancelDelivery,
		ActivityCompleteOrder:              activities.CompleteOrder,
		ActivityFailOrCancelOrder:          activities.FailOrCancelOrder,
		ActivityConfirmSlotReservation:     activities.ConfirmSlotReservation,
		ActivityPlanSchedule:               activities.PlanSchedule,
		ActivityQueueRestaurantOrder:       activities.QueueRestaurantOrder,
		ActivityReleaseSlotReservation:     activities.ReleaseSlotReservation,
	} {
		if _, ok := activityPriority[name]; !ok {
			panic("temporal: activity " + name + " has no priority key")
		}
		registry.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}
