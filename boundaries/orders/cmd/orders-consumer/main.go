// Command orders-consumer читает integration events restaurants и delivery из
// Kafka и вызывает use cases слайса order через Watermill EventGroupProcessor
// (ADR 28): Inbox, UoW и ack только после commit.
package main

import (
	"context"
	"fmt"
	"log"

	"nomnom/orders/di"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/lifecycle"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
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
