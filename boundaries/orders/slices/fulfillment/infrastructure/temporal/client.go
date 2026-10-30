package temporal

import (
	"context"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// ErrWorkflowNotFound is returned when the workflow of an Order does not exist
// or is already closed.
var ErrWorkflowNotFound = errors.New("order fulfillment workflow not found or closed")

// ErrCancellationRefused is returned when the workflow refuses a Cancellation
// Request in its current Workflow Stage.
var ErrCancellationRefused = errors.New("order fulfillment workflow refused cancellation")

// StartRequest carries OrderPlaced of the Order. RestaurantID is the fairness
// key of the workflow; DeliveryWindow set starts the scheduled route.
type StartRequest struct {
	OrderID        string
	RestaurantID   string
	EventID        string
	OccurredAt     time.Time
	DeliveryWindow *DeliveryWindow
}

// Client addresses the workflow of an Order on a Temporal client.
type Client struct {
	temporal client.Client
	policy   Policy
}

// NewClient binds to a Temporal client; policy becomes the input of every
// workflow started by this client.
func NewClient(temporal client.Client, policy Policy) *Client {
	return &Client{temporal: temporal, policy: policy}
}

// Start starts the workflow with OrderPlaced as its first Signal
// (SignalWithStart): the running workflow is reused and deduplicates the
// event_id, a closed one is not started again.
func (c *Client) Start(ctx context.Context, request StartRequest) error {
	if request.RestaurantID == "" {
		return errors.New("start order fulfillment workflow: restaurant_id is required")
	}
	_, err := c.temporal.SignalWithStartWorkflow(ctx, WorkflowID(request.OrderID), SignalOrderPlaced,
		SignalPayload{EventID: request.EventID, OccurredAt: request.OccurredAt},
		c.startOptions(request), WorkflowName, c.input(request))
	if err != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return fmt.Errorf("signal-with-start order fulfillment workflow: %w", err)
	}
	return nil
}

// Signal hands an Integration Event Signal to the workflow of an Order.
func (c *Client) Signal(ctx context.Context, orderID, signal string, payload SignalPayload) error {
	err := c.temporal.SignalWorkflow(ctx, WorkflowID(orderID), "", signal, payload)
	return mapError(err, "signal order fulfillment workflow")
}

// RequestCancellation passes a Cancellation Request by Update, idempotently by
// its RequestID.
func (c *Client) RequestCancellation(ctx context.Context, orderID string, request CancellationRequest) error {
	handle, err := c.temporal.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   WorkflowID(orderID),
		UpdateID:     request.RequestID,
		UpdateName:   UpdateCancellationRequested,
		Args:         []any{request},
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
func (c *Client) startOptions(request StartRequest) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                       WorkflowID(request.OrderID),
		TaskQueue:                TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		Priority:                 temporal.Priority{FairnessKey: request.RestaurantID},
	}
}

// input carries the Delivery Window of OrderPlaced: it chooses the scheduled
// route of a Scheduled Order.
func (c *Client) input(request StartRequest) Input {
	return Input{OrderID: request.OrderID, Policy: c.policy, DeliveryWindow: request.DeliveryWindow}
}

func mapError(err error, operation string) error {
	var notFound *serviceerror.NotFound
	var application *temporal.ApplicationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &notFound):
		return fmt.Errorf("%s: %w: %w", operation, ErrWorkflowNotFound, err)
	case errors.As(err, &application) && application.Type() == ErrTypeCancellationRefused:
		return fmt.Errorf("%s: %w: %w", operation, ErrCancellationRefused, err)
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}
