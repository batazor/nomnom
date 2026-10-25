package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		ManagementAddr: "127.0.0.1:0",
		ReadinessDelay: 50 * time.Millisecond,
		DrainTimeout:   200 * time.Millisecond,
	}
}

// fakeWorker обслуживает работу до Shutdown; drain задаёт длительность
// завершения начатой работы.
type fakeWorker struct {
	startErr error
	drain    time.Duration
	runExit  chan error
	started  chan struct{}
}

func newFakeWorker() *fakeWorker {
	return &fakeWorker{runExit: make(chan error, 1), started: make(chan struct{})}
}

func (w *fakeWorker) Start(context.Context) error {
	if w.startErr == nil {
		close(w.started)
	}
	return w.startErr
}

func (w *fakeWorker) Run() error { return <-w.runExit }

func (w *fakeWorker) Shutdown(ctx context.Context) error {
	defer func() { w.runExit <- nil }()
	select {
	case <-time.After(w.drain):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runAsync(rt *Runtime, ctx context.Context, w Worker) chan error {
	result := make(chan error, 1)
	go func() { result <- rt.Run(ctx, w) }()
	return result
}

func TestRunMarksReadyAndShutsDownGracefully(t *testing.T) {
	rt := New(testConfig())
	worker := newFakeWorker()
	ctx, cancel := context.WithCancel(context.Background())
	result := runAsync(rt, ctx, worker)

	<-worker.started
	require.Eventually(t, rt.Health().Ready, time.Second, time.Millisecond)

	cancel()
	require.Eventually(t, func() bool { return !rt.Health().Ready() }, time.Second, time.Millisecond)
	require.NoError(t, <-result)
}

func TestRunWaitsReadinessDelayBeforeShutdown(t *testing.T) {
	cfg := testConfig()
	cfg.ReadinessDelay = 150 * time.Millisecond
	rt := New(cfg)
	worker := newFakeWorker()
	ctx, cancel := context.WithCancel(context.Background())
	result := runAsync(rt, ctx, worker)
	<-worker.started
	require.Eventually(t, rt.Health().Ready, time.Second, time.Millisecond)

	begin := time.Now()
	cancel()
	require.NoError(t, <-result)
	assert.GreaterOrEqual(t, time.Since(begin), cfg.ReadinessDelay)
}

func TestRunFailsWhenDrainTimeoutExceeded(t *testing.T) {
	rt := New(testConfig())
	worker := newFakeWorker()
	worker.drain = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	result := runAsync(rt, ctx, worker)
	<-worker.started

	cancel()
	require.ErrorIs(t, <-result, context.DeadlineExceeded)
}

func TestRunFailsWithoutReadyWhenStartFails(t *testing.T) {
	rt := New(testConfig())
	worker := newFakeWorker()
	worker.startErr = errors.New("address in use")

	err := rt.Run(context.Background(), worker)
	require.ErrorIs(t, err, worker.startErr)
	assert.False(t, rt.Health().Ready())
}

func TestRunFailsWhenWorkerStopsOnItsOwn(t *testing.T) {
	rt := New(testConfig())
	worker := newFakeWorker()
	result := runAsync(rt, context.Background(), worker)
	<-worker.started

	cause := errors.New("listener closed")
	worker.runExit <- cause
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
