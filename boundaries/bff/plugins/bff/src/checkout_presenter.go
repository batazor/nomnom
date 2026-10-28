package main

import (
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

var placeOrderProblemCodes = map[applications.ProblemCode]service.PlaceOrderProblemCode{
	applications.ProblemOrderItemUnavailable:         service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_ORDER_ITEM_UNAVAILABLE,
	applications.ProblemOrderTotalBelowMinimum:       service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_ORDER_TOTAL_BELOW_MINIMUM,
	applications.ProblemOrderTotalAboveMaximum:       service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_ORDER_TOTAL_ABOVE_MAXIMUM,
	applications.ProblemRestaurantNotAcceptingOrders: service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_RESTAURANT_NOT_ACCEPTING_ORDERS,
	applications.ProblemPremiumUnavailable:           service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_PREMIUM_UNAVAILABLE,
	applications.ProblemSlotUnavailable:              service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_SLOT_UNAVAILABLE,
	applications.ProblemScheduledOrdersUnavailable:   service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_SCHEDULED_ORDERS_UNAVAILABLE,
}

// placeOrderPayload carries the placed Order or the problems.
func placeOrderPayload(result applications.Result) (*service.PlaceOrderPayload, error) {
	payload := &service.PlaceOrderPayload{Problems: make([]*service.PlaceOrderProblem, 0, len(result.Problems))}
	for _, problem := range result.Problems {
		code, err := enumValue(placeOrderProblemCodes, "place order problem", problem.Code)
		if err != nil {
			return nil, err
		}
		message := &service.PlaceOrderProblem{
			Code:               code,
			MenuItemIds:        problem.MenuItemIDs,
			AlternativeWindows: make([]*service.DeliveryWindow, 0, len(problem.AlternativeWindows)),
		}
		// orders offers only windows that still have room.
		for _, window := range problem.AlternativeWindows {
			message.AlternativeWindows = append(message.AlternativeWindows, &service.DeliveryWindow{
				Start: timestamp(window.Start), End: timestamp(window.End), Available: true,
			})
		}
		payload.Problems = append(payload.Problems, message)
	}
	if result.Order != nil {
		order, err := orderMessage(*result.Order)
		if err != nil {
			return nil, err
		}
		payload.Order = order
	}
	return payload, nil
}
