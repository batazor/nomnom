package di

import (
	"context"
	"fmt"

	"nomnom/orders/di/schema"
	"nomnom/orders/pkg/config/database"
	"nomnom/orders/slices/order/infrastructure/repository"
)

// NewStore — общий store API, worker'а, consumer'а и reconciler'а;
// без POSTGRES__DSN процесс не стартует. Сначала процесс приводит схему к
// встроенным наборам миграций (schema.Migrate): применённые golang-migrate
// пропускает, недостающие применяет под advisory lock, так что одновременный
// старт pods сериализуется. Ошибка миграции останавливает процесс до запуска
// остального. Затем catch-up проекции OrderDetails: Orders без строки, со
// старой projection_version или другой версией Order проецируются заново
// (ADR orders 4); ошибка останавливает процесс так же, как ошибка миграции.
func NewStore(cfg database.Config) (*repository.Store, func(), error) {
	if err := schema.Migrate(context.Background(), cfg); err != nil {
		return nil, nil, err
	}
	store, err := repository.Open(context.Background(), cfg)
	if err != nil {
		return nil, nil, err
	}
	if _, err := store.CatchUpOrderDetails(context.Background()); err != nil {
		store.Close()
		return nil, nil, fmt.Errorf("catch up order details: %w", err)
	}
	return store, store.Close, nil
}
