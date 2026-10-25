package di

import (
	"context"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// Migrator — composition root процесса orders-migrate: применяет миграции
// PostgreSQL orders и завершается. Соединением владеет сам проход миграций.
type Migrator struct {
	cfg config.PostgresConfig
}

func NewMigrator(cfg config.PostgresConfig) *Migrator { return &Migrator{cfg: cfg} }

// Run применяет миграции; ctx отменяется сигналом остановки процесса.
func (m *Migrator) Run(ctx context.Context) error { return postgres.Migrate(ctx, m.cfg) }

// MigratorServiceName — service.name процесса orders-migrate по умолчанию;
// OTEL_SERVICE_NAME переопределяет. Migrate экспортирует только логи.
const MigratorServiceName = "orders-migrate"
