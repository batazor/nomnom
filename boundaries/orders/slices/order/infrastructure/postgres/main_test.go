//go:build integration

package postgres_test

import (
	"context"
	"log"
	"testing"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/testfixture"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// pg is the database of the package: POSTGRES_DSN or a container, migrated once.
var pg *testfixture.Postgres

func TestMain(m *testing.M) {
	pg = testfixture.MustStartPostgres()
	if err := postgres.Migrate(context.Background(), config.PostgresConfig{DSN: pg.DSN, MaxConns: 5}); err != nil {
		pg.Close()
		log.Fatalf("migrate test database: %v", err)
	}
	defer pg.Close()
	m.Run()
}
