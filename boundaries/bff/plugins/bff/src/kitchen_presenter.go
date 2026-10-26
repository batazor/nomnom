package main

import (
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
)

var (
	kitchenStages = map[applications.Stage]service.KitchenOrderStage{
		applications.StageReceived:  service.KitchenOrderStage_KITCHEN_ORDER_STAGE_RECEIVED,
		applications.StageQueued:    service.KitchenOrderStage_KITCHEN_ORDER_STAGE_QUEUED,
		applications.StagePreparing: service.KitchenOrderStage_KITCHEN_ORDER_STAGE_PREPARING,
		applications.StageAssembled: service.KitchenOrderStage_KITCHEN_ORDER_STAGE_ASSEMBLED,
		applications.StageReady:     service.KitchenOrderStage_KITCHEN_ORDER_STAGE_READY,
	}
	kitchenActions = map[service.KitchenAction]applications.Action{
		service.KitchenAction_KITCHEN_ACTION_ACCEPT:            applications.ActionAccept,
		service.KitchenAction_KITCHEN_ACTION_REJECT:            applications.ActionReject,
		service.KitchenAction_KITCHEN_ACTION_START_PREPARATION: applications.ActionStartPreparation,
		service.KitchenAction_KITCHEN_ACTION_MARK_ASSEMBLED:    applications.ActionMarkAssembled,
		service.KitchenAction_KITCHEN_ACTION_MARK_READY:        applications.ActionMarkReady,
	}
	kitchenProblems = map[applications.Problem]service.KitchenActionProblemCode{
		applications.ProblemOrderStageConflict: service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_ORDER_STAGE_CONFLICT,
	}
)

// kitchenMessage leaves the menu of the restaurant null: the kitchen does not
// read it.
func kitchenMessage(view applications.KitchenView) (*service.Kitchen, error) {
	message := &service.Kitchen{
		Restaurant: &service.Restaurant{
			Id:              view.Restaurant.ID,
			Name:            view.Restaurant.Name,
			Address:         view.Restaurant.Address,
			AcceptingOrders: view.Restaurant.IsOpen,
		},
		Orders: make([]*service.KitchenOrder, 0, len(view.Orders)),
	}
	for _, order := range view.Orders {
		kitchenOrder, err := kitchenOrderMessage(order)
		if err != nil {
			return nil, err
		}
		message.Orders = append(message.Orders, kitchenOrder)
	}
	return message, nil
}

func kitchenOrderMessage(order applications.OrderView) (*service.KitchenOrder, error) {
	stage, err := enumValue(kitchenStages, "kitchen order stage", order.Stage)
	if err != nil {
		return nil, err
	}
	items := make([]*service.KitchenOrderItem, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, &service.KitchenOrderItem{Name: item.Name, Quantity: int32(item.Quantity)})
	}
	return &service.KitchenOrder{
		Id:              order.ID,
		OrderId:         order.OrderID,
		Stage:           stage,
		Items:           items,
		ReceivedAt:      timestamp(order.ReceivedAt),
		AcceptedAt:      deadline(order.AcceptedAt),
		ExpectedReadyAt: deadline(order.ExpectedReadyAt),
	}, nil
}

// kitchenActionPayload carries either the problem or the order; both stay
// null when the order left the kitchen.
func kitchenActionPayload(result applications.ActionResult) (*service.KitchenActionPayload, error) {
	payload := &service.KitchenActionPayload{}
	if result.Problem != "" {
		problem, err := enumValue(kitchenProblems, "kitchen problem", result.Problem)
		if err != nil {
			return nil, err
		}
		payload.Problem = problem
		return payload, nil
	}
	if result.Order != nil {
		order, err := kitchenOrderMessage(*result.Order)
		if err != nil {
			return nil, err
		}
		payload.Order = order
	}
	return payload, nil
}
