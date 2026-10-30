package di

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/pkg/config"
	fulfillmentdi "nomnom/orders/slices/fulfillment/di"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// activityNames are the 15 Activities the fulfillment worker registers.
var activityNames = []string{
	fulfillmenttemporal.ActivityAuthorizePayment,
	fulfillmenttemporal.ActivityCapturePayment,
	fulfillmenttemporal.ActivityCancelPaymentAuthorization,
	fulfillmenttemporal.ActivityRefundPayment,
	fulfillmenttemporal.ActivityRecordPaymentAuthorization,
	fulfillmenttemporal.ActivityRequestRestaurantOrder,
	fulfillmenttemporal.ActivityCancelRestaurantOrder,
	fulfillmenttemporal.ActivityCreateDelivery,
	fulfillmenttemporal.ActivityCancelDelivery,
	fulfillmenttemporal.ActivityCompleteOrder,
	fulfillmenttemporal.ActivityFailOrCancelOrder,
	fulfillmenttemporal.ActivityConfirmSlotReservation,
	fulfillmenttemporal.ActivityPlanSchedule,
	fulfillmenttemporal.ActivityQueueRestaurantOrder,
	fulfillmenttemporal.ActivityRecordRestaurantPickup,
	fulfillmenttemporal.ActivityReleaseSlotReservation,
}

func testConfig() config.Config {
	return config.Config{
		Temporal: config.TemporalConfig{
			HostPort:       "temporal:7233",
			Namespace:      "orders-local",
			ConnectTimeout: time.Second,
			DeploymentName: "nomnom-orders/orders-worker",
			BuildID:        "orders-1a2b3c",
		},
		Fulfillment: config.FulfillmentConfig{PublishStage: true},
		Shutdown:    config.ShutdownConfig{DrainTimeout: 3 * time.Second},
	}
}

func newTestWorker(t *testing.T, cfg config.Config, poller *mockRunner) (*TemporalWorker, *mocks.Client, *worker.Options) {
	t.Helper()
	temporalClient := mocks.NewClient(t)
	w := NewTemporalWorker(cfg, fulfillmentdi.NewActivities(NewFulfillmentOrders(orderdi.NewFulfillmentModule(orderdi.FulfillmentDependencies{
		Store:     repositorytest.Open(t),
		Scheduled: cfg.ScheduledOrder,
		Telemetry: observability.UseCases(),
	})), testNeighbours()))
	w.dial = func(ctx context.Context, options client.Options) (client.Client, error) {
		_, hasDeadline := ctx.Deadline()
		assert.True(t, hasDeadline, "connection is bounded by connect timeout")
		assert.Equal(t, "temporal:7233", options.HostPort)
		assert.Equal(t, "orders-local", options.Namespace)
		assert.NotNil(t, options.MetricsHandler, "Temporal SDK metrics go to the OTel pipeline")
		return temporalClient, nil
	}
	var captured worker.Options
	w.newWorker = func(_ client.Client, options worker.Options) runner {
		captured = options
		return poller
	}
	w.ensure = func(context.Context, client.Client) error { return nil }
	return w, temporalClient, &captured
}

func TestTemporalWorkerStartsAndStops(t *testing.T) {
	runner := newMockRunner(t)
	runner.EXPECT().RegisterWorkflowWithOptions(mock.Anything, workflow.RegisterOptions{
		Name:               "OrderFulfillment",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	}).Return().Once()
	for _, name := range activityNames {
		runner.EXPECT().RegisterActivityWithOptions(mock.Anything, activity.RegisterOptions{Name: name}).Return().Once()
	}
	runner.EXPECT().Start().Return(nil).Once()
	runner.EXPECT().Stop().Return().Once()
	w, temporalClient, options := newTestWorker(t, testConfig(), runner)
	temporalClient.On("Close").Return().Once()

	require.NoError(t, w.Start(context.Background()))
	assert.Equal(t, 3*time.Second, options.WorkerStopTimeout)
	assert.Equal(t, worker.DeploymentOptions{
		UseVersioning: true,
		Version:       worker.WorkerDeploymentVersion{DeploymentName: "nomnom-orders/orders-worker", BuildID: "orders-1a2b3c"},
	}, options.DeploymentOptions)

	runErr := make(chan error, 1)
	go func() { runErr <- w.Run() }()
	require.NoError(t, w.Shutdown(context.Background()))
	require.NoError(t, <-runErr)
}

func TestTemporalWorkerChecksSearchAttributeOnlyWhenPublishing(t *testing.T) {
	for _, publish := range []bool{true, false} {
		cfg := testConfig()
		cfg.Fulfillment.PublishStage = publish
		runner := newMockRunner(t)
		if !publish {
			runner.EXPECT().RegisterWorkflowWithOptions(mock.Anything, mock.MatchedBy(func(options workflow.RegisterOptions) bool {
				return options.Name == "OrderFulfillment"
			})).Return().Once()
			for _, name := range activityNames {
				runner.EXPECT().RegisterActivityWithOptions(mock.Anything, activity.RegisterOptions{Name: name}).Return().Once()
			}
			runner.EXPECT().Start().Return(nil).Once()
		}
		w, temporalClient, _ := newTestWorker(t, cfg, runner)
		if publish {
			temporalClient.On("Close").Return().Once()
		}
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
	startErr := errors.New("namespace not found")
	runner := newMockRunner(t)
	runner.EXPECT().RegisterWorkflowWithOptions(mock.Anything, mock.MatchedBy(func(options workflow.RegisterOptions) bool {
		return options.Name == "OrderFulfillment"
	})).Return().Once()
	for _, name := range activityNames {
		runner.EXPECT().RegisterActivityWithOptions(mock.Anything, activity.RegisterOptions{Name: name}).Return().Once()
	}
	runner.EXPECT().Start().Return(startErr).Once()
	w, temporalClient, _ := newTestWorker(t, testConfig(), runner)
	temporalClient.On("Close").Return().Once()

	require.ErrorIs(t, w.Start(context.Background()), startErr)
}

// Without the Worker Deployment Version set by Temporal Worker Controller the
// worker fails startup before connecting.
func TestTemporalWorkerWithoutVersionFailsStartup(t *testing.T) {
	for _, env := range []string{config.EnvTemporalDeploymentName, config.EnvTemporalWorkerBuildID} {
		cfg := testConfig()
		if env == config.EnvTemporalDeploymentName {
			cfg.Temporal.DeploymentName = ""
		} else {
			cfg.Temporal.BuildID = ""
		}
		w := NewTemporalWorker(cfg, nil)
		w.dial = func(context.Context, client.Options) (client.Client, error) {
			t.Fatal("worker without a version must not connect")
			return nil, nil
		}

		require.ErrorContains(t, w.Start(context.Background()), env)
	}
}

// AC-451: an unreachable Temporal fails startup within the connect timeout.
func TestTemporalWorkerDialFailureFailsStartup(t *testing.T) {
	cfg := testConfig()
	cfg.Temporal.ConnectTimeout = 50 * time.Millisecond
	w, _, _ := newTestWorker(t, cfg, newMockRunner(t))
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
	runner := newMockRunner(t)
	runner.EXPECT().RegisterWorkflowWithOptions(mock.Anything, mock.MatchedBy(func(options workflow.RegisterOptions) bool {
		return options.Name == "OrderFulfillment"
	})).Return().Once()
	for _, name := range activityNames {
		runner.EXPECT().RegisterActivityWithOptions(mock.Anything, activity.RegisterOptions{Name: name}).Return().Once()
	}
	runner.EXPECT().Start().Return(nil).Once()
	w, _, options := newTestWorker(t, testConfig(), runner)
	require.NoError(t, w.Start(context.Background()))

	fatal := errors.New("poller failed")
	options.OnFatalError(fatal)

	require.ErrorIs(t, w.Run(), fatal)
}

func TestTemporalWorkerShutdownRespectsDeadline(t *testing.T) {
	runner := newMockRunner(t)
	runner.EXPECT().RegisterWorkflowWithOptions(mock.Anything, mock.MatchedBy(func(options workflow.RegisterOptions) bool {
		return options.Name == "OrderFulfillment"
	})).Return().Once()
	for _, name := range activityNames {
		runner.EXPECT().RegisterActivityWithOptions(mock.Anything, activity.RegisterOptions{Name: name}).Return().Once()
	}
	runner.EXPECT().Start().Return(nil).Once()
	runner.EXPECT().Stop().RunAndReturn(func() { time.Sleep(200 * time.Millisecond) }).Once()
	w, temporalClient, _ := newTestWorker(t, testConfig(), runner)
	temporalClient.On("Close").Return().Once()
	require.NoError(t, w.Start(context.Background()))
	runErr := make(chan error, 1)
	go func() { runErr <- w.Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	require.ErrorIs(t, w.Shutdown(ctx), context.DeadlineExceeded)
	require.NoError(t, <-runErr, "Run returns after Shutdown even when the drain deadline passes")
	time.Sleep(250 * time.Millisecond)
}
