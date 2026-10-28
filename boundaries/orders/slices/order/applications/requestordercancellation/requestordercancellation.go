// Package requestordercancellation hands an authorized Cancellation Request to
// the fulfillment workflow after the Order confirms it is admissible. The use
// case neither cancels the Order nor runs compensations.
package requestordercancellation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

const (
	PermissionCancel application.Permission   = "cancel"
	ResourceOrder    application.ResourceType = "order"
)

type Input struct {
	Subject application.Subject
	OrderID order.ID
	// RequestID is the client idempotency key of this Cancellation Request.
	RequestID string
	Reason    string
}

type Outcome string

const (
	OutcomeRequested        Outcome = "requested"
	OutcomeAlreadyCancelled Outcome = "already_cancelled"
)

type Result struct {
	Outcome Outcome
}

type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
}

type Orders interface {
	Get(ctx context.Context, id order.ID) (*order.Order, error)
}

// CancellationPolicy supplies the cancellation window of an Order. The product
// rule is not agreed yet, so it stays behind this port.
type CancellationPolicy interface {
	Window(ctx context.Context, placedAt time.Time, delivery order.DeliveryWindow) (order.CancellationWindow, error)
}

type Dependencies struct {
	Authorizer Authorizer
	Orders     Orders
	Policy     CancellationPolicy
	Clock      application.Clock
	Workflow   fulfillment.CancellationRequester
}

type Handler struct {
	deps Dependencies
}

func NewHandler(deps Dependencies) Handler {
	return Handler{deps: deps}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	if err := in.Subject.Validate(); err != nil {
		return Result{}, err
	}
	if in.OrderID == "" {
		return Result{}, application.InvalidInput("order_id", "is required")
	}
	if in.RequestID == "" {
		return Result{}, application.InvalidInput("request_id", "is required")
	}

	// The Customer cancels the own Order by customer_id: order#customer is
	// written after the placement commit (ADR 18). Any other subject, and any
	// subject of a missing Order, passes the cancel check first.
	current, readErr := h.deps.Orders.Get(ctx, in.OrderID)
	missing := errors.Is(readErr, order.ErrOrderNotFound)
	if readErr != nil && !missing {
		return Result{}, readErr
	}
	if missing || !in.Subject.IsUser(string(current.CustomerID())) {
		err := h.deps.Authorizer.Check(ctx, application.AccessCheck{
			Subject:    in.Subject,
			Permission: PermissionCancel,
			Resource:   application.Resource{Type: ResourceOrder, ID: string(in.OrderID)},
		})
		if err := application.FailClosed(err); err != nil {
			return Result{}, err
		}
		if missing {
			return Result{}, readErr
		}
	}
	window, err := h.deps.Policy.Window(ctx, current.PlacedAt(), current.DeliveryWindow())
	if err != nil {
		return Result{}, fmt.Errorf("read cancellation window: %w", err)
	}

	requestedAt := h.deps.Clock.Now()
	var alreadyCancelled order.AlreadyInState
	switch err := current.CheckCancellation(requestedAt, window); {
	case errors.As(err, &alreadyCancelled):
		return Result{Outcome: OutcomeAlreadyCancelled}, nil
	case err != nil:
		return Result{}, err
	}

	err = h.deps.Workflow.RequestCancellation(ctx, fulfillment.CancellationRequest{
		OrderID:     in.OrderID,
		RequestID:   in.RequestID,
		RequestedBy: in.Subject,
		RequestedAt: requestedAt,
		Reason:      in.Reason,
	})
	if err != nil {
		return Result{}, fmt.Errorf("request order cancellation: %w", err)
	}
	return Result{Outcome: OutcomeRequested}, nil
}
