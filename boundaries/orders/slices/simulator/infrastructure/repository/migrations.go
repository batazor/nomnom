package repository

import (
	"embed"

	"nomnom/orders/infrastructure/migration"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations is the migration set of the simulator slice: simulator_state.
var Migrations = migration.Set{ID: migrationsID, Files: migrations}

// migrationsID names the data of the set in the schema contract (version
// table schema_migrations_simulator); it is not derived from this package or
// slice and changes only with a migration of its version table.
const migrationsID = "simulator"
