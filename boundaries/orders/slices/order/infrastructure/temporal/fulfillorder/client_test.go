package fulfillorder_test

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
	"go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/infrastructure/temporal/fulfillorder"
)

func newClient(t *testing.T) (*fulfillorder.Client, *mocks.Client) {
	t.Helper()
	temporalClient := mocks.NewClient(t)
	return fulfillorder.NewClient(temporalClient, policy()), temporalClient
}

var wantStartOptions = client.StartWorkflowOptions{
	ID:                       "order:order-1",
	TaskQueue:                fulfillorder.TaskQueue,
	WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
}

func TestClientStartsWorkflowWithStableIdentity(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("ExecuteWorkflow", mock.Anything, wantStartOptions, fulfillorder.WorkflowName,
		fulfillorder.Input{OrderID: orderID, Policy: policy()}).Return(mocks.NewWorkflowRun(t), nil)

	require.NoError(t, c.Start(context.Background(), fulfillment.StartRequest{OrderID: orderID}))
}

// AC-445: a closed workflow is not started again and Start succeeds.
func TestClientStartOfClosedWorkflowSucceeds(t *testing.T) {
	c, temporalClient := newClient(t)
	temporalClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("closed", "", "run-1"))

	require.NoError(t, c.Start(context.Background(), fulfillment.StartRequest{OrderID: orderID}))
}

func TestClientStartFailureIsReturned(t *testing.T) {
	c, temporalClient := newClient(t)
	cause := serviceerror.NewUnavailable("temporal down")
	temporalClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, cause)

	require.ErrorIs(t, c.Start(context.Background(), fulfillment.StartRequest{OrderID: orderID}), cause)
}

func TestClientSignalsWithStartOnOrderPlaced(t *testing.T) {
	c, temporalClient := newClient(t)
	fact := fulfillorder.Fact{EventID: "event-1"}
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, "order:order-1", fulfillorder.SignalOrderPlaced, fact,
		wantStartOptions, fulfillorder.WorkflowName, fulfillorder.Input{OrderID: orderID, Policy: policy()}).
		Return(mocks.NewWorkflowRun(t), nil)

	require.NoError(t, c.StartFromOrderPlaced(context.Background(), orderID, fact))
}

func TestClientNotifiesWithIntegrationEventName(t *testing.T) {
	occurredAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		kind   fulfillment.Kind
		signal string
	}{
		{fulfillment.KindRestaurantAccepted, fulfillorder.SignalRestaurantOrderAccepted},
		{fulfillment.KindRestaurantRejected, fulfillorder.SignalRestaurantOrderRejected},
		{fulfillment.KindPreparationStarted, fulfillorder.SignalPreparationStarted},
		{fulfillment.KindPreparationDelayed, fulfillorder.SignalPreparationDelayed},
		{fulfillment.KindPreparationFailed, fulfillorder.SignalPreparationFailed},
		{fulfillment.KindReadyForPickup, fulfillorder.SignalRestaurantOrderReady},
		{fulfillment.KindCourierAssigned, fulfillorder.SignalCourierAssignedToDelivery},
		{fulfillment.KindPickedUp, fulfillorder.SignalDeliveryPickedUp},
		{fulfillment.KindDeliveryCompleted, fulfillorder.SignalDeliveryCompleted},
		{fulfillment.KindDeliveryFailed, fulfillorder.SignalDeliveryFailed},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			c, temporalClient := newClient(t)
			temporalClient.On("SignalWorkflow", mock.Anything, "order:order-1", "", tt.signal,
				fulfillorder.Fact{EventID: "event-1", OccurredAt: occurredAt, Reason: "r"}).Return(nil)

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
		UpdateName: fulfillorder.UpdateCancellationRequested,
		Args: []any{fulfillorder.CancellationRequest{
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
			result:  temporal.NewApplicationError("refused", fulfillorder.ErrTypeCancellationRefused),
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
