package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/pkg/application"
)

// Телеметрия use cases снаружи: декоратор application.Handler пишет один span и
// одно измерение длительности на вызов. Use cases и domain не импортируют
// OpenTelemetry (docs/observability.md#размещение-в-коде).

const useCaseInstrumentation = "nomnom/orders/pkg/telemetry"

// Kind отделяет Commands от Queries.
type Kind string

const (
	KindCommand Kind = "command"
	KindQuery   Kind = "query"
)

// Outcome — закрытый набор результатов вызова use case.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	// OutcomeRejected — отказ по правилам use case: невалидный вход, запрет
	// доступа, отсутствующий ресурс, недопустимый переход FSM. Ожидаемый ответ,
	// а не сбой.
	OutcomeRejected Outcome = "rejected"
	// OutcomeError — технический сбой: инфраструктура, конфликт для retry,
	// отмена или неклассифицированная ошибка.
	OutcomeError Outcome = "error"
)

// Общие для всех slices error types; slice добавляет свои закрытые значения.
const (
	ErrorTypeCanceled         = "canceled"
	ErrorTypeDeadlineExceeded = "deadline_exceeded"
	ErrorTypeInternal         = "internal"
)

// Failure — как сообщается ошибка use case. ErrorType берётся из закрытого
// набора: он становится label метрики.
type Failure struct {
	Outcome   Outcome
	ErrorType string
}

// Classifier переводит ненулевую ошибку use case в Failure. Им владеет slice:
// только он знает свои доменные ошибки.
type Classifier func(err error) Failure

// UseCase — имя use case в телеметрии. Name — стабильный snake_case
// идентификатор, например place_order.
type UseCase struct {
	Name string
	Kind Kind
}

func Command(name string) UseCase { return UseCase{Name: name, Kind: KindCommand} }

func Query(name string) UseCase { return UseCase{Name: name, Kind: KindQuery} }

// UseCases — инструменты, общие для декорированных handlers процесса. Имя
// метрики — из ops/grafana/inventory (раздел usecase); attributes — usecase,
// kind, outcome и error_type (только при ошибке), без идентификаторов и payload.
// Span несёт те же attributes; текст ошибки не пишется, он может содержать вход.
type UseCases struct {
	tracer   trace.Tracer
	duration metric.Float64Histogram
	classify Classifier
}

// NewUseCases строит инструменты на глобальных OpenTelemetry providers: до
// Start они no-op. Ошибка создания инструмента не останавливает процесс и уходит
// в глобальный OpenTelemetry error handler.
func NewUseCases(classify Classifier) *UseCases {
	duration, err := otel.Meter(useCaseInstrumentation).Float64Histogram("nomnom.usecase.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of one application use case call."),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30))
	if err != nil {
		otel.Handle(err)
	}
	return &UseCases{tracer: otel.Tracer(useCaseInstrumentation), duration: duration, classify: classify}
}

// Instrument декорирует Handle конкретного handler, не меняя результат и
// ошибку. Метод, а не интерфейс, — чтобы типы In и Out выводились компилятором.
func Instrument[In, Out any](u *UseCases, uc UseCase, handle func(context.Context, In) (Out, error)) application.Handler[In, Out] {
	return instrumented[In, Out]{u: u, uc: uc, next: handle}
}

type instrumented[In, Out any] struct {
	u    *UseCases
	uc   UseCase
	next func(context.Context, In) (Out, error)
}

func (h instrumented[In, Out]) Handle(ctx context.Context, in In) (Out, error) {
	attrs := []attribute.KeyValue{
		attribute.String("nomnom.usecase.name", h.uc.Name),
		attribute.String("nomnom.usecase.kind", string(h.uc.Kind)),
	}
	ctx, span := h.u.tracer.Start(ctx, "usecase "+h.uc.Name, trace.WithAttributes(attrs...))
	defer span.End()

	started := time.Now()
	out, err := h.next(ctx, in)
	elapsed := time.Since(started).Seconds()

	failure := Failure{Outcome: OutcomeSuccess}
	if err != nil {
		failure = h.u.failure(err)
	}
	attrs = append(attrs, attribute.String("nomnom.outcome", string(failure.Outcome)))
	if failure.ErrorType != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(failure.ErrorType))
	}
	span.SetAttributes(attrs[2:]...)
	if failure.Outcome == OutcomeError {
		span.SetStatus(codes.Error, failure.ErrorType)
	}
	if h.u.duration != nil {
		h.u.duration.Record(ctx, elapsed, metric.WithAttributes(attrs...))
	}
	return out, err
}

// failure классифицирует отмену раньше slice: отменённый вызов — не бизнес-отказ,
// во что бы handler ни обернул ошибку.
func (u *UseCases) failure(err error) Failure {
	switch {
	case errors.Is(err, context.Canceled):
		return Failure{Outcome: OutcomeError, ErrorType: ErrorTypeCanceled}
	case errors.Is(err, context.DeadlineExceeded):
		return Failure{Outcome: OutcomeError, ErrorType: ErrorTypeDeadlineExceeded}
	case u.classify != nil:
		if f := u.classify(err); f.Outcome != "" && f.Outcome != OutcomeSuccess {
			return f
		}
	}
	return Failure{Outcome: OutcomeError, ErrorType: ErrorTypeInternal}
}
