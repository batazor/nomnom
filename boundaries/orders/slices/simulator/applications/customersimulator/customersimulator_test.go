package customersimulator_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/placeorder"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
	"nomnom/orders/slices/simulator/applications/customersimulator"
)

const (
	customerID = "simulated-customer"
	minDelay   = time.Minute
	maxDelay   = 10 * time.Minute
)

var start = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

type switchStub struct{ enabled bool }

func (s *switchStub) Enabled(context.Context) (bool, error) { return s.enabled, nil }

type catalogStub struct {
	restaurants []order.RestaurantID
	menu        []order.MenuItemID
	calls       int
}

func (c *catalogStub) OpenRestaurants(context.Context) ([]order.RestaurantID, error) {
	c.calls++
	return c.restaurants, nil
}

func (c *catalogStub) AvailableMenuItems(context.Context, order.RestaurantID) ([]order.MenuItemID, error) {
	return c.menu, nil
}

// scriptedRandom returns the queued values of each method in order and fails
// the test on an unexpected draw.
type scriptedRandom struct {
	t      *testing.T
	ints   []int
	delays []int64
	floats []float64
}

func (r *scriptedRandom) IntN(n int) int {
	require.NotEmpty(r.t, r.ints, "unexpected IntN(%d)", n)
	v := r.ints[0]
	r.ints = r.ints[1:]
	require.Less(r.t, v, n)
	return v
}

func (r *scriptedRandom) Int64N(n int64) int64 {
	require.NotEmpty(r.t, r.delays, "unexpected Int64N(%d)", n)
	v := r.delays[0]
	r.delays = r.delays[1:]
	require.Less(r.t, v, n)
	return v
}

func (r *scriptedRandom) Float64() float64 {
	require.NotEmpty(r.t, r.floats, "unexpected Float64")
	v := r.floats[0]
	r.floats = r.floats[1:]
	return v
}

type keys struct{ n int }

func (k *keys) NewKey() string {
	k.n++
	return fmt.Sprintf("key-%d", k.n)
}

type harness struct {
	customer *customersimulator.Customer
	store    *repositorytest.Store
	workflow *fake.Workflow
	catalog  *catalogStub
	random   *scriptedRandom
	switcher *switchStub
}

// newHarness wires the simulated customer to the real order use cases on
// PostgreSQL; the catalog of Place Order serves restaurant-1.
func newHarness(t *testing.T) harness {
	t.Helper()
	store := repositorytest.Open(t)
	placementCatalog := fake.NewCatalog()
	placementCatalog.Put("restaurant-1", placeorder.CatalogSnapshot{
		Currency: "EUR",
		Items: map[order.MenuItemID]placeorder.CatalogItem{
			"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
			"soup":  {UnitPrice: ordertest.Money(t, "6.00"), Available: true},
			"tea":   {UnitPrice: ordertest.Money(t, "2.00"), Available: true},
		},
		MinimumTotal: ordertest.Money(t, "0"),
		MaximumTotal: ordertest.Money(t, "1000"),
	})
	policy, err := cancellationpolicy.NewFixed(time.Hour)
	require.NoError(t, err)
	workflow := fake.NewWorkflow()
	module := orderdi.NewModule(orderdi.Dependencies{
		Commands:   store,
		ReadModels: store,
		Authorizer: fake.NewAuthorizer(),
		Policy:     policy,
		Catalog:    placementCatalog,
		Workflow:   workflow,
		Clock:      fake.NewClock(start),
		IDs:        &fake.IDs{},
		Telemetry:  observability.UseCases(),
	})
	h := harness{
		store:    store,
		workflow: workflow,
		catalog:  &catalogStub{restaurants: []order.RestaurantID{"restaurant-1"}, menu: []order.MenuItemID{"pizza", "soup", "tea"}},
		random:   &scriptedRandom{t: t},
		switcher: &switchStub{enabled: true},
	}
	h.customer = customersimulator.New(customersimulator.Dependencies{
		Switch:              h.switcher,
		Catalog:             h.catalog,
		PlaceOrder:          module.PlaceOrder,
		GetOrder:            module.GetOrder,
		RequestCancellation: module.RequestOrderCancellation,
		Random:              h.random,
		Keys:                &keys{},
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
	h.random.delays = []int64{int64(30 * time.Second)}
	h.customer.Tick(ctx, start)
	h.customer.Tick(ctx, start.Add(89*time.Second))
	assert.Zero(t, h.catalog.calls, "nothing is due before the delay")

	// Due: next delay, restaurant 0, 2 items (tea ×2, soup ×1), no cancellation.
	h.random.delays = []int64{0}
	h.random.ints = []int{0, 1, 2, 1, 0, 0}
	h.random.floats = []float64{0.5}
	h.customer.Tick(ctx, start.Add(90*time.Second))

	starts := h.workflow.Starts()
	require.Len(t, starts, 1)
	placed, ok := h.store.Snapshot(starts[0].OrderID)
	require.True(t, ok)
	assert.Equal(t, order.CustomerID(customerID), placed.CustomerID)
	assert.Equal(t, order.RestaurantID("restaurant-1"), placed.RestaurantID)
	quantities := map[order.MenuItemID]int{}
	for _, item := range placed.Items {
		quantities[item.MenuItemID()] = item.Quantity().Value()
	}
	assert.Equal(t, map[order.MenuItemID]int{"tea": 2, "soup": 1}, quantities)
	assert.Empty(t, h.workflow.Cancellations())
}

func TestCustomerCancelsPlacedOrderAfterDelay(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.random.delays = []int64{0}
	h.customer.Tick(ctx, start)

	// Placement with the 10% cancellation branch, cancellation due in MinDelay + 1m.
	h.random.delays = []int64{int64(5 * time.Minute), int64(time.Minute)}
	h.random.ints = []int{0, 0, 0, 0}
	h.random.floats = []float64{0.05}
	h.customer.Tick(ctx, start.Add(minDelay))
	require.Len(t, h.workflow.Starts(), 1)

	h.customer.Tick(ctx, start.Add(minDelay+time.Minute))
	assert.Empty(t, h.workflow.Cancellations(), "cancellation is not due yet")

	h.customer.Tick(ctx, start.Add(minDelay+2*time.Minute))
	cancellations := h.workflow.Cancellations()
	require.Len(t, cancellations, 1)
	assert.Equal(t, h.workflow.Starts()[0].OrderID, cancellations[0].OrderID)

	h.customer.Tick(ctx, start.Add(minDelay+2*time.Minute+time.Second))
	assert.Len(t, h.workflow.Cancellations(), 1, "each cancellation is attempted once")
}

// AC-764: a disabled Simulator does nothing and enabling it starts a fresh delay.
func TestDisabledCustomerDoesNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.random.delays = []int64{0}
	h.customer.Tick(ctx, start)

	h.switcher.enabled = false
	h.customer.Tick(ctx, start.Add(time.Hour))
	assert.Zero(t, h.catalog.calls)

	h.switcher.enabled = true
	h.random.delays = []int64{0}
	h.customer.Tick(ctx, start.Add(2*time.Hour))
	assert.Zero(t, h.catalog.calls, "the schedule restarts after re-enabling")
	assert.Empty(t, h.workflow.Starts())
}

// A failed placement is logged and the next one is scheduled.
func TestCustomerContinuesAfterFailedPlacement(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.catalog.restaurants = nil
	h.random.delays = []int64{0}
	h.customer.Tick(ctx, start)
	h.random.delays = []int64{0}
	h.customer.Tick(ctx, start.Add(minDelay))
	assert.Equal(t, 1, h.catalog.calls)

	h.catalog.restaurants = []order.RestaurantID{"unknown-restaurant"}
	h.random.delays = []int64{0}
	h.random.ints = []int{0, 0, 0, 0}
	h.customer.Tick(ctx, start.Add(2*minDelay))
	assert.Equal(t, 2, h.catalog.calls)
	assert.Empty(t, h.workflow.Starts(), "Place Order refused the unknown restaurant")
}
