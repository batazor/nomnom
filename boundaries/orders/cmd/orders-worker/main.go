// Command orders-worker исполняет Order Fulfillment Workflow: опрашивает task
// queue orders в Temporal и выполняет workflow и Activities слайса fulfillment.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	// GOMEMLIMIT = 90% memory limit cgroup контейнера; явная переменная GOMEMLIMIT имеет приоритет,
	// без limit — no-op. GOMAXPROCS Go 1.25+ выводит из CPU limit сам.
	_ "github.com/KimMachineGun/automemlimit"

	"nomnom/orders/di"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/lifecycle"
	"nomnom/orders/pkg/telemetry"
)

func main() {
	// JSON logger процесса — до конфигурации, чтобы и ошибки загрузки шли по схеме логов;
	// после загрузки его заменяет logger из spec.telemetry и spec.logging.
	telemetry.SetupStartupLogging(config.RoleWorker)
	if err := run(); err != nil {
		slog.Error("orders-worker stopped", telemetry.LogErrorKey, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(config.RoleWorker)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if _, err := telemetry.SetupLogging(context.Background(), cfg.Observability); err != nil {
		return fmt.Errorf("setup logging: %w", err)
	}

	worker, cleanup, err := di.InitializeWorker(cfg)
	if err != nil {
		return fmt.Errorf("initialize worker: %w", err)
	}
	defer cleanup()

	return lifecycle.New(lifecycle.Config{
		ManagementAddr: fmt.Sprintf(":%d", cfg.Management.Port),
		ReadinessDelay: cfg.Shutdown.ReadinessDelay,
		DrainTimeout:   cfg.Shutdown.DrainTimeout,
	}).Run(context.Background(), worker)
}
