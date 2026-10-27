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

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/fulfillment"
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
}

var placedAt = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

func startRequest() fulfillment.StartRequest {
	return fulfillment.StartRequest{OrderID: orderID, EventID: "event-1", OccurredAt: placedAt}
}

// FR-638: OrderPlaced starts the workflow with a stable identity as its first Signal.
func TestClientSignalsWithStartOnOrderPlaced(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, "order:order-1", temporal.SignalOrderPlaced,
		temporal.Fact{EventID: "event-1", OccurredAt: placedAt},
		wantStartOptions, temporal.WorkflowName, temporal.Input{OrderID: orderID, Policy: policy()}).
		Return(mocks.NewWorkflowRun(t), nil)

	require.NoError(t, c.Start(context.Background(), startRequest()))
}

// AC-445: a closed workflow is not started again and Start succeeds.
func TestClientStartOfClosedWorkflowSucceeds(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("closed", "", "run-1"))

	require.NoError(t, c.Start(context.Background(), startRequest()))
}

func TestClientStartFailureIsReturned(t *testing.T) {
	c, temporalClient := newClient(t)
	cause := serviceerror.NewUnavailable("temporal down")
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Return(nil, cause)

	require.ErrorIs(t, c.Start(context.Background(), startRequest()), cause)
}

func TestClientNotifiesWithIntegrationEventName(t *testing.T) {
	occurredAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		kind   fulfillment.Kind
		signal string
	}{
		{fulfillment.KindRestaurantAccepted, temporal.SignalRestaurantOrderAccepted},
		{fulfillment.KindRestaurantRejected, temporal.SignalRestaurantOrderRejected},
		{fulfillment.KindPreparationStarted, temporal.SignalPreparationStarted},
		{fulfillment.KindPreparationDelayed, temporal.SignalPreparationDelayed},
		{fulfillment.KindPreparationFailed, temporal.SignalPreparationFailed},
		{fulfillment.KindReadyForPickup, temporal.SignalRestaurantOrderReady},
		{fulfillment.KindCourierAssigned, temporal.SignalCourierAssignedToDelivery},
		{fulfillment.KindPickedUp, temporal.SignalDeliveryPickedUp},
		{fulfillment.KindDeliveryCompleted, temporal.SignalDeliveryCompleted},
		{fulfillment.KindDeliveryFailed, temporal.SignalDeliveryFailed},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			c, temporalClient := newClient(t)
			temporalClient.On("SignalWorkflow", mock.Anything, "order:order-1", "", tt.signal,
				temporal.Fact{EventID: "event-1", OccurredAt: occurredAt, Reason: "r"}).Return(nil)

			require.NoError(t, c.Notify(context.Background(), fulfillment.Notification{
				OrderID: orderID, EventID: "event-1", Kind: tt.kind, OccurredAt: occurredAt, Reason: "r",
			}))
		})
	}
}

// AC-444: a missing or closed workflow is ErrWorkflowNotFound.
func TestClientMapsNotFoundSignal(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("SignalWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(serviceerror.NewNotFound("workflow execution already completed"))

	err := c.Notify(context.Background(), fulfillment.Notification{
		OrderID: orderID, EventID: "event-1", Kind: fulfillment.KindPickedUp,
	})

	require.ErrorIs(t, err, fulfillment.ErrWorkflowNotFound)
}

func TestClientRejectsUnknownKind(t *testing.T) {
	c, _ := newClient(t)

	require.Error(t, c.Notify(context.Background(), fulfillment.Notification{OrderID: orderID, EventID: "e", Kind: "unknown"}))
}

func TestClientRequestsCancellationByUpdate(t *testing.T) {
	requestedAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	request := fulfillment.CancellationRequest{
		OrderID:     orderID,
		RequestID:   "cancel-1",
		RequestedBy: application.Subject{Kind: application.SubjectUser, ID: "customer-1"},
		RequestedAt: requestedAt,
		Reason:      "changed mind",
	}
	wantOptions := client.UpdateWorkflowOptions{
		WorkflowID: "order:order-1",
		UpdateID:   "cancel-1",
		UpdateName: temporal.UpdateCancellationRequested,
		Args: []any{temporal.CancellationRequest{
			RequestID: "cancel-1", RequestedBy: "user:customer-1", RequestedAt: requestedAt, Reason: "changed mind",
		}},
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
			wantErr: fulfillment.ErrCancellationRefused,
		},
		{
			name:    "workflow closed",
			update:  serviceerror.NewNotFound("closed"),
			wantErr: fulfillment.ErrWorkflowNotFound,
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

			err := c.RequestCancellation(context.Background(), request)

			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
