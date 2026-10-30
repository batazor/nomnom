package di

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	sdktemporal "go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/config"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// AC-514: the API client of Temporal is lazy: an unreachable Temporal does not
// fail the start, only the call that needs the workflow.
func TestFulfillmentWorkflowDoesNotDialAtStart(t *testing.T) {
	c, cleanup, err := NewTemporalClient(config.TemporalConfig{HostPort: "127.0.0.1:1", Namespace: "default"})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	workflow := NewFulfillmentWorkflow(c, config.FulfillmentConfig{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.Error(t, workflow.RequestCancellation(ctx, fulfillment.CancellationRequest{OrderID: "order-1", RequestID: "request-1"}))
}

func TestPortNotifiesWithIntegrationEventName(t *testing.T) {
	occurredAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		kind   fulfillment.Kind
		signal string
	}{
		{fulfillment.KindRestaurantAccepted, fulfillmenttemporal.SignalRestaurantOrderAccepted},
		{fulfillment.KindRestaurantRejected, fulfillmenttemporal.SignalRestaurantOrderRejected},
		{fulfillment.KindPreparationStarted, fulfillmenttemporal.SignalPreparationStarted},
		{fulfillment.KindPreparationDelayed, fulfillmenttemporal.SignalPreparationDelayed},
		{fulfillment.KindPreparationFailed, fulfillmenttemporal.SignalPreparationFailed},
		{fulfillment.KindReadyForPickup, fulfillmenttemporal.SignalRestaurantOrderReady},
		{fulfillment.KindCourierAssigned, fulfillmenttemporal.SignalCourierAssignedToDelivery},
		{fulfillment.KindPickedUp, fulfillmenttemporal.SignalDeliveryPickedUp},
		{fulfillment.KindDeliveryCompleted, fulfillmenttemporal.SignalDeliveryCompleted},
		{fulfillment.KindDeliveryFailed, fulfillmenttemporal.SignalDeliveryFailed},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			temporalClient := mocks.NewClient(t)
			temporalClient.On("SignalWorkflow", mock.Anything, "order:order-1", "", tt.signal,
				fulfillmenttemporal.SignalPayload{EventID: "event-1", OccurredAt: occurredAt, Reason: "r"}).Return(nil)

			require.NoError(t, newFulfillmentPort(temporalClient, config.FulfillmentConfig{}).Notify(context.Background(), fulfillment.Notification{
				OrderID: "order-1", EventID: "event-1", Kind: tt.kind, OccurredAt: occurredAt, Reason: "r",
			}))
		})
	}
}

func TestPortRejectsUnknownKind(t *testing.T) {
	port := newFulfillmentPort(mocks.NewClient(t), config.FulfillmentConfig{})

	require.Error(t, port.Notify(context.Background(), fulfillment.Notification{OrderID: "order-1", EventID: "e", Kind: "unknown"}))
}

// AC-444: a missing or closed workflow is ErrWorkflowNotFound of the port.
func TestPortMapsNotFoundSignal(t *testing.T) {
	temporalClient := mocks.NewClient(t)
	temporalClient.On("SignalWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(serviceerror.NewNotFound("workflow execution already completed"))

	err := newFulfillmentPort(temporalClient, config.FulfillmentConfig{}).Notify(context.Background(), fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp,
	})

	require.ErrorIs(t, err, fulfillment.ErrWorkflowNotFound)
}

// FR-415: the Delivery Window of OrderPlaced is the input that chooses the
// scheduled route.
func TestPortStartsScheduledOrderWithItsWindow(t *testing.T) {
	placedAt := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	start := placedAt.Add(7 * time.Hour)
	window, err := order.NewDeliveryWindow(start, start.Add(15*time.Minute))
	require.NoError(t, err)
	temporalClient := mocks.NewClient(t)
	temporalClient.On("SignalWithStartWorkflow", mock.Anything, "order:order-1", fulfillmenttemporal.SignalOrderPlaced,
		fulfillmenttemporal.SignalPayload{EventID: "event-1", OccurredAt: placedAt},
		mock.Anything, fulfillmenttemporal.WorkflowName,
		mock.MatchedBy(func(in fulfillmenttemporal.Input) bool {
			return in.OrderID == "order-1" && in.DeliveryWindow != nil && in.DeliveryWindow.Start.Equal(start)
		})).
		Return(mocks.NewWorkflowRun(t), nil)

	require.NoError(t, newFulfillmentPort(temporalClient, config.FulfillmentConfig{}).Start(context.Background(), fulfillment.StartRequest{
		OrderID: "order-1", RestaurantID: "restaurant-1", EventID: "event-1", OccurredAt: placedAt, DeliveryWindow: window,
	}))
}

func TestPortRequestsCancellationWithSubject(t *testing.T) {
	requestedAt := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	wantOptions := client.UpdateWorkflowOptions{
		WorkflowID: "order:order-1",
		UpdateID:   "cancel-1",
		UpdateName: fulfillmenttemporal.UpdateCancellationRequested,
		Args: []any{fulfillmenttemporal.CancellationRequest{
			RequestID: "cancel-1", RequestedBy: "user:customer-1", RequestedAt: requestedAt, Reason: "changed mind",
		}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	}
	request := fulfillment.CancellationRequest{
		OrderID:     "order-1",
		RequestID:   "cancel-1",
		RequestedBy: authz.Subject{Kind: authz.SubjectUser, ID: "customer-1"},
		RequestedAt: requestedAt,
		Reason:      "changed mind",
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
			result:  sdktemporal.NewApplicationError("refused", fulfillmenttemporal.ErrTypeCancellationRefused),
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
			temporalClient := mocks.NewClient(t)
			if tt.update != nil {
				temporalClient.On("UpdateWorkflow", mock.Anything, wantOptions).Return(nil, tt.update)
			} else {
				handle := mocks.NewWorkflowUpdateHandle(t)
				handle.On("Get", mock.Anything, nil).Return(tt.result)
				temporalClient.On("UpdateWorkflow", mock.Anything, wantOptions).Return(handle, nil)
			}

			err := newFulfillmentPort(temporalClient, config.FulfillmentConfig{}).RequestCancellation(context.Background(), request)

			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
