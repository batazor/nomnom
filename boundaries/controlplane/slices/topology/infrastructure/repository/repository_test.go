package repository_test

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/di/schema"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/testfixture"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/getplacementmap"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/repository"
)

// pg is the PostgreSQL server of the package; every test gets a database of its own.
var (
	pg        *testfixture.Postgres
	databases atomic.Int64
	createMu  sync.Mutex
)

func TestMain(m *testing.M) {
	pg = testfixture.MustStartPostgres()
	defer pg.Close()
	m.Run()
}

// open migrates a fresh database the way the process does and opens the store on it.
func open(t *testing.T) *repository.Store {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("topology_%d", databases.Add(1))
	createMu.Lock()
	admin, err := pgx.Connect(ctx, pg.DSN)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	_ = admin.Close(ctx)
	createMu.Unlock()
	require.NoError(t, err)
	dsn, err := url.Parse(pg.DSN)
	require.NoError(t, err)
	dsn.Path = "/" + name
	cfg := database.Config{DSN: dsn.String(), MaxConns: 4}
	require.NoError(t, schema.Migrate(ctx, cfg))
	require.NoError(t, schema.Migrate(ctx, cfg), "a second start applies nothing")
	store, err := repository.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}

func TestCellIsAddedOnce(t *testing.T) {
	ctx := context.Background()
	cells := repository.NewCells(open(t))
	first := cell.New("cell-1", "gateway:8080")

	added, err := cells.Add(ctx, first)
	require.NoError(t, err)
	assert.True(t, added)
	added, err = cells.Add(ctx, cell.New("cell-1", "other:8080"))
	require.NoError(t, err)
	assert.False(t, added, "a known id is not overwritten")

	stored, err := cells.Get(ctx, "cell-1")
	require.NoError(t, err)
	assert.Equal(t, first, stored)
	_, err = cells.Get(ctx, "cell-9")
	assert.ErrorIs(t, err, cell.ErrNotFound)
}

func TestMarketNeedsARegisteredCell(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	markets := repository.NewMarkets(store)

	_, err := markets.Add(ctx, market.New("berlin", "Berlin", "EUR", "cell-1"))
	assert.ErrorIs(t, err, market.ErrUnknownCell)

	_, err = repository.NewCells(store).Add(ctx, cell.New("cell-1", "gateway:8080"))
	require.NoError(t, err)
	berlin := market.New("berlin", "Berlin", "EUR", "cell-1")
	added, err := markets.Add(ctx, berlin)
	require.NoError(t, err)
	assert.True(t, added)
	added, err = markets.Add(ctx, market.New("berlin", "Berlin", "USD", "cell-1"))
	require.NoError(t, err)
	assert.False(t, added)

	stored, err := markets.Get(ctx, "berlin")
	require.NoError(t, err)
	assert.Equal(t, berlin, stored)
	_, err = markets.Get(ctx, "paris")
	assert.ErrorIs(t, err, market.ErrNotFound)
}

func TestMarketsAndPlacementsAreOrdered(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	cells, markets := repository.NewCells(store), repository.NewMarkets(store)
	for _, c := range []cell.Cell{cell.New("cell-1", "gateway-1:8080"), cell.New("cell-2", "gateway-2:8080")} {
		_, err := cells.Add(ctx, c)
		require.NoError(t, err)
	}
	for _, m := range []market.Market{
		market.New("munich", "Munich", "EUR", "cell-2"),
		market.New("berlin-east", "Berlin", "EUR", "cell-1"),
		market.New("berlin", "Berlin", "EUR", "cell-1"),
	} {
		_, err := markets.Add(ctx, m)
		require.NoError(t, err)
	}

	listed, err := markets.List(ctx)
	require.NoError(t, err)
	var ids []string
	for _, m := range listed {
		ids = append(ids, string(m.ID()))
	}
	assert.Equal(t, []string{"berlin", "berlin-east", "munich"}, ids, "by name, then by market_id")

	placements, err := repository.NewPlacements(store).Placements(ctx)
	require.NoError(t, err)
	assert.Equal(t, []getplacementmap.Placement{
		{MarketID: "berlin", CellID: "cell-1", GatewayAddress: "gateway-1:8080"},
		{MarketID: "berlin-east", CellID: "cell-1", GatewayAddress: "gateway-1:8080"},
		{MarketID: "munich", CellID: "cell-2", GatewayAddress: "gateway-2:8080"},
	}, placements)
}

func TestEmptyRegistryReadsAsEmpty(t *testing.T) {
	store := open(t)
	listed, err := repository.NewMarkets(store).List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, listed)
	placements, err := repository.NewPlacements(store).Placements(context.Background())
	require.NoError(t, err)
	assert.Empty(t, placements)
}
