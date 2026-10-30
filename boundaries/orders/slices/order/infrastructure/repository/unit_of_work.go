package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/pkg/usecase"
)

// Retry policy of the outer Unit of Work (ADR 20): bounded by attempts and by
// the deadline of ctx, with a jittered exponential pause.
const (
	// transactionAttempts counts the first attempt.
	transactionAttempts = 3
	// retryBackoff is the pause before the first retry; it doubles per retry.
	retryBackoff = 10 * time.Millisecond
)

// Outcomes of one transaction (nomnom.outcome of the uow section of
// ops/grafana/inventory).
const (
	outcomeCommitted    = "committed"
	outcomeRolledBack   = "rolled_back"
	outcomeCommitFailed = "commit_failed"
)

// Retry reasons (nomnom.reason of nomnom.uow.retries).
const (
	reasonSerialization = "serialization"
	reasonDeadlock      = "deadlock"
	reasonWriteConflict = "write_conflict"
	reasonOther         = "other"
)

// SQLSTATE of the transaction rollback class: the server rolled the
// transaction back, so the closure can run again.
const (
	sqlstateSerializationFailure = "40001"
	sqlstateDeadlockDetected     = "40P01"
	sqlstateTransactionRollback  = "40"
)

// unitOfWorkMetrics — nomnom.uow.* of the outer Unit of Work: one transaction
// and one duration per attempt, one retry per repeated attempt. A failed BEGIN
// opens no transaction and is not counted here.
type unitOfWorkMetrics struct {
	transactions metric.Int64Counter
	duration     metric.Float64Histogram
	retries      metric.Int64Counter
}

func newUnitOfWorkMetrics(meter metric.Meter) (*unitOfWorkMetrics, error) {
	m := &unitOfWorkMetrics{}
	var errs []error
	var err error
	m.transactions, err = meter.Int64Counter("nomnom.uow.transactions",
		metric.WithUnit("{transaction}"), metric.WithDescription("Unit of Work transactions by outcome."))
	errs = append(errs, err)
	m.duration, err = meter.Float64Histogram("nomnom.uow.duration",
		metric.WithUnit("s"), metric.WithDescription("Duration of one Unit of Work transaction."),
		metric.WithExplicitBucketBoundaries(telemetry.SecondsBuckets...))
	errs = append(errs, err)
	m.retries, err = meter.Int64Counter("nomnom.uow.retries",
		metric.WithUnit("{retry}"), metric.WithDescription("Unit of Work retries by conflict class."))
	errs = append(errs, err)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *unitOfWorkMetrics) transaction(ctx context.Context, outcome string, started time.Time) {
	attrs := metric.WithAttributes(attribute.String("nomnom.outcome", outcome))
	m.transactions.Add(ctx, 1, attrs)
	m.duration.Record(ctx, time.Since(started).Seconds(), attrs)
}

// Do runs fn in one READ COMMITTED transaction; a nested Do joins the outer
// one. An error or panic rolls back; a panic is re-raised. The outer scope
// repeats a rolled-back fn on a serialization failure, a deadlock, another
// server-side transaction rollback or a version conflict of an aggregate
// loaded inside it, up to transactionAttempts. A failed commit has an unknown
// result and is never repeated: idempotency of the operation resolves it.
func (s *Store) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := transaction(ctx); ok {
		return fn(ctx)
	}
	for attempt := 1; ; attempt++ {
		outcome, err := s.attempt(ctx, fn)
		reason, retryable := retryReason(err)
		if outcome != outcomeRolledBack || !retryable || attempt == transactionAttempts {
			return err
		}
		if waitErr := pause(ctx, attempt); waitErr != nil {
			return errors.Join(err, waitErr)
		}
		s.uow.retries.Add(ctx, 1, metric.WithAttributes(attribute.String("nomnom.reason", reason)))
		trace.SpanFromContext(ctx).AddEvent("unit_of_work.retry", trace.WithAttributes(attribute.String("nomnom.reason", reason)))
		slog.WarnContext(ctx, "unit of work retried", slog.String("reason", reason), slog.Int("attempt", attempt+1))
	}
}

// attempt runs one transaction and reports its outcome; the outcome is empty
// when BEGIN failed.
func (s *Store) attempt(ctx context.Context, fn func(ctx context.Context) error) (outcome string, err error) {
	started := time.Now()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			s.uow.transaction(ctx, outcomeRolledBack, started)
			panic(recovered)
		}
		if outcome == outcomeRolledBack {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
		s.uow.transaction(ctx, outcome, started)
	}()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return outcomeRolledBack, err
	}
	if err := tx.Commit(ctx); err != nil {
		return outcomeCommitFailed, fmt.Errorf("commit transaction: %w", err)
	}
	return outcomeCommitted, nil
}

// retryReason classifies an error of a rolled-back attempt; false — the error
// is not a conflict and the attempt is final.
func retryReason(err error) (string, bool) {
	var dbErr *pgconn.PgError
	switch {
	case err == nil:
		return "", false
	case errors.As(err, &dbErr) && dbErr.Code == sqlstateSerializationFailure:
		return reasonSerialization, true
	case errors.As(err, &dbErr) && dbErr.Code == sqlstateDeadlockDetected:
		return reasonDeadlock, true
	case errors.As(err, &dbErr) && strings.HasPrefix(dbErr.Code, sqlstateTransactionRollback):
		return reasonOther, true
	case errors.Is(err, usecase.ErrConcurrencyConflict):
		return reasonWriteConflict, true
	}
	return "", false
}

// pause waits before retry number attempt: the doubled backoff, of which the
// upper half is jittered so that two deadlocked transactions do not meet again.
func pause(ctx context.Context, attempt int) error {
	backoff := retryBackoff << (attempt - 1)
	timer := time.NewTimer(backoff/2 + rand.N(backoff/2))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
