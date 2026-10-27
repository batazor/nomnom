package catalog_test

import (
	"context"
	"net"
	"nomnom/orders/pkg/gateway"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"nomnom/orders/pkg/application"
	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/order/infrastructure/catalog"
)

func item(id, amount, currency string, available bool) *restaurantsv1.MenuItem {
	return &restaurantsv1.MenuItem{
		MenuItemId: id,
		Name:       id,
		Price:      &restaurantsv1.Money{Amount: amount, Currency: currency},
		Available:  available,
	}
}

func dial(t *testing.T, server *MockRestaurantsServiceServer, options ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	restaurantsv1.RegisterRestaurantsServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	options = append(options,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	conn, err := grpc.NewClient("passthrough:///bufnet", options...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

var limits = catalog.Limits{MinimumTotal: decimal.RequireFromString("5"), MaximumTotal: decimal.RequireFromString("100")}

func newCatalog(t *testing.T, server *MockRestaurantsServiceServer) *catalog.Catalog {
	t.Helper()
	return catalog.New(dial(t, server), limits, time.Second)
}

func TestSnapshotReadsRequestedItemsOfOpenRestaurant(t *testing.T) {
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, MarketId: ordertest.Market}}, nil).Once()
	server.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetMenuRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{
		item("pizza", "12.50", "EUR", true),
		item("gyoza", "6", "EUR", false),
		item("soup", "4", "EUR", true),
	}}, nil).Once()

	snapshot, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza", "gyoza", "missing"})
	require.NoError(t, err)

	assert.Equal(t, "EUR", snapshot.Currency)
	assert.Equal(t, ordertest.MarketID(t), snapshot.MarketID, "the Order is placed in the Market of the Restaurant")
	assert.Equal(t, map[order.MenuItemID]placeorder.CatalogItem{
		"pizza": {Name: "pizza", UnitPrice: ordertest.Money(t, "12.50"), Available: true},
		"gyoza": {Name: "gyoza", UnitPrice: ordertest.Money(t, "6"), Available: false},
	}, snapshot.Items, "only requested items with their names; unknown ones stay out and are unavailable")
	assert.True(t, snapshot.MinimumTotal.Equal(ordertest.Money(t, "5")))
	assert.True(t, snapshot.MaximumTotal.Equal(ordertest.Money(t, "100")))
	assert.Nil(t, snapshot.PremiumFee, "a restaurant without premium_fee offers no premium")
}

// FR-650: the premium fee of the Restaurant reaches the snapshot.
func TestSnapshotReadsPremiumFee(t *testing.T) {
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{
		RestaurantId: "restaurant-1", IsOpen: true, MarketId: ordertest.Market,
		PremiumFee: &restaurantsv1.Money{Amount: "1.50", Currency: "EUR"},
	}}, nil).Once()
	server.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetMenuRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{
		item("pizza", "12.50", "EUR", true),
	}}, nil).Once()

	snapshot, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})

	require.NoError(t, err)
	require.NotNil(t, snapshot.PremiumFee)
	assert.True(t, snapshot.PremiumFee.Equal(ordertest.Money(t, "1.50")))
}

// The name is kept in the Order: a requested item without one breaks the
// contract of restaurants and fails Place Order instead of an empty name.
func TestSnapshotRefusesItemWithoutName(t *testing.T) {
	nameless := item("pizza", "12.50", "EUR", true)
	nameless.Name = ""
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, MarketId: ordertest.Market}}, nil).Once()
	server.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetMenuRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{nameless}}, nil).Once()

	_, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.ErrorContains(t, err, "has no name")
}

// ADR 41: restaurants always publishes the Market of a Restaurant; a missing or
// malformed market_id breaks its contract and is never replaced by a Market of
// orders. It is not invalid input of the Customer.
func TestSnapshotRefusesRestaurantWithoutMarket(t *testing.T) {
	for name, market := range map[string]string{"missing": "", "malformed": "Berlin"} {
		t.Run(name, func(t *testing.T) {
			server := NewMockRestaurantsServiceServer(t)
			server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
				return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
			})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, MarketId: market}}, nil).Once()
			// No GetMenu expectation: the menu is not read.

			_, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
			require.ErrorIs(t, err, catalog.ErrInvalidRestaurant)
			assert.NotErrorIs(t, err, application.ErrInvalidInput)
			assert.NotErrorIs(t, err, vo.ErrInvalidValue)
		})
	}
}

// AC-566: a closed restaurant is refused before the menu is read.
func TestSnapshotRefusesClosedRestaurant(t *testing.T) {
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: false, MarketId: ordertest.Market}}, nil).Once()
	// No GetMenu expectation: the menu is not read.

	_, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.ErrorIs(t, err, placeorder.ErrRestaurantNotAcceptingOrders)
}

func TestSnapshotMapsErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		restaurantErr error
		menuErr       error
		items         []*restaurantsv1.MenuItem
		want          error
	}{
		"restaurant not found": {restaurantErr: status.Error(codes.NotFound, "no restaurant"), want: placeorder.ErrUnknownRestaurant},
		"menu not found":       {menuErr: status.Error(codes.NotFound, "no menu"), want: placeorder.ErrUnknownRestaurant},
		"empty menu":           {want: placeorder.ErrUnknownRestaurant},
		"invalid argument":     {restaurantErr: status.Error(codes.InvalidArgument, "bad id"), want: application.ErrInvalidInput},
		"unavailable":          {restaurantErr: status.Error(codes.Unavailable, "down"), want: application.ErrDependencyUnavailable},
		"unauthenticated":      {restaurantErr: status.Error(codes.Unauthenticated, "who"), want: application.ErrDependencyUnavailable},
		"route not registered": {menuErr: status.Error(codes.Unimplemented, "404"), want: application.ErrDependencyUnavailable},
		"mixed currencies": {items: []*restaurantsv1.MenuItem{
			item("pizza", "12.50", "EUR", true), item("soup", "4", "USD", true),
		}, want: vo.ErrInvalidValue},
		"invalid price": {items: []*restaurantsv1.MenuItem{item("pizza", "twelve", "EUR", true)}, want: vo.ErrInvalidValue},
	} {
		t.Run(name, func(t *testing.T) {
			server := NewMockRestaurantsServiceServer(t)
			switch {
			case tc.restaurantErr != nil:
				// No GetMenu expectation: the menu is not read.
				server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
					return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
				})).Return(nil, tc.restaurantErr).Once()
			case tc.menuErr != nil:
				server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
					return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
				})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, MarketId: ordertest.Market}}, nil).Once()
				server.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool {
					return proto.Equal(r, &restaurantsv1.GetMenuRequest{RestaurantId: "restaurant-1"})
				})).Return(nil, tc.menuErr).Once()
			default:
				server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
					return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
				})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, MarketId: ordertest.Market}}, nil).Once()
				server.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool {
					return proto.Equal(r, &restaurantsv1.GetMenuRequest{RestaurantId: "restaurant-1"})
				})).Return(&restaurantsv1.GetMenuResponse{Items: tc.items}, nil).Once()
			}

			_, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestSnapshotTimeoutIsUnavailable(t *testing.T) {
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	})).RunAndReturn(
		func(ctx context.Context, _ *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}).Once()
	adapter := catalog.New(dial(t, server), limits, 50*time.Millisecond)

	_, err := adapter.Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.ErrorIs(t, err, application.ErrDependencyUnavailable)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestSnapshotCallsCarryServiceToken(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nomnom.restaurants"), []byte("token-1\n"), 0o600))
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().GetRestaurant(mock.MatchedBy(func(ctx context.Context) bool {
		md, _ := metadata.FromIncomingContext(ctx)
		return assert.ObjectsAreEqual([]string{"Bearer token-1"}, md.Get("authorization"))
	}), mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, MarketId: ordertest.Market}}, nil).Once()
	server.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetMenuRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{item("pizza", "12.50", "EUR", true)}}, nil).Once()
	adapter := catalog.New(dial(t, server, grpc.WithUnaryInterceptor(gateway.ServiceToken(dir))), limits, time.Second)

	_, err := adapter.Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.NoError(t, err)
}
