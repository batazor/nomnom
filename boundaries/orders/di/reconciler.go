package di

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	authzed "github.com/authzed/authzed-go/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/infrastructure/reconcile"
	"nomnom/orders/slices/order/infrastructure/repository"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

// RelationshipReconciler — lifecycle.Worker процесса orders-reconciler: сверяет
// relations order (customer, restaurant, delivery) в SpiceDB с заказами
// PostgreSQL сразу после старта и затем каждые interval. Тем же проходом догоняет
// проекцию OrderDetails (ADR orders 4): строки, которые старые pods изменили во
// время rolling update без проекции, отстают не дольше interval. Ошибка прохода
// логируется; следующий проход повторит его.
type RelationshipReconciler struct {
	reconciler *reconcile.Reconciler
	store      *repository.Store
	interval   time.Duration
	newMetrics func(ctx context.Context, serviceName string) (*telemetry.Metrics, error)
	metrics    *telemetry.Metrics
	tracer     trace.Tracer
	cancel     context.CancelFunc
	ctx        context.Context
	done       chan struct{}
}

func NewRelationshipReconciler(cfg config.Config, store *repository.Store, client *authzed.Client) *RelationshipReconciler {
	ctx, cancel := context.WithCancel(context.Background())
	return &RelationshipReconciler{
		reconciler: reconcile.New(client, store, cfg.Reconciler.Grace, cfg.Reconciler.PageSize),
		store:      store,
		interval:   cfg.Reconciler.Interval,
		newMetrics: telemetry.Start,
		tracer:     otel.Tracer(reconcilerInstrumentation),
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
}

// ReconcilerServiceName — service.name процесса по умолчанию; OTEL_SERVICE_NAME переопределяет.
const ReconcilerServiceName = "orders-reconciler"

const reconcilerInstrumentation = "nomnom/orders/di/reconciler"

// Start поднимает telemetry: spans прохода, PostgreSQL и SpiceDB, runtime-метрики.
func (r *RelationshipReconciler) Start(ctx context.Context) error {
	metrics, err := r.newMetrics(ctx, ReconcilerServiceName)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	r.metrics = metrics
	return nil
}

// pass — один проход сверки в своём root span: запросы PostgreSQL и вызовы
// SpiceDB прохода — его дочерние spans.
func (r *RelationshipReconciler) pass() {
	ctx, span := r.tracer.Start(r.ctx, "reconcile order relationships")
	defer span.End()
	stats, err := r.reconciler.Reconcile(ctx)
	switch {
	case r.ctx.Err() != nil:
	case err != nil:
		span.SetStatus(codes.Error, telemetry.ErrorType(err))
		slog.ErrorContext(ctx, "reconcile order relationships failed", telemetry.LogErrorKey, err)
	default:
		slog.InfoContext(ctx, "reconcile order relationships completed",
			slog.Int("checked", stats.Checked), slog.Int("orphans_pending", stats.OrphansPending),
			slog.Int("orphans_deleted", stats.OrphansDeleted), slog.Int("missing_repaired", stats.MissingRepaired),
			slog.Int("missing_superseded", stats.MissingSuperseded), slog.Int("unrepairable", stats.Unrepairable))
	}
}

// catchUp — catch-up проекции OrderDetails в своём root span.
func (r *RelationshipReconciler) catchUp() {
	ctx, span := r.tracer.Start(r.ctx, "catch up order details")
	defer span.End()
	projected, err := r.store.CatchUpOrderDetails(ctx)
	switch {
	case r.ctx.Err() != nil:
	case err != nil:
		span.SetStatus(codes.Error, telemetry.ErrorType(err))
		slog.ErrorContext(ctx, "catch up order details failed", telemetry.LogErrorKey, err)
	default:
		slog.InfoContext(ctx, "catch up order details completed", slog.Int("projected", projected))
	}
}

func (r *RelationshipReconciler) Run() error {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.pass()
		r.catchUp()
		if r.ctx.Err() != nil {
			return nil
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
		return r.flushMetrics(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// flushMetrics выполняет финальный export telemetry после остановки прохода.
func (r *RelationshipReconciler) flushMetrics(ctx context.Context) error {
	if r.metrics == nil {
		return nil
	}
	return r.metrics.Shutdown(ctx)
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
