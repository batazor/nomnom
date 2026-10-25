// Command orders-migrate применяет миграции PostgreSQL orders и завершается.
// В Kubernetes запускается init container'ом каждого workload'а; goose берёт
// advisory lock, поэтому параллельные запуски сериализуются.
package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"nomnom/orders/di"
	"nomnom/orders/pkg/config"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := di.InitializeMigrator(cfg).Run(ctx); err != nil {
		return err
	}
	log.Print("orders migrations applied")
	return nil
}
