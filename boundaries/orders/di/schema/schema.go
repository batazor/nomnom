// Package schema — единственное место, где composition root перечисляет наборы
// миграций базы orders (ADR 39): явный упорядоченный список без автопоиска,
// по одному набору на слайс с таблицами. Его применяют процессы
// (корневой di) и шаблонная база тестов (repositorytest).
package schema

import (
	"context"

	"nomnom/orders/pkg/config/database"
	"nomnom/orders/pkg/migration"
	orderrepository "nomnom/orders/slices/order/infrastructure/repository"
	simulatorrepository "nomnom/orders/slices/simulator/infrastructure/repository"
)

// Migrate приводит схему к наборам под одним advisory lock процесса. У
// fulfillment таблиц нет. Новый слайс с таблицами добавляет сюда свой набор.
func Migrate(ctx context.Context, cfg database.Config) error {
	return migration.Apply(ctx, cfg,
		orderrepository.Migrations,
		simulatorrepository.Migrations,
	)
}
