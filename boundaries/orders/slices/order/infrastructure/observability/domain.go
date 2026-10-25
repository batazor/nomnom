package observability

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	"nomnom/orders/slices/order/domains/order"
)

const domainInstrumentation = "nomnom/orders/slices/order/domain"

// Границы fulfillment duration: заказ живёт минуты–часы, общие SecondsBuckets
// заканчиваются на 300 s.
var fulfillmentBuckets = []float64{300, 600, 900, 1200, 1800, 2700, 3600, 5400, 7200, 10800, 14400}

// ReasonCodes — закрытый набор label reason (FailOrCancelOrder); другое значение
// пишется как "other", чтобы опечатка не порождала новую series. Набор задаёт
// процесс fulfillment; composition root передаёт его сюда, чтобы слайс order не
// зависел от слайса fulfillment.
type ReasonCodes []string

// Outcomes cancellation requests: ответ use case и отказ по правилам.
const (
	CancellationRequested        = string(requestordercancellation.OutcomeRequested)
	CancellationAlreadyCancelled = string(requestordercancellation.OutcomeAlreadyCancelled)
	CancellationRejected         = "rejected"
)

// DomainMetrics — бизнес-счётчики order (раздел domain инвентаря ops/grafana).
// Они меняются по Domain Events, записанным в Outbox, и только после commit
// внешнего Unit of Work: rollback не считается, а повтор без новых событий
// (AlreadyInState, idempotency replay) не считается дважды. Домен о метриках не
// знает. Nil-значение — no-op для тестов, собирающих Module без телеметрии.
type DomainMetrics struct {
	placed, completed, cancelled, failed metric.Int64Counter
	cancellationRequests                 metric.Int64Counter
	fulfillment                          metric.Float64Histogram
	reasons                              ReasonCodes
	now                                  func() time.Time
}

// NewDomainMetrics строит инструменты на глобальном MeterProvider: до Start они
// no-op. Ошибка создания уходит в OpenTelemetry error handler.
func NewDomainMetrics(reasons ReasonCodes) *DomainMetrics {
	return newDomainMetrics(otel.Meter(domainInstrumentation), time.Now, reasons)
}

func newDomainMetrics(meter metric.Meter, now func() time.Time, reasons ReasonCodes) *DomainMetrics {
	counter := func(name, description string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithUnit("{order}"), metric.WithDescription(description))
		if err != nil {
			otel.Handle(err)
		}
		return c
	}
	m := &DomainMetrics{
		placed:    counter("nomnom.orders.placed", "Orders placed (OrderPlaced committed)."),
		completed: counter("nomnom.orders.completed", "Orders completed (OrderCompleted committed)."),
		cancelled: counter("nomnom.orders.cancelled", "Orders cancelled, by reason (OrderCancelled with outcome cancellation)."),
		failed:    counter("nomnom.orders.failed", "Orders failed, by reason (OrderCancelled with outcome failure)."),
		reasons:   reasons,
		now:       now,
	}
	requests, err := meter.Int64Counter("nomnom.orders.cancellation_requests",
		metric.WithUnit("{request}"), metric.WithDescription("Request Order Cancellation calls answered, by outcome."))
	if err != nil {
		otel.Handle(err)
	}
	m.cancellationRequests = requests
	m.fulfillment, err = meter.Float64Histogram("nomnom.orders.fulfillment.duration",
		metric.WithUnit("s"), metric.WithDescription("placed_at → OrderCompleted commit."),
		metric.WithExplicitBucketBoundaries(fulfillmentBuckets...))
	if err != nil {
		otel.Handle(err)
	}
	return m
}

type collectorKey struct{}

type collector struct {
	mu     sync.Mutex
	events []order.DomainEvent
}

// Track начинает сбор событий Unit of Work. Вложенный Do получает no-op: события
// записываются один раз, после commit внешнего. done(nil) записывает события,
// done(err) отбрасывает их.
func (m *DomainMetrics) Track(ctx context.Context) (context.Context, func(err error)) {
	if m == nil {
		return ctx, func(error) {}
	}
	if _, active := ctx.Value(collectorKey{}).(*collector); active {
		return ctx, func(error) {}
	}
	c := &collector{}
	return context.WithValue(ctx, collectorKey{}, c), func(err error) {
		if err == nil {
			m.record(ctx, c.events)
		}
	}
}

// Appended — события, успешно записанные в Outbox. Вне Unit of Work запись в
// Outbox уже зафиксирована, поэтому события учитываются сразу.
func (m *DomainMetrics) Appended(ctx context.Context, events []order.DomainEvent) {
	if m == nil {
		return
	}
	if c, ok := ctx.Value(collectorKey{}).(*collector); ok {
		c.mu.Lock()
		c.events = append(c.events, events...)
		c.mu.Unlock()
		return
	}
	m.record(ctx, events)
}

func (m *DomainMetrics) record(ctx context.Context, events []order.DomainEvent) {
	for _, event := range events {
		switch e := event.(type) {
		case order.OrderPlaced:
			m.placed.Add(ctx, 1)
		case order.OrderCompleted:
			m.completed.Add(ctx, 1)
			if !e.PlacedAt.IsZero() {
				m.fulfillment.Record(ctx, m.now().Sub(e.PlacedAt).Seconds())
			}
		case order.OrderCancelled:
			reason := metric.WithAttributes(attribute.String("reason", m.reasonLabel(e.Reason.Code())))
			if e.Reason.Outcome() == order.OutcomeFailure {
				m.failed.Add(ctx, 1, reason)
			} else {
				m.cancelled.Add(ctx, 1, reason)
			}
		}
	}
}

func (m *DomainMetrics) reasonLabel(code string) string {
	if slices.Contains(m.reasons, code) {
		return code
	}
	return "other"
}

// CancellationRequests декорирует Request Order Cancellation: ответ use case —
// его outcome, отказ по правилам — rejected; технический сбой здесь не
// считается (он виден в nomnom_usecase_duration_seconds).
func CancellationRequests(m *DomainMetrics, handle func(context.Context, requestordercancellation.Input) (requestordercancellation.Result, error)) func(context.Context, requestordercancellation.Input) (requestordercancellation.Result, error) {
	if m == nil {
		return handle
	}
	return func(ctx context.Context, in requestordercancellation.Input) (requestordercancellation.Result, error) {
		result, err := handle(ctx, in)
		outcome := string(result.Outcome)
		if err != nil {
			if errors.Is(err, context.Canceled) || Classify(err).Outcome != telemetry.OutcomeRejected {
				return result, err
			}
			outcome = CancellationRejected
		}
		m.cancellationRequests.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
		return result, err
	}
}

// inStateTimeout ограничивает SQL-запрос одного сбора gauge.
const inStateTimeout = 5 * time.Second

// ObserveOrdersInState регистрирует gauge nomnom.orders.in_state: projection из
// counters не выводится, поэтому значение — запрос к хранилищу при каждом сборе
// метрик. Регистрируется одной ролью (API): реплики дают одинаковые значения,
// dashboard агрегирует их max. Ошибка запроса пропускает сбор, не процесс.
func ObserveOrdersInState(counts func(context.Context) (map[order.State]int64, error)) {
	meter := otel.Meter(domainInstrumentation)
	gauge, err := meter.Int64ObservableGauge("nomnom.orders.in_state",
		metric.WithUnit("{order}"), metric.WithDescription("Orders currently in each state."))
	if err != nil {
		otel.Handle(err)
		return
	}
	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, inStateTimeout)
		defer cancel()
		byState, err := counts(ctx)
		if err != nil {
			return err
		}
		for state, n := range byState {
			o.ObserveInt64(gauge, n, metric.WithAttributes(attribute.String("state", string(state))))
		}
		return nil
	}, gauge)
	if err != nil {
		otel.Handle(err)
	}
}
