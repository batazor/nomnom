package di

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/infrastructure/repository"
)

func TestProjectionCatchUpReportsFailure(t *testing.T) {
	boom := errors.New("boom")
	catchUp := &ProjectionCatchUp{
		catchUp: func(context.Context) (int, error) { return 0, boom },
		ctx:     context.Background(),
		cancel:  func() {},
		done:    make(chan struct{}),
		failed:  make(chan error, 1),
	}

	catchUp.Run()

	require.ErrorIs(t, <-catchUp.Failed(), boom)
}

func TestProjectionCatchUpSkipsWhenAnotherProcessRunsIt(t *testing.T) {
	catchUp := &ProjectionCatchUp{
		catchUp: func(context.Context) (int, error) { return 0, repository.ErrCatchUpInProgress },
		ctx:     context.Background(),
		cancel:  func() {},
		done:    make(chan struct{}),
		failed:  make(chan error, 1),
	}

	catchUp.Run()

	assert.Empty(t, catchUp.Failed())
}

func TestProjectionCatchUpShutdownInterruptsThePass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	catchUp := &ProjectionCatchUp{
		catchUp: func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			return 0, ctx.Err()
		},
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		failed: make(chan error, 1),
	}
	go catchUp.Run()
	<-started

	require.NoError(t, catchUp.Shutdown(context.Background()))

	assert.Empty(t, catchUp.Failed(), "an interrupted pass is not a failure")
}
