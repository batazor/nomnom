package di

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestProjectionCatchUpReportsCompletion(t *testing.T) {
	catchUp := newProjectionCatchUp(func(context.Context) (int, error) { return 3, nil })

	catchUp.Run()

	assert.Empty(t, catchUp.Failed())
	<-catchUp.CaughtUp()
}

func TestProjectionCatchUpShutdownInterruptsThePass(t *testing.T) {
	started := make(chan struct{})
	catchUp := newProjectionCatchUp(func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	go catchUp.Run()
	<-started

	require.NoError(t, catchUp.Shutdown(context.Background()))

	assert.Empty(t, catchUp.Failed(), "an interrupted pass is not a failure")
	select {
	case <-catchUp.CaughtUp():
		t.Fatal("an interrupted pass did not catch up")
	default:
	}
}

// A failed pass stops the serving process and is reported by Run.
func TestAppRunStopsWhenCatchUpFails(t *testing.T) {
	boom := errors.New("boom")
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	app := &App{
		server:   grpc.NewServer(),
		listener: listener,
		catchUp:  newProjectionCatchUp(func(context.Context) (int, error) { return 0, boom }),
		simulator: &SimulatorRunner{
			tick:     func(context.Context, time.Time) {},
			interval: time.Hour,
			ctx:      t.Context(),
			done:     make(chan struct{}),
		},
	}

	require.ErrorIs(t, app.Run(), boom)
}
