package repository

import (
	"embed"

	"github.com/batazor/nomnom/boundaries/controlplane/infrastructure/migration"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations is the migration set of the topology slice: cells and markets.
var Migrations = migration.Set{ID: migrationsID, Files: migrations}

// migrationsID names the data of the set in the schema contract (version
// table schema_migrations_topology); it is not derived from this package or
// slice and changes only with a migration of its version table.
const migrationsID = "topology"
