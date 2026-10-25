package di

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/pkg/config"
	fulfillmentdi "nomnom/orders/slices/fulfillment/di"
	"nomnom/orders/slices/order/infrastructure/memory"
	"nomnom/orders/slices/order/infrastructure/observability"
)

type fakeRunner struct {
	options    worker.Options
	workflows  []string
	activities int
	startErr   error
	started    bool
	stopped    bool
	stopDelay  time.Duration
}

func (f *fakeRunner) RegisterWorkflowWithOptions(_ any, options workflow.RegisterOptions) {
	f.workflows = append(f.workflows, options.Name)
}

func (f *fakeRunner) RegisterActivityWithOptions(any, activity.RegisterOptions) { f.activities++ }

func (f *fakeRunner) Start() error {
	f.started = true
	return f.startErr
}

func (f *fakeRunner) Stop() {
	time.Sleep(f.stopDelay)
	f.stopped = true
}

func testConfig() config.Config {
	return config.Config{
		Temporal: config.TemporalConfig{
			HostPort:       "temporal:7233",
			Namespace:      "orders-local",
			ConnectTimeout: time.Second,
		},
		Fulfillment: config.FulfillmentConfig{PublishStage: true, Neighbours: config.NeighboursUnavailable},
		Shutdown:    config.ShutdownConfig{DrainTimeout: 3 * time.Second},
	}
}

func newTestWorker(t *testing.T, cfg config.Config, fake *fakeRunner) (*TemporalWorker, *mocks.Client) {
	t.Helper()
	temporalClient := mocks.NewClient(t)
	neighbours, _, err := fulfillmentdi.NewNeighbours(cfg.Fulfillment, cfg.InternalGateway, cfg.Identity)
	require.NoError(t, err)
	w := NewTemporalWorker(cfg, fulfillmentdi.NewActivities(memory.NewStore(), neighbours, observability.UseCases()))
	w.dial = func(ctx context.Context, options client.Options) (client.Client, error) {
		_, hasDeadline := ctx.Deadline()
		assert.True(t, hasDeadline, "connection is bounded by connect timeout")
		assert.Equal(t, "temporal:7233", options.HostPort)
		assert.Equal(t, "orders-local", options.Namespace)
		assert.NotNil(t, options.MetricsHandler, "Temporal SDK metrics go to the OTel pipeline")
		return temporalClient, nil
	}
	w.newWorker = func(_ client.Client, options worker.Options) runner {
		fake.options = options
		return fake
	}
	w.ensure = func(context.Context, client.Client) error { return nil }
	return w, temporalClient
}

func TestTemporalWorkerStartsAndStops(t *testing.T) {
	runner := &fakeRunner{}
	w, temporalClient := newTestWorker(t, testConfig(), runner)
	temporalClient.On("Close").Return().Once()

	require.NoError(t, w.Start(context.Background()))
	assert.True(t, runner.started)
	assert.Equal(t, []string{"OrderFulfillment"}, runner.workflows)
	assert.Equal(t, 11, runner.activities)
	assert.Equal(t, 3*time.Second, runner.options.WorkerStopTimeout)

	runErr := make(chan error, 1)
	go func() { runErr <- w.Run() }()
	require.NoError(t, w.Shutdown(context.Background()))
	require.NoError(t, <-runErr)
	assert.True(t, runner.stopped)
}

func TestTemporalWorkerChecksSearchAttributeOnlyWhenPublishing(t *testing.T) {
	for _, publish := range []bool{true, false} {
		cfg := testConfig()
		cfg.Fulfillment.PublishStage = publish
		w, temporalClient := newTestWorker(t, cfg, &fakeRunner{})
		temporalClient.On("Close").Return().Maybe()
		missing := errors.New("search attribute missing")
		w.ensure = func(context.Context, client.Client) error { return missing }

		err := w.Start(context.Background())

		if publish {
			require.ErrorIs(t, err, missing)
			temporalClient.AssertCalled(t, "Close")
		} else {
			require.NoError(t, err)
			temporalClient.AssertNotCalled(t, "Close")
		}
	}
}

func TestTemporalWorkerStartFailureClosesClient(t *testing.T) {
	runner := &fakeRunner{startErr: errors.New("namespace not found")}
	w, temporalClient := newTestWorker(t, testConfig(), runner)
	temporalClient.On("Close").Return().Once()

	require.ErrorIs(t, w.Start(context.Background()), runner.startErr)
}

// AC-451: an unreachable Temporal fails startup within the connect timeout.
func TestTemporalWorkerDialFailureFailsStartup(t *testing.T) {
	cfg := testConfig()
	cfg.Temporal.ConnectTimeout = 50 * time.Millisecond
	w, _ := newTestWorker(t, cfg, &fakeRunner{})
	w.dial = func(ctx context.Context, _ client.Options) (client.Client, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	started := time.Now()
	err := w.Start(context.Background())

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(started), time.Second)
}

func TestTemporalWorkerRunReturnsFatalError(t *testing.T) {
	runner := &fakeRunner{}
	w, _ := newTestWorker(t, testConfig(), runner)
	require.NoError(t, w.Start(context.Background()))

	fatal := errors.New("poller failed")
	runner.options.OnFatalError(fatal)

	require.ErrorIs(t, w.Run(), fatal)
}

func TestTemporalWorkerShutdownRespectsDeadline(t *testing.T) {
	runner := &fakeRunner{stopDelay: 200 * time.Millisecond}
	w, temporalClient := newTestWorker(t, testConfig(), runner)
	temporalClient.On("Close").Return().Maybe()
	require.NoError(t, w.Start(context.Background()))
	runErr := make(chan error, 1)
	go func() { runErr <- w.Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	require.ErrorIs(t, w.Shutdown(ctx), context.DeadlineExceeded)
	require.NoError(t, <-runErr, "Run returns after Shutdown even when the drain deadline passes")
	time.Sleep(250 * time.Millisecond)
}

func TestNewFulfillmentPolicyCopiesConfig(t *testing.T) {
	policy := NewFulfillmentPolicy(config.FulfillmentConfig{
		AcceptanceTimeout: time.Minute, MaxReplans: 3, PublishStage: true,
	})

	assert.Equal(t, time.Minute, policy.AcceptanceTimeout)
	assert.Equal(t, 3, policy.MaxReplans)
	assert.True(t, policy.PublishStage)
}
