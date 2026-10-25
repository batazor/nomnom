package di

import (
	"context"
	"fmt"
	"log"
	"time"

	authzed "github.com/authzed/authzed-go/v1"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/postgres"
	"nomnom/orders/slices/order/infrastructure/reconcile"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

// RelationshipReconciler — lifecycle.Worker процесса orders-reconciler: сверяет
// order#customer в SpiceDB с заказами PostgreSQL сразу после старта и затем
// каждые interval. Ошибка прохода логируется; следующий проход повторит его.
type RelationshipReconciler struct {
	reconciler *reconcile.Reconciler
	interval   time.Duration
	cancel     context.CancelFunc
	ctx        context.Context
	done       chan struct{}
}

func NewRelationshipReconciler(cfg config.Config, store *postgres.Store, client *authzed.Client) *RelationshipReconciler {
	ctx, cancel := context.WithCancel(context.Background())
	return &RelationshipReconciler{
		reconciler: reconcile.New(client, store, cfg.Reconciler.Grace, cfg.Reconciler.PageSize),
		interval:   cfg.Reconciler.Interval,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
}

func (r *RelationshipReconciler) Start(context.Context) error { return nil }

func (r *RelationshipReconciler) Run() error {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		stats, err := r.reconciler.Reconcile(r.ctx)
		switch {
		case r.ctx.Err() != nil:
			return nil
		case err != nil:
			log.Printf("reconcile order#customer failed: %v", err)
		default:
			log.Printf("reconcile order#customer: checked=%d orphans_pending=%d orphans_deleted=%d missing_repaired=%d unrepairable=%d",
				stats.Checked, stats.OrphansPending, stats.OrphansDeleted, stats.MissingRepaired, stats.Unrepairable)
		}
		select {
		case <-r.ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Shutdown прерывает проход: операции идемпотентны, незавершённый проход
// повторит следующий экземпляр.
func (r *RelationshipReconciler) Shutdown(ctx context.Context) error {
	r.cancel()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// NewSpiceDBClient — клиент SpiceDB процесса; без endpoint процесс не стартует.
func NewSpiceDBClient(cfg config.SpiceDBConfig) (*authzed.Client, func(), error) {
	if cfg.Endpoint == "" {
		return nil, nil, fmt.Errorf("%s is required: orders-reconciler reads SpiceDB", config.EnvSpiceDBEndpoint)
	}
	client, err := spicedb.Dial(cfg)
	if err != nil {
		return nil, nil, err
	}
	return client, func() { _ = client.Close() }, nil
}
