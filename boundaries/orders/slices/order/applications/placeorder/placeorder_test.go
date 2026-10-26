package placeorder_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var now = time.Date(2026, time.October, 25, 12, 30, 0, 0, time.UTC)

var customer = application.Subject{Kind: application.SubjectUser, ID: "customer-1"}

type env struct {
	store      *repositorytest.Store
	catalog    *fake.Catalog
	authorizer *fake.Authorizer
	workflow   *fake.Workflow
	handler    placeorder.Handler
}

func newEnv(t *testing.T) env {
	t.Helper()
	e := env{
		store:      repositorytest.Open(t),
		catalog:    fake.NewCatalog(),
		authorizer: fake.NewAuthorizer(),
		workflow:   fake.NewWorkflow(),
	}
	e.catalog.Put("restaurant-1", placeorder.CatalogSnapshot{
		MarketID: ordertest.MarketID(t),
		Currency: "EUR",
		Items: map[order.MenuItemID]placeorder.CatalogItem{
			"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
			"soup":  {UnitPrice: ordertest.Money(t, "4.00"), Available: true},
			"cake":  {UnitPrice: ordertest.Money(t, "6.00"), Available: false},
		},
		MinimumTotal: ordertest.Money(t, "10"),
		MaximumTotal: ordertest.Money(t, "100"),
	})
	e.handler = placeorder.NewHandler(placeorder.Dependencies{
		UnitOfWork:      e.store,
		Orders:          e.store,
		Outbox:          e.store,
		IdempotencyKeys: e.store,
		Catalog:         e.catalog,
		Relationships:   e.authorizer,
		Revisions:       e.store,
		IDs:             &fake.IDs{},
		Clock:           fake.NewClock(now),
		Workflow:        e.workflow,
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

// AC-100: the Order is placed from snapshot prices with OrderPlaced in the same transaction.
func TestPlaceOrderFromCatalogSnapshot(t *testing.T) {
	e := newEnv(t)

	result, err := e.handler.Handle(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, placeorder.Result{OrderID: "order-1", FulfillmentStarted: true}, result)
	placed, ok := e.store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, order.StatePlaced, placed.State)
	assert.Equal(t, order.CustomerID("customer-1"), placed.CustomerID)
	assert.Equal(t, now, placed.PlacedAt)
	assert.Equal(t, ordertest.MarketID(t), placed.MarketID, "the Order takes the Market of the Restaurant")

	records := e.store.OutboxRecords()
	require.Len(t, records, 1)
	assert.Equal(t, repositorytest.ExpectedOutbox(t, "order-1", 1, order.OrderPlaced{
		OrderID:      "order-1",
		CustomerID:   "customer-1",
		RestaurantID: "restaurant-1",
		MarketID:     ordertest.MarketID(t),
		Total:        ordertest.Money(t, "29.00"),
		PlacedAt:     now,
		Items:        placed.Items,
	})[0], records[0])
	assert.Equal(t, []fulfillment.StartRequest{{OrderID: "order-1"}}, e.workflow.Starts())

	// The creation relationship lets the Customer read the new Order.
	require.NoError(t, e.authorizer.Check(context.Background(), application.AccessCheck{
		Subject:    customer,
		Permission: getorder.PermissionView,
		Resource:   application.Resource{Type: getorder.ResourceOrder, ID: "order-1"},
	}))
}

// ADR 41: the Market comes only from the Restaurant of the catalog snapshot; a
// snapshot without one places nothing and is not invalid input of the Customer.
func TestPlaceOrderRequiresMarketOfRestaurant(t *testing.T) {
	e := newEnv(t)
	e.catalog.Put("restaurant-2", placeorder.CatalogSnapshot{
		Currency:     "EUR",
		Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}},
		MinimumTotal: ordertest.Money(t, "10"),
		MaximumTotal: ordertest.Money(t, "100"),
	})
	input := validInput()
	input.RestaurantID = "restaurant-2"
	input.Items = []placeorder.ItemInput{{MenuItemID: "pizza", Quantity: 1}}

	_, err := e.handler.Handle(context.Background(), input)

	require.ErrorContains(t, err, "no market_id")
	assert.NotErrorIs(t, err, application.ErrInvalidInput)
	assert.NotErrorIs(t, err, vo.ErrInvalidValue)
	assert.Empty(t, e.store.OutboxRecords())
	assert.Empty(t, e.workflow.Starts())
}

// AC-101: unavailable items or limits reject placement without effects.
func TestPlaceOrderRejectsNotOrderableItems(t *testing.T) {
	tests := []struct {
		name    string
		items   []placeorder.ItemInput
		reasons []order.PolicyReasonCode
	}{
		{
			name:    "unavailable item",
			items:   []placeorder.ItemInput{{MenuItemID: "pizza", Quantity: 1}, {MenuItemID: "cake", Quantity: 1}},
			reasons: []order.PolicyReasonCode{order.OrderItemUnavailable},
		},
		{
			name:    "unknown item",
			items:   []placeorder.ItemInput{{MenuItemID: "pizza", Quantity: 1}, {MenuItemID: "ghost", Quantity: 1}},
			reasons: []order.PolicyReasonCode{order.OrderItemUnavailable},
		},
		{
			name:    "below minimum",
			items:   []placeorder.ItemInput{{MenuItemID: "soup", Quantity: 1}},
			reasons: []order.PolicyReasonCode{order.OrderTotalBelowMinimum},
		},
		{
			name:    "above maximum",
			items:   []placeorder.ItemInput{{MenuItemID: "pizza", Quantity: 9}},
			reasons: []order.PolicyReasonCode{order.OrderTotalAboveMaximum},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := validInput()
			in.Items = tt.items

			_, err := e.handler.Handle(context.Background(), in)

			var notOrderable order.NotOrderable
			require.ErrorAs(t, err, &notOrderable)
			codes := make([]order.PolicyReasonCode, 0, len(notOrderable.Reasons))
			for _, reason := range notOrderable.Reasons {
				codes = append(codes, reason.Code)
			}
			assert.Equal(t, tt.reasons, codes)
			assertNothingPlaced(t, e)
		})
	}
}

// AC-102: malformed input is rejected before the catalog is read.
func TestPlaceOrderValidatesInputBeforeCatalog(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*placeorder.Input)
		wantErr error
	}{
		{name: "no items", mutate: func(in *placeorder.Input) { in.Items = nil }, wantErr: order.ErrOrderRequiresItems},
		{
			name:    "zero quantity",
			mutate:  func(in *placeorder.Input) { in.Items[0].Quantity = 0 },
			wantErr: vo.ErrInvalidQuantity,
		},
		{
			name:    "duplicate menu item",
			mutate:  func(in *placeorder.Input) { in.Items[1].MenuItemID = in.Items[0].MenuItemID },
			wantErr: order.ErrDuplicateMenuItem,
		},
		{name: "no idempotency key", mutate: func(in *placeorder.Input) { in.IdempotencyKey = "" }, wantErr: application.ErrInvalidInput},
		{name: "no restaurant", mutate: func(in *placeorder.Input) { in.RestaurantID = "" }, wantErr: application.ErrInvalidInput},
		{
			name: "service subject",
			mutate: func(in *placeorder.Input) {
				in.Subject = application.Subject{Kind: application.SubjectService, ID: "bff"}
			},
			wantErr: application.ErrPermissionDenied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := validInput()
			tt.mutate(&in)

			_, err := e.handler.Handle(context.Background(), in)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Zero(t, e.catalog.Calls())
			assertNothingPlaced(t, e)
		})
	}
}

// AC-103: a repeated request returns the same Order and retries the start.
func TestPlaceOrderIsIdempotentByKey(t *testing.T) {
	e := newEnv(t)

	first, err := e.handler.Handle(context.Background(), validInput())
	require.NoError(t, err)
	second, err := e.handler.Handle(context.Background(), validInput())
	require.NoError(t, err)

	assert.Equal(t, placeorder.Result{OrderID: first.OrderID, Replayed: true, FulfillmentStarted: true}, second)
	assert.Len(t, e.store.OutboxRecords(), 1)
	assert.Equal(t, 1, e.catalog.Calls(), "a replay does not read the catalog again")
	assert.Equal(t, []fulfillment.StartRequest{{OrderID: "order-1"}, {OrderID: "order-1"}}, e.workflow.Starts())

	other := validInput()
	other.IdempotencyKey = "key-2"
	third, err := e.handler.Handle(context.Background(), other)
	require.NoError(t, err)
	assert.Equal(t, order.ID("order-2"), third.OrderID)
}

// AC-104: a failed relationship write cancels the placement.
func TestPlaceOrderFailsClosedWhenRelationshipWriteFails(t *testing.T) {
	e := newEnv(t)
	e.authorizer.FailWrites(errors.New("spicedb unavailable"))

	_, err := e.handler.Handle(context.Background(), validInput())

	require.Error(t, err)
	assertNothingPlaced(t, e)
}

// AC-105: a failed start after commit keeps the Order; a replay retries it.
func TestPlaceOrderKeepsOrderWhenStartFails(t *testing.T) {
	e := newEnv(t)
	e.workflow.FailStart(errors.New("temporal unavailable"))

	result, err := e.handler.Handle(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, placeorder.Result{OrderID: "order-1"}, result)
	_, ok := e.store.Snapshot("order-1")
	assert.True(t, ok)
	assert.Len(t, e.store.OutboxRecords(), 1)

	e.workflow.FailStart(nil)
	replayed, err := e.handler.Handle(context.Background(), validInput())
	require.NoError(t, err)
	assert.True(t, replayed.FulfillmentStarted)
	assert.Equal(t, []fulfillment.StartRequest{{OrderID: "order-1"}}, e.workflow.Starts())
}

func TestPlaceOrderPropagatesCatalogFailure(t *testing.T) {
	e := newEnv(t)
	in := validInput()
	in.RestaurantID = "restaurant-unknown"

	_, err := e.handler.Handle(context.Background(), in)

	require.ErrorIs(t, err, placeorder.ErrUnknownRestaurant)
	assertNothingPlaced(t, e)
}

func assertNothingPlaced(t *testing.T, e env) {
	t.Helper()
	_, ok := e.store.Snapshot("order-1")
	assert.False(t, ok)
	assert.Empty(t, e.store.OutboxRecords())
	assert.Empty(t, e.workflow.Starts())
}

// racingKeys hides a key committed by a concurrent request from the first
// hidden lookups (one by default).
type racingKeys struct {
	*repositorytest.Store
	hidden  int
	lookups int
}

func (k *racingKeys) FindOrder(ctx context.Context, customerID order.CustomerID, key string) (order.ID, bool, error) {
	k.lookups++
	if k.lookups <= max(k.hidden, 1) {
		return "", false, nil
	}
	return k.Store.FindOrder(ctx, customerID, key)
}

func (e env) handlerWithKeys(keys placeorder.IdempotencyKeys) placeorder.Handler {
	return placeorder.NewHandler(placeorder.Dependencies{
		UnitOfWork:      e.store,
		Orders:          e.store,
		Outbox:          e.store,
		IdempotencyKeys: keys,
		Catalog:         e.catalog,
		Relationships:   e.authorizer,
		Revisions:       e.store,
		IDs:             &fake.IDs{},
		Clock:           fake.NewClock(now),
		Workflow:        e.workflow,
	})
}

// AC-671: the concurrent request with the same key commits while this one
// waits on the key in RememberOrder (both lookups missed it): the request
// replays the committed Order instead of failing.
func TestPlaceOrderReplaysOrderCommittedDuringKeyConflict(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.store.Add(context.Background(), ordertest.New(t, "order-0")))
	require.NoError(t, e.store.RememberOrder(context.Background(), "customer-1", "key-1", "order-0"))

	result, err := e.handlerWithKeys(&racingKeys{Store: e.store, hidden: 2}).Handle(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, placeorder.Result{OrderID: "order-0", Replayed: true, FulfillmentStarted: true}, result)
	assertNothingPlaced(t, env{store: e.store, workflow: fake.NewWorkflow()})
}

// takenKeys reports the key as taken without a committed Order behind it.
type takenKeys struct{ *repositorytest.Store }

func (takenKeys) FindOrder(context.Context, order.CustomerID, string) (order.ID, bool, error) {
	return "", false, nil
}

func (takenKeys) RememberOrder(context.Context, order.CustomerID, string, order.ID) error {
	return placeorder.ErrIdempotencyKeyTaken
}

// AC-671: if the conflicting Order cannot be read, the request fails with a
// retryable concurrency conflict, not an internal error.
func TestPlaceOrderKeyConflictWithoutOrderIsConcurrencyConflict(t *testing.T) {
	e := newEnv(t)

	_, err := e.handlerWithKeys(takenKeys{Store: e.store}).Handle(context.Background(), validInput())

	require.ErrorIs(t, err, application.ErrConcurrencyConflict)
}

func TestPlaceOrderReturnsConcurrentlyPlacedOrder(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.store.Add(context.Background(), ordertest.New(t, "order-0")))
	require.NoError(t, e.store.RememberOrder(context.Background(), "customer-1", "key-1", "order-0"))
	handler := e.handlerWithKeys(&racingKeys{Store: e.store})

	result, err := handler.Handle(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, placeorder.Result{OrderID: "order-0", Replayed: true, FulfillmentStarted: true}, result)
	assertNothingPlaced(t, env{store: e.store, workflow: fake.NewWorkflow()})
}

// FR-440: the revision of the creation-time relationship is stored with the Order.
func TestPlaceOrderStoresAuthorizationRevision(t *testing.T) {
	e := newEnv(t)

	result, err := e.handler.Handle(context.Background(), validInput())

	require.NoError(t, err)
	revision, found, err := e.store.Revision(context.Background(), string(result.OrderID))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, application.AuthorizationRevision("fake:"+string(result.OrderID)), revision)
}
