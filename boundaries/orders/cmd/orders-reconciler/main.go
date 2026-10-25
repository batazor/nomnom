// Command orders-reconciler сверяет relationships order#customer, #restaurant,
// #courier и #delivery в SpiceDB с заказами PostgreSQL (docs/security.md). Работает одним экземпляром.
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
	// JSON logger процесса — до конфигурации, чтобы и ошибки загрузки шли по схеме логов.
	telemetry.SetupLogging(di.ReconcilerServiceName)
	if err := run(); err != nil {
		slog.Error("orders-reconciler stopped", telemetry.LogErrorKey, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	reconciler, cleanup, err := di.InitializeReconciler(cfg)
	if err != nil {
		return fmt.Errorf("initialize reconciler: %w", err)
	}
	defer cleanup()

	return lifecycle.New(lifecycle.Config{
		ManagementAddr: fmt.Sprintf(":%d", cfg.Management.Port),
		ReadinessDelay: cfg.Shutdown.ReadinessDelay,
		DrainTimeout:   cfg.Shutdown.DrainTimeout,
	}).Run(context.Background(), reconciler)
}
