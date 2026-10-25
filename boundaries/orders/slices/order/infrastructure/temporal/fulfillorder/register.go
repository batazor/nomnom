package fulfillorder

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
// polling task queue TaskQueue.
func Register(registry Registry, activities *Activities) {
	registry.RegisterWorkflowWithOptions(Workflow, workflow.RegisterOptions{Name: WorkflowName})
	for name, fn := range map[string]any{
		ActivityAuthorizePayment:           activities.AuthorizePayment,
		ActivityCapturePayment:             activities.CapturePayment,
		ActivityCancelPaymentAuthorization: activities.CancelPaymentAuthorization,
		ActivityRefundPayment:              activities.RefundPayment,
		ActivityRequestRestaurantOrder:     activities.RequestRestaurantOrder,
		ActivityCancelRestaurantOrder:      activities.CancelRestaurantOrder,
		ActivityCreateDelivery:             activities.CreateDelivery,
		ActivityReplanDelivery:             activities.ReplanDelivery,
		ActivityCancelDelivery:             activities.CancelDelivery,
		ActivityCompleteOrder:              activities.CompleteOrder,
		ActivityFailOrCancelOrder:          activities.FailOrCancelOrder,
	} {
		registry.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}
