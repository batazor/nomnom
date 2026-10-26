// Command orders-admin — операторские действия orders, запускается разово в pod
// orders-consumer (kubectl exec), где уже есть конфигурация Kafka и PostgreSQL, а
// projections catch-up — ещё и hook Job chart после каждого install/upgrade:
//
//	orders-admin dlq list --stream <topic>
//	orders-admin dlq redrive --stream <topic> [--partition <p> --through <offset>]
//	orders-admin dlq discard --stream <topic> --partition <p> --through <offset> --reason <text>
//	orders-admin projections catch-up|rebuild order-details
//
// DLQ — dead letters consumer group процесса (ADR 28, runbook docs/dlq.md);
// проекции — OrderDetails (ADR orders 4).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/config/database"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/pkg/telemetry"
	ordermessaging "nomnom/orders/slices/order/infrastructure/messaging"
	"nomnom/orders/slices/order/infrastructure/repository"
)

const serviceName = "orders-admin"

// streams are the topics orders-consumer reads; only their DLQs are its own.
var streams = []string{ordermessaging.RestaurantOrderEventsTopic, ordermessaging.DeliveryEventsTopic}

const usage = `usage:
  orders-admin dlq list --stream <topic>
  orders-admin dlq redrive --stream <topic> [--partition <p> --through <offset>]
  orders-admin dlq discard --stream <topic> --partition <p> --through <offset> --reason <text>
  orders-admin projections catch-up|rebuild order-details`

func main() {
	telemetry.SetupLogging(serviceName)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		slog.Error("orders-admin failed", telemetry.LogErrorKey, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New(usage)
	}
	// Each command reads only the section it uses: the hook Job of the chart
	// runs projections with the database configuration alone.
	switch args[0] {
	case "dlq":
		cfg, err := config.LoadKafka()
		if err != nil {
			return fmt.Errorf("load kafka config: %w", err)
		}
		return deadLetters(ctx, cfg, args[1], args[2:], out)
	case "projections":
		cfg, err := config.LoadDatabase()
		if err != nil {
			return fmt.Errorf("load postgres config: %w", err)
		}
		return projections(ctx, cfg, args[1:])
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func deadLetters(ctx context.Context, cfg config.KafkaConfig, action string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dlq "+action, flag.ContinueOnError)
	stream := flags.String("stream", "", "stream topic whose DLQ is handled")
	partition := flags.Int("partition", -1, "DLQ partition")
	through := flags.Int64("through", -1, "last DLQ offset of the partition, inclusive")
	reason := flags.String("reason", "", "why the dead letters are discarded")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !slices.Contains(streams, *stream) {
		return fmt.Errorf("--stream must be one of %v, got %q", streams, *stream)
	}
	bound, err := throughFlag(*partition, *through)
	if err != nil {
		return err
	}

	letters, err := messaging.NewDeadLetters(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = letters.Close() }()
	log := slog.With(slog.String("stream", *stream), slog.String("messaging.consumer.group.name", cfg.ConsumerGroup))

	switch action {
	case "list":
		if bound != nil {
			return errors.New("dlq list takes no --partition and --through")
		}
		found, err := letters.List(ctx, *stream)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(out)
		for _, letter := range found {
			if err := encoder.Encode(describe(letter)); err != nil {
				return err
			}
		}
		return nil
	case "redrive":
		redriven, err := letters.Redrive(ctx, *stream, bound)
		log.InfoContext(ctx, "dead letters redriven", slog.Int("count", redriven))
		return err
	case "discard":
		if bound == nil || *reason == "" {
			return errors.New("dlq discard requires --partition, --through and --reason")
		}
		discarded, err := letters.Discard(ctx, *stream, *bound, func(letter messaging.DeadLetter) {
			log.WarnContext(ctx, "dead letter discarded",
				slog.Any("dead_letter", describe(letter)), slog.String("discard_reason", *reason))
		})
		log.InfoContext(ctx, "dead letters discarded", slog.Int("count", discarded))
		return err
	default:
		return fmt.Errorf("unknown dlq action %q\n%s", action, usage)
	}
}

// throughFlag is the bound of --partition and --through: both or neither.
func throughFlag(partition int, through int64) (*messaging.Through, error) {
	switch {
	case partition < 0 && through < 0:
		return nil, nil
	case partition < 0 || through < 0:
		return nil, errors.New("--partition and --through go together")
	}
	return &messaging.Through{Partition: int32(partition), Offset: through}, nil //nolint:gosec // partitions are int32 in Kafka.
}

// deadLetterView is one line of dlq list.
type deadLetterView struct {
	Partition      int32     `json:"partition"`
	Offset         int64     `json:"offset"`
	DeadLetteredAt time.Time `json:"dead_lettered_at"`
	EventID        string    `json:"event_id"`
	EventType      string    `json:"event_type"`
	AggregateID    string    `json:"aggregate_id"`
	OccurredAt     string    `json:"occurred_at"`
	Reason         string    `json:"reason"`
	RedrivenFrom   string    `json:"redriven_from,omitempty"`
}

func describe(letter messaging.DeadLetter) deadLetterView {
	return deadLetterView{
		Partition:      letter.Partition,
		Offset:         letter.Offset,
		DeadLetteredAt: letter.DeadLetteredAt,
		EventID:        letter.Headers[messaging.EventIDMetadata],
		EventType:      letter.Headers[messaging.EventTypeMetadata],
		AggregateID:    letter.Headers[messaging.AggregateIDMetadata],
		OccurredAt:     letter.Headers[messaging.OccurredAtMetadata],
		Reason:         letter.Reason(),
		RedrivenFrom:   letter.Headers[messaging.RedrivenFromMetadata],
	}
}

func projections(ctx context.Context, cfg database.Config, args []string) error {
	if len(args) != 2 || args[1] != "order-details" {
		return errors.New(usage)
	}
	var scope repository.ProjectionScope
	switch args[0] {
	case "catch-up":
		scope = repository.ScopeStale
	case "rebuild":
		scope = repository.ScopeAll
	default:
		return fmt.Errorf("unknown projections action %q\n%s", args[0], usage)
	}
	// The schema is migrated by the running processes; a missing table fails
	// the command.
	store, err := repository.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	projected, err := store.ReprojectOrderDetails(ctx, scope)
	slog.InfoContext(ctx, "order details projected", slog.String("scope", string(scope)), slog.Int("orders", projected))
	return err
}
