package di

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"nomnom/orders/slices/order/infrastructure/repository"
)

// ProjectionCatchUp один раз догоняет проекцию OrderDetails в фоне процесса
// cmd/orders (ADR orders 4): длинный проход не задерживает открытие портов и не
// упирается в startupProbe. Владелец — App, как у SimulatorRunner. Занятая
// блокировка значит, что проход уже выполняет другой процесс.
type ProjectionCatchUp struct {
	catchUp func(ctx context.Context) (int, error)
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	failed  chan error
}

func NewProjectionCatchUp(store *repository.Store) *ProjectionCatchUp {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProjectionCatchUp{
		catchUp: store.CatchUpOrderDetails,
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		failed:  make(chan error, 1),
	}
}

// Run выполняет один проход. Ошибка, кроме прерывания через Shutdown, приходит
// в Failed: App завершает процесс, как при ошибке миграции.
func (c *ProjectionCatchUp) Run() {
	defer close(c.done)
	_, err := c.catchUp(c.ctx)
	switch {
	case err == nil, c.ctx.Err() != nil:
	case errors.Is(err, repository.ErrCatchUpInProgress):
		slog.InfoContext(c.ctx, "order details catch-up skipped", slog.String("reason", err.Error()))
	default:
		c.failed <- fmt.Errorf("catch up order details: %w", err)
	}
}

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
