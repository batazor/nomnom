package catalog_test

import (
	"context"
	"net"
	"nomnom/orders/pkg/gateway"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"nomnom/orders/pkg/application"
	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/order/infrastructure/catalog"
)

// fakeRestaurants serves GetRestaurant and GetMenu of one restaurant.
type fakeRestaurants struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer

	mu            sync.Mutex
	open          bool
	items         []*restaurantsv1.MenuItem
	restaurantErr error
	menuErr       error
	block         bool
	authorization []string
	menuCalls     int
}

func (f *fakeRestaurants) GetRestaurant(ctx context.Context, r *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	f.mu.Lock()
	md, _ := metadata.FromIncomingContext(ctx)
	f.authorization = md.Get("authorization")
	block, err, open := f.block, f.restaurantErr, f.open
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if err != nil {
		return nil, err
	}
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: r.GetRestaurantId(), IsOpen: open}}, nil
}

func (f *fakeRestaurants) GetMenu(context.Context, *restaurantsv1.GetMenuRequest) (*restaurantsv1.GetMenuResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.menuCalls++
	if f.menuErr != nil {
		return nil, f.menuErr
	}
	return &restaurantsv1.GetMenuResponse{Items: f.items}, nil
}

func item(id, amount, currency string, available bool) *restaurantsv1.MenuItem {
	return &restaurantsv1.MenuItem{
		MenuItemId: id,
		Name:       id,
		Price:      &restaurantsv1.Money{Amount: amount, Currency: currency},
		Available:  available,
	}
}

func dial(t *testing.T, server *fakeRestaurants, options ...grpc.DialOption) *grpc.ClientConn {
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

func newCatalog(t *testing.T, server *fakeRestaurants) *catalog.Catalog {
	t.Helper()
	return catalog.New(dial(t, server), limits, time.Second)
}

func TestSnapshotReadsRequestedItemsOfOpenRestaurant(t *testing.T) {
	server := &fakeRestaurants{open: true, items: []*restaurantsv1.MenuItem{
		item("pizza", "12.50", "EUR", true),
		item("gyoza", "6", "EUR", false),
		item("soup", "4", "EUR", true),
	}}

	snapshot, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza", "gyoza", "missing"})
	require.NoError(t, err)

	assert.Equal(t, "EUR", snapshot.Currency)
	assert.Equal(t, map[order.MenuItemID]placeorder.CatalogItem{
		"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
		"gyoza": {UnitPrice: ordertest.Money(t, "6"), Available: false},
	}, snapshot.Items, "only requested items; unknown ones stay out and are unavailable")
	assert.True(t, snapshot.MinimumTotal.Equal(ordertest.Money(t, "5")))
	assert.True(t, snapshot.MaximumTotal.Equal(ordertest.Money(t, "100")))
}

// AC-566: a closed restaurant is refused before the menu is read.
func TestSnapshotRefusesClosedRestaurant(t *testing.T) {
	server := &fakeRestaurants{open: false, items: []*restaurantsv1.MenuItem{item("pizza", "12.50", "EUR", true)}}

	_, err := newCatalog(t, server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.ErrorIs(t, err, placeorder.ErrRestaurantNotAcceptingOrders)
	assert.Zero(t, server.menuCalls)
}

func TestSnapshotMapsErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		server *fakeRestaurants
		want   error
	}{
		"restaurant not found": {&fakeRestaurants{restaurantErr: status.Error(codes.NotFound, "no restaurant")}, placeorder.ErrUnknownRestaurant},
		"menu not found":       {&fakeRestaurants{open: true, menuErr: status.Error(codes.NotFound, "no menu")}, placeorder.ErrUnknownRestaurant},
		"empty menu":           {&fakeRestaurants{open: true}, placeorder.ErrUnknownRestaurant},
		"invalid argument":     {&fakeRestaurants{restaurantErr: status.Error(codes.InvalidArgument, "bad id")}, application.ErrInvalidInput},
		"unavailable":          {&fakeRestaurants{restaurantErr: status.Error(codes.Unavailable, "down")}, application.ErrDependencyUnavailable},
		"unauthenticated":      {&fakeRestaurants{restaurantErr: status.Error(codes.Unauthenticated, "who")}, application.ErrDependencyUnavailable},
		"route not registered": {&fakeRestaurants{open: true, menuErr: status.Error(codes.Unimplemented, "404")}, application.ErrDependencyUnavailable},
		"mixed currencies": {&fakeRestaurants{open: true, items: []*restaurantsv1.MenuItem{
			item("pizza", "12.50", "EUR", true), item("soup", "4", "USD", true),
		}}, vo.ErrInvalidValue},
		"invalid price": {&fakeRestaurants{open: true, items: []*restaurantsv1.MenuItem{item("pizza", "twelve", "EUR", true)}}, vo.ErrInvalidValue},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newCatalog(t, tc.server).Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestSnapshotTimeoutIsUnavailable(t *testing.T) {
	server := &fakeRestaurants{block: true}
	adapter := catalog.New(dial(t, server), limits, 50*time.Millisecond)

	_, err := adapter.Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.ErrorIs(t, err, application.ErrDependencyUnavailable)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestSnapshotCallsCarryServiceToken(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nomnom.restaurants"), []byte("token-1\n"), 0o600))
	server := &fakeRestaurants{open: true, items: []*restaurantsv1.MenuItem{item("pizza", "12.50", "EUR", true)}}
	adapter := catalog.New(dial(t, server, grpc.WithUnaryInterceptor(gateway.ServiceToken(dir))), limits, time.Second)

	_, err := adapter.Snapshot(context.Background(), "restaurant-1", []order.MenuItemID{"pizza"})
	require.NoError(t, err)
	assert.Equal(t, []string{"Bearer token-1"}, server.authorization)
}
