// Package schema — единственное место, где composition root перечисляет наборы
// миграций базы orders (ADR 39): явный упорядоченный список без автопоиска,
// набор pkg (общие таблицы) первым, затем слайсы. Его применяют процессы
// (корневой di) и шаблонная база тестов (postgrestest).
package schema

import (
	"context"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/pkg/migration"
	orderpostgres "nomnom/orders/slices/order/infrastructure/postgres"
	simulatorpostgres "nomnom/orders/slices/simulator/infrastructure/postgres"
)

// Migrate приводит схему к наборам под одним advisory lock процесса. У
// fulfillment таблиц нет. Новый слайс с таблицами добавляет сюда свой набор.
func Migrate(ctx context.Context, cfg config.PostgresConfig) error {
	return migration.Apply(ctx, cfg,
		messaging.Migrations,
		orderpostgres.Migrations,
		simulatorpostgres.Migrations,
	)
}
