package di

import (
	"context"

	"nomnom/orders/di/schema"
	"nomnom/orders/pkg/config/database"
	"nomnom/orders/slices/order/infrastructure/repository"
)

// NewStore — общий store API, worker'а, consumer'а и reconciler'а;
// без POSTGRES__DSN процесс не стартует. Сначала процесс приводит схему к
// встроенным наборам миграций (schema.Migrate): применённые golang-migrate
// пропускает, недостающие применяет под advisory lock, так что одновременный
// старт pods сериализуется. Ошибка миграции останавливает процесс до запуска
// остального. Pool подключается лениво.
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
