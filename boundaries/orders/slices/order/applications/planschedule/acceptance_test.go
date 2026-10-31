package planschedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/planschedule"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

// TestAcceptance runs acceptance/planschedule.feature (ADR 19). A scenario is a subtest;
// its mocks report to it through godog.T. The store and the cleanup of the mocks
// belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	clock := func(ctx context.Context, value string) time.Time {
		parsed, err := time.Parse("15:04", value)
		require.NoError(godog.T(ctx), err)
		return time.Date(windowStart.Year(), windowStart.Month(), windowStart.Day(), parsed.Hour(), parsed.Minute(), 0, 0, time.UTC)
	}
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler     planschedule.Handler
				preparation *MockPreparation
				delivery    *MockDeliveryDuration
				plan        order.SchedulePlan
				failure     error
			)

			sc.Given(`^a scheduled order with a delivery window at 19:00$`, func(ctx context.Context) {
				handler, preparation, delivery = newHandler(t, scheduled(t))
				preparation.Test(godog.T(ctx))
				delivery.Test(godog.T(ctx))
			})
			sc.Given(`^an order delivered as soon as possible$`, func(ctx context.Context) {
				handler, preparation, delivery = newHandler(t)
				preparation.Test(godog.T(ctx))
				delivery.Test(godog.T(ctx))
			})
			sc.Given(`^the restaurant prepares in (\d+) minutes$`, func(minutes int) {
				preparation.EXPECT().
					BaselinePreparation(mock.Anything, order.RestaurantID("restaurant-1")).
					Return(time.Duration(minutes)*time.Minute, nil).
					Once()
			})
			sc.Given(`^the market delivers with a (\d+) minute courier lead and a (\d+) minute transit$`, func(lead, transit int) {
				delivery.EXPECT().
					EstimateDeliveryDuration(mock.Anything, ordertest.MarketID(t)).
					Return(planschedule.DeliveryEstimate{CourierLead: time.Duration(lead) * time.Minute, Transit: time.Duration(transit) * time.Minute}, nil).
					Once()
			})

			sc.When(`^order fulfillment plans the schedule$`, func(ctx context.Context) {
				var err error
				plan, err = handler.Handle(ctx, planschedule.Input{OrderID: "order-1"})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment replans the schedule with the ready time (\d\d:\d\d)$`, func(ctx context.Context, readyAt string) {
				var err error
				plan, err = handler.Handle(ctx, planschedule.Input{OrderID: "order-1", PlannedReadyAt: clock(ctx, readyAt)})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment tries to plan the schedule$`, func(ctx context.Context) {
				_, failure = handler.Handle(ctx, planschedule.Input{OrderID: "order-1"})
			})

			sc.Then(`^the order is planned ready at (\d\d:\d\d)$`, func(ctx context.Context, at string) {
				assert.Equal(godog.T(ctx), clock(ctx, at), plan.PlannedReadyAt)
			})
			sc.Then(`^preparation is planned to start at (\d\d:\d\d)$`, func(ctx context.Context, at string) {
				assert.Equal(godog.T(ctx), clock(ctx, at), plan.StartAt)
			})
			sc.Then(`^the courier is planned to be requested at (\d\d:\d\d)$`, func(ctx context.Context, at string) {
				assert.Equal(godog.T(ctx), clock(ctx, at), plan.CourierRequestAt)
			})
			sc.Then(`^the plan is checked again at (\d\d:\d\d)$`, func(ctx context.Context, at string) {
				assert.Equal(godog.T(ctx), clock(ctx, at), plan.ReplanAt)
			})
			sc.Then(`^the plan is refused because the order is not scheduled$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, order.ErrNotScheduled)
			})
		},
		Options: &godog.Options{
			Format:   "progress",
			Paths:    []string{"acceptance"},
			Strict:   true,
			TestingT: t,
		},
	}

	require.Zero(t, suite.Run(), "acceptance scenarios failed")
}
