package postgres_test

import (
	"context"
	"log"
	"testing"

	"nomnom/orders/di/schema"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/testfixture"
)

// pg is the database container of the package, migrated once.
var pg *testfixture.Postgres

func TestMain(m *testing.M) {
	pg = testfixture.MustStartPostgres()
	if err := schema.Migrate(context.Background(), config.PostgresConfig{DSN: pg.DSN, MaxConns: 5}); err != nil {
		pg.Close()
		log.Fatalf("migrate test database: %v", err)
	}
	defer pg.Close()
	m.Run()
}
