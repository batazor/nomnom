// Command orders-worker исполняет Order Fulfillment Workflow: опрашивает task
// queue orders в Temporal и выполняет workflow и Activities слайса fulfillment.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"nomnom/orders/di"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/lifecycle"
	"nomnom/orders/pkg/telemetry"
)

func main() {
	// JSON logger процесса — до конфигурации, чтобы и ошибки загрузки шли по схеме логов.
	telemetry.SetupLogging(di.WorkerServiceName)
	if err := run(); err != nil {
		slog.Error("orders-worker stopped", telemetry.LogErrorKey, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
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
