// Package schema — единственное место, где composition root перечисляет наборы
// миграций базы controlplane (ADR 42, ADR 39): явный упорядоченный список без
// автопоиска, по одному набору на слайс с таблицами. Его применяют процесс
// (корневой di) и тесты репозиториев.
package schema

import (
	"context"

	"github.com/batazor/nomnom/boundaries/controlplane/infrastructure/migration"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
	topologyrepository "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/repository"
)

// Migrate приводит схему к наборам под одним advisory lock процесса.
func Migrate(ctx context.Context, cfg database.Config) error {
	return migration.Apply(ctx, cfg, topologyrepository.Migrations)
}
