package place_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/place"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/place.feature (ADR 19). A scenario is a
// subtest; its mocks report to it through godog.T. The store and the cleanup of
// the mocks belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	refused := map[string]struct {
		items   []place.ItemInput
		catalog func() map[order.MenuItemID]place.CatalogItem
	}{
		"an unavailable item": {
			items: []place.ItemInput{{MenuItemID: "pizza", Quantity: 1}, {MenuItemID: "cake", Quantity: 1}},
			catalog: func() map[order.MenuItemID]place.CatalogItem {
				return map[order.MenuItemID]place.CatalogItem{
					"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
					"cake":  {UnitPrice: ordertest.Money(t, "6.00"), Available: false},
				}
			},
		},
		"an item not on the menu": {
			items: []place.ItemInput{{MenuItemID: "pizza", Quantity: 1}, {MenuItemID: "ghost", Quantity: 1}},
			catalog: func() map[order.MenuItemID]place.CatalogItem {
				return map[order.MenuItemID]place.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}}
			},
		},
		"below the minimum total": {
			items: []place.ItemInput{{MenuItemID: "soup", Quantity: 1}},
			catalog: func() map[order.MenuItemID]place.CatalogItem {
				return map[order.MenuItemID]place.CatalogItem{"soup": {UnitPrice: ordertest.Money(t, "4.00"), Available: true}}
			},
		},
		"above the maximum total": {
			items: []place.ItemInput{{MenuItemID: "pizza", Quantity: 9}},
			catalog: func() map[order.MenuItemID]place.CatalogItem {
				return map[order.MenuItemID]place.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}}
			},
		},
	}
	malformed := map[string]struct {
		mutate  func(*place.Input)
		wantErr error
	}{
		"no items":                 {mutate: func(in *place.Input) { in.Items = nil }, wantErr: order.ErrOrderRequiresItems},
		"a zero quantity":          {mutate: func(in *place.Input) { in.Items[0].Quantity = 0 }, wantErr: vo.ErrInvalidQuantity},
		"the same menu item twice": {mutate: func(in *place.Input) { in.Items[1].MenuItemID = in.Items[0].MenuItemID }, wantErr: order.ErrDuplicateMenuItem},
	}

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				e            env
				result       place.Result
				repeated     place.Result
				failure      error
				malformedErr error
			)
			given := func(ctx context.Context) {
				e = newEnv(t)
				e.catalog.Test(godog.T(ctx))
				e.slots.Test(godog.T(ctx))
			}
			alternatives := func() []order.DeliveryWindow {
				return []order.DeliveryWindow{
					deliveryWindow(t, windowStart.Add(15*time.Minute)),
					deliveryWindow(t, windowStart.Add(30*time.Minute)),
				}
			}

			sc.Given(`^a restaurant with available items$`, given)
			sc.Given(`^a customer has placed an order$`, func(ctx context.Context) {
				given(ctx)
				e.catalog.EXPECT().
					Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
					Return(openSnapshot(t), nil).
					Once()
				var err error
				result, err = e.handler.Handle(ctx, validInput())
				require.NoError(godog.T(ctx), err)
			})
			sc.Given(`^a restaurant with a premium fee of 99 RUB$`, func(ctx context.Context) {
				given(ctx)
				fee, err := vo.ParseMoney("99.00", "RUB")
				require.NoError(godog.T(ctx), err)
				e.catalog.EXPECT().
					Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).
					Return(premiumSnapshot(t, &fee), nil).
					Once()
			})
			sc.Given(`^a restaurant without a premium fee$`, func(ctx context.Context) {
				given(ctx)
				e.catalog.EXPECT().
					Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).
					Return(premiumSnapshot(t, nil), nil).
					Once()
			})
			sc.Given(`^a restaurant with a free delivery window$`, func(ctx context.Context) {
				given(ctx)
				e.catalog.EXPECT().
					Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
					Return(openSnapshot(t), nil).
					Once()
				e.slots.EXPECT().
					Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-1")).
					Return(deliveryWindow(t, windowStart), nil).
					Once()
			})
			sc.Given(`^a restaurant with a full delivery window$`, func(ctx context.Context) {
				given(ctx)
				e.catalog.EXPECT().
					Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
					Return(openSnapshot(t), nil).
					Once()
				e.slots.EXPECT().
					Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-1")).
					Return(order.DeliveryWindow{}, place.SlotUnavailable{Alternatives: alternatives()}).
					Once()
			})

			sc.When(`^the customer places an order$`, func(ctx context.Context) {
				e.catalog.EXPECT().
					Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
					Return(openSnapshot(t), nil).
					Once()
				var err error
				result, err = e.handler.Handle(ctx, validInput())
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the customer orders (an unavailable item|an item not on the menu|below the minimum total|above the maximum total)$`, func(ctx context.Context, request string) {
				tt := refused[request]
				menuItemIDs := make([]order.MenuItemID, 0, len(tt.items))
				for _, item := range tt.items {
					menuItemIDs = append(menuItemIDs, item.MenuItemID)
				}
				e.catalog.EXPECT().
					Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), menuItemIDs).
					Return(place.CatalogSnapshot{
						MarketID:     ordertest.MarketID(t),
						Currency:     "EUR",
						Items:        tt.catalog(),
						MinimumTotal: ordertest.Money(t, "10"),
						MaximumTotal: ordertest.Money(t, "100"),
					}, nil).
					Once()
				in := validInput()
				in.Items = tt.items
				_, failure = e.handler.Handle(ctx, in)
			})
			sc.When(`^the customer submits (no items|a zero quantity|the same menu item twice)$`, func(ctx context.Context, request string) {
				tt := malformed[request]
				in := validInput()
				tt.mutate(&in)
				malformedErr = tt.wantErr
				_, failure = e.handler.Handle(ctx, in)
			})
			sc.When(`^the customer repeats the request with the same idempotency key$`, func(ctx context.Context) {
				var err error
				repeated, err = e.handler.Handle(ctx, validInput())
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the customer places a premium order for 500 RUB$`, func(ctx context.Context) {
				_, err := e.handler.Handle(ctx, premiumInput())
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the customer tries to place a premium order$`, func(ctx context.Context) {
				_, failure = e.handler.Handle(ctx, premiumInput())
			})
			sc.When(`^the customer places an order for that delivery window$`, func(ctx context.Context) {
				var err error
				result, err = e.handler.Handle(ctx, scheduledInput())
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the customer tries to place an order for that delivery window$`, func(ctx context.Context) {
				_, failure = e.handler.Handle(ctx, scheduledInput())
			})

			sc.Then(`^the order is placed for the customer in the market of the restaurant$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), place.Result{OrderID: "order-1"}, result)
				placed, ok := e.store.Snapshot("order-1")
				require.True(godog.T(ctx), ok)
				assert.Equal(godog.T(ctx), order.StatePlaced, placed.State)
				assert.Equal(godog.T(ctx), order.CustomerID("customer-1"), placed.CustomerID)
				assert.Equal(godog.T(ctx), now, placed.PlacedAt)
				assert.Equal(godog.T(ctx), ordertest.MarketID(t), placed.MarketID)
			})
			sc.Then(`^the total is computed from the catalog prices$`, func(ctx context.Context) {
				placed, _ := e.store.Snapshot("order-1")
				restored, err := order.Restore(placed)
				require.NoError(godog.T(ctx), err)
				assert.Equal(godog.T(ctx), "29 EUR", restored.Total().String())
			})
			sc.Then(`^the placement is published with the order$`, func(ctx context.Context) {
				placed, _ := e.store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), repositorytest.ExpectedOutbox(t, "order-1", 1, order.OrderPlaced{
					OrderID:      "order-1",
					CustomerID:   "customer-1",
					RestaurantID: "restaurant-1",
					MarketID:     ordertest.MarketID(t),
					Total:        ordertest.Money(t, "29.00"),
					PlacedAt:     now,
					Items:        placed.Items,
				}), e.store.OutboxRecords())
			})
			sc.Then(`^the relationships of the order are recorded for synchronization$`, func(ctx context.Context) {
				// ADR 18: orders-consumer syncs them from RelationshipsChanged committed with the Order.
				assert.Equal(godog.T(ctx), []order.ID{"order-1"}, e.store.RelationshipChanges())
			})
			sc.Then(`^the order is refused with the reason ([a-z_]+)$`, func(ctx context.Context, reason string) {
				var notOrderable order.NotOrderable
				require.ErrorAs(godog.T(ctx), failure, &notOrderable)
				codes := make([]order.PolicyReasonCode, 0, len(notOrderable.Reasons))
				for _, r := range notOrderable.Reasons {
					codes = append(codes, r.Code)
				}
				assert.Equal(godog.T(ctx), []order.PolicyReasonCode{order.PolicyReasonCode(reason)}, codes)
			})
			sc.Then(`^nothing is placed$`, func(ctx context.Context) {
				_, ok := e.store.Snapshot("order-1")
				assert.False(godog.T(ctx), ok)
				assert.Empty(godog.T(ctx), e.store.OutboxRecords())
				assert.Empty(godog.T(ctx), e.store.RelationshipChanges())
			})
			sc.Then(`^the order is rejected as malformed$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, malformedErr)
			})
			sc.Then(`^the catalog is not read$`, func(ctx context.Context) {
				e.catalog.AssertNotCalled(godog.T(ctx), "Snapshot", mock.Anything, mock.Anything, mock.Anything)
			})
			sc.Then(`^the customer gets the same order back$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), place.Result{OrderID: result.OrderID, Replayed: true}, repeated)
			})
			sc.Then(`^no second order is placed$`, func(ctx context.Context) {
				assert.Len(godog.T(ctx), e.store.OutboxRecords(), 1)
				e.catalog.AssertNumberOfCalls(godog.T(ctx), "Snapshot", 1)
			})
			sc.Then(`^the order keeps the premium fee$`, func(ctx context.Context) {
				placed, ok := e.store.Snapshot("order-1")
				require.True(godog.T(ctx), ok)
				assert.Equal(godog.T(ctx), order.PriorityPremium, placed.Service.Priority())
				fee, premium := placed.Service.PremiumFee()
				require.True(godog.T(ctx), premium)
				want, err := vo.ParseMoney("99.00", "RUB")
				require.NoError(godog.T(ctx), err)
				assert.True(godog.T(ctx), fee.Equal(want), "the fee of the snapshot is kept in the Order")
			})
			sc.Then(`^the total of the order is 599 RUB$`, func(ctx context.Context) {
				placed, _ := e.store.Snapshot("order-1")
				restored, err := order.Restore(placed)
				require.NoError(godog.T(ctx), err)
				assert.Equal(godog.T(ctx), "599 RUB", restored.Total().String())
			})
			sc.Then(`^the order is refused because premium priority is unavailable$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, place.ErrPremiumUnavailable)
			})
			sc.Then(`^the order keeps the reserved delivery window$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), place.Result{OrderID: "order-1"}, result)
				placed, ok := e.store.Snapshot("order-1")
				require.True(godog.T(ctx), ok)
				assert.Equal(godog.T(ctx), windowStart, placed.DeliveryWindow.Start())
				assert.Equal(godog.T(ctx), windowEnd, placed.DeliveryWindow.End())
			})
			sc.Then(`^the placement is published with the delivery window$`, func(ctx context.Context) {
				placed, _ := e.store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), repositorytest.ExpectedOutbox(t, "order-1", 1, order.OrderPlaced{
					OrderID:        "order-1",
					CustomerID:     "customer-1",
					RestaurantID:   "restaurant-1",
					MarketID:       ordertest.MarketID(t),
					Total:          ordertest.Money(t, "29.00"),
					PlacedAt:       now,
					Items:          placed.Items,
					DeliveryWindow: deliveryWindow(t, windowStart),
				}), e.store.OutboxRecords())
			})
			sc.Then(`^the order is refused with the nearest available windows$`, func(ctx context.Context) {
				var unavailable place.SlotUnavailable
				require.ErrorAs(godog.T(ctx), failure, &unavailable)
				assert.Equal(godog.T(ctx), alternatives(), unavailable.Alternatives)
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
