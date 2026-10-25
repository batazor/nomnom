// Package postgrestest gives tests of other packages the real PostgreSQL store
// of orders. The container (or POSTGRES_DSN) starts on the first Open of the
// test binary and is migrated once into a template database; every Open
// clones the template, so each test starts from an empty, isolated database
// and may use fixed identifiers such as "order-1".
//
//	func TestMain(m *testing.M) { postgrestest.Main(m) }
//
// Production code does not import this package.
package postgrestest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/pkg/testfixture"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

const maxConns = 5

var (
	startOnce sync.Once
	startErr  error
	server    *testfixture.Postgres
	template  string
	// cloneMu serializes CREATE DATABASE ... TEMPLATE: PostgreSQL refuses a
	// template another clone is reading.
	cloneMu   sync.Mutex
	databases atomic.Int64
)

// Main runs the tests of a package and removes the container afterwards; the
// test binary exits with the result of m.Run once TestMain returns.
func Main(m *testing.M) {
	m.Run()
	if server != nil {
		server.Close()
	}
}

func start() {
	ctx := context.Background()
	server, startErr = testfixture.StartPostgres(ctx)
	if startErr != nil {
		return
	}
	// The process id keeps templates of parallel test binaries apart when
	// they share POSTGRES_DSN.
	template = fmt.Sprintf("orders_template_%d", os.Getpid())
	if startErr = exec(ctx, "CREATE DATABASE "+pgx.Identifier{template}.Sanitize()); startErr != nil {
		return
	}
	startErr = postgres.Migrate(ctx, config.PostgresConfig{DSN: withDatabase(server.DSN, template), MaxConns: maxConns})
}

// exec runs an administrative statement on the server's own database.
func exec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, server.DSN)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("%s: %w", sql, err)
	}
	return nil
}

func withDatabase(dsn, database string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		log.Fatalf("parse postgres dsn: %v", err)
	}
	parsed.Path = "/" + database
	return parsed.String()
}

// Store is the PostgreSQL store with the Outbox port of the use cases and
// readers of the persisted state for assertions.
type Store struct {
	*postgres.Store
	outbound.Outbox
	t testing.TB
}

// Open returns a store on a fresh migrated database, closed with the test.
func Open(t testing.TB) *Store {
	t.Helper()
	startOnce.Do(start)
	require.NoError(t, startErr, "start postgres")
	ctx := context.Background()

	database := fmt.Sprintf("%s_%d", template, databases.Add(1))
	cloneMu.Lock()
	err := exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s",
		pgx.Identifier{database}.Sanitize(), pgx.Identifier{template}.Sanitize()))
	cloneMu.Unlock()
	require.NoError(t, err)

	store, err := postgres.Open(ctx, config.PostgresConfig{DSN: withDatabase(server.DSN, database), MaxConns: maxConns})
	require.NoError(t, err)
	t.Cleanup(func() {
		store.Close()
		if err := exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("drop test database: %v", err)
		}
	})
	return &Store{Store: store, Outbox: outbound.NewOutbox(store), t: t}
}

// DSN is the connection string of the database behind the store.
func (s *Store) DSN() string {
	return s.Pool().Config().ConnString()
}

// Snapshot returns the persisted form of an Order, or false when it is missing.
func (s *Store) Snapshot(id order.ID) (order.Snapshot, bool) {
	s.t.Helper()
	var exists bool
	require.NoError(s.t, s.Pool().QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`, string(id)).Scan(&exists))
	if !exists {
		return order.Snapshot{}, false
	}
	loaded, err := s.Get(context.Background(), id)
	require.NoError(s.t, err)
	return loaded.Snapshot(), true
}

// InboxSize counts the processed events of every consumer.
func (s *Store) InboxSize() int {
	s.t.Helper()
	var count int
	require.NoError(s.t, s.Pool().QueryRow(context.Background(), `SELECT count(*) FROM inbox`).Scan(&count))
	return count
}

// OutboxRecord is a committed Outbox entry: the Integration Event message the
// Forwarder will publish. OccurredAt keeps the RFC 3339 metadata value.
type OutboxRecord struct {
	Topic            string
	EventID          string
	EventType        string
	OccurredAt       string
	AggregateType    string
	AggregateID      order.ID
	AggregateVersion uint64
	Payload          []byte
}

// envelope is the forwarder envelope stored in the Outbox payload.
type envelope struct {
	DestinationTopic string            `json:"destination_topic"`
	Payload          []byte            `json:"payload"`
	Metadata         map[string]string `json:"metadata"`
}

// OutboxRecords returns the Outbox entries in the order they were appended.
func (s *Store) OutboxRecords() []OutboxRecord {
	s.t.Helper()
	rows, err := s.Pool().Query(context.Background(), `SELECT payload FROM watermill_orders_outbox ORDER BY "offset"`)
	require.NoError(s.t, err)
	payloads, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	require.NoError(s.t, err)

	records := make([]OutboxRecord, 0, len(payloads))
	for _, payload := range payloads {
		var e envelope
		require.NoError(s.t, json.Unmarshal(payload, &e))
		version, err := strconv.ParseUint(e.Metadata[messaging.AggregateVersionMetadata], 10, 64)
		require.NoError(s.t, err)
		records = append(records, OutboxRecord{
			Topic:            e.DestinationTopic,
			EventID:          e.Metadata[messaging.EventIDMetadata],
			EventType:        e.Metadata[messaging.EventTypeMetadata],
			OccurredAt:       e.Metadata[messaging.OccurredAtMetadata],
			AggregateType:    e.Metadata[messaging.AggregateTypeMetadata],
			AggregateID:      order.ID(e.Metadata[messaging.AggregateIDMetadata]),
			AggregateVersion: version,
			Payload:          e.Payload,
		})
	}
	return records
}

// ExpectedOutbox is what the Outbox stores for the events of one change of an
// Order: the messages of the production IntegrationMapper.
func ExpectedOutbox(t testing.TB, aggregateID order.ID, aggregateVersion uint64, events ...order.DomainEvent) []OutboxRecord {
	t.Helper()
	records := make([]OutboxRecord, 0, len(events))
	for index, event := range events {
		msg, err := outbound.IntegrationMapper{}.Map(aggregateID, aggregateVersion, index, event)
		require.NoError(t, err)
		records = append(records, OutboxRecord{
			Topic:            outbound.OrderEventsTopic,
			EventID:          msg.Metadata.Get(messaging.EventIDMetadata),
			EventType:        msg.Metadata.Get(messaging.EventTypeMetadata),
			OccurredAt:       msg.Metadata.Get(messaging.OccurredAtMetadata),
			AggregateType:    msg.Metadata.Get(messaging.AggregateTypeMetadata),
			AggregateID:      aggregateID,
			AggregateVersion: aggregateVersion,
			Payload:          msg.Payload,
		})
	}
	return records
}
