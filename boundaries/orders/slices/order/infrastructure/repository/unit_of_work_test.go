package repository_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository"
)

// meteredStore opens a store whose Unit of Work writes to a test reader. The
// provider is installed before Open: global instruments bind to it.
func meteredStore(t *testing.T) (*repository.Store, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return open(t), reader
}

type uowMetrics struct {
	transactions map[string]int64
	durations    map[string]uint64
	retries      map[string]int64
}

func collectUnitOfWork(t *testing.T, reader *sdkmetric.ManualReader) uowMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	got := uowMetrics{transactions: map[string]int64{}, durations: map[string]uint64{}, retries: map[string]int64{}}
	label := func(m metricdata.Metrics, set attribute.Set, key attribute.Key) string {
		value, ok := set.Value(key)
		require.True(t, ok, "%s of %s", key, m.Name)
		return value.AsString()
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "nomnom.uow.transactions":
				assert.Equal(t, "{transaction}", m.Unit)
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					got.transactions[label(m, dp.Attributes, "nomnom.outcome")] += dp.Value
				}
			case "nomnom.uow.duration":
				assert.Equal(t, "s", m.Unit)
				for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
					assert.Equal(t, telemetry.SecondsBuckets, dp.Bounds)
					got.durations[label(m, dp.Attributes, "nomnom.outcome")] += dp.Count
				}
			case "nomnom.uow.retries":
				assert.Equal(t, "{retry}", m.Unit)
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					got.retries[label(m, dp.Attributes, "nomnom.reason")] += dp.Value
				}
			}
		}
	}
	return got
}

// Имена и attributes — контракт ops/grafana/inventory (раздел uow): одна
// транзакция и одна длительность на попытку, outcome — её исход.
func TestUnitOfWorkRecordsTransactionOutcomes(t *testing.T) {
	store, reader := meteredStore(t)
	ctx := context.Background()

	require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
		return store.Do(ctx, func(ctx context.Context) error { return store.Add(ctx, ordertest.New(t, uniqueID("uow"))) })
	}))
	failure := errors.New("use case failed")
	require.ErrorIs(t, store.Do(ctx, func(context.Context) error { return failure }), failure)
	// COMMIT under a cancelled context fails after fn succeeded.
	cancelled, cancel := context.WithCancel(ctx)
	err := store.Do(cancelled, func(context.Context) error { cancel(); return nil })
	require.ErrorContains(t, err, "commit transaction")
	assert.Panics(t, func() {
		_ = store.Do(ctx, func(context.Context) error { panic("use case panicked") })
	})

	got := collectUnitOfWork(t, reader)
	want := map[string]int64{"committed": 1, "rolled_back": 2, "commit_failed": 1}
	assert.Equal(t, want, got.transactions, "nested Do is part of the outer transaction")
	assert.Equal(t, map[string]uint64{"committed": 1, "rolled_back": 2, "commit_failed": 1}, got.durations)
	assert.Empty(t, got.retries)
}

// The outer Unit of Work repeats a rolled-back attempt on a conflict class and
// counts every repetition by reason; other errors are final.
func TestUnitOfWorkRetriesConflicts(t *testing.T) {
	for _, tc := range []struct {
		reason   string
		conflict error
	}{
		{"serialization", &pgconn.PgError{Code: "40001"}},
		{"deadlock", fmt.Errorf("update order: %w", &pgconn.PgError{Code: "40P01"})},
		{"other", &pgconn.PgError{Code: "40000"}},
		{"write_conflict", fmt.Errorf("%w: order version 1", usecase.ErrConcurrencyConflict)},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			store, reader := meteredStore(t)
			attempts := 0

			require.NoError(t, store.Do(context.Background(), func(context.Context) error {
				attempts++
				if attempts == 1 {
					return tc.conflict
				}
				return nil
			}))

			assert.Equal(t, 2, attempts)
			got := collectUnitOfWork(t, reader)
			assert.Equal(t, map[string]int64{"rolled_back": 1, "committed": 1}, got.transactions)
			assert.Equal(t, map[string]int64{tc.reason: 1}, got.retries)
		})
	}
}

func TestUnitOfWorkRetryIsBounded(t *testing.T) {
	store, reader := meteredStore(t)
	attempts := 0
	conflict := &pgconn.PgError{Code: "40001"}

	err := store.Do(context.Background(), func(context.Context) error { attempts++; return conflict })

	require.ErrorIs(t, err, conflict)
	assert.Equal(t, 3, attempts)
	got := collectUnitOfWork(t, reader)
	assert.Equal(t, map[string]int64{"rolled_back": 3}, got.transactions)
	assert.Equal(t, map[string]int64{"serialization": 2}, got.retries)

	attempts = 0
	unique := &pgconn.PgError{Code: "23505"}
	require.ErrorIs(t, store.Do(context.Background(), func(context.Context) error { attempts++; return unique }), unique)
	assert.Equal(t, 1, attempts, "not a conflict class")
}

// A version conflict of an Order loaded inside the transaction is repeated:
// the second attempt loads the version a concurrent writer committed.
func TestUnitOfWorkRetriesVersionConflict(t *testing.T) {
	store, reader := meteredStore(t)
	ctx := context.Background()
	id := uniqueID("conflict")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	attempts := 0

	require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
		attempts++
		current, err := store.Get(ctx, id)
		if err != nil {
			return err
		}
		if attempts == 1 {
			concurrent, err := store.Get(context.Background(), id)
			require.NoError(t, err)
			_, err = concurrent.RecordPreparationStarted(ordertest.PlacedAt)
			require.NoError(t, err)
			require.NoError(t, store.Save(context.Background(), concurrent))
		}
		if _, err := current.Confirm(ordertest.PlacedAt); err != nil {
			return err
		}
		return store.Save(ctx, current)
	}))

	assert.Equal(t, 2, attempts)
	saved, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, order.StatePreparing, saved.State(), "the late confirmation does not move the Order back")
	got := collectUnitOfWork(t, reader)
	assert.Equal(t, map[string]int64{"write_conflict": 1}, got.retries)
	assert.Equal(t, map[string]int64{"rolled_back": 1, "committed": 1}, got.transactions)
}
