package di

import (
	"context"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/infrastructure/system"
	simulatordi "nomnom/orders/slices/simulator/di"
)

// SimulatorRunner вызывает демо-покупателя (FR-605) каждые
// SIMULATOR__INTERVAL в процессе cmd/orders: ему принадлежат use cases Place
// Order и Request Order Cancellation. Владелец — App (ADR 17): Run стартует
// вместе с gRPC-сервером, Shutdown прерывает тик и ждёт выхода цикла.
type SimulatorRunner struct {
	tick     func(ctx context.Context, now time.Time)
	interval time.Duration
	clock    application.Clock
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewSimulatorRunner(module *simulatordi.Module) *SimulatorRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &SimulatorRunner{
		tick:     module.Customer.Tick,
		interval: module.Interval,
		clock:    system.Clock{},
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
}

func (r *SimulatorRunner) Run() {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.tick(r.ctx, r.clock.Now())
		}
	}
}

// Shutdown прерывает текущий тик: незавершённое действие use case не
// фиксирует, следующий экземпляр начнёт с нового расписания.
func (r *SimulatorRunner) Shutdown(ctx context.Context) error {
	r.cancel()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
