package temporal

import (
	"context"
	"errors"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"nomnom/orders/slices/order/applications/fulfillment"
)

var signalByKind = map[fulfillment.Kind]string{
	fulfillment.KindRestaurantAccepted: SignalRestaurantOrderAccepted,
	fulfillment.KindRestaurantRejected: SignalRestaurantOrderRejected,
	fulfillment.KindPreparationStarted: SignalPreparationStarted,
	fulfillment.KindPreparationDelayed: SignalPreparationDelayed,
	fulfillment.KindPreparationFailed:  SignalPreparationFailed,
	fulfillment.KindReadyForPickup:     SignalRestaurantOrderReady,
	fulfillment.KindCourierAssigned:    SignalCourierAssignedToDelivery,
	fulfillment.KindPickedUp:           SignalDeliveryPickedUp,
	fulfillment.KindDeliveryCompleted:  SignalDeliveryCompleted,
	fulfillment.KindDeliveryFailed:     SignalDeliveryFailed,
}

// Client implements the application port fulfillment on a Temporal client.
type Client struct {
	temporal client.Client
	policy   Policy
}

var (
	_ fulfillment.Starter               = (*Client)(nil)
	_ fulfillment.Notifier              = (*Client)(nil)
	_ fulfillment.CancellationRequester = (*Client)(nil)
)

// NewClient binds the port to a Temporal client; policy becomes the input of
// every workflow started by this client.
func NewClient(temporal client.Client, policy Policy) *Client {
	return &Client{temporal: temporal, policy: policy}
}

// Start starts the workflow with OrderPlaced as its first Signal
// (SignalWithStart): the running workflow is reused and deduplicates the
// event_id, a closed one is not started again.
func (c *Client) Start(ctx context.Context, request fulfillment.StartRequest) error {
	if request.RestaurantID == "" {
		return errors.New("start order fulfillment workflow: restaurant_id is required")
	}
	_, err := c.temporal.SignalWithStartWorkflow(ctx, WorkflowID(string(request.OrderID)), SignalOrderPlaced,
		SignalPayload{EventID: request.EventID, OccurredAt: request.OccurredAt},
		c.startOptions(request), WorkflowName, c.input(request))
	if err != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return fmt.Errorf("signal-with-start order fulfillment workflow: %w", err)
	}
	return nil
}

func (c *Client) Notify(ctx context.Context, notification fulfillment.Notification) error {
	signal, ok := signalByKind[notification.Kind]
	if !ok {
		return fmt.Errorf("no signal for fulfillment kind %q", notification.Kind)
	}
	err := c.temporal.SignalWorkflow(ctx, WorkflowID(string(notification.OrderID)), "", signal, SignalPayload{
		EventID:         notification.EventID,
		OccurredAt:      notification.OccurredAt,
		Reason:          notification.Reason,
		ExpectedReadyAt: notification.ExpectedReadyAt,
	})
	return mapError(err, "signal order fulfillment workflow")
}

func (c *Client) RequestCancellation(ctx context.Context, request fulfillment.CancellationRequest) error {
	handle, err := c.temporal.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: WorkflowID(string(request.OrderID)),
		UpdateID:   request.RequestID,
		UpdateName: UpdateCancellationRequested,
		Args: []any{CancellationRequest{
			RequestID:   request.RequestID,
			RequestedBy: request.RequestedBy.String(),
			RequestedAt: request.RequestedAt,
			Reason:      request.Reason,
		}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err == nil {
		err = handle.Get(ctx, nil)
	}
	return mapError(err, "request order cancellation")
}

// startOptions keys the workflow by its restaurant for Task Queue Fairness:
// workflow tasks and Activities inherit the key, so a restaurant at its peak
// does not hold back the tasks of the others.
func (c *Client) startOptions(request fulfillment.StartRequest) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                       WorkflowID(string(request.OrderID)),
		TaskQueue:                TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		Priority:                 temporal.Priority{FairnessKey: string(request.RestaurantID)},
	}
}

// input carries the Delivery Window of OrderPlaced: it chooses the scheduled
// route of a Scheduled Order.
func (c *Client) input(request fulfillment.StartRequest) Input {
	in := Input{OrderID: string(request.OrderID), Policy: c.policy}
	if !request.DeliveryWindow.IsZero() {
		in.DeliveryWindow = &DeliveryWindow{Start: request.DeliveryWindow.Start(), End: request.DeliveryWindow.End()}
	}
	return in
}

func mapError(err error, operation string) error {
	var notFound *serviceerror.NotFound
	var application *temporal.ApplicationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &notFound):
		return fmt.Errorf("%s: %w: %w", operation, fulfillment.ErrWorkflowNotFound, err)
	case errors.As(err, &application) && application.Type() == ErrTypeCancellationRefused:
		return fmt.Errorf("%s: %w: %w", operation, fulfillment.ErrCancellationRefused, err)
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}
