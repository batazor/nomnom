package main

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// The tables name the GraphQL value of every application value. A value that
// is missing is an error of the resolver, never UNSPECIFIED (AC-641).
var (
	orderStatuses = map[applications.Status]service.OrderStatus{
		applications.StatusReceived:       service.OrderStatus_ORDER_STATUS_RECEIVED,
		applications.StatusConfirmed:      service.OrderStatus_ORDER_STATUS_CONFIRMED,
		applications.StatusPreparing:      service.OrderStatus_ORDER_STATUS_PREPARING,
		applications.StatusReadyForPickup: service.OrderStatus_ORDER_STATUS_READY_FOR_PICKUP,
		applications.StatusPickedUp:       service.OrderStatus_ORDER_STATUS_PICKED_UP,
		applications.StatusDelivered:      service.OrderStatus_ORDER_STATUS_DELIVERED,
		applications.StatusCancelled:      service.OrderStatus_ORDER_STATUS_CANCELLED,
		applications.StatusFailed:         service.OrderStatus_ORDER_STATUS_FAILED,
	}
	orderEndReasons = map[applications.EndReason]service.OrderEndReason{
		applications.EndCustomerCancelled:  service.OrderEndReason_ORDER_END_REASON_CUSTOMER_CANCELLED,
		applications.EndRestaurantRejected: service.OrderEndReason_ORDER_END_REASON_RESTAURANT_REJECTED,
		applications.EndDeliveryFailed:     service.OrderEndReason_ORDER_END_REASON_DELIVERY_FAILED,
		applications.EndNotFulfilled:       service.OrderEndReason_ORDER_END_REASON_NOT_FULFILLED,
	}
	orderMilestoneKinds = map[applications.MilestoneKind]service.OrderMilestoneKind{
		applications.MilestonePlaced:             service.OrderMilestoneKind_ORDER_MILESTONE_KIND_PLACED,
		applications.MilestoneConfirmed:          service.OrderMilestoneKind_ORDER_MILESTONE_KIND_CONFIRMED,
		applications.MilestonePreparationStarted: service.OrderMilestoneKind_ORDER_MILESTONE_KIND_PREPARATION_STARTED,
		applications.MilestoneReadyForPickup:     service.OrderMilestoneKind_ORDER_MILESTONE_KIND_READY_FOR_PICKUP,
		applications.MilestoneCourierAssigned:    service.OrderMilestoneKind_ORDER_MILESTONE_KIND_COURIER_ASSIGNED,
		applications.MilestonePickedUp:           service.OrderMilestoneKind_ORDER_MILESTONE_KIND_PICKED_UP,
		applications.MilestoneDelivered:          service.OrderMilestoneKind_ORDER_MILESTONE_KIND_DELIVERED,
		applications.MilestoneEnded:              service.OrderMilestoneKind_ORDER_MILESTONE_KIND_ENDED,
	}
)

// enumValue looks value up in table; kind names the enum in the error.
func enumValue[A ~string, G any](table map[A]G, kind string, value A) (G, error) {
	result, ok := table[value]
	if !ok {
		var zero G
		return zero, fmt.Errorf("unknown %s %q", kind, value)
	}
	return result, nil
}

// orderMessage presents the customer view of an Order; order tracking and
// checkout return the same Order type.
func orderMessage(view applications.OrderView) (*service.Order, error) {
	status, err := enumValue(orderStatuses, "order status", view.Status)
	if err != nil {
		return nil, err
	}
	order := &service.Order{
		Id:     view.ID,
		Status: status,
		Restaurant: &service.Restaurant{
			Id:              view.Restaurant.ID,
			Name:            view.Restaurant.Name,
			Address:         view.Restaurant.Address,
			AcceptingOrders: view.Restaurant.IsOpen,
		},
		Total:           money(view.Total),
		DeliveryAddress: view.DeliveryAddress,
		PlacedAt:        timestamp(view.PlacedAt),
	}
	if view.EndReason != "" {
		if order.EndReason, err = enumValue(orderEndReasons, "order end reason", view.EndReason); err != nil {
			return nil, err
		}
	}
	for _, item := range view.Items {
		order.Items = append(order.Items, &service.OrderItem{
			MenuItem:  &service.OrderedMenuItem{Id: item.MenuItemID, Name: item.Name},
			Quantity:  int32(item.Quantity),
			UnitPrice: money(item.UnitPrice),
			LineTotal: money(item.LineTotal),
		})
	}
	for _, milestone := range view.Milestones {
		kind, err := enumValue(orderMilestoneKinds, "order milestone kind", milestone.Kind)
		if err != nil {
			return nil, err
		}
		order.Milestones = append(order.Milestones, &service.OrderMilestone{
			Kind:       kind,
			OccurredAt: timestamp(milestone.OccurredAt),
		})
	}
	if view.AvailableUntil != nil {
		order.Cancellation = &service.OrderCancellationAvailability{AvailableUntil: timestamp(*view.AvailableUntil)}
	}
	return order, nil
}

func money(value applications.Money) *service.Money {
	return &service.Money{Amount: value.Amount, Currency: value.Currency}
}

// timestamp formats moments in RFC 3339, as the public schema requires.
func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// orderConnection presents a page of the order history; every node is the
// same Order as order(id).
func orderConnection(page applications.OrderPage) (*service.OrderConnection, error) {
	connection := &service.OrderConnection{
		Edges:    make([]*service.OrderEdge, 0, len(page.Edges)),
		PageInfo: &service.PageInfo{HasNextPage: page.HasNextPage},
	}
	if page.EndCursor != "" {
		connection.PageInfo.EndCursor = wrapperspb.String(page.EndCursor)
	}
	for _, edge := range page.Edges {
		order, err := orderMessage(edge.Order)
		if err != nil {
			return nil, err
		}
		connection.Edges = append(connection.Edges, &service.OrderEdge{Cursor: edge.Cursor, Node: order})
	}
	return connection, nil
}
