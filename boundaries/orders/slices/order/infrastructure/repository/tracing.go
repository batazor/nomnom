package repository

import (
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
)

// newQueryTracer instruments pgx operations (docs/observability.md#postgresql-и-mongodb)
// with otelpgx: client spans with db.system.name, db.namespace, db.operation.name,
// server attributes and db.query.text, and semconv db.client.operation.duration.
// The statement text is parameterized ($n placeholders, values are bound), so it
// carries no data and lets Database Observability match the span to the query.
// Bind parameters are never recorded. Pool acquire is measured by poolMetrics,
// not as spans. Providers are the global ones: telemetry.Start registers them
// later and the global delegates pick them up.
func newQueryTracer() *otelpgx.Tracer {
	return otelpgx.NewTracer(
		otelpgx.WithMetricOperationName(otelpgx.SQLOperationName),
		otelpgx.WithDisableAcquireTracer(),
	)
}

// connTracer combines the query tracer with the pool metrics hooks; pgx accepts
// one ConnConfig.Tracer, multitracer fans the pool hooks out as well.
func connTracer(pool *poolMetrics) pgx.QueryTracer {
	return multitracer.New(newQueryTracer(), pool)
}
