package relaypreparationissue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
	"nomnom/orders/slices/order/infrastructure/mocks"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func TestHandleNotifiesWorkflow(t *testing.T) {
	workflow := mocks.NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID:    "order-1",
		EventID:    "event-1",
		Kind:       fulfillment.KindPreparationFailed,
		OccurredAt: occurredAt,
		Reason:     "oven_broken",
	}).Return(nil).Once()
	in := relaypreparationissue.Input{EventID: "event-1", OrderID: "order-1", OccurredAt: occurredAt, Reason: "oven_broken"}

	result, err := relaypreparationissue.NewHandler(workflow).Handle(context.Background(), in)

	require.NoError(t, err)
	assert.True(t, result.WorkflowNotified)
}

func TestClosedWorkflowIsNotAnError(t *testing.T) {
	workflow := mocks.NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationFailed, OccurredAt: occurredAt,
	}).Return(fulfillment.ErrWorkflowNotFound).Once()
	in := relaypreparationissue.Input{EventID: "event-1", OrderID: "order-1", OccurredAt: occurredAt}

	result, err := relaypreparationissue.NewHandler(workflow).Handle(context.Background(), in)

	require.NoError(t, err)
	assert.False(t, result.WorkflowNotified)
}

func TestNotifyFailureIsReturned(t *testing.T) {
	workflow := mocks.NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationFailed, OccurredAt: occurredAt,
	}).Return(errors.New("temporal unavailable")).Once()
	in := relaypreparationissue.Input{EventID: "event-1", OrderID: "order-1", OccurredAt: occurredAt}

	_, err := relaypreparationissue.NewHandler(workflow).Handle(context.Background(), in)

	require.Error(t, err)
}

func TestRejectsInvalidInput(t *testing.T) {
	in := relaypreparationissue.Input{EventID: "event-1", OrderID: "order-1"}

	_, err := relaypreparationissue.NewHandler(mocks.NewMockNotifier(t)).Handle(context.Background(), in)

	require.ErrorIs(t, err, application.ErrInvalidInput)
}
