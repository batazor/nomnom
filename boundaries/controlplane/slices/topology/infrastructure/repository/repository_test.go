package repository_test

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/di/schema"
	"github.com/batazor/nomnom/boundaries/controlplane/infrastructure/migration"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/testfixture"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	mvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
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
	cfg := freshDatabase(t)
	require.NoError(t, schema.Migrate(ctx, cfg))
	require.NoError(t, schema.Migrate(ctx, cfg), "a second start applies nothing")
	store, err := repository.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}

// freshDatabase creates an empty database of the test.
func freshDatabase(t *testing.T) database.Config {
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
	return database.Config{DSN: dsn.String(), MaxConns: 4}
}

// beforeTimeZones migrates a fresh database to the registry before Market
// time zones (00002) and stores the Markets in cell-1.
func beforeTimeZones(t *testing.T, marketIDs ...string) database.Config {
	t.Helper()
	ctx := context.Background()
	files := fstest.MapFS{}
	for _, name := range []string{"migrations/00001_topology.up.sql", "migrations/00002_cell_router.up.sql"} {
		content, err := fs.ReadFile(repository.Migrations.Files, name)
		require.NoError(t, err)
		files[name] = &fstest.MapFile{Data: content}
	}
	cfg := freshDatabase(t)
	require.NoError(t, migration.Apply(ctx, cfg, migration.Set{ID: repository.Migrations.ID, Files: files}))
	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, `INSERT INTO cells (cell_id, registered_at, router_namespace, router_name, router_port)
		VALUES ('cell-1', now(), 'nomnom-bff', 'bff', 3010)`)
	require.NoError(t, err)
	for _, id := range marketIDs {
		_, err = conn.Exec(ctx, `INSERT INTO markets (market_id, name, currency, cell_id, created_at)
			VALUES ($1, $1, 'EUR', 'cell-1', now())`, id)
		require.NoError(t, err)
	}
	return cfg
}

func TestMigrationSetsTheTimeZoneOfBerlin(t *testing.T) {
	ctx := context.Background()
	cfg := beforeTimeZones(t, "berlin")

	require.NoError(t, schema.Migrate(ctx, cfg))
	store, err := repository.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	stored, err := repository.NewMarkets(store).Get(ctx, "berlin")
	require.NoError(t, err)
	assert.Equal(t, mvo.TimeZone("Europe/Berlin"), stored.TimeZone())
}

func TestMigrationRefusesAMarketWithoutAKnownTimeZone(t *testing.T) {
	cfg := beforeTimeZones(t, "berlin", "hamburg")

	err := schema.Migrate(context.Background(), cfg)
	assert.ErrorContains(t, err, "markets without a known time zone: hamburg")
}

func router(t *testing.T, name string) vo.Router {
	t.Helper()
	r, err := vo.NewRouter("nomnom-bff", name, 3010)
	require.NoError(t, err)
	return r
}

func TestCellIsAddedOnce(t *testing.T) {
	ctx := context.Background()
	cells := repository.NewCells(open(t))
	first := cell.New("cell-1", router(t, "bff"))

	added, err := cells.Add(ctx, first)
	require.NoError(t, err)
	assert.True(t, added)
	added, err = cells.Add(ctx, cell.New("cell-1", router(t, "other")))
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

	_, err := markets.Add(ctx, market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1"))
	assert.ErrorIs(t, err, market.ErrUnknownCell)

	_, err = repository.NewCells(store).Add(ctx, cell.New("cell-1", router(t, "bff")))
	require.NoError(t, err)
	berlin := market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1")
	added, err := markets.Add(ctx, berlin)
	require.NoError(t, err)
	assert.True(t, added)
	added, err = markets.Add(ctx, market.New("berlin", "Berlin", "EUR", "Europe/Paris", "cell-1"))
	require.NoError(t, err)
	assert.False(t, added)

	stored, err := markets.Get(ctx, "berlin")
	require.NoError(t, err)
	assert.Equal(t, berlin, stored)
	_, err = markets.Get(ctx, "paris")
	assert.ErrorIs(t, err, market.ErrNotFound)
}

func TestMarketsAndRoutesAreOrdered(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	cells, markets := repository.NewCells(store), repository.NewMarkets(store)
	for _, c := range []cell.Cell{cell.New("cell-1", router(t, "bff-1")), cell.New("cell-2", router(t, "bff-2"))} {
		_, err := cells.Add(ctx, c)
		require.NoError(t, err)
	}
	for _, m := range []market.Market{
		market.New("munich", "Munich", "EUR", "Europe/Berlin", "cell-2"),
		market.New("berlin-east", "Berlin", "EUR", "Europe/Berlin", "cell-1"),
		market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1"),
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

	routes := snapshot(t, store)
	assert.Equal(t, []publishroutes.MarketRoute{
		{Market: market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1"), Router: router(t, "bff-1")},
		{Market: market.New("berlin-east", "Berlin", "EUR", "Europe/Berlin", "cell-1"), Router: router(t, "bff-1")},
		{Market: market.New("munich", "Munich", "EUR", "Europe/Berlin", "cell-2"), Router: router(t, "bff-2")},
	}, routes)
}

// snapshot reads the routes the way route publication does.
func snapshot(t *testing.T, store *repository.Store) []publishroutes.MarketRoute {
	t.Helper()
	var routes []publishroutes.MarketRoute
	require.NoError(t, repository.NewRoutes(store).Locked(context.Background(), func(_ context.Context, got []publishroutes.MarketRoute) error {
		routes = got
		return nil
	}))
	return routes
}

func TestRoutePublicationIsSerialized(t *testing.T) {
	store := open(t)
	registry := repository.NewRoutes(store)
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- registry.Locked(context.Background(), func(context.Context, []publishroutes.MarketRoute) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	var order []string
	var mu sync.Mutex
	second := make(chan error, 1)
	go func() {
		second <- registry.Locked(context.Background(), func(context.Context, []publishroutes.MarketRoute) error {
			mu.Lock()
			order = append(order, "second")
			mu.Unlock()
			return nil
		})
	}()
	mu.Lock()
	order = append(order, "first done")
	mu.Unlock()
	close(release)
	require.NoError(t, <-first)
	require.NoError(t, <-second)
	assert.Equal(t, []string{"first done", "second"}, order, "the second publisher waits for the lock")
}

func TestEmptyRegistryReadsAsEmpty(t *testing.T) {
	store := open(t)
	listed, err := repository.NewMarkets(store).List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, listed)
	assert.Empty(t, snapshot(t, store))
}

func TestArchivedMarketLeavesTheListAndTheRoutes(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	_, err := repository.NewCells(store).Add(ctx, cell.New("cell-1", router(t, "bff")))
	require.NoError(t, err)
	markets := repository.NewMarkets(store)
	for _, m := range []market.Market{
		market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1"),
		market.New("kind-e2e-1", "Kind e2e 1", "EUR", "Europe/Berlin", "cell-1"),
	} {
		_, err := markets.Add(ctx, m)
		require.NoError(t, err)
	}

	archived, err := markets.Archive(ctx, "kind-e2e-1")
	require.NoError(t, err)
	assert.True(t, archived)
	archived, err = markets.Archive(ctx, "kind-e2e-1")
	require.NoError(t, err)
	assert.False(t, archived, "a repeat changes nothing")
	_, err = markets.Archive(ctx, "paris")
	assert.ErrorIs(t, err, market.ErrNotFound)

	listed, err := markets.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []market.Market{market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1")}, listed)
	assert.Equal(t, []publishroutes.MarketRoute{
		{Market: market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1"), Router: router(t, "bff")},
	}, snapshot(t, store))

	stored, err := markets.Get(ctx, "kind-e2e-1")
	require.NoError(t, err)
	assert.True(t, stored.Archived(), "the row stays and keeps its id taken")
	added, err := markets.Add(ctx, market.New("kind-e2e-1", "Kind e2e 1", "EUR", "Europe/Berlin", "cell-1"))
	require.NoError(t, err)
	assert.False(t, added)
}
