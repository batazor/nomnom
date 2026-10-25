package di

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"
	"github.com/ThreeDotsLabs/watermill/message"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// OutboxForwarder — lifecycle.Worker процесса orders-forwarder (ADR 13):
// переносит Integration Events из Outbox PostgreSQL в Kafka. Запускается одним
// активным экземпляром; replicas — только failover.
type OutboxForwarder struct {
	store     *postgres.Store
	kafka     config.KafkaConfig
	logger    watermill.LoggerAdapter
	newKafka  func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Publisher, error)
	publisher message.Publisher
	forwarder *forwarder.Forwarder
	cancel    context.CancelFunc
	done      chan error
}

func NewOutboxForwarder(cfg config.Config, store *postgres.Store) *OutboxForwarder {
	return &OutboxForwarder{
		store:  store,
		kafka:  cfg.Kafka,
		logger: watermill.NewStdLogger(false, false),
		newKafka: func(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (message.Publisher, error) {
			return messaging.NewPublisher(cfg, logger)
		},
		done: make(chan error, 1),
	}
}

// Start подключает Kafka publisher и собирает Forwarder; poll начинается в Run.
func (f *OutboxForwarder) Start(context.Context) error {
	publisher, err := f.newKafka(f.kafka, f.logger)
	if err != nil {
		return fmt.Errorf("kafka publisher: %w", err)
	}
	fwd, err := postgres.NewForwarder(f.store, publisher, f.logger)
	if err != nil {
		_ = publisher.Close()
		return err
	}
	f.publisher, f.forwarder = publisher, fwd
	log.Printf("orders-forwarder: %s → kafka %s", postgres.OutboxTopic, postgres.OrderEventsTopic)
	return nil
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
		done <- errors.Join(err, f.publisher.Close())
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("stop outbox forwarder: %w", ctx.Err())
	}
}
