// Command orders-migrate применяет миграции PostgreSQL orders и завершается.
// В Kubernetes запускается init container'ом каждого workload'а; goose берёт
// advisory lock, поэтому параллельные запуски сериализуются.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"nomnom/orders/di"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
)

func main() {
	// JSON logger процесса — до конфигурации, чтобы и ошибки загрузки шли по схеме логов.
	telemetry.SetupLogging(di.MigratorServiceName)
	if err := run(); err != nil {
		slog.Error("orders-migrate stopped", telemetry.LogErrorKey, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := di.InitializeMigrator(cfg).Run(ctx); err != nil {
		return err
	}
	slog.InfoContext(ctx, "orders migrations applied")
	return nil
}
