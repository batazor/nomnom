package repository

import (
	"embed"

	"nomnom/orders/infrastructure/migration"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations is the migration set of the order slice: orders, order_items,
// order_transitions, inbox, idempotency_keys and the Outbox orders_outbox with its CDC
// publication (ADR 37) — the tables of the repositories in this package.
var Migrations = migration.Set{ID: migrationsID, Files: migrations}

// migrationsID names the data of the set in the schema contract (version
// table schema_migrations_orders); it is not derived from this package or
// slice and changes only with a migration of its version table.
const migrationsID = "orders"
