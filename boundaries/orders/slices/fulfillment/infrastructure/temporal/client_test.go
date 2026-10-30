package temporal_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	sdktemporal "go.temporal.io/sdk/temporal"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

func newClient(t *testing.T) (*temporal.Client, *mocks.Client) {
	t.Helper()
	temporalClient := mocks.NewClient(t)
	return temporal.NewClient(temporalClient, policy()), temporalClient
}

var wantStartOptions = client.StartWorkflowOptions{
	ID:                       "order:order-1",
	TaskQueue:                temporal.TaskQueue,
	WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	Priority:                 sdktemporal.Priority{FairnessKey: "restaurant-1"},
}

var placedAt = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

func startRequest() temporal.StartRequest {
	return temporal.StartRequest{OrderID: orderID, RestaurantID: "restaurant-1", EventID: "event-1", OccurredAt: placedAt}
}

// FR-638: OrderPlaced starts the workflow with a stable identity as its first Signal.
func TestClientSignalsWithStartOnOrderPlaced(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, "order:order-1", temporal.SignalOrderPlaced,
		temporal.SignalPayload{EventID: "event-1", OccurredAt: placedAt},
		wantStartOptions, temporal.WorkflowName, temporal.Input{OrderID: orderID, Policy: policy()}).
		Return(mocks.NewWorkflowRun(t), nil)

	require.NoError(t, c.Start(context.Background(), startRequest()))
}

// FR-415: the Delivery Window of OrderPlaced is the input that chooses the
// scheduled route.
func TestClientStartsScheduledOrderWithItsWindow(t *testing.T) {
	c, temporalClient := newClient(t)
	start := placedAt.Add(7 * time.Hour)
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, "order:order-1", temporal.SignalOrderPlaced,
		temporal.SignalPayload{EventID: "event-1", OccurredAt: placedAt},
		wantStartOptions, temporal.WorkflowName, temporal.Input{
			OrderID:        orderID,
			Policy:         policy(),
			DeliveryWindow: &temporal.DeliveryWindow{Start: start, End: start.Add(15 * time.Minute)},
		}).
		Return(mocks.NewWorkflowRun(t), nil)
	request := startRequest()
	request.DeliveryWindow = &temporal.DeliveryWindow{Start: start, End: start.Add(15 * time.Minute)}

	require.NoError(t, c.Start(context.Background(), request))
}

// AC-445: a closed workflow is not started again and Start succeeds.
func TestClientStartOfClosedWorkflowSucceeds(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("closed", "", "run-1"))

	require.NoError(t, c.Start(context.Background(), startRequest()))
}

// The fairness key of the workflow is its restaurant: a start without it is
// refused before Temporal.
func TestClientStartRequiresRestaurant(t *testing.T) {
	c, _ := newClient(t)
	request := startRequest()
	request.RestaurantID = ""

	require.ErrorContains(t, c.Start(context.Background(), request), "restaurant_id is required")
}

func TestClientStartFailureIsReturned(t *testing.T) {
	c, temporalClient := newClient(t)
	cause := serviceerror.NewUnavailable("temporal down")
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Return(nil, cause)

	require.ErrorIs(t, c.Start(context.Background(), startRequest()), cause)
}

func TestClientSignalsWorkflowOfOrder(t *testing.T) {
	c, temporalClient := newClient(t)
	payload := temporal.SignalPayload{EventID: "event-1", OccurredAt: placedAt, Reason: "r"}
	temporalClient.On("SignalWorkflow", mock.Anything, "order:order-1", "", temporal.SignalDeliveryPickedUp, payload).Return(nil)

	require.NoError(t, c.Signal(context.Background(), orderID, temporal.SignalDeliveryPickedUp, payload))
}

// AC-444: a missing or closed workflow is ErrWorkflowNotFound.
func TestClientMapsNotFoundSignal(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("SignalWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(serviceerror.NewNotFound("workflow execution already completed"))

	err := c.Signal(context.Background(), orderID, temporal.SignalDeliveryPickedUp, temporal.SignalPayload{EventID: "event-1"})

	require.ErrorIs(t, err, temporal.ErrWorkflowNotFound)
}

func TestClientRequestsCancellationByUpdate(t *testing.T) {
	requestedAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	request := temporal.CancellationRequest{
		RequestID: "cancel-1", RequestedBy: "user:customer-1", RequestedAt: requestedAt, Reason: "changed mind",
	}
	wantOptions := client.UpdateWorkflowOptions{
		WorkflowID:   "order:order-1",
		UpdateID:     "cancel-1",
		UpdateName:   temporal.UpdateCancellationRequested,
		Args:         []any{request},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	}
	tests := []struct {
		name    string
		update  error
		result  error
		wantErr error
	}{
		{name: "accepted"},
		{
			name:    "refused by validator",
			result:  sdktemporal.NewApplicationError("refused", temporal.ErrTypeCancellationRefused),
			wantErr: temporal.ErrCancellationRefused,
		},
		{
			name:    "workflow closed",
			update:  serviceerror.NewNotFound("closed"),
			wantErr: temporal.ErrWorkflowNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, temporalClient := newClient(t)
			if tt.update != nil {
				temporalClient.On("UpdateWorkflow", mock.Anything, wantOptions).Return(nil, tt.update)
			} else {
				handle := mocks.NewWorkflowUpdateHandle(t)
				handle.On("Get", mock.Anything, nil).Return(tt.result)
				temporalClient.On("UpdateWorkflow", mock.Anything, wantOptions).Return(handle, nil)
			}

			err := c.RequestCancellation(context.Background(), orderID, request)

			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
