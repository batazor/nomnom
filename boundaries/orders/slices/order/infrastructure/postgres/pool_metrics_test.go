package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newMeteredPool(t *testing.T) (*poolMetrics, *sdkmetric.ManualReader, *fakeClock, *pgxpool.Pool) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	meter := provider.Meter(poolInstrumentation)
	metrics, err := newPoolMetrics(meter, clock.Now)
	require.NoError(t, err)

	poolConfig, err := pgxpool.ParseConfig("postgres://orders@127.0.0.1:1/orders")
	require.NoError(t, err)
	poolConfig.MaxConns = 3
	poolConfig.ConnConfig.Tracer = metrics
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = metrics.observe(meter, pool)
	require.NoError(t, err)
	return metrics, reader, clock, pool
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func poolNameOf(t *testing.T, set attribute.Set) string {
	t.Helper()
	value, ok := set.Value("db.client.connection.pool.name")
	require.True(t, ok, "db.client.connection.pool.name")
	return value.AsString()
}

func TestPoolMetricsObserveStat(t *testing.T) {
	_, reader, _, _ := newMeteredPool(t)

	got := collect(t, reader)

	maxSum := got["db.client.connection.max"].Data.(metricdata.Sum[int64])
	require.Len(t, maxSum.DataPoints, 1)
	assert.Equal(t, int64(3), maxSum.DataPoints[0].Value)
	assert.False(t, maxSum.IsMonotonic)
	assert.Equal(t, PoolName, poolNameOf(t, maxSum.DataPoints[0].Attributes))
	assert.Equal(t, "{connection}", got["db.client.connection.max"].Unit)

	count := got["db.client.connection.count"].Data.(metricdata.Sum[int64])
	states := map[string]int64{}
	for _, dp := range count.DataPoints {
		state, ok := dp.Attributes.Value("db.client.connection.state")
		require.True(t, ok)
		states[state.AsString()] = dp.Value
	}
	assert.Equal(t, map[string]int64{"idle": 0, "used": 0}, states)

	idleMin := got["db.client.connection.idle.min"].Data.(metricdata.Sum[int64])
	require.Len(t, idleMin.DataPoints, 1)
	assert.Equal(t, int64(0), idleMin.DataPoints[0].Value)
}

func TestPoolMetricsTraceAcquireAndRelease(t *testing.T) {
	metrics, reader, clock, pool := newMeteredPool(t)
	ctx := context.Background()
	conn := &pgx.Conn{}

	acquireCtx := metrics.TraceAcquireStart(ctx, pool, pgxpool.TraceAcquireStartData{})
	pending := collect(t, reader)["db.client.connection.pending_requests"].Data.(metricdata.Sum[int64])
	assert.Equal(t, int64(1), pending.DataPoints[0].Value)

	clock.advance(20 * time.Millisecond)
	metrics.TraceAcquireEnd(acquireCtx, pool, pgxpool.TraceAcquireEndData{Conn: conn})
	clock.advance(300 * time.Millisecond)
	metrics.TraceRelease(pool, pgxpool.TraceReleaseData{Conn: conn})

	got := collect(t, reader)
	assert.Equal(t, int64(0), got["db.client.connection.pending_requests"].Data.(metricdata.Sum[int64]).DataPoints[0].Value)
	wait := got["db.client.connection.wait_time"].Data.(metricdata.Histogram[float64]).DataPoints[0]
	assert.Equal(t, uint64(1), wait.Count)
	assert.InDelta(t, 0.02, wait.Sum, 1e-9)
	assert.Equal(t, PoolName, poolNameOf(t, wait.Attributes))
	assert.Equal(t, "s", got["db.client.connection.wait_time"].Unit)
	use := got["db.client.connection.use_time"].Data.(metricdata.Histogram[float64]).DataPoints[0]
	assert.InDelta(t, 0.3, use.Sum, 1e-9)
	_, timedOut := got["db.client.connection.timeouts"]
	assert.False(t, timedOut, "no timeouts recorded")
}

func TestPoolMetricsCountDeadlineAsTimeout(t *testing.T) {
	metrics, reader, _, pool := newMeteredPool(t)
	ctx := context.Background()

	for _, err := range []error{
		context.DeadlineExceeded,
		errors.Join(errors.New("connect"), context.DeadlineExceeded),
		context.Canceled,
	} {
		acquireCtx := metrics.TraceAcquireStart(ctx, pool, pgxpool.TraceAcquireStartData{})
		metrics.TraceAcquireEnd(acquireCtx, pool, pgxpool.TraceAcquireEndData{Err: err})
	}

	got := collect(t, reader)
	timeouts := got["db.client.connection.timeouts"].Data.(metricdata.Sum[int64])
	assert.True(t, timeouts.IsMonotonic)
	assert.Equal(t, int64(2), timeouts.DataPoints[0].Value)
	_, waited := got["db.client.connection.wait_time"]
	assert.False(t, waited, "failed acquires are not wait samples")
	assert.Equal(t, int64(0), got["db.client.connection.pending_requests"].Data.(metricdata.Sum[int64]).DataPoints[0].Value)
}

func TestPoolMetricsRecordCreateTimeOfEstablishedConnections(t *testing.T) {
	metrics, reader, clock, _ := newMeteredPool(t)

	ctx := metrics.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
	clock.advance(40 * time.Millisecond)
	metrics.TraceConnectEnd(ctx, pgx.TraceConnectEndData{})
	failed := metrics.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
	metrics.TraceConnectEnd(failed, pgx.TraceConnectEndData{Err: errors.New("refused")})

	create := collect(t, reader)["db.client.connection.create_time"].Data.(metricdata.Histogram[float64]).DataPoints[0]
	assert.Equal(t, uint64(1), create.Count)
	assert.InDelta(t, 0.04, create.Sum, 1e-9)
}
