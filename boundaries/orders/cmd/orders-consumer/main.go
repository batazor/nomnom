// Command orders-consumer читает integration events restaurants и delivery из
// Kafka и вызывает use cases слайса order через Watermill EventGroupProcessor
// (ADR 28): Inbox, UoW и ack только после commit.
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
	telemetry.SetupLogging(di.ConsumerServiceName)
	if err := run(); err != nil {
		slog.Error("orders-consumer stopped", telemetry.LogErrorKey, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	consumer, cleanup, err := di.InitializeConsumer(cfg)
	if err != nil {
		return fmt.Errorf("initialize consumer: %w", err)
	}
	defer cleanup()

	return lifecycle.New(lifecycle.Config{
		ManagementAddr: fmt.Sprintf(":%d", cfg.Management.Port),
		ReadinessDelay: cfg.Shutdown.ReadinessDelay,
		DrainTimeout:   cfg.Shutdown.DrainTimeout,
	}).Run(context.Background(), consumer)
}
