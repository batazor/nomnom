package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	watermillsql "github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/domains/order"
)

const (
	// OutboxTopic is the watermill-sql topic read by the Forwarder (ADR 13).
	OutboxTopic = "orders_outbox"
	// ForwarderHandlerName distinguishes the Forwarder from consumer handlers in
	// handler_name of Watermill router metrics (ADR 28).
	ForwarderHandlerName = "orders-outbox-forwarder"
	// OrderEventsTopic is the Kafka stream of Integration Events of orders.
	OrderEventsTopic = "orders.v1.order-events"

	aggregateType = "order"
)

// eventNamespace makes event_id deterministic per (Order, version, index):
// a repeated write of the same change keeps its identity.
var eventNamespace = uuid.MustParse("5b0e5f6c-6a0e-4f63-9d1e-6f1c3a9e2b10")

// ErrOutboxOutsideTransaction: Outbox records exist only with a business change.
var ErrOutboxOutsideTransaction = errors.New("outbox append requires an active unit of work")

// Append maps Domain Events to Integration Events and stores them in the
// active transaction; the Forwarder publishes them after commit.
func (s *Store) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, ok := transaction(ctx)
	if !ok {
		return ErrOutboxOutsideTransaction
	}
	messages := make([]*message.Message, 0, len(events))
	for index, event := range events {
		msg, err := integrationMessage(aggregateID, aggregateVersion, index, event)
		if err != nil {
			return err
		}
		messages = append(messages, msg)
	}

	publisher, err := watermillsql.NewPublisher(watermillsql.TxFromPgx(tx), watermillsql.PublisherConfig{
		SchemaAdapter: watermillsql.DefaultPostgreSQLSchema{},
	}, watermill.NopLogger{})
	if err != nil {
		return fmt.Errorf("outbox publisher: %w", err)
	}
	outbox := forwarder.NewPublisher(publisher, forwarder.PublisherConfig{ForwarderTopic: OutboxTopic})
	if err := outbox.Publish(OrderEventsTopic, messages...); err != nil {
		return fmt.Errorf("append outbox of order %s: %w", aggregateID, err)
	}
	return nil
}

func integrationMessage(id order.ID, version uint64, index int, event order.DomainEvent) (*message.Message, error) {
	payload, occurredAt, err := contract(event)
	if err != nil {
		return nil, err
	}
	envelope := messaging.Envelope{
		EventID:          uuid.NewSHA1(eventNamespace, []byte(string(id)+":"+strconv.FormatUint(version, 10)+":"+strconv.Itoa(index))).String(),
		OccurredAt:       occurredAt,
		AggregateType:    aggregateType,
		AggregateID:      string(id),
		AggregateVersion: version,
	}
	msg, err := messaging.NewIntegrationEventMessage(envelope, payload)
	if err != nil {
		return nil, fmt.Errorf("integration event of order %s: %w", id, err)
	}
	msg.Metadata.Set(messaging.PartitionKeyMetadata, string(id))
	return msg, nil
}

// contract is the infrastructure mapper Domain Event → Integration Event (ADR 25).
func contract(event order.DomainEvent) (proto.Message, time.Time, error) {
	switch e := event.(type) {
	case order.OrderPlaced:
		return &ordersv1.OrderPlaced{
			OrderId:      string(e.OrderID),
			CustomerId:   string(e.CustomerID),
			RestaurantId: string(e.RestaurantID),
			Total:        &ordersv1.Money{Amount: e.Total.Amount().String(), Currency: e.Total.Currency()},
			PlacedAt:     timestamppb.New(e.PlacedAt),
		}, e.PlacedAt, nil
	case order.OrderCompleted:
		return &ordersv1.OrderCompleted{OrderId: string(e.OrderID)}, time.Now().UTC(), nil
	case order.OrderCancelled:
		return &ordersv1.OrderCancelled{
			OrderId: string(e.OrderID),
			Cancellation: &ordersv1.Cancellation{
				Outcome:     string(e.Reason.Outcome()),
				ReasonCode:  e.Reason.Code(),
				CancelledAt: timestamppb.New(e.RequestedAt),
			},
		}, e.RequestedAt, nil
	default:
		return nil, time.Time{}, fmt.Errorf("no integration event for domain event %T", event)
	}
}

// NewForwarder moves Outbox records to the Kafka publisher. It must run as a
// single active instance (ADR 13).
func NewForwarder(s *Store, out message.Publisher, logger watermill.LoggerAdapter) (*forwarder.Forwarder, error) {
	subscriber, err := watermillsql.NewSubscriber(watermillsql.BeginnerFromPgx(s.pool), watermillsql.SubscriberConfig{
		ConsumerGroup:  "orders-forwarder",
		SchemaAdapter:  watermillsql.DefaultPostgreSQLSchema{},
		OffsetsAdapter: watermillsql.DefaultPostgreSQLOffsetsAdapter{},
		PollInterval:   200 * time.Millisecond,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("outbox subscriber: %w", err)
	}
	fwd, err := forwarder.NewForwarder(subscriber, out, logger, forwarder.Config{ForwarderTopic: OutboxTopic, HandlerName: ForwarderHandlerName})
	if err != nil {
		return nil, fmt.Errorf("outbox forwarder: %w", err)
	}
	return fwd, nil
}
