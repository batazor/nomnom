package di

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"
	watermillmetrics "github.com/ThreeDotsLabs/watermill/components/metrics"
	"github.com/ThreeDotsLabs/watermill/message"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// OutboxForwarder — lifecycle.Worker процесса orders-forwarder (ADR 13):
// переносит Integration Events из Outbox PostgreSQL в Kafka. Запускается одним
// активным экземпляром; replicas — только failover.
type OutboxForwarder struct {
	store      *postgres.Store
	kafka      config.KafkaConfig
	logger     watermill.LoggerAdapter
	newKafka   func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Publisher, error)
	newMetrics func(ctx context.Context, serviceName string) (*telemetry.Metrics, error)
	metrics    *telemetry.Metrics
	publisher  message.Publisher
	forwarder  *forwarder.Forwarder
	cancel     context.CancelFunc
	done       chan error
}

func NewOutboxForwarder(cfg config.Config, store *postgres.Store) *OutboxForwarder {
	return &OutboxForwarder{
		store: store,
		kafka: cfg.Kafka,
		// Watermill пишет через JSON logger процесса (telemetry.SetupLogging).
		logger: watermill.NewSlogLogger(slog.Default()),
		newKafka: func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Publisher, error) {
			return messaging.NewPublisher(cfg, logger)
		},
		newMetrics: telemetry.Start,
		done:       make(chan error, 1),
	}
}

// ForwarderServiceName — service.name процесса по умолчанию; OTEL_SERVICE_NAME переопределяет.
const ForwarderServiceName = "orders-forwarder"

// Start поднимает telemetry, подключает Kafka publisher и собирает Forwarder;
// poll начинается в Run. Telemetry — первой: propagator и TracerProvider
// otelsarama берёт при создании publisher'а, и producer span продолжает
// traceparent, записанный в Outbox вместе с событием.
func (f *OutboxForwarder) Start(ctx context.Context) error {
	metrics, err := f.newMetrics(ctx, ForwarderServiceName)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	f.metrics = metrics

	publisher, err := f.newKafka(f.kafka, f.logger)
	if err != nil {
		return errors.Join(fmt.Errorf("kafka publisher: %w", err), f.flushMetrics(ctx))
	}
	// Встроенные метрики Watermill роли outbox_forwarder: publish в Kafka и
	// выполнение handler'а, через Prometheus bridge в общий OTLP pipeline.
	builtin := watermillmetrics.NewPrometheusMetricsBuilder(metrics.Registry, messaging.WatermillMetricsNamespace, messaging.ForwarderMetricsSubsystem)
	decorated, err := builtin.DecoratePublisher(publisher)
	if err != nil {
		return errors.Join(fmt.Errorf("decorate kafka publisher: %w", err), publisher.Close(), f.flushMetrics(ctx))
	}
	fwd, err := postgres.NewForwarder(f.store, decorated, f.logger, builtin.NewRouterMiddleware().Middleware)
	if err != nil {
		return errors.Join(err, publisher.Close(), f.flushMetrics(ctx))
	}
	f.publisher, f.forwarder = decorated, fwd
	slog.InfoContext(ctx, "orders-forwarder started",
		slog.String("nomnom.outbox.topic", postgres.OutboxTopic), slog.String("messaging.destination.name", outbound.OrderEventsTopic))
	return nil
}

// flushMetrics выполняет финальный export telemetry после остановки Forwarder.
func (f *OutboxForwarder) flushMetrics(ctx context.Context) error {
	if f.metrics == nil {
		return nil
	}
	return f.metrics.Shutdown(ctx)
}

func (f *OutboxForwarder) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	err := f.forwarder.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Shutdown закрывает Forwarder (начатая пересылка завершается) и publisher.
func (f *OutboxForwarder) Shutdown(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		err := f.forwarder.Close()
		if f.cancel != nil {
			f.cancel()
		}
		done <- errors.Join(err, f.publisher.Close(), f.flushMetrics(ctx))
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("stop outbox forwarder: %w", ctx.Err())
	}
}
