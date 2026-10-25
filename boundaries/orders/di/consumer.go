package di

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"go.temporal.io/sdk/client"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/pkg/telemetry"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	orderdi "nomnom/orders/slices/order/di"
	ordermessaging "nomnom/orders/slices/order/infrastructure/messaging"
)

// KafkaConsumer — lifecycle.Worker процесса orders-consumer (ADR 28): в Start
// подключается к Temporal и Kafka и собирает Watermill Router с handler groups
// слайса order; Shutdown закрывает Router, дожидаясь начатых сообщений.
type KafkaConsumer struct {
	cfg      config.Config
	handlers orderdi.Consumer

	dial          func(ctx context.Context, options client.Options) (client.Client, error)
	newSubscriber func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Subscriber, error)
	newPoison     func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Publisher, error)
	newMetrics    func(ctx context.Context, serviceName string) (*telemetry.Metrics, error)
	logger        watermill.LoggerAdapter

	metrics    *telemetry.Metrics
	temporal   client.Client
	subscriber message.Subscriber
	poison     message.Publisher
	router     *message.Router
	done       chan error
}

// NewKafkaConsumer собирает consumer поверх handlers слайса order; store
// handlers — общий PostgreSQL orders.
func NewKafkaConsumer(cfg config.Config, handlers orderdi.Consumer) *KafkaConsumer {
	return &KafkaConsumer{
		cfg:      cfg,
		handlers: handlers,
		dial:     client.DialContext,
		newSubscriber: func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Subscriber, error) {
			return messaging.NewSubscriber(cfg, logger)
		},
		newPoison: func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Publisher, error) {
			return messaging.NewDeadLetterPublisher(cfg, logger)
		},
		newMetrics: telemetry.Start,
		// slog.Default writes through the standard log package, as the process does.
		logger: watermill.NewSlogLogger(slog.Default()),
		done:   make(chan error, 1),
	}
}

// ConsumerServiceName — service.name процесса по умолчанию; OTEL_SERVICE_NAME переопределяет.
const ConsumerServiceName = "orders-consumer"

func (c *KafkaConsumer) Start(ctx context.Context) error {
	metrics, err := c.newMetrics(ctx, ConsumerServiceName)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	c.metrics = metrics

	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.Temporal.ConnectTimeout)
	defer cancel()
	temporal, err := c.dial(dialCtx, client.Options{
		HostPort:       c.cfg.Temporal.HostPort,
		Namespace:      c.cfg.Temporal.Namespace,
		MetricsHandler: metrics.TemporalMetricsHandler(),
	})
	if err != nil {
		return fmt.Errorf("connect temporal %s: %w", c.cfg.Temporal.HostPort, err)
	}
	c.temporal = temporal

	if c.subscriber, err = c.newSubscriber(c.cfg.Kafka, c.logger); err != nil {
		return errors.Join(err, c.closeResources())
	}
	if c.poison, err = c.newPoison(c.cfg.Kafka, c.logger); err != nil {
		return errors.Join(err, c.closeResources())
	}

	workflow := fulfillmenttemporal.NewClient(temporal, NewFulfillmentPolicy(c.cfg.Fulfillment))
	streams := ordermessaging.Streams(c.handlers.Handlers(workflow))
	c.router, err = messaging.NewConsumerRouter(messaging.ConsumerConfig{
		ConsumerGroup: c.cfg.Kafka.ConsumerGroup,
		Retry:         messaging.DefaultRetryPolicy,
		CloseTimeout:  c.cfg.Shutdown.DrainTimeout,
		Prometheus:    c.metrics.Registry,
	}, c.subscriber, c.poison, streams, c.logger)
	if err != nil {
		return errors.Join(err, c.closeResources())
	}

	go func() { c.done <- c.router.Run(context.Background()) }()
	select {
	case <-c.router.Running():
		log.Printf("orders-consumer consuming %d streams in group %s", len(streams), c.cfg.Kafka.ConsumerGroup)
		return nil
	case err := <-c.done:
		return errors.Join(fmt.Errorf("run router: %w", err), c.closeResources())
	case <-ctx.Done():
		return errors.Join(ctx.Err(), c.router.Close(), c.closeResources())
	}
}

// Run ждёт остановки Router: штатной после Shutdown или аварийной.
func (c *KafkaConsumer) Run() error {
	if err := <-c.done; err != nil {
		return fmt.Errorf("kafka consumer failed: %w", err)
	}
	return nil
}

// Shutdown прекращает чтение и ждёт начатые сообщения до CloseTimeout Router;
// offsets подтверждаются только для сообщений, обработанных до commit.
func (c *KafkaConsumer) Shutdown(ctx context.Context) error {
	stopped := make(chan error, 1)
	go func() { stopped <- errors.Join(c.router.Close(), c.closeResources(), c.flushMetrics(ctx)) }()
	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
		return fmt.Errorf("stop kafka consumer: %w", ctx.Err())
	}
}

// flushMetrics выполняет финальный export метрик после drain Router.
func (c *KafkaConsumer) flushMetrics(ctx context.Context) error {
	if c.metrics == nil {
		return nil
	}
	return c.metrics.Shutdown(ctx)
}

func (c *KafkaConsumer) closeResources() error {
	var errs []error
	if c.subscriber != nil {
		errs = append(errs, c.subscriber.Close())
	}
	if c.poison != nil {
		errs = append(errs, c.poison.Close())
	}
	if c.temporal != nil {
		c.temporal.Close()
	}
	return errors.Join(errs...)
}
