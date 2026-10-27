package di

import (
	"context"

	"github.com/batazor/nomnom/boundaries/controlplane/di/schema"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/repository"
)

// NewStore — store процесса; без POSTGRES__DSN процесс не стартует. Сначала
// процесс приводит схему к встроенным наборам миграций (ADR 42): применённые
// golang-migrate пропускает, недостающие применяет под advisory lock, так что
// одновременный старт pods сериализуется. Ошибка миграции останавливает процесс.
func NewStore(cfg database.Config) (*repository.Store, func(), error) {
	if err := schema.Migrate(context.Background(), cfg); err != nil {
		return nil, nil, err
	}
	store, err := repository.Open(context.Background(), cfg)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}
