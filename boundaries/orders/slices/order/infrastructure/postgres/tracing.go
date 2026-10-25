package postgres

import (
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
)

// newQueryTracer instruments pgx operations (docs/observability.md#postgresql-и-mongodb)
// with otelpgx: client spans with db.system.name, db.operation.name and
// server/database attributes, and semconv db.client.operation.duration. Bind
// parameters are never recorded and the statement text is disabled, so spans
// carry no query data. Pool acquire is measured by poolMetrics, not as spans.
// Providers are the global ones: telemetry.Start registers them later and the
// global delegates pick them up.
func newQueryTracer() *otelpgx.Tracer {
	return otelpgx.NewTracer(
		otelpgx.WithDisableSQLStatementInAttributes(),
		otelpgx.WithMetricOperationName(otelpgx.SQLOperationName),
		otelpgx.WithDisableAcquireTracer(),
	)
}

// connTracer combines the query tracer with the pool metrics hooks; pgx accepts
// one ConnConfig.Tracer, multitracer fans the pool hooks out as well.
func connTracer(pool *poolMetrics) pgx.QueryTracer {
	return multitracer.New(newQueryTracer(), pool)
}
