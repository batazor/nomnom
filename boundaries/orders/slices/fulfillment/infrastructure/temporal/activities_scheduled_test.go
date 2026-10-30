package temporal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	sdktemporal "go.temporal.io/sdk/temporal"

	"nomnom/orders/di"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"
	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/getfulfillmentorder"
	"nomnom/orders/slices/order/applications/planschedule"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// scheduledActivities builds Activities over a real store holding the
// Scheduled Order of window 19:00–19:15; neighbours and estimates are mocks.
func scheduledActivities(t *testing.T) (*temporal.Activities, *MockRestaurants, *MockPreparation, *MockDeliveryDuration) {
	t.Helper()
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, orderID,
		ordertest.WithDeliveryWindow(t, windowStart, windowStart.Add(15*time.Minute)))))
	restaurants := NewMockRestaurants(t)
	preparation := NewMockPreparation(t)
	delivery := NewMockDeliveryDuration(t)
	return &temporal.Activities{
		Orders: di.NewFulfillmentOrders(&orderdi.FulfillmentModule{
			GetOrder: getfulfillmentorder.NewHandler(store),
			Plan: planschedule.NewHandler(store, preparation, delivery, order.SchedulePolicy{
				PreparationBuffer: 5 * time.Minute, ReplanLead: 30 * time.Minute,
			}),
		}),
		Restaurants: restaurants,
	}, restaurants, preparation, delivery
}

// FR-683: the restaurant gets the window of the Order and planned_ready_at of
// the workflow; the restaurant_order_id comes back for QueueRestaurantOrder.
func TestRequestRestaurantOrderCarriesTheSchedule(t *testing.T) {
	a, restaurants, _, _ := scheduledActivities(t)
	plannedReadyAt := windowStart.Add(-20 * time.Minute)
	restaurants.EXPECT().RequestOrder(mock.Anything, ports.RestaurantOrderRequest{
		OrderID:        orderID,
		RestaurantID:   "restaurant-1",
		Items:          []ports.RestaurantOrderItem{{MenuItemID: "menu-1", Quantity: 2}},
		Priority:       ports.PriorityStandard,
		IdempotencyKey: "order:order-1:request_restaurant_order",
		Schedule: &ports.RestaurantOrderSchedule{
			WindowStart: windowStart, WindowEnd: windowStart.Add(15 * time.Minute), PlannedReadyAt: plannedReadyAt,
		},
	}).Return("ro-1", nil).Once()

	requested, err := a.RequestRestaurantOrder(context.Background(), temporal.StepInput{
		OrderID: orderID, IdempotencyKey: "order:order-1:request_restaurant_order", PlannedReadyAt: plannedReadyAt,
	})

	require.NoError(t, err)
	assert.Equal(t, temporal.RestaurantOrderRequested{RestaurantOrderID: "ro-1"}, requested)
}

func TestRequestRestaurantOrderOfScheduledOrderRequiresPlannedReadyAt(t *testing.T) {
	a, _, _, _ := scheduledActivities(t)

	_, err := a.RequestRestaurantOrder(context.Background(), temporal.StepInput{OrderID: orderID, IdempotencyKey: "k"})

	var application *sdktemporal.ApplicationError
	require.True(t, errors.As(err, &application))
	assert.True(t, application.NonRetryable())
}

// FR-682, FR-224: the slot and queue Activities address restaurants by the
// order and the stable step key.
func TestSlotAndQueueActivities(t *testing.T) {
	a, restaurants, _, _ := scheduledActivities(t)
	ctx := context.Background()
	restaurants.EXPECT().ConfirmSlot(mock.Anything, ports.SlotRequest{OrderID: orderID, IdempotencyKey: "order:order-1:confirm_slot"}).
		Return(ports.SlotDecision{}, nil).Once()
	restaurants.EXPECT().ReleaseSlot(mock.Anything, ports.SlotRequest{OrderID: orderID, IdempotencyKey: "order:order-1:release_slot"}).
		Return(nil).Once()
	restaurants.EXPECT().QueueOrder(mock.Anything, ports.QueueRequest{
		OrderID: orderID, RestaurantOrderID: "ro-1", IdempotencyKey: "order:order-1:queue",
	}).Return(nil).Once()

	decision, err := a.ConfirmSlotReservation(ctx, temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:confirm_slot"})
	require.NoError(t, err)
	assert.False(t, decision.Confirmed, "a full window is a decision, not an error")
	require.NoError(t, a.ReleaseSlotReservation(ctx, temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:release_slot"}))
	require.NoError(t, a.QueueRestaurantOrder(ctx, temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:queue", RestaurantOrderID: "ro-1"}))

	err = a.QueueRestaurantOrder(ctx, temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:queue"})
	var application *sdktemporal.ApplicationError
	require.True(t, errors.As(err, &application), "a queue without restaurant_order_id is invalid input")
	assert.Equal(t, temporal.ErrTypeInvalidInput, application.Type())
}

// AC-906: PlanSchedule runs the use case and returns the plan to the workflow.
func TestPlanScheduleActivityReturnsThePlan(t *testing.T) {
	a, _, preparation, delivery := scheduledActivities(t)
	preparation.EXPECT().BaselinePreparation(mock.Anything, order.RestaurantID("restaurant-1")).Return(25*time.Minute, nil).Once()
	delivery.EXPECT().EstimateDeliveryDuration(mock.Anything, ordertest.MarketID(t)).
		Return(planschedule.DeliveryEstimate{CourierLead: 15 * time.Minute, Transit: 20 * time.Minute}, nil).Once()

	plan, err := a.PlanSchedule(context.Background(), temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:plan_schedule"})

	require.NoError(t, err)
	assert.Equal(t, windowPlan(), plan)
}

// The kitchen gets the Pickup with its own moment; a step without it is a bug.
func TestRecordRestaurantPickupActivity(t *testing.T) {
	a, restaurants, _, _ := scheduledActivities(t)
	pickedUpAt := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	restaurants.EXPECT().RecordPickup(mock.Anything, ports.RestaurantPickupRequest{OrderID: orderID, PickedUpAt: pickedUpAt}).
		Return(nil).Once()

	require.NoError(t, a.RecordRestaurantPickup(context.Background(), temporal.StepInput{
		OrderID: orderID, IdempotencyKey: "order:order-1:record_restaurant_pickup", DecidedAt: pickedUpAt,
	}))

	err := a.RecordRestaurantPickup(context.Background(), temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:record_restaurant_pickup"})
	require.Error(t, err)
}
