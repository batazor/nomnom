package main

import (
	"google.golang.org/protobuf/types/known/wrapperspb"

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
		service.KitchenAction_KITCHEN_ACTION_ACCEPT:         applications.ActionAccept,
		service.KitchenAction_KITCHEN_ACTION_REJECT:         applications.ActionReject,
		service.KitchenAction_KITCHEN_ACTION_MARK_ASSEMBLED: applications.ActionMarkAssembled,
		service.KitchenAction_KITCHEN_ACTION_MARK_READY:     applications.ActionMarkReady,
	}
	kitchenPriorities = map[applications.Priority]service.OrderPriority{
		applications.PriorityStandard: service.OrderPriority_ORDER_PRIORITY_STANDARD,
		applications.PriorityPremium:  service.OrderPriority_ORDER_PRIORITY_PREMIUM,
	}
	kitchenProblems = map[applications.Problem]service.KitchenActionProblemCode{
		applications.ProblemOrderStageConflict: service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_ORDER_STAGE_CONFLICT,
		applications.ProblemNoCookAvailable:    service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_NO_COOK_AVAILABLE,
	}
	cookPoolProblems = map[applications.CookPoolProblem]service.CookPoolProblemCode{
		applications.ProblemCookNameInvalid: service.CookPoolProblemCode_COOK_POOL_PROBLEM_CODE_COOK_NAME_INVALID,
		applications.ProblemCookBusy:        service.CookPoolProblemCode_COOK_POOL_PROBLEM_CODE_COOK_BUSY,
		applications.ProblemCookOnLine:      service.CookPoolProblemCode_COOK_POOL_PROBLEM_CODE_COOK_ON_LINE,
		applications.ProblemCookRetired:     service.CookPoolProblemCode_COOK_POOL_PROBLEM_CODE_COOK_RETIRED,
	}
)

// kitchenMessage leaves the menu of the restaurant null: the kitchen does not
// read it.
func kitchenMessage(view applications.KitchenView) (*service.Kitchen, error) {
	message := &service.Kitchen{
		Restaurant: &service.Restaurant{
			Id:              view.Restaurant.ID,
			Name:            view.Restaurant.Name,
			AcceptingOrders: view.Restaurant.IsOpen,
		},
		Orders:   make([]*service.KitchenOrder, 0, len(view.Orders)),
		CookPool: cookPoolMessage(view.CookPool),
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
	priority, err := enumValue(kitchenPriorities, "kitchen order priority", order.Priority)
	if err != nil {
		return nil, err
	}
	items := make([]*service.KitchenOrderItem, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, &service.KitchenOrderItem{Name: item.Name, Quantity: int32(item.Quantity)})
	}
	message := &service.KitchenOrder{
		Id:              order.ID,
		OrderId:         order.OrderID,
		Stage:           stage,
		Items:           items,
		ReceivedAt:      timestamp(order.ReceivedAt),
		AcceptedAt:      deadline(order.AcceptedAt),
		ExpectedReadyAt: deadline(order.ExpectedReadyAt),
		Priority:        priority,
	}
	if order.Cook != nil {
		message.Cook = cookMessage(*order.Cook)
	}
	return message, nil
}

func cookMessage(cook applications.Cook) *service.Cook {
	message := &service.Cook{Id: cook.ID, Name: cook.Name, OnLine: cook.OnLine}
	if cook.CurrentKitchenOrderID != "" {
		message.CurrentKitchenOrderId = wrapperspb.String(cook.CurrentKitchenOrderID)
	}
	return message
}

func cookPoolMessage(pool applications.CookPool) *service.CookPool {
	message := &service.CookPool{
		Size:   int32(pool.Size),
		OnLine: int32(pool.OnLine),
		Busy:   int32(pool.Busy),
		Queued: int32(pool.Queued),
		Cooks:  make([]*service.Cook, 0, len(pool.Cooks)),
	}
	for _, cook := range pool.Cooks {
		message.Cooks = append(message.Cooks, cookMessage(cook))
	}
	return message
}

func cookPoolPayload(result applications.CookPoolResult) (*service.CookPoolPayload, error) {
	payload := &service.CookPoolPayload{CookPool: cookPoolMessage(result.Pool)}
	if result.Problem != "" {
		problem, err := enumValue(cookPoolProblems, "cook pool problem", result.Problem)
		if err != nil {
			return nil, err
		}
		payload.Problem = problem
	}
	return payload, nil
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
