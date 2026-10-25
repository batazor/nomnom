package di

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/infrastructure/memory"
	"nomnom/orders/slices/order/infrastructure/neighbours"
	"nomnom/orders/slices/order/infrastructure/neighbours/grpcclient"
	"nomnom/orders/slices/order/infrastructure/temporal/fulfillorder"
)

// TemporalWorker — lifecycle.Worker процесса orders-worker: подключается к
// Temporal в Start, опрашивает task queue orders и останавливает poll в Shutdown.
type TemporalWorker struct {
	cfg        config.TemporalConfig
	stage      bool
	drain      time.Duration
	activities *fulfillorder.Activities

	dial      func(ctx context.Context, options client.Options) (client.Client, error)
	newWorker func(c client.Client, options worker.Options) runner
	ensure    func(ctx context.Context, c client.Client) error

	client  client.Client
	worker  runner
	fatal   chan error
	stopped chan struct{}
	once    sync.Once
}

// runner — часть worker.Worker, которой управляет lifecycle.
type runner interface {
	fulfillorder.Registry
	Start() error
	Stop()
}

func NewTemporalWorker(cfg config.Config, activities *fulfillorder.Activities) *TemporalWorker {
	return &TemporalWorker{
		cfg:        cfg.Temporal,
		stage:      cfg.Fulfillment.PublishStage,
		drain:      cfg.Shutdown.DrainTimeout,
		activities: activities,
		dial:       client.DialContext,
		newWorker: func(c client.Client, options worker.Options) runner {
			return worker.New(c, fulfillorder.TaskQueue, options)
		},
		ensure: func(ctx context.Context, c client.Client) error {
			return fulfillorder.EnsureStageSearchAttribute(ctx, c.OperatorService(), cfg.Temporal.Namespace, cfg.Temporal.RegisterSearchAttributes)
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

	c, err := w.dial(ctx, client.Options{HostPort: w.cfg.HostPort, Namespace: w.cfg.Namespace})
	if err != nil {
		return fmt.Errorf("connect temporal %s: %w", w.cfg.HostPort, err)
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
	fulfillorder.Register(w.worker, w.activities)
	if err := w.worker.Start(); err != nil {
		c.Close()
		return fmt.Errorf("start temporal worker: %w", err)
	}
	w.client = c
	log.Printf("orders-worker polling task queue %s in namespace %s", fulfillorder.TaskQueue, w.cfg.Namespace)
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
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop temporal worker: %w", ctx.Err())
	}
}

// NewActivities собирает Activities fulfillment поверх store слайса и адаптеров
// соседних контекстов, выбранных конфигурацией; cleanup закрывает соединение
// с internal gateway.
func NewActivities(store *memory.Store, cfg config.Config) (*fulfillorder.Activities, func(), error) {
	activities := &fulfillorder.Activities{
		Orders:   store,
		Complete: completeorder.NewHandler(store, store, store),
		Finalize: failorcancelorder.NewHandler(store, store, store),
	}
	switch cfg.Fulfillment.Neighbours {
	case config.NeighboursGRPC:
		conn, err := grpcclient.Dial(cfg.InternalGateway.Address, cfg.Identity.TokenDir)
		if err != nil {
			return nil, nil, err
		}
		activities.Payments = grpcclient.NewPayments(conn)
		activities.Restaurants = grpcclient.NewRestaurants(conn)
		activities.Delivery = grpcclient.NewDelivery(conn)
		return activities, func() { _ = conn.Close() }, nil
	case config.NeighboursSimulated:
		log.Printf("WARNING: fulfillment neighbours are simulated: payments, restaurants and delivery calls are approved without effect")
		activities.Payments, activities.Restaurants, activities.Delivery = neighbours.Simulated{}, neighbours.Simulated{}, neighbours.Simulated{}
	default:
		activities.Payments, activities.Restaurants, activities.Delivery = neighbours.Unavailable{}, neighbours.Unavailable{}, neighbours.Unavailable{}
	}
	return activities, func() {}, nil
}

// NewFulfillmentPolicy переводит конфигурацию во вход workflow.
func NewFulfillmentPolicy(cfg config.FulfillmentConfig) fulfillorder.Policy {
	return fulfillorder.Policy{
		AcceptanceTimeout:       cfg.AcceptanceTimeout,
		AssignmentTimeout:       cfg.AssignmentTimeout,
		MaxReplans:              cfg.MaxReplans,
		OperatorDecisionTimeout: cfg.OperatorDecisionTimeout,
		PreparationTimeout:      cfg.PreparationTimeout,
		MaxPreparationExtension: cfg.MaxPreparationExtension,
		PickupTimeout:           cfg.PickupTimeout,
		DeliveryTimeout:         cfg.DeliveryTimeout,
		PublishStage:            cfg.PublishStage,
	}
}
