package di

import (
	"context"
	"fmt"

	"nomnom/orders/slices/order/infrastructure/repository"
)

// ProjectionCatchUp один раз догоняет проекцию OrderDetails в фоне процесса
// cmd/orders (ADR orders 4): startupProbe не ждёт длинный проход, а pod не
// становится ready до его конца, поэтому при выкатке запросы обслуживают старые
// pods, а ошибка прохода не трогает их. Владелец — App, как у SimulatorRunner.
type ProjectionCatchUp struct {
	catchUp  func(ctx context.Context) (int, error)
	ctx      context.Context
	cancel   context.CancelFunc
	caughtUp chan struct{}
	done     chan struct{}
	failed   chan error
}

func NewProjectionCatchUp(store *repository.Store) *ProjectionCatchUp {
	return newProjectionCatchUp(store.CatchUpOrderDetails)
}

func newProjectionCatchUp(catchUp func(ctx context.Context) (int, error)) *ProjectionCatchUp {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProjectionCatchUp{
		catchUp:  catchUp,
		ctx:      ctx,
		cancel:   cancel,
		caughtUp: make(chan struct{}),
		done:     make(chan struct{}),
		failed:   make(chan error, 1),
	}
}

// Run выполняет один проход. Ошибка, кроме прерывания через Shutdown, приходит
// в Failed: App завершает процесс, как при ошибке миграции.
func (c *ProjectionCatchUp) Run() {
	defer close(c.done)
	_, err := c.catchUp(c.ctx)
	switch {
	case err == nil:
		close(c.caughtUp)
	case c.ctx.Err() == nil:
		c.failed <- fmt.Errorf("catch up order details: %w", err)
	}
}

// CaughtUp закрывается после успешного прохода.
func (c *ProjectionCatchUp) CaughtUp() <-chan struct{} { return c.caughtUp }

// Failed отдаёт ошибку прохода.
func (c *ProjectionCatchUp) Failed() <-chan error { return c.failed }

// Shutdown прерывает проход: заказ обрабатывается в своей транзакции, а
// оставшиеся устаревшие строки догонит следующий проход.
func (c *ProjectionCatchUp) Shutdown(ctx context.Context) error {
	c.cancel()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
