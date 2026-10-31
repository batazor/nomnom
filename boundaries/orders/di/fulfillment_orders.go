package di

import (
	"context"
	"fmt"
	"time"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/fulfillment/applications/orders"
	"nomnom/orders/slices/fulfillment/domains/fulfillment"
	"nomnom/orders/slices/order/applications/closure/complete"
	"nomnom/orders/slices/order/applications/closure/failorcancel"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordpaymentauthorization"
	"nomnom/orders/slices/order/applications/getforfulfillment"
	"nomnom/orders/slices/order/applications/planschedule"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
)

// FulfillmentOrders реализует порт orders.Orders слайса fulfillment поверх use
// cases слайса order. Это единственное место, где встречаются словари двух
// слайсов: ошибки use cases переводятся в ошибки порта.
type FulfillmentOrders struct {
	module *orderdi.FulfillmentModule
}

var _ orders.Orders = FulfillmentOrders{}

// NewFulfillmentOrders — порт Order для Activities orders-worker.
func NewFulfillmentOrders(module *orderdi.FulfillmentModule) orders.Orders {
	return FulfillmentOrders{module: module}
}

func (o FulfillmentOrders) Get(ctx context.Context, orderID string) (orders.Order, error) {
	view, err := o.module.GetForFulfillment.Handle(ctx, getforfulfillment.Query{OrderID: order.ID(orderID)})
	if err != nil {
		return orders.Order{}, portError(err)
	}
	items := make([]orders.Item, 0, len(view.Items))
	for _, item := range view.Items {
		items = append(items, orders.Item{MenuItemID: item.MenuItemID, Quantity: item.Quantity})
	}
	current := orders.Order{
		OrderID:      view.OrderID,
		CustomerID:   view.CustomerID,
		RestaurantID: view.RestaurantID,
		MarketID:     view.MarketID,
		Amount:       view.Amount,
		Currency:     view.Currency,
		Items:        items,
		Priority:     view.Priority,
	}
	if view.DeliveryWindow != nil {
		current.DeliveryWindow = &orders.DeliveryWindow{Start: view.DeliveryWindow.Start, End: view.DeliveryWindow.End}
	}
	return current, nil
}

func (o FulfillmentOrders) RecordPaymentAuthorization(ctx context.Context, orderID string, authorizedAt time.Time) error {
	_, err := o.module.RecordPayment.Handle(ctx, recordpaymentauthorization.Input{OrderID: order.ID(orderID), AuthorizedAt: authorizedAt})
	return portError(err)
}

func (o FulfillmentOrders) Complete(ctx context.Context, orderID string, completedAt time.Time) error {
	_, err := o.module.Complete.Handle(ctx, complete.Input{OrderID: order.ID(orderID), CompletedAt: completedAt})
	return portError(err)
}

func (o FulfillmentOrders) FailOrCancel(ctx context.Context, decision orders.FinalDecision) error {
	outcome, err := negativeOutcome(decision.Outcome)
	if err != nil {
		return err
	}
	_, err = o.module.Finalize.Handle(ctx, failorcancel.Input{
		OrderID:    order.ID(decision.OrderID),
		Outcome:    outcome,
		ReasonCode: decision.ReasonCode,
		DecidedAt:  decision.DecidedAt,
	})
	return portError(err)
}

func (o FulfillmentOrders) PlanSchedule(ctx context.Context, orderID string, plannedReadyAt time.Time) (orders.Plan, error) {
	plan, err := o.module.Plan.Handle(ctx, planschedule.Input{OrderID: order.ID(orderID), PlannedReadyAt: plannedReadyAt})
	if err != nil {
		return orders.Plan{}, portError(err)
	}
	return orders.Plan{
		PlannedReadyAt:   plan.PlannedReadyAt,
		StartAt:          plan.StartAt,
		CourierRequestAt: plan.CourierRequestAt,
		ReplanAt:         plan.ReplanAt,
	}, nil
}

func negativeOutcome(outcome fulfillment.Outcome) (order.NegativeOutcome, error) {
	switch outcome {
	case fulfillment.OutcomeCancellation:
		return order.OutcomeCancellation, nil
	case fulfillment.OutcomeFailure:
		return order.OutcomeFailure, nil
	default:
		return "", fmt.Errorf("%w: outcome %q is not negative", usecase.ErrInvalidInput, outcome)
	}
}

// portErrorByKind покрывает каждый usecase.ErrorKind: ретрай не исправит
// невалидный ввод, отсутствующий Order и отказ домена. Для остальных видов у
// порта нет своей ошибки, и она остаётся как есть, поэтому Activity её ретраит.
var portErrorByKind = map[usecase.ErrorKind]error{
	usecase.KindInvalidInput:  usecase.ErrInvalidInput,
	usecase.KindNotFound:      orders.ErrOrderNotFound,
	usecase.KindDomainRule:    orders.ErrStepRefused,
	usecase.KindAlreadyExists: nil,
	usecase.KindConflict:      nil,
	usecase.KindUnavailable:   nil,
	usecase.KindPermission:    nil,
	usecase.KindInternal:      nil,
}

// portError переводит ошибку use case слайса order в ошибку порта.
func portError(err error) error {
	if err == nil {
		return nil
	}
	target := portErrorByKind[errorkind.Classify(err)]
	if target == nil {
		return err
	}
	return fmt.Errorf("%w: %w", target, err)
}
