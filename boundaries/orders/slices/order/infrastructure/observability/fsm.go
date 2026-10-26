package observability

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"nomnom/orders/slices/order/domains/order"
)

// FSM-переходы Order (раздел fsm инвентаря ops/grafana, ADR 21) наблюдаются
// снаружи домена: repository внутри Unit of Work запоминает State загруженного
// Order, Save — State после перехода, trigger называет таблица переходов
// (order.TriggerOf). Применённый переход считается после commit внешнего Unit
// of Work, отклонённый — по order.InvalidTransition, которым Unit of Work
// завершился. Labels — только значения таблицы переходов, без ID.

const (
	fsmAggregateOrder = "order"
	fsmApplied        = "applied"
	fsmRejected       = "rejected"
)

type transition struct {
	from, to order.State
	trigger  order.Trigger
}

// Loaded запоминает State Order, загруженного внутри Unit of Work. Вне Unit of
// Work переходы не наблюдаются.
func (m *DomainMetrics) Loaded(ctx context.Context, current *order.Order) {
	if m == nil {
		return
	}
	if c, ok := ctx.Value(collectorKey{}).(*collector); ok {
		c.mu.Lock()
		c.loaded[current] = current.State()
		c.mu.Unlock()
	}
}

// Saved — Order, сохранённый после доменного метода: переход из State при
// загрузке в текущий State.
func (m *DomainMetrics) Saved(ctx context.Context, changed *order.Order) {
	if m == nil {
		return
	}
	c, ok := ctx.Value(collectorKey{}).(*collector)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	from, ok := c.loaded[changed]
	if !ok {
		return
	}
	to := changed.State()
	trigger, ok := order.TriggerOf(from, to)
	if !ok {
		otel.Handle(fmt.Errorf("fsm transitions: no order transition from %q to %q", from, to))
		return
	}
	c.transitions = append(c.transitions, transition{from: from, to: to, trigger: trigger})
	c.loaded[changed] = to
}

func (m *DomainMetrics) applied(ctx context.Context, transitions []transition) {
	for _, t := range transitions {
		m.transition(ctx, t, fsmApplied)
	}
}

// rejected считает недопустимый переход: State не меняется.
func (m *DomainMetrics) rejected(ctx context.Context, err error) {
	var invalid order.InvalidTransition
	if errors.As(err, &invalid) {
		m.transition(ctx, transition{from: invalid.From, to: invalid.From, trigger: invalid.Trigger}, fsmRejected)
	}
}

func (m *DomainMetrics) transition(ctx context.Context, t transition, result string) {
	m.transitions.Add(ctx, 1, metric.WithAttributes(
		attribute.String("nomnom.fsm.aggregate_type", fsmAggregateOrder),
		attribute.String("nomnom.fsm.from_state", string(t.from)),
		attribute.String("nomnom.fsm.to_state", string(t.to)),
		attribute.String("nomnom.fsm.trigger", string(t.trigger)),
		attribute.String("nomnom.fsm.result", result),
		attribute.Bool("nomnom.fsm.terminal", t.to.IsFinal()),
	))
}
