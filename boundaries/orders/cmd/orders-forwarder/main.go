// Command orders-forwarder доставляет Integration Events из Transactional
// Outbox PostgreSQL в Kafka (ADR 13). Работает одним активным экземпляром.
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
	forwarder, cleanup, err := di.InitializeForwarder(cfg)
	if err != nil {
		return fmt.Errorf("initialize forwarder: %w", err)
	}
	defer cleanup()

	return lifecycle.New(lifecycle.Config{
		ManagementAddr: fmt.Sprintf(":%d", cfg.Management.Port),
		ReadinessDelay: cfg.Shutdown.ReadinessDelay,
		DrainTimeout:   cfg.Shutdown.DrainTimeout,
	}).Run(context.Background(), forwarder)
}
