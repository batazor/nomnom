// Command orders-worker исполняет Order Fulfillment Workflow: опрашивает task
// queue orders в Temporal и выполняет workflow и Activities fulfillorder.
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
