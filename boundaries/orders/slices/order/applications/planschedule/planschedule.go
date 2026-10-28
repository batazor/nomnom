// Package planschedule is the Plan Schedule use case of a Scheduled Order
// (FR-677): it reads the current estimates — the baseline preparation time of
// the restaurant and the Delivery Duration Estimate of the Market — and
// computes the backward plan by the domain policy order.PlanSchedule. The
// Order Fulfillment Workflow calls it through its Activity PlanSchedule and
// keeps the plan in Event History; the Order does not store it.
package planschedule

import (
	"context"
	"fmt"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// Orders reads the Scheduled Order: its window, restaurant and Market.
type Orders interface {
	Get(ctx context.Context, id order.ID) (*order.Order, error)
}

// Preparation reads the baseline preparation time of a restaurant (median of
// its Wait Time Statistics) from restaurants.
type Preparation interface {
	BaselinePreparation(ctx context.Context, restaurantID order.RestaurantID) (time.Duration, error)
}

// DeliveryEstimate is the Delivery Duration Estimate of a Market:
// CourierLead is Create Delivery → Pickup, Transit is Pickup → handover.
type DeliveryEstimate struct {
	CourierLead time.Duration
	Transit     time.Duration
}

// DeliveryDuration reads the Delivery Duration Estimate of a Market from
// delivery.
type DeliveryDuration interface {
	EstimateDeliveryDuration(ctx context.Context, marketID vo.MarketID) (DeliveryEstimate, error)
}

// Input addresses the Order; a non-zero PlannedReadyAt is the replan
// checkpoint: the moment the kitchen got is kept (FR-679).
type Input struct {
	OrderID        order.ID
	PlannedReadyAt time.Time
}

type Handler struct {
	orders      Orders
	preparation Preparation
	delivery    DeliveryDuration
	policy      order.SchedulePolicy
}

// NewHandler takes SCHEDULED_ORDER__PREPARATION_BUFFER and
// SCHEDULED_ORDER__REPLAN_LEAD as policy, validated at startup.
func NewHandler(orders Orders, preparation Preparation, delivery DeliveryDuration, policy order.SchedulePolicy) Handler {
	return Handler{orders: orders, preparation: preparation, delivery: delivery, policy: policy}
}

// Handle reads fresh estimates on every call; an ASAP Order has no plan
// (order.ErrNotScheduled).
func (h Handler) Handle(ctx context.Context, in Input) (order.SchedulePlan, error) {
	if in.OrderID == "" {
		return order.SchedulePlan{}, application.InvalidInput("order_id", "is required")
	}
	current, err := h.orders.Get(ctx, in.OrderID)
	if err != nil {
		return order.SchedulePlan{}, err
	}
	if !current.Scheduled() {
		return order.SchedulePlan{}, fmt.Errorf("plan order %s: %w", in.OrderID, order.ErrNotScheduled)
	}
	preparation, err := h.preparation.BaselinePreparation(ctx, current.RestaurantID())
	if err != nil {
		return order.SchedulePlan{}, fmt.Errorf("read baseline preparation: %w", err)
	}
	delivery, err := h.delivery.EstimateDeliveryDuration(ctx, current.MarketID())
	if err != nil {
		return order.SchedulePlan{}, fmt.Errorf("read delivery duration estimate: %w", err)
	}
	return order.PlanSchedule(current.DeliveryWindow(), order.ScheduleEstimates{
		Preparation: preparation,
		CourierLead: delivery.CourierLead,
		Transit:     delivery.Transit,
	}, h.policy, in.PlannedReadyAt)
}
