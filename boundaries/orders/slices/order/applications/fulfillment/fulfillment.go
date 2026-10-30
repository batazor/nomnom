// Package fulfillment is the application port of the Order Fulfillment Workflow
// shared by the use cases of the order slice. It is not a use case: it declares
// how handlers start the workflow, hand signals to it and request
// cancellation, and the common flow that reflects inbound signals in an Order.
// Temporal specifics (Workflow ID, Signal/Update names) belong to the adapter.
package fulfillment

import (
	"context"
	"errors"
	"time"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/slices/order/domains/order"
)

// ErrWorkflowNotFound is returned by adapters when the workflow of an Order does
// not exist or is already closed.
var ErrWorkflowNotFound = errors.New("order fulfillment workflow not found or closed")

// ErrCancellationRefused is returned when the workflow refuses a Cancellation
// Request in its current Workflow Stage, for example after Pickup.
var ErrCancellationRefused = errors.New("order fulfillment workflow refused cancellation")

// Kind is a signal of a neighbouring context handed to the workflow.
type Kind string

const (
	KindRestaurantAccepted Kind = "restaurant_accepted"
	KindRestaurantRejected Kind = "restaurant_rejected"
	KindPreparationStarted Kind = "preparation_started"
	KindReadyForPickup     Kind = "ready_for_pickup"
	KindCourierAssigned    Kind = "courier_assigned"
	KindPickedUp           Kind = "picked_up"
	KindDeliveryCompleted  Kind = "delivery_completed"
	KindDeliveryFailed     Kind = "delivery_failed"
	// KindPreparationDelayed and KindPreparationFailed are not Fulfillment
	// Steps of the Order: a delay only replaces its expected ready time, a
	// failure is an orchestration input only.
	KindPreparationDelayed Kind = "preparation_delayed"
	KindPreparationFailed  Kind = "preparation_failed"
)

// StartRequest carries OrderPlaced of the Order: the workflow is started with
// it as the first signal and deduplicates it by EventID.
type StartRequest struct {
	OrderID order.ID
	// RestaurantID is the fairness key of the workflow: every restaurant gets
	// its share of the worker, whatever the load of the others.
	RestaurantID order.RestaurantID
	EventID      string
	OccurredAt   time.Time
	// DeliveryWindow of OrderPlaced: set starts the scheduled route of a
	// Scheduled Order (FR-415), zero the ASAP route.
	DeliveryWindow order.DeliveryWindow
}

// Starter starts the workflow of one Order with its OrderPlaced; repeated calls
// address the same workflow, a closed one is not started again.
type Starter interface {
	Start(ctx context.Context, request StartRequest) error
}

// Notification carries the source event_id so the workflow can deduplicate it.
type Notification struct {
	OrderID    order.ID
	EventID    string
	Kind       Kind
	OccurredAt time.Time
	Reason     string
	// ExpectedReadyAt is set only for KindPreparationDelayed.
	ExpectedReadyAt time.Time
}

// Notifier hands a signal to the running workflow of an Order.
type Notifier interface {
	Notify(ctx context.Context, notification Notification) error
}

// CancellationRequest keeps the original subject for audit; the workflow acts
// further under its own service identity.
type CancellationRequest struct {
	OrderID     order.ID
	RequestID   string
	RequestedBy authz.Subject
	RequestedAt time.Time
	Reason      string
}

// CancellationRequester passes a Cancellation Request to the existing workflow,
// idempotently by RequestID. A business refusal of the workflow is returned as error.
type CancellationRequester interface {
	RequestCancellation(ctx context.Context, request CancellationRequest) error
}
