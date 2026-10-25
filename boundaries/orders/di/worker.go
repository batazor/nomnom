package di

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/fulfillment/domains/fulfillment"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

// WorkerServiceName — service.name Temporal worker по умолчанию; OTEL_SERVICE_NAME переопределяет.
const WorkerServiceName = "orders-worker"

// TemporalWorker — lifecycle.Worker процесса orders-worker: подключается к
// Temporal в Start, опрашивает task queue orders и останавливает poll в Shutdown.
type TemporalWorker struct {
	cfg        config.TemporalConfig
	stage      bool
	drain      time.Duration
	activities *fulfillmenttemporal.Activities

	dial      func(ctx context.Context, options client.Options) (client.Client, error)
	newWorker func(c client.Client, options worker.Options) runner
	ensure    func(ctx context.Context, c client.Client) error

	newMetrics func(ctx context.Context, serviceName string) (*telemetry.Metrics, error)
	metrics    *telemetry.Metrics
	client     client.Client
	worker     runner
	fatal      chan error
	stopped    chan struct{}
	once       sync.Once
}

// runner — часть worker.Worker, которой управляет lifecycle.
type runner interface {
	fulfillmenttemporal.Registry
	Start() error
	Stop()
}

func NewTemporalWorker(cfg config.Config, activities *fulfillmenttemporal.Activities) *TemporalWorker {
	return &TemporalWorker{
		cfg:        cfg.Temporal,
		stage:      cfg.Fulfillment.PublishStage,
		drain:      cfg.Shutdown.DrainTimeout,
		activities: activities,
		dial:       client.DialContext,
		newMetrics: telemetry.Start,
		newWorker: func(c client.Client, options worker.Options) runner {
			return worker.New(c, fulfillmenttemporal.TaskQueue, options)
		},
		ensure: func(ctx context.Context, c client.Client) error {
			return fulfillmenttemporal.EnsureStageSearchAttribute(ctx, c.OperatorService(), cfg.Temporal.Namespace, cfg.Temporal.RegisterSearchAttributes)
		},
		fatal:   make(chan error, 1),
		stopped: make(chan struct{}),
	}
}

// Start подключается к Temporal, проверяет Search Attribute и начинает poll в
// пределах connect timeout; ошибка завершает процесс.
func (w *TemporalWorker) Start(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, w.cfg.ConnectTimeout)
	defer cancel()

	metrics, err := w.newMetrics(ctx, WorkerServiceName)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	w.metrics = metrics

	c, err := w.dial(ctx, client.Options{
		HostPort:       w.cfg.HostPort,
		Namespace:      w.cfg.Namespace,
		MetricsHandler: metrics.TemporalMetricsHandler(),
	})
	if err != nil {
		return errors.Join(fmt.Errorf("connect temporal %s: %w", w.cfg.HostPort, err), metrics.Shutdown(ctx))
	}
	if w.stage {
		if err := w.ensure(ctx, c); err != nil {
			c.Close()
			return err
		}
	}

	w.worker = w.newWorker(c, worker.Options{
		WorkerStopTimeout: w.drain,
		OnFatalError: func(err error) {
			select {
			case w.fatal <- err:
			default:
			}
		},
	})
	fulfillmenttemporal.Register(w.worker, w.activities)
	if err := w.worker.Start(); err != nil {
		c.Close()
		return fmt.Errorf("start temporal worker: %w", err)
	}
	w.client = c
	log.Printf("orders-worker polling task queue %s in namespace %s", fulfillmenttemporal.TaskQueue, w.cfg.Namespace)
	return nil
}

// Run ждёт Shutdown или фатальной ошибки worker'а.
func (w *TemporalWorker) Run() error {
	select {
	case <-w.stopped:
		return nil
	case err := <-w.fatal:
		return fmt.Errorf("temporal worker failed: %w", err)
	}
}

// Shutdown прекращает poll и ждёт начатые Activities до дедлайна ctx.
func (w *TemporalWorker) Shutdown(ctx context.Context) error {
	defer w.once.Do(func() { close(w.stopped) })
	done := make(chan struct{})
	go func() {
		w.worker.Stop()
		w.client.Close()
		close(done)
	}()
	defer func() {
		if w.metrics != nil {
			// Финальный export после остановки poll; ошибку flush не смешиваем с drain.
			if err := w.metrics.Shutdown(ctx); err != nil {
				log.Printf("flush metrics: %v", err)
			}
		}
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop temporal worker: %w", ctx.Err())
	}
}

// NewFulfillmentPolicy переводит конфигурацию во вход workflow.
func NewFulfillmentPolicy(cfg config.FulfillmentConfig) fulfillmenttemporal.Policy {
	return fulfillmenttemporal.Policy{
		Policy: fulfillment.Policy{
			AcceptanceTimeout:       cfg.AcceptanceTimeout,
			AssignmentTimeout:       cfg.AssignmentTimeout,
			MaxReplans:              cfg.MaxReplans,
			OperatorDecisionTimeout: cfg.OperatorDecisionTimeout,
			PreparationTimeout:      cfg.PreparationTimeout,
			MaxPreparationExtension: cfg.MaxPreparationExtension,
			PickupTimeout:           cfg.PickupTimeout,
			DeliveryTimeout:         cfg.DeliveryTimeout,
		},
		PublishStage: cfg.PublishStage,
	}
}
