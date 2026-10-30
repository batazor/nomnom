package planschedule_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/planschedule"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var windowStart = time.Date(2026, time.October, 25, 19, 0, 0, 0, time.UTC)

var policy = order.SchedulePolicy{PreparationBuffer: 5 * time.Minute, ReplanLead: 30 * time.Minute}

func newHandler(t *testing.T, options ...ordertest.Option) (planschedule.Handler, *MockPreparation, *MockDeliveryDuration) {
	t.Helper()
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", options...)))
	preparation := NewMockPreparation(t)
	delivery := NewMockDeliveryDuration(t)
	return planschedule.NewHandler(store, preparation, delivery, policy), preparation, delivery
}

func scheduled(t *testing.T) ordertest.Option {
	return ordertest.WithDeliveryWindow(t, windowStart, windowStart.Add(15*time.Minute))
}

// AC-906: the plan of window 19:00 reads the baseline of the restaurant and
// the estimate of the Market of the Order.
func TestPlanScheduleReadsTheEstimatesOfTheOrder(t *testing.T) {
	handler, preparation, delivery := newHandler(t, scheduled(t))
	preparation.EXPECT().BaselinePreparation(mock.Anything, order.RestaurantID("restaurant-1")).Return(25*time.Minute, nil).Once()
	delivery.EXPECT().EstimateDeliveryDuration(mock.Anything, ordertest.MarketID(t)).
		Return(planschedule.DeliveryEstimate{CourierLead: 15 * time.Minute, Transit: 20 * time.Minute}, nil).Once()

	plan, err := handler.Handle(context.Background(), planschedule.Input{OrderID: "order-1"})

	require.NoError(t, err)
	assert.Equal(t, order.SchedulePlan{
		PlannedReadyAt:   windowStart.Add(-20 * time.Minute),
		StartAt:          windowStart.Add(-50 * time.Minute),
		CourierRequestAt: windowStart.Add(-35 * time.Minute),
		ReplanAt:         windowStart.Add(-80 * time.Minute),
	}, plan)
}

// AC-909: at the checkpoint the baseline grew to 35 minutes: start_at moves
// 10 minutes earlier, planned_ready_at stays.
func TestPlanScheduleReplanKeepsPlannedReadyAt(t *testing.T) {
	handler, preparation, delivery := newHandler(t, scheduled(t))
	preparation.EXPECT().BaselinePreparation(mock.Anything, order.RestaurantID("restaurant-1")).Return(35*time.Minute, nil).Once()
	delivery.EXPECT().EstimateDeliveryDuration(mock.Anything, ordertest.MarketID(t)).
		Return(planschedule.DeliveryEstimate{CourierLead: 15 * time.Minute, Transit: 30 * time.Minute}, nil).Once()

	plan, err := handler.Handle(context.Background(), planschedule.Input{OrderID: "order-1", PlannedReadyAt: windowStart.Add(-20 * time.Minute)})

	require.NoError(t, err)
	assert.Equal(t, windowStart.Add(-20*time.Minute), plan.PlannedReadyAt)
	assert.Equal(t, windowStart.Add(-60*time.Minute), plan.StartAt)
}

func TestPlanScheduleRefusesAnASAPOrder(t *testing.T) {
	handler, _, _ := newHandler(t)

	_, err := handler.Handle(context.Background(), planschedule.Input{OrderID: "order-1"})

	require.ErrorIs(t, err, order.ErrNotScheduled)
}

func TestPlanScheduleReturnsEstimateFailures(t *testing.T) {
	handler, preparation, _ := newHandler(t, scheduled(t))
	unavailable := errors.New("restaurants unavailable")
	preparation.EXPECT().BaselinePreparation(mock.Anything, order.RestaurantID("restaurant-1")).Return(0, unavailable).Once()

	_, err := handler.Handle(context.Background(), planschedule.Input{OrderID: "order-1"})

	require.ErrorIs(t, err, unavailable)
}

func TestPlanScheduleRequiresOrderID(t *testing.T) {
	handler, _, _ := newHandler(t)

	_, err := handler.Handle(context.Background(), planschedule.Input{})

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}
