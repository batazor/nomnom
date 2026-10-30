// Package recordcourierrelease records the end of a Courier Assignment: an
// unassignment without a replacement (CourierUnassignedFromDelivery) only
// clears the courier of the stored assignment, because delivery revokes the
// access of the courier (delivery#courier); a cancelled Delivery
// (DeliveryCancelled) also deletes order#delivery. The Order and the
// fulfillment workflow are not touched: neither fact is a fulfillment fact of
// orders.
package recordcourierrelease

import (
	"context"
	"fmt"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the stable logical Inbox consumer name of this use case.
const Consumer = "orders.recordcourierrelease"

// Outcomes of a release. A duplicate event is fulfillment.OutcomeDuplicate.
const (
	// OutcomeReleased: the stored assignment matched and its relationships
	// were deleted.
	OutcomeReleased fulfillment.Outcome = "released"
	// OutcomeRememberedAhead: no assignment is stored yet (the older
	// CourierAssignedToDelivery has not been handled); the release is
	// remembered with its Delivery version, so that assignment is skipped.
	OutcomeRememberedAhead fulfillment.Outcome = "remembered_ahead"
	// OutcomeIgnoredStale: the stored assignment belongs to another Delivery
	// or has a newer Delivery version; nothing changes except the Inbox record.
	OutcomeIgnoredStale fulfillment.Outcome = "ignored_stale"
)

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
	// DeliveryVersion is the aggregate_version of the Delivery in the event
	// envelope; it orders the courier events of the Order.
	DeliveryVersion uint64
	DeliveryID      string
	// CourierID is the unassigned courier; it is empty for a cancellation,
	// which releases whichever courier the Delivery has.
	CourierID string
	// DeliveryCancelled also removes order#delivery: the Delivery is closed.
	DeliveryCancelled bool
}

type Result struct {
	Outcome fulfillment.Outcome
}

type Handler struct {
	uow         unitofwork.UnitOfWork
	inbox       unitofwork.Inbox
	assignments order.CourierAssignments
	changes     syncorderrelationships.Changes
}

func NewHandler(
	uow unitofwork.UnitOfWork,
	inbox unitofwork.Inbox,
	assignments order.CourierAssignments,
	changes syncorderrelationships.Changes,
) Handler {
	return Handler{uow: uow, inbox: inbox, assignments: assignments, changes: changes}
}

func (in Input) validate() error {
	switch {
	case in.EventID == "":
		return usecase.InvalidInput("event_id", "is required")
	case in.OrderID == "":
		return usecase.InvalidInput("order_id", "is required")
	case in.OccurredAt.IsZero():
		return usecase.InvalidInput("occurred_at", "is required")
	case in.DeliveryID == "":
		return usecase.InvalidInput("delivery_id", "is required")
	case !in.DeliveryCancelled && in.CourierID == "":
		return usecase.InvalidInput("courier_id", "is required")
	}
	return nil
}

// Handle runs unitofwork.AcceptOnce { read assignment under row lock →
// remember the release → record RelationshipsChanged when order#delivery goes }.
// orders-consumer then syncs the relationships (ADR 18).
func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}
	var outcome fulfillment.Outcome
	accepted, err := unitofwork.AcceptOnce(ctx, h.uow, h.inbox, Consumer, in.EventID, func(ctx context.Context) error {
		var err error
		outcome, err = h.release(ctx, in)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if !accepted {
		outcome = fulfillment.OutcomeDuplicate
	}
	return Result{Outcome: outcome}, nil
}

// release applies the event to the stored assignment; order.CourierAssignment
// decides whether the event is stale (another Delivery or an older Delivery
// version), whether it removes order#delivery (only a cancellation) and what
// assignment is kept. Without a stored assignment nothing is deleted. In every applied case the
// release is remembered with its version, so an older assignment handled
// afterwards is skipped by Record Courier Assignment.
func (h Handler) release(ctx context.Context, in Input) (fulfillment.Outcome, error) {
	stored, found, err := h.assignments.CourierAssignment(ctx, in.OrderID)
	if err != nil {
		return "", fmt.Errorf("read courier assignment: %w", err)
	}
	release := order.CourierRelease{
		DeliveryID:        in.DeliveryID,
		CourierID:         in.CourierID,
		DeliveryCancelled: in.DeliveryCancelled,
		OccurredAt:        in.OccurredAt,
		DeliveryVersion:   in.DeliveryVersion,
	}
	outcome, kept := OutcomeRememberedAhead, release.Kept()
	unlinked := false
	if found {
		var ok bool
		if kept, ok = stored.Release(release); !ok {
			return OutcomeIgnoredStale, nil
		}
		outcome = OutcomeReleased
		unlinked = stored.HoldsRelationsOf(release)
	}
	if err := h.assignments.RememberCourierAssignment(ctx, in.OrderID, kept); err != nil {
		return "", fmt.Errorf("remember courier release: %w", err)
	}
	if unlinked {
		if err := h.changes.RelationshipsChanged(ctx, in.OrderID); err != nil {
			return "", fmt.Errorf("record relationships change: %w", err)
		}
	}
	return outcome, nil
}
