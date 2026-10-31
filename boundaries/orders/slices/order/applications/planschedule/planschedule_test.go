package planschedule_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
