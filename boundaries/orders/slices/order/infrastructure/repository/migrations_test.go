package repository_test

import (
	"context"
	"io/fs"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config/database"
	"nomnom/orders/pkg/migration"
	"nomnom/orders/slices/order/infrastructure/repository"
)

// ADR 41: Orders placed before Markets belong to berlin. The migration sets it
// explicitly on existing orders and order_details rows and then makes the
// column required.
func TestMarketMigrationBackfillsExistingOrdersWithBerlin(t *testing.T) {
	ctx := context.Background()
	cfg := database.Config{DSN: emptyOrdersDatabase(t), MaxConns: 1}
	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00008_market.up.sql")))

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, total_amount, total_currency, state, placed_at, version)
		VALUES ('legacy-1', 'customer-1', 'restaurant-1', 10, 'EUR', 'placed', now(), 1)`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO order_details (order_id, customer_id, placed_at, order_version, projection_version, details)
		VALUES ('legacy-1', 'customer-1', now(), 1, 1, '{}')`)
	require.NoError(t, err)

	require.NoError(t, migration.Apply(ctx, cfg, repository.Migrations))

	var orderMarket, detailsMarket string
	require.NoError(t, conn.QueryRow(ctx, `SELECT market_id FROM orders WHERE id = 'legacy-1'`).Scan(&orderMarket))
	require.NoError(t, conn.QueryRow(ctx, `SELECT market_id FROM order_details WHERE order_id = 'legacy-1'`).Scan(&detailsMarket))
	assert.Equal(t, "berlin", orderMarket)
	assert.Equal(t, "berlin", detailsMarket)

	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, total_amount, total_currency, state, placed_at, version)
		VALUES ('new-1', 'customer-1', 'restaurant-1', 10, 'EUR', 'placed', now(), 1)`)
	require.ErrorContains(t, err, "market_id", "the Market has no default")
	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version)
		VALUES ('new-2', 'customer-1', 'restaurant-1', 'Berlin', 10, 'EUR', 'placed', now(), 1)`)
	require.ErrorContains(t, err, "orders_market_id_format")
}

// migrationsBefore is the order migration set without first and every later file.
func migrationsBefore(t *testing.T, first string) migration.Set {
	t.Helper()
	files := fstest.MapFS{}
	require.NoError(t, fs.WalkDir(repository.Migrations.Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.TrimPrefix(path, "migrations/") >= first {
			return err
		}
		data, err := fs.ReadFile(repository.Migrations.Files, path)
		files[path] = &fstest.MapFile{Data: data}
		return err
	}))
	return migration.Set{ID: repository.Migrations.ID, Files: files}
}

// emptyOrdersDatabase creates a database without schema in the package container.
func emptyOrdersDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, pg.DSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close(ctx) }()
	name := "market_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	dsn, err := url.Parse(pg.DSN)
	require.NoError(t, err)
	dsn.Path = "/" + name
	return dsn.String()
}
