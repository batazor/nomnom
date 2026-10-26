package messaging

import (
	"embed"

	"nomnom/orders/pkg/migration"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations is the pkg migration set: tables shared by the slices — the
// Outbox orders_outbox and its CDC publication (ADR 37). The composition root
// applies it before the sets of the slices.
var Migrations = migration.Set{Table: "schema_migrations_pkg", Files: migrations}
