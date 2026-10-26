package repository

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// PoolName is db.client.connection.pool.name of the orders pool; the process
// is told apart by service.name (job in Prometheus).
const PoolName = "orders"

const poolInstrumentation = "nomnom/orders/postgres"

// poolTimeBuckets: acquire from a warm pool takes microseconds, a new
// connection through the pooler milliseconds, a saturated pool up to the
// connect timeout; use time spans a Unit of Work.
var poolTimeBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// poolMetrics exports OTel semconv db.client.connection.* for one pgx pool:
// gauges from Pool.Stat at collection, histograms and counters from the pool
// tracer hooks (acquire, release, connect).
type poolMetrics struct {
	now        func() time.Time
	attrs      metric.MeasurementOption
	pending    metric.Int64UpDownCounter
	timeouts   metric.Int64Counter
	waitTime   metric.Float64Histogram
	useTime    metric.Float64Histogram
	createTime metric.Float64Histogram

	count   metric.Int64ObservableUpDownCounter
	max     metric.Int64ObservableUpDownCounter
	idleMin metric.Int64ObservableUpDownCounter

	mu       sync.Mutex
	acquired map[*pgx.Conn]time.Time
}

type acquireStartKey struct{}

type connectStartKey struct{}

func newPoolMetrics(meter metric.Meter, now func() time.Time) (*poolMetrics, error) {
	m := &poolMetrics{
		now:      now,
		attrs:    metric.WithAttributeSet(attribute.NewSet(semconv.DBClientConnectionPoolName(PoolName))),
		acquired: map[*pgx.Conn]time.Time{},
	}
	var errs []error
	collect := func(err error) { errs = append(errs, err) }
	var err error
	m.pending, err = meter.Int64UpDownCounter("db.client.connection.pending_requests",
		metric.WithUnit("{request}"), metric.WithDescription("The number of current pending requests for an open connection."))
	collect(err)
	m.timeouts, err = meter.Int64Counter("db.client.connection.timeouts",
		metric.WithUnit("{timeout}"), metric.WithDescription("The number of connection timeouts that have occurred trying to obtain a connection from the pool."))
	collect(err)
	m.waitTime, err = meter.Float64Histogram("db.client.connection.wait_time",
		metric.WithUnit("s"), metric.WithDescription("The time it took to obtain an open connection from the pool."),
		metric.WithExplicitBucketBoundaries(poolTimeBuckets...))
	collect(err)
	m.useTime, err = meter.Float64Histogram("db.client.connection.use_time",
		metric.WithUnit("s"), metric.WithDescription("The time between borrowing a connection and returning it to the pool."),
		metric.WithExplicitBucketBoundaries(poolTimeBuckets...))
	collect(err)
	m.createTime, err = meter.Float64Histogram("db.client.connection.create_time",
		metric.WithUnit("s"), metric.WithDescription("The time it took to create a new connection."),
		metric.WithExplicitBucketBoundaries(poolTimeBuckets...))
	collect(err)
	m.count, err = meter.Int64ObservableUpDownCounter("db.client.connection.count",
		metric.WithUnit("{connection}"), metric.WithDescription("The number of connections that are currently in state described by the state attribute."))
	collect(err)
	m.max, err = meter.Int64ObservableUpDownCounter("db.client.connection.max",
		metric.WithUnit("{connection}"), metric.WithDescription("The maximum number of open connections allowed."))
	collect(err)
	m.idleMin, err = meter.Int64ObservableUpDownCounter("db.client.connection.idle.min",
		metric.WithUnit("{connection}"), metric.WithDescription("The minimum number of idle open connections allowed."))
	collect(err)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return m, nil
}

// observe registers the Stat gauges of pool; the returned registration is
// unregistered when the store closes.
func (m *poolMetrics) observe(meter metric.Meter, pool *pgxpool.Pool) (metric.Registration, error) {
	used := metric.WithAttributeSet(attribute.NewSet(semconv.DBClientConnectionPoolName(PoolName), semconv.DBClientConnectionStateUsed))
	idle := metric.WithAttributeSet(attribute.NewSet(semconv.DBClientConnectionPoolName(PoolName), semconv.DBClientConnectionStateIdle))
	minConns := int64(pool.Config().MinConns)
	return meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		stat := pool.Stat()
		o.ObserveInt64(m.count, int64(stat.AcquiredConns()), used)
		o.ObserveInt64(m.count, int64(stat.IdleConns()), idle)
		o.ObserveInt64(m.max, int64(stat.MaxConns()), m.attrs)
		o.ObserveInt64(m.idleMin, minConns, m.attrs)
		return nil
	}, m.count, m.max, m.idleMin)
}

// TraceAcquireStart implements pgxpool.AcquireTracer.
func (m *poolMetrics) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	m.pending.Add(ctx, 1, m.attrs)
	return context.WithValue(ctx, acquireStartKey{}, m.now())
}

// TraceAcquireEnd implements pgxpool.AcquireTracer. A deadline while waiting
// for or creating a connection is a timeout; a cancelled caller is not.
func (m *poolMetrics) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	m.pending.Add(ctx, -1, m.attrs)
	start, ok := ctx.Value(acquireStartKey{}).(time.Time)
	if data.Err != nil {
		if errors.Is(data.Err, context.DeadlineExceeded) {
			m.timeouts.Add(ctx, 1, m.attrs)
		}
		return
	}
	now := m.now()
	if ok {
		m.waitTime.Record(ctx, now.Sub(start).Seconds(), m.attrs)
	}
	if data.Conn != nil {
		m.mu.Lock()
		m.acquired[data.Conn] = now
		m.mu.Unlock()
	}
}

// TraceRelease implements pgxpool.ReleaseTracer.
func (m *poolMetrics) TraceRelease(_ *pgxpool.Pool, data pgxpool.TraceReleaseData) {
	m.mu.Lock()
	start, ok := m.acquired[data.Conn]
	delete(m.acquired, data.Conn)
	m.mu.Unlock()
	if ok {
		m.useTime.Record(context.Background(), m.now().Sub(start).Seconds(), m.attrs)
	}
}

// TraceConnectStart implements pgx.ConnectTracer.
func (m *poolMetrics) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	return context.WithValue(ctx, connectStartKey{}, m.now())
}

// TraceConnectEnd implements pgx.ConnectTracer: only established connections
// count toward create_time.
func (m *poolMetrics) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	start, ok := ctx.Value(connectStartKey{}).(time.Time)
	if ok && data.Err == nil {
		m.createTime.Record(ctx, m.now().Sub(start).Seconds(), m.attrs)
	}
}

// TraceQueryStart implements pgx.QueryTracer, which ConnConfig.Tracer
// requires; queries are measured by use cases and Unit of Work, not here.
func (m *poolMetrics) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (m *poolMetrics) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
