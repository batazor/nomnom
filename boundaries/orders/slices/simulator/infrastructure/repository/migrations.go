package repository

import (
	"embed"

	"nomnom/orders/pkg/migration"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations is the migration set of the simulator slice: simulator_state.
var Migrations = migration.Set{Table: "schema_migrations_simulator", Files: migrations}
