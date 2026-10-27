package customersimulator_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placeorder"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/mocks"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
	"nomnom/orders/slices/simulator/applications/customersimulator"
)

const (
	customerID = "simulated-customer"
	minDelay   = time.Minute
	maxDelay   = 10 * time.Minute
	// delayRange is the bound of Random.Int64N for a delay in [minDelay, maxDelay].
	delayRange = int64(maxDelay-minDelay) + 1
)

var start = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

type harness struct {
	customer         *customersimulator.Customer
	store            *repositorytest.Store
	placementCatalog *mocks.MockCatalog
	workflow         *mocks.MockCancellationRequester
	catalog          *MockCatalog
	random           *MockRandom
	keys             *MockKeys
	switcher         *MockSwitch
}

// newHarness wires the simulated customer to the real order use cases on
// PostgreSQL. The catalog of Place Order and the workflow expect no call until
// a test sets its expectations, as do the switch, the catalog, the random
// source and the keys of the simulated customer; the simulated customer touches
// only its own Orders, so no permission is checked.
func newHarness(t *testing.T) harness {
	t.Helper()
	store := repositorytest.Open(t)
	placementCatalog := mocks.NewMockCatalog(t)
	policy, err := cancellationpolicy.NewFixed(time.Hour)
	require.NoError(t, err)
	workflow := mocks.NewMockCancellationRequester(t)
	module := orderdi.NewModule(orderdi.Dependencies{
		Commands:   store,
		ReadModels: store,
		Authorizer: mocks.NewMockAuthorizer(t),
		Policy:     policy,
		Catalog:    placementCatalog,
		Workflow:   workflow,
		Clock:      fake.NewClock(start),
		IDs:        &fake.IDs{},
		Telemetry:  observability.UseCases(),
	})
	h := harness{
		store:            store,
		placementCatalog: placementCatalog,
		workflow:         workflow,
		catalog:          NewMockCatalog(t),
		random:           NewMockRandom(t),
		keys:             NewMockKeys(t),
		switcher:         NewMockSwitch(t),
	}
	h.customer = customersimulator.New(customersimulator.Dependencies{
		Switch:              h.switcher,
		Catalog:             h.catalog,
		PlaceOrder:          module.PlaceOrder,
		GetOrder:            module.GetOrder,
		RequestCancellation: module.RequestOrderCancellation,
		Random:              h.random,
		Keys:                h.keys,
		CustomerID:          customerID,
		MinDelay:            minDelay,
		MaxDelay:            maxDelay,
	})
	return h
}

func TestCustomerPlacesOrderAfterRandomDelay(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// First tick only schedules: delay = MinDelay + 30s.
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Times(2)
	h.random.EXPECT().Int64N(delayRange).Return(int64(30 * time.Second)).Once()
	h.customer.Tick(ctx, start)
	h.customer.Tick(ctx, start.Add(89*time.Second))
	h.catalog.AssertNotCalled(t, "OpenRestaurants", mock.Anything)

	// Due: next delay, restaurant 0, 2 items (tea ×2, soup ×1), no cancellation:
	// the workflow expects no call.
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.random.EXPECT().Int64N(delayRange).Return(int64(0)).Once()
	h.catalog.EXPECT().OpenRestaurants(mock.Anything).Return([]order.RestaurantID{"restaurant-1"}, nil).Once()
	h.random.EXPECT().IntN(1).Return(0).Once()
	h.catalog.EXPECT().AvailableMenuItems(mock.Anything, order.RestaurantID("restaurant-1")).
		Return([]order.MenuItemID{"pizza", "soup", "tea"}, nil).Once()
	h.random.EXPECT().IntN(3).Return(1).Once()
	h.random.EXPECT().IntN(3).Return(2).Once()
	h.random.EXPECT().IntN(2).Return(1).Once()
	h.random.EXPECT().IntN(2).Return(0).Once()
	h.random.EXPECT().IntN(2).Return(0).Once()
	h.keys.EXPECT().NewKey().Return("key-1").Once()
	h.placementCatalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"tea", "soup"}).
		Return(placeorder.CatalogSnapshot{
			MarketID: ordertest.MarketID(t),
			Currency: "EUR",
			Items: map[order.MenuItemID]placeorder.CatalogItem{
				"soup": {UnitPrice: ordertest.Money(t, "6.00"), Available: true},
				"tea":  {UnitPrice: ordertest.Money(t, "2.00"), Available: true},
			},
			MinimumTotal: ordertest.Money(t, "0"),
			MaximumTotal: ordertest.Money(t, "1000"),
		}, nil).Once()
	h.random.EXPECT().Float64().Return(0.5).Once()
	h.customer.Tick(ctx, start.Add(90*time.Second))

	placedIDs := h.placedOrders()
	require.Len(t, placedIDs, 1)
	placed, ok := h.store.Snapshot(placedIDs[0])
	require.True(t, ok)
	assert.Equal(t, order.CustomerID(customerID), placed.CustomerID)
	assert.Equal(t, order.RestaurantID("restaurant-1"), placed.RestaurantID)
	quantities := map[order.MenuItemID]int{}
	for _, item := range placed.Items {
		quantities[item.MenuItemID()] = item.Quantity().Value()
	}
	assert.Equal(t, map[order.MenuItemID]int{"tea": 2, "soup": 1}, quantities)
}

func TestCustomerCancelsPlacedOrderAfterDelay(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.random.EXPECT().Int64N(delayRange).Return(int64(0)).Once()
	h.customer.Tick(ctx, start)

	// Placement of pizza ×1 with the 10% cancellation branch, cancellation due
	// in MinDelay + 1m.
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.random.EXPECT().Int64N(delayRange).Return(int64(5 * time.Minute)).Once()
	h.catalog.EXPECT().OpenRestaurants(mock.Anything).Return([]order.RestaurantID{"restaurant-1"}, nil).Once()
	h.random.EXPECT().IntN(1).Return(0).Once()
	h.catalog.EXPECT().AvailableMenuItems(mock.Anything, order.RestaurantID("restaurant-1")).
		Return([]order.MenuItemID{"pizza", "soup", "tea"}, nil).Once()
	h.random.EXPECT().IntN(3).Return(0).Times(2)
	h.random.EXPECT().IntN(2).Return(0).Once()
	h.keys.EXPECT().NewKey().Return("key-1").Once()
	h.placementCatalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).
		Return(placeorder.CatalogSnapshot{
			MarketID:     ordertest.MarketID(t),
			Currency:     "EUR",
			Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}},
			MinimumTotal: ordertest.Money(t, "0"),
			MaximumTotal: ordertest.Money(t, "1000"),
		}, nil).Once()
	h.random.EXPECT().Float64().Return(0.05).Once()
	h.random.EXPECT().Int64N(delayRange).Return(int64(time.Minute)).Once()
	h.customer.Tick(ctx, start.Add(minDelay))
	require.Len(t, h.placedOrders(), 1)

	// The cancellation is not due yet: the workflow expects no call.
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.customer.Tick(ctx, start.Add(minDelay+time.Minute))

	cancelled := make(chan struct{}, 1)
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.keys.EXPECT().NewKey().Return("key-2").Once()
	h.workflow.EXPECT().RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
		OrderID:     h.placedOrders()[0],
		RequestID:   "key-2",
		RequestedBy: application.Subject{Kind: application.SubjectUser, ID: customerID},
		RequestedAt: start,
		Reason:      "simulated customer changed their mind",
	}).RunAndReturn(func(context.Context, fulfillment.CancellationRequest) error {
		cancelled <- struct{}{}
		return nil
	}).Once()
	h.customer.Tick(ctx, start.Add(minDelay+2*time.Minute))
	require.Len(t, cancelled, 1, "the due cancellation is requested")

	// Each cancellation is attempted once: Once() fails a second request.
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.customer.Tick(ctx, start.Add(minDelay+2*time.Minute+time.Second))
}

// AC-764: a disabled Simulator does nothing and enabling it starts a fresh delay.
func TestDisabledCustomerDoesNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.random.EXPECT().Int64N(delayRange).Return(int64(0)).Once()
	h.customer.Tick(ctx, start)

	h.switcher.EXPECT().Enabled(mock.Anything).Return(false, nil).Once()
	h.customer.Tick(ctx, start.Add(time.Hour))
	h.catalog.AssertNotCalled(t, "OpenRestaurants", mock.Anything)

	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.random.EXPECT().Int64N(delayRange).Return(int64(0)).Once()
	h.customer.Tick(ctx, start.Add(2*time.Hour))
	h.catalog.AssertNotCalled(t, "OpenRestaurants", mock.Anything)
	assert.Empty(t, h.placedOrders())
}

// A failed placement is logged and the next one is scheduled.
func TestCustomerContinuesAfterFailedPlacement(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Times(2)
	h.random.EXPECT().Int64N(delayRange).Return(int64(0)).Times(2)
	h.catalog.EXPECT().OpenRestaurants(mock.Anything).Return(nil, nil).Once()
	h.customer.Tick(ctx, start)
	h.customer.Tick(ctx, start.Add(minDelay))
	h.catalog.AssertNumberOfCalls(t, "OpenRestaurants", 1)

	h.switcher.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	h.random.EXPECT().Int64N(delayRange).Return(int64(0)).Once()
	h.catalog.EXPECT().OpenRestaurants(mock.Anything).Return([]order.RestaurantID{"unknown-restaurant"}, nil).Once()
	h.random.EXPECT().IntN(1).Return(0).Once()
	h.catalog.EXPECT().AvailableMenuItems(mock.Anything, order.RestaurantID("unknown-restaurant")).
		Return([]order.MenuItemID{"pizza", "soup", "tea"}, nil).Once()
	h.random.EXPECT().IntN(3).Return(0).Times(2)
	h.random.EXPECT().IntN(2).Return(0).Once()
	h.keys.EXPECT().NewKey().Return("key-1").Once()
	h.placementCatalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("unknown-restaurant"), []order.MenuItemID{"pizza"}).
		Return(placeorder.CatalogSnapshot{}, fmt.Errorf("%w: %q", placeorder.ErrUnknownRestaurant, "unknown-restaurant")).Once()
	h.customer.Tick(ctx, start.Add(2*minDelay))
	h.catalog.AssertNumberOfCalls(t, "OpenRestaurants", 2)
	assert.Empty(t, h.placedOrders(), "Place Order refused the unknown restaurant")
}

// placedOrders are the Orders whose OrderPlaced is in the Outbox: the workflow
// is started from it by the consumer, not by Place Order.
func (h *harness) placedOrders() []order.ID {
	var ids []order.ID
	for _, record := range h.store.OutboxRecords() {
		if record.EventType == "nomnom.orders.v1.OrderPlaced" {
			ids = append(ids, record.AggregateID)
		}
	}
	return ids
}
