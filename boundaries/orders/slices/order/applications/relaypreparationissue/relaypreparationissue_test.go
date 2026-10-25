package relaypreparationissue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
	"nomnom/orders/slices/order/infrastructure/fake"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func TestHandleNotifiesWorkflow(t *testing.T) {
	workflow := fake.NewWorkflow()
	in := relaypreparationissue.Input{
		EventID:         "event-1",
		OrderID:         "order-1",
		Kind:            fulfillment.KindPreparationDelayed,
		OccurredAt:      occurredAt,
		ExpectedReadyAt: occurredAt.Add(10 * time.Minute),
	}

	result, err := relaypreparationissue.NewHandler(workflow).Handle(context.Background(), in)

	require.NoError(t, err)
	assert.True(t, result.WorkflowNotified)
	assert.Equal(t, []fulfillment.Notification{{
		OrderID:         "order-1",
		EventID:         "event-1",
		Kind:            fulfillment.KindPreparationDelayed,
		OccurredAt:      occurredAt,
		ExpectedReadyAt: occurredAt.Add(10 * time.Minute),
	}}, workflow.Notifications())
}

func TestClosedWorkflowIsNotAnError(t *testing.T) {
	workflow := fake.NewWorkflow()
	workflow.Close("order-1")
	in := relaypreparationissue.Input{EventID: "event-1", OrderID: "order-1", Kind: fulfillment.KindPreparationFailed, OccurredAt: occurredAt}

	result, err := relaypreparationissue.NewHandler(workflow).Handle(context.Background(), in)

	require.NoError(t, err)
	assert.False(t, result.WorkflowNotified)
}

func TestNotifyFailureIsReturned(t *testing.T) {
	workflow := fake.NewWorkflow()
	workflow.FailNotify(errors.New("temporal unavailable"))
	in := relaypreparationissue.Input{EventID: "event-1", OrderID: "order-1", Kind: fulfillment.KindPreparationFailed, OccurredAt: occurredAt}

	_, err := relaypreparationissue.NewHandler(workflow).Handle(context.Background(), in)

	require.Error(t, err)
}

func TestRejectsInvalidInput(t *testing.T) {
	in := relaypreparationissue.Input{EventID: "event-1", OrderID: "order-1", Kind: fulfillment.KindPreparationDelayed, OccurredAt: occurredAt}

	_, err := relaypreparationissue.NewHandler(fake.NewWorkflow()).Handle(context.Background(), in)

	require.ErrorIs(t, err, application.ErrInvalidInput)
}
