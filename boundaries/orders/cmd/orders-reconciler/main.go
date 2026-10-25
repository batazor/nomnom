// Command orders-reconciler сверяет relationships order#customer, #restaurant,
// #courier и #delivery в SpiceDB с заказами PostgreSQL (docs/security.md). Работает одним экземпляром.
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
