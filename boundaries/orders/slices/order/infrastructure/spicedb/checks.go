package spicedb

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/telemetry"
)

const checkInstrumentation = "nomnom/orders/spicedb"

// Results of a permission check (nomnom.authz.result of the authz section of
// ops/grafana/inventory, ADR 18): denied is a business answer, error and
// timeout are technical failures that use cases fail closed on.
const (
	resultAllowed = "allowed"
	resultDenied  = "denied"
	resultError   = "error"
	resultTimeout = "timeout"
)

// checkMetrics — counter and duration of permission checks. Labels are the
// permission, the resource type and the result; subject and resource IDs are
// never recorded.
type checkMetrics struct {
	checks   metric.Int64Counter
	duration metric.Float64Histogram
}

// newCheckMetrics builds the instruments on the global MeterProvider: until
// Start they are no-op. An instrument error goes to the OpenTelemetry error
// handler and does not stop the process.
func newCheckMetrics() checkMetrics {
	meter := otel.Meter(checkInstrumentation)
	checks, err := meter.Int64Counter("nomnom.authz.checks",
		metric.WithUnit("{check}"), metric.WithDescription("Authorization checks by result."))
	if err != nil {
		otel.Handle(err)
	}
	duration, err := meter.Float64Histogram("nomnom.authz.check.duration",
		metric.WithUnit("s"), metric.WithDescription("Duration of one authorization check."),
		metric.WithExplicitBucketBoundaries(telemetry.SecondsBuckets...))
	if err != nil {
		otel.Handle(err)
	}
	return checkMetrics{checks: checks, duration: duration}
}

func (m checkMetrics) record(ctx context.Context, check authz.AccessCheck, result string, elapsed time.Duration) {
	attrs := metric.WithAttributes(
		attribute.String("nomnom.authz.permission", string(check.Permission)),
		attribute.String("nomnom.authz.resource_type", string(check.Resource.Type)),
		attribute.String("nomnom.authz.result", result),
	)
	m.checks.Add(ctx, 1, attrs)
	m.duration.Record(ctx, elapsed.Seconds(), attrs)
}

// checkResult classifies the answer of Check: a deadline of the check — its
// own timeout or the caller's — or DeadlineExceeded from SpiceDB is a timeout.
func checkResult(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return resultAllowed
	case errors.Is(err, authz.ErrPermissionDenied):
		return resultDenied
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded),
		status.Code(err) == codes.DeadlineExceeded:
		return resultTimeout
	}
	return resultError
}
