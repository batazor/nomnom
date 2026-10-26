package repository

import (
	"embed"

	"nomnom/orders/pkg/migration"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations is the migration set of the order slice: orders, order_items,
// order_facts, inbox, idempotency_keys and the Outbox orders_outbox with its CDC
// publication (ADR 37) — the tables of the repositories in this package.
var Migrations = migration.Set{Table: "schema_migrations_order", Files: migrations}
