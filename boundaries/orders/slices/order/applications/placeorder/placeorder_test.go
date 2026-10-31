package placeorder_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var now = time.Date(2026, time.October, 25, 12, 30, 0, 0, time.UTC)

var customer = authz.Subject{Kind: authz.SubjectUser, ID: "customer-1"}

type env struct {
	store   *repositorytest.Store
	catalog *MockCatalog
	slots   *MockSlotReservations
	handler placeorder.Handler
}

func newEnv(t *testing.T) env {
	t.Helper()
	e := env{
		store:   repositorytest.Open(t),
		catalog: NewMockCatalog(t),
		slots:   NewMockSlotReservations(t),
	}
	e.handler = placeorder.NewHandler(placeorder.Dependencies{
		UnitOfWork:      e.store,
		Orders:          e.store,
		Outbox:          e.store,
		IdempotencyKeys: e.store,
		Catalog:         e.catalog,
		Slots:           e.slots,
		Relationships:   e.store,
		IDs:             &fake.IDs{},
		Clock:           fake.NewClock(now),
	})
	return e
}

func validInput() placeorder.Input {
	return placeorder.Input{
		Subject:        customer,
		IdempotencyKey: "key-1",
		RestaurantID:   "restaurant-1",
		Items:          []placeorder.ItemInput{{MenuItemID: "pizza", Quantity: 2}, {MenuItemID: "soup", Quantity: 1}},
	}
}

// ADR 41: the Market comes only from the Restaurant of the catalog snapshot; a
// snapshot without one places nothing and is not invalid input of the Customer.
func TestPlaceOrderRequiresMarketOfRestaurant(t *testing.T) {
	e := newEnv(t)
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-2"), []order.MenuItemID{"pizza"}).
		Return(placeorder.CatalogSnapshot{
			Currency:     "EUR",
			Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}},
			MinimumTotal: ordertest.Money(t, "10"),
			MaximumTotal: ordertest.Money(t, "100"),
		}, nil).
		Once()
	input := validInput()
	input.RestaurantID = "restaurant-2"
	input.Items = []placeorder.ItemInput{{MenuItemID: "pizza", Quantity: 1}}

	_, err := e.handler.Handle(context.Background(), input)

	require.ErrorContains(t, err, "no market_id")
	assert.NotErrorIs(t, err, usecase.ErrInvalidInput)
	assert.NotErrorIs(t, err, vo.ErrInvalidValue)
	assert.Empty(t, e.store.OutboxRecords())
	assert.Empty(t, e.store.RelationshipChanges())
}

// Technical input is rejected before the catalog is read; the malformed items of
// AC-102 are scenarios of placeorder.feature.
func TestPlaceOrderValidatesInputBeforeCatalog(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*placeorder.Input)
		wantErr error
	}{
		{name: "no idempotency key", mutate: func(in *placeorder.Input) { in.IdempotencyKey = "" }, wantErr: usecase.ErrInvalidInput},
		{name: "no restaurant", mutate: func(in *placeorder.Input) { in.RestaurantID = "" }, wantErr: usecase.ErrInvalidInput},
		{
			name: "service subject",
			mutate: func(in *placeorder.Input) {
				in.Subject = authz.Subject{Kind: authz.SubjectService, ID: "bff"}
			},
			wantErr: authz.ErrPermissionDenied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := validInput()
			tt.mutate(&in)

			_, err := e.handler.Handle(context.Background(), in)

			require.ErrorIs(t, err, tt.wantErr)
			// The catalog mock has no expectation: any read fails the test.
			assertNothingPlaced(t, e)
		})
	}
}

// AC-103: another idempotency key places another Order; the repeat with the same
// key is a scenario of placeorder.feature.
func TestPlaceOrderWithAnotherKeyPlacesAnotherOrder(t *testing.T) {
	e := newEnv(t)
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
		Return(openSnapshot(t), nil).
		Times(2)
	_, err := e.handler.Handle(context.Background(), validInput())
	require.NoError(t, err)

	other := validInput()
	other.IdempotencyKey = "key-2"
	result, err := e.handler.Handle(context.Background(), other)

	require.NoError(t, err)
	assert.Equal(t, order.ID("order-2"), result.OrderID)
}

func TestPlaceOrderPropagatesCatalogFailure(t *testing.T) {
	e := newEnv(t)
	in := validInput()
	in.RestaurantID = "restaurant-unknown"
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-unknown"), []order.MenuItemID{"pizza", "soup"}).
		Return(placeorder.CatalogSnapshot{}, fmt.Errorf("%w: %q", placeorder.ErrUnknownRestaurant, "restaurant-unknown")).
		Once()

	_, err := e.handler.Handle(context.Background(), in)

	require.ErrorIs(t, err, placeorder.ErrUnknownRestaurant)
	assertNothingPlaced(t, e)
}

func assertNothingPlaced(t *testing.T, e env) {
	t.Helper()
	_, ok := e.store.Snapshot("order-1")
	assert.False(t, ok)
	assert.Empty(t, e.store.OutboxRecords())
}

func (e env) handlerWithKeys(keys placeorder.IdempotencyKeys) placeorder.Handler {
	return placeorder.NewHandler(placeorder.Dependencies{
		UnitOfWork:      e.store,
		Orders:          e.store,
		Outbox:          e.store,
		IdempotencyKeys: keys,
		Catalog:         e.catalog,
		Slots:           e.slots,
		Relationships:   e.store,
		IDs:             &fake.IDs{},
		Clock:           fake.NewClock(now),
	})
}

// AC-671: the concurrent request with the same key commits while this one
// waits on the key in RememberOrder (both lookups missed it): the request
// replays the committed Order instead of failing.
func TestPlaceOrderReplaysOrderCommittedDuringKeyConflict(t *testing.T) {
	e := newEnv(t)
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
		Return(placeorder.CatalogSnapshot{
			MarketID: ordertest.MarketID(t),
			Currency: "EUR",
			Items: map[order.MenuItemID]placeorder.CatalogItem{
				"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
				"soup":  {UnitPrice: ordertest.Money(t, "4.00"), Available: true},
			},
			MinimumTotal: ordertest.Money(t, "10"),
			MaximumTotal: ordertest.Money(t, "100"),
		}, nil).
		Once()
	require.NoError(t, e.store.Add(context.Background(), ordertest.New(t, "order-0")))
	require.NoError(t, e.store.RememberOrder(context.Background(), "customer-1", "key-1", "order-0"))

	// Both lookups miss the key committed by the concurrent request; RememberOrder
	// waits on it, and the replay reads it.
	keys := NewMockIdempotencyKeys(t)
	keys.EXPECT().FindOrder(mock.Anything, order.CustomerID("customer-1"), "key-1").Return("", false, nil).Twice()
	keys.EXPECT().
		RememberOrder(mock.Anything, order.CustomerID("customer-1"), "key-1", order.ID("order-1")).
		RunAndReturn(e.store.RememberOrder).
		Once()
	keys.EXPECT().FindOrder(mock.Anything, order.CustomerID("customer-1"), "key-1").RunAndReturn(e.store.FindOrder).Once()

	result, err := e.handlerWithKeys(keys).Handle(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, placeorder.Result{OrderID: "order-0", Replayed: true}, result)
	assertNothingPlaced(t, env{store: e.store})
}

// AC-671: if the conflicting Order cannot be read, the request fails with a
// retryable concurrency conflict, not an internal error.
func TestPlaceOrderKeyConflictWithoutOrderIsConcurrencyConflict(t *testing.T) {
	e := newEnv(t)
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
		Return(placeorder.CatalogSnapshot{
			MarketID: ordertest.MarketID(t),
			Currency: "EUR",
			Items: map[order.MenuItemID]placeorder.CatalogItem{
				"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
				"soup":  {UnitPrice: ordertest.Money(t, "4.00"), Available: true},
			},
			MinimumTotal: ordertest.Money(t, "10"),
			MaximumTotal: ordertest.Money(t, "100"),
		}, nil).
		Once()

	// The key is taken without a committed Order behind it: every lookup misses.
	keys := NewMockIdempotencyKeys(t)
	keys.EXPECT().FindOrder(mock.Anything, order.CustomerID("customer-1"), "key-1").Return("", false, nil).Times(3)
	keys.EXPECT().
		RememberOrder(mock.Anything, order.CustomerID("customer-1"), "key-1", order.ID("order-1")).
		Return(placeorder.ErrIdempotencyKeyTaken).
		Once()

	_, err := e.handlerWithKeys(keys).Handle(context.Background(), validInput())

	require.ErrorIs(t, err, usecase.ErrConcurrencyConflict)
}

func TestPlaceOrderReturnsConcurrentlyPlacedOrder(t *testing.T) {
	e := newEnv(t)
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
		Return(placeorder.CatalogSnapshot{
			MarketID: ordertest.MarketID(t),
			Currency: "EUR",
			Items: map[order.MenuItemID]placeorder.CatalogItem{
				"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
				"soup":  {UnitPrice: ordertest.Money(t, "4.00"), Available: true},
			},
			MinimumTotal: ordertest.Money(t, "10"),
			MaximumTotal: ordertest.Money(t, "100"),
		}, nil).
		Once()
	require.NoError(t, e.store.Add(context.Background(), ordertest.New(t, "order-0")))
	require.NoError(t, e.store.RememberOrder(context.Background(), "customer-1", "key-1", "order-0"))
	// The first lookup misses the key committed by the concurrent request; the
	// lookup in the transaction finds it.
	keys := NewMockIdempotencyKeys(t)
	keys.EXPECT().FindOrder(mock.Anything, order.CustomerID("customer-1"), "key-1").Return("", false, nil).Once()
	keys.EXPECT().FindOrder(mock.Anything, order.CustomerID("customer-1"), "key-1").RunAndReturn(e.store.FindOrder).Once()
	handler := e.handlerWithKeys(keys)

	result, err := handler.Handle(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, placeorder.Result{OrderID: "order-0", Replayed: true}, result)
	assertNothingPlaced(t, env{store: e.store})
}

// premiumSnapshot prices pizza at 500 RUB; fee is the premium fee of the
// Restaurant, nil when it offers no premium.
func premiumSnapshot(t *testing.T, fee *vo.Money) placeorder.CatalogSnapshot {
	t.Helper()
	price, err := vo.ParseMoney("500.00", "RUB")
	require.NoError(t, err)
	minimum, err := vo.ParseMoney("10", "RUB")
	require.NoError(t, err)
	maximum, err := vo.ParseMoney("10000", "RUB")
	require.NoError(t, err)
	return placeorder.CatalogSnapshot{
		MarketID:     ordertest.MarketID(t),
		Currency:     "RUB",
		Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {Name: "Pizza", UnitPrice: price, Available: true}},
		MinimumTotal: minimum,
		MaximumTotal: maximum,
		PremiumFee:   fee,
	}
}

func premiumInput() placeorder.Input {
	return placeorder.Input{
		Subject:        customer,
		IdempotencyKey: "key-premium",
		RestaurantID:   "restaurant-1",
		Items:          []placeorder.ItemInput{{MenuItemID: "pizza", Quantity: 1}},
		Priority:       string(order.PriorityPremium),
	}
}

// FR-650: an unknown priority is refused before the catalog.
func TestPlaceOrderRefusesUnknownPriority(t *testing.T) {
	e := newEnv(t)
	input := validInput()
	input.Priority = "EXPRESS"

	_, err := e.handler.Handle(context.Background(), input)

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
	assertNothingPlaced(t, e)
}

// AC-870: an Order placed without priority is STANDARD without a fee.
func TestPlaceOrderWithoutPriorityIsStandard(t *testing.T) {
	e := newEnv(t)
	fee, err := vo.ParseMoney("99.00", "RUB")
	require.NoError(t, err)
	e.catalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).
		Return(premiumSnapshot(t, &fee), nil).Once()
	input := premiumInput()
	input.Priority = ""

	_, err = e.handler.Handle(context.Background(), input)

	require.NoError(t, err)
	placed, ok := e.store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, order.PriorityStandard, placed.Service.Priority())
	_, premium := placed.Service.PremiumFee()
	assert.False(t, premium, "a standard Order pays no fee even where premium is offered")
}
