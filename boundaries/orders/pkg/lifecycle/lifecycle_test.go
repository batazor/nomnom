package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		ManagementAddr: "127.0.0.1:0",
		ReadinessDelay: 50 * time.Millisecond,
		DrainTimeout:   200 * time.Millisecond,
	}
}

// newServingWorker возвращает MockWorker, который обслуживает работу до
// Shutdown: started закрывается после успешного Start, отправка в runExit
// завершает Run.
func newServingWorker(t *testing.T) (*MockWorker, chan struct{}, chan error) {
	worker := NewMockWorker(t)
	started := make(chan struct{})
	runExit := make(chan error, 1)
	worker.EXPECT().Start(mock.Anything).RunAndReturn(func(context.Context) error {
		close(started)
		return nil
	}).Once()
	worker.EXPECT().Run().RunAndReturn(func() error { return <-runExit }).Once()
	return worker, started, runExit
}

// expectDrain ожидает Shutdown, который завершает начатую работу за drain
// и затем останавливает Run.
func expectDrain(worker *MockWorker, runExit chan error, drain time.Duration) {
	worker.EXPECT().Shutdown(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		defer func() { runExit <- nil }()
		select {
		case <-time.After(drain):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}).Once()
}

func runAsync(rt *Runtime, ctx context.Context, w Worker) chan error {
	result := make(chan error, 1)
	go func() { result <- rt.Run(ctx, w) }()
	return result
}

func TestRunMarksReadyAndShutsDownGracefully(t *testing.T) {
	rt := New(testConfig())
	worker, started, runExit := newServingWorker(t)
	expectDrain(worker, runExit, 0)
	ctx, cancel := context.WithCancel(context.Background())
	result := runAsync(rt, ctx, worker)

	<-started
	require.Eventually(t, rt.Health().Ready, time.Second, time.Millisecond)

	cancel()
	require.Eventually(t, func() bool { return !rt.Health().Ready() }, time.Second, time.Millisecond)
	require.NoError(t, <-result)
}

func TestRunWaitsReadinessDelayBeforeShutdown(t *testing.T) {
	cfg := testConfig()
	cfg.ReadinessDelay = 150 * time.Millisecond
	rt := New(cfg)
	worker, started, runExit := newServingWorker(t)
	expectDrain(worker, runExit, 0)
	ctx, cancel := context.WithCancel(context.Background())
	result := runAsync(rt, ctx, worker)
	<-started
	require.Eventually(t, rt.Health().Ready, time.Second, time.Millisecond)

	begin := time.Now()
	cancel()
	require.NoError(t, <-result)
	assert.GreaterOrEqual(t, time.Since(begin), cfg.ReadinessDelay)
}

func TestRunFailsWhenDrainTimeoutExceeded(t *testing.T) {
	rt := New(testConfig())
	worker, started, runExit := newServingWorker(t)
	expectDrain(worker, runExit, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	result := runAsync(rt, ctx, worker)
	<-started

	cancel()
	require.ErrorIs(t, <-result, context.DeadlineExceeded)
}

func TestRunFailsWithoutReadyWhenStartFails(t *testing.T) {
	rt := New(testConfig())
	worker := NewMockWorker(t)
	startErr := errors.New("address in use")
	worker.EXPECT().Start(mock.Anything).Return(startErr).Once()

	err := rt.Run(context.Background(), worker)
	require.ErrorIs(t, err, startErr)
	assert.False(t, rt.Health().Ready())
}

func TestRunFailsWhenWorkerStopsOnItsOwn(t *testing.T) {
	rt := New(testConfig())
	worker, started, runExit := newServingWorker(t)
	result := runAsync(rt, context.Background(), worker)
	<-started

	cause := errors.New("listener closed")
	runExit <- cause
	require.ErrorIs(t, <-result, cause)
	assert.False(t, rt.Health().Ready())
}

func TestHealthHandler(t *testing.T) {
	var health Health
	handler := health.Handler()
	status := func(path string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code
	}

	assert.Equal(t, http.StatusServiceUnavailable, status("/health/startup"))
	assert.Equal(t, http.StatusOK, status("/health/live"))
	assert.Equal(t, http.StatusServiceUnavailable, status("/health/ready"))

	health.markStarted()
	assert.Equal(t, http.StatusOK, status("/health/startup"))
	assert.Equal(t, http.StatusOK, status("/health/ready"))

	health.markShuttingDown()
	assert.Equal(t, http.StatusOK, status("/health/startup"))
	assert.Equal(t, http.StatusOK, status("/health/live"))
	assert.Equal(t, http.StatusServiceUnavailable, status("/health/ready"))
}
