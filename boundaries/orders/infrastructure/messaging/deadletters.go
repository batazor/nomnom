package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IBM/sarama"

	"nomnom/orders/pkg/config"
)

// RedrivenFromMetadata names the DLQ record a redriven message came from:
// <stream>.dlq/<partition>@<offset>. A message that fails again carries it
// into its new dead letter.
const RedrivenFromMetadata = "redriven_from"

// watermillUUIDHeader is set by the Watermill DLQ publisher; publishers of
// the stream never set it, so a redriven record does not carry it either.
const watermillUUIDHeader = "_watermill_message_uuid"

// ResolvedGroupSuffix names the Kafka consumer group that tracks the resolved
// dead letters of a consumer group: <group>.dlq. Its committed offset on
// <stream>.dlq is the first dead letter not yet redriven or discarded, so its
// lag is the unresolved DLQ backlog of the group.
const ResolvedGroupSuffix = ".dlq"

// DeadLetter is one record of <stream>.dlq.
type DeadLetter struct {
	Partition int32
	Offset    int64
	// DeadLetteredAt is the time of the DLQ record.
	DeadLetteredAt time.Time
	Key            []byte
	Value          []byte
	// Headers are the headers of the dead letter: the envelope of the event and
	// the dead letter metadata.
	Headers map[string]string
}

// Group is the consumer group that dead-lettered the record.
func (l DeadLetter) Group() string { return l.Headers[GroupDeadLetteredMetadata] }

// Reason is why the consumer group rejected the record.
func (l DeadLetter) Reason() string { return l.Headers[ReasonDeadLetteredMetadata] }

// Through bounds an operation to one partition of the DLQ up to an offset
// inclusive.
type Through struct {
	Partition int32
	Offset    int64
}

// DeadLetters lists, redrives and discards the dead letters of one consumer
// group (ADR 28). Dead letters of other groups sharing the DLQ are passed
// over: each group resolves its own. The DLQ is read from the committed offset
// of the <group>.dlq group up to the end the DLQ had when the operation began.
type DeadLetters struct {
	client   sarama.Client
	producer sarama.SyncProducer
	group    string
}

func NewDeadLetters(cfg config.KafkaConfig) (*DeadLetters, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	saramaConfig := sarama.NewConfig()
	saramaConfig.ClientID = cfg.ClientID
	saramaConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	saramaConfig.Consumer.Offsets.AutoCommit.Enable = false
	saramaConfig.Consumer.Return.Errors = true
	saramaConfig.Producer.RequiredAcks = sarama.WaitForAll
	saramaConfig.Producer.Idempotent = true
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Net.MaxOpenRequests = 1
	client, err := sarama.NewClient(cfg.Brokers, saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("connect kafka: %w", err)
	}
	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create redrive producer: %w", err), client.Close())
	}
	return &DeadLetters{client: client, producer: producer, group: cfg.ConsumerGroup}, nil
}

func (d *DeadLetters) Close() error {
	return errors.Join(d.producer.Close(), d.client.Close())
}

// List returns the unresolved dead letters of the group on the DLQ of stream.
// A DLQ that does not exist yet has none.
func (d *DeadLetters) List(ctx context.Context, stream string) ([]DeadLetter, error) {
	var letters []DeadLetter
	err := d.scan(ctx, stream, nil, false, func(letter DeadLetter) error {
		letters = append(letters, letter)
		return nil
	})
	return letters, err
}

// Redrive publishes the unresolved dead letters of the group back to stream
// with their key and envelope headers, without the dead letter metadata, and
// marks each resolved after the broker accepted it. Every consumer group of
// the stream receives the event again; the Inbox makes it a no-op for groups
// that already processed it. through nil redrives every partition.
func (d *DeadLetters) Redrive(ctx context.Context, stream string, through *Through) (int, error) {
	redriven := 0
	err := d.scan(ctx, stream, through, true, func(letter DeadLetter) error {
		if topic := letter.Headers[TopicDeadLetteredMetadata]; topic != stream {
			return fmt.Errorf("dead letter %d@%d came from topic %q, not %s", letter.Partition, letter.Offset, topic, stream)
		}
		record := &sarama.ProducerMessage{Topic: stream, Key: sarama.ByteEncoder(letter.Key), Value: sarama.ByteEncoder(letter.Value)}
		for key, value := range letter.Headers {
			switch key {
			case ReasonDeadLetteredMetadata, TopicDeadLetteredMetadata, GroupDeadLetteredMetadata,
				DeserializationErrorMetadata, RedrivenFromMetadata, watermillUUIDHeader:
				continue
			}
			record.Headers = append(record.Headers, sarama.RecordHeader{Key: []byte(key), Value: []byte(value)})
		}
		record.Headers = append(record.Headers, sarama.RecordHeader{
			Key:   []byte(RedrivenFromMetadata),
			Value: fmt.Appendf(nil, "%s/%d@%d", DeadLetterTopic(stream), letter.Partition, letter.Offset),
		})
		if _, _, err := d.producer.SendMessage(record); err != nil {
			return fmt.Errorf("redrive dead letter %d@%d to %s: %w", letter.Partition, letter.Offset, stream, err)
		}
		redriven++
		return nil
	})
	return redriven, err
}

// Discard marks the unresolved dead letters of the group on one partition up
// to through resolved without publishing them; seen receives each one first,
// so the caller records what was discarded.
func (d *DeadLetters) Discard(ctx context.Context, stream string, through Through, seen func(DeadLetter)) (int, error) {
	discarded := 0
	err := d.scan(ctx, stream, &through, true, func(letter DeadLetter) error {
		seen(letter)
		discarded++
		return nil
	})
	return discarded, err
}

// scan passes the unresolved dead letters of the group to fn in partition
// order. With resolve, the offset of <group>.dlq moves past every record fn
// accepted, records of other groups included, and is committed record by
// record: an interrupted operation resumes after the last resolved one.
func (d *DeadLetters) scan(ctx context.Context, stream string, through *Through, resolve bool, fn func(DeadLetter) error) error {
	topic := DeadLetterTopic(stream)
	partitions, err := d.client.Partitions(topic)
	if errors.Is(err, sarama.ErrUnknownTopicOrPartition) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("partitions of %s: %w", topic, err)
	}
	if through != nil && !contains(partitions, through.Partition) {
		return fmt.Errorf("%s has no partition %d", topic, through.Partition)
	}
	consumer, err := sarama.NewConsumerFromClient(d.client)
	if err != nil {
		return fmt.Errorf("consume %s: %w", topic, err)
	}
	defer func() { _ = consumer.Close() }()

	for _, partition := range partitions {
		if through != nil && partition != through.Partition {
			continue
		}
		if err := d.scanPartition(ctx, topic, partition, through, resolve, consumer, fn); err != nil {
			return err
		}
	}
	return nil
}

func (d *DeadLetters) scanPartition(
	ctx context.Context,
	topic string,
	partition int32,
	through *Through,
	resolve bool,
	consumer sarama.Consumer,
	fn func(DeadLetter) error,
) error {
	end, err := d.client.GetOffset(topic, partition, sarama.OffsetNewest)
	if err != nil {
		return fmt.Errorf("end of %s/%d: %w", topic, partition, err)
	}
	if through != nil && through.Offset+1 < end {
		end = through.Offset + 1
	}
	next, err := d.resolvedOffset(topic, partition)
	if err != nil {
		return err
	}
	if next < 0 {
		if next, err = d.client.GetOffset(topic, partition, sarama.OffsetOldest); err != nil {
			return fmt.Errorf("start of %s/%d: %w", topic, partition, err)
		}
	}
	if next >= end {
		return nil
	}
	records, err := consumer.ConsumePartition(topic, partition, next)
	if err != nil {
		return fmt.Errorf("consume %s/%d from %d: %w", topic, partition, next, err)
	}
	defer func() { _ = records.Close() }()

	for {
		var record *sarama.ConsumerMessage
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-records.Errors():
			return fmt.Errorf("read %s/%d: %w", topic, partition, err.Err)
		case record = <-records.Messages():
		}
		letter := deadLetter(record)
		if letter.Group() == d.group {
			if err := fn(letter); err != nil {
				return err
			}
		}
		if resolve {
			if err := d.resolve(topic, partition, record.Offset+1); err != nil {
				return err
			}
		}
		if record.Offset+1 >= end {
			return nil
		}
	}
}

func (d *DeadLetters) resolvedGroup() string { return d.group + ResolvedGroupSuffix }

// resolvedOffset is the committed offset of <group>.dlq; -1 — nothing resolved yet.
func (d *DeadLetters) resolvedOffset(topic string, partition int32) (int64, error) {
	var offset int64
	err := d.withCoordinator(func(coordinator *sarama.Broker) error {
		request := &sarama.OffsetFetchRequest{Version: 1, ConsumerGroup: d.resolvedGroup()}
		request.AddPartition(topic, partition)
		response, err := coordinator.FetchOffset(request)
		if err != nil {
			return err
		}
		block := response.GetBlock(topic, partition)
		if block == nil {
			return errors.New("no offset in the response")
		}
		if !errors.Is(block.Err, sarama.ErrNoError) {
			return block.Err
		}
		offset = block.Offset
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("fetch offset of %s on %s/%d: %w", d.resolvedGroup(), topic, partition, err)
	}
	return offset, nil
}

// resolve commits next as the first unresolved offset of <group>.dlq. The
// group has no members: the commit is a simple one, outside any generation.
func (d *DeadLetters) resolve(topic string, partition int32, next int64) error {
	err := d.withCoordinator(func(coordinator *sarama.Broker) error {
		request := &sarama.OffsetCommitRequest{
			Version:                 2,
			ConsumerGroup:           d.resolvedGroup(),
			ConsumerGroupGeneration: sarama.GroupGenerationUndefined,
			RetentionTime:           -1,
		}
		request.AddBlock(topic, partition, next, 0, "")
		response, err := coordinator.CommitOffset(request)
		if err != nil {
			return err
		}
		if kerr := response.Errors[topic][partition]; !errors.Is(kerr, sarama.ErrNoError) {
			return kerr
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("resolve %s/%d before %d: %w", topic, partition, next, err)
	}
	return nil
}

// coordinatorAttempts bounds the calls to the group coordinator while it
// moves or loads the offsets of <group>.dlq, as after the first use of the group.
const coordinatorAttempts = 10

// withCoordinator calls fn on the coordinator of <group>.dlq and finds the
// coordinator again when the broker answers that it is not or not yet one.
func (d *DeadLetters) withCoordinator(fn func(*sarama.Broker) error) error {
	var err error
	for attempt := range coordinatorAttempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
			if refreshErr := d.client.RefreshCoordinator(d.resolvedGroup()); refreshErr != nil {
				err = refreshErr
				continue
			}
		}
		coordinator, coordinatorErr := d.client.Coordinator(d.resolvedGroup())
		if coordinatorErr != nil {
			err = coordinatorErr
			continue
		}
		err = fn(coordinator)
		if !errors.Is(err, sarama.ErrNotCoordinatorForConsumer) &&
			!errors.Is(err, sarama.ErrConsumerCoordinatorNotAvailable) &&
			!errors.Is(err, sarama.ErrOffsetsLoadInProgress) {
			return err
		}
	}
	return fmt.Errorf("coordinator of %s after %d attempts: %w", d.resolvedGroup(), coordinatorAttempts, err)
}

func deadLetter(record *sarama.ConsumerMessage) DeadLetter {
	letter := DeadLetter{
		Partition:      record.Partition,
		Offset:         record.Offset,
		DeadLetteredAt: record.Timestamp.UTC(),
		Key:            record.Key,
		Value:          record.Value,
		Headers:        make(map[string]string, len(record.Headers)),
	}
	for _, header := range record.Headers {
		if header != nil {
			letter.Headers[string(header.Key)] = string(header.Value)
		}
	}
	return letter
}

func contains(partitions []int32, partition int32) bool {
	for _, p := range partitions {
		if p == partition {
			return true
		}
	}
	return false
}
