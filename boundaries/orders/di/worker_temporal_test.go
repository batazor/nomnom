package di

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/testfixture"
	fulfillmentdi "nomnom/orders/slices/fulfillment/di"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recorddeliverycompletion"
	"nomnom/orders/slices/order/applications/recordorderready"
	"nomnom/orders/slices/order/applications/recordpickup"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// AC-446: the worker runs the Order Fulfillment Workflow end to end against a
// real Temporal Server; inbound facts go through the application handlers.
// AC-471: the Order lives in PostgreSQL, so another process placed it and the
// worker completes it there. Temporal and PostgreSQL run in testcontainers.
func TestWorkerCompletesOrderAgainstTemporal(t *testing.T) {
	temporal, err := testfixture.StartTemporal(context.Background())
	require.NoError(t, err)
	t.Cleanup(temporal.Close)
	address, dsn := temporal.Address, repositorytest.Open(t).DSN()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cfg := config.Config{
		Temporal: config.TemporalConfig{
			HostPort:                 address,
			Namespace:                "default",
			ConnectTimeout:           10 * time.Second,
			RegisterSearchAttributes: true,
		},
		Fulfillment: config.FulfillmentConfig{
			AcceptanceTimeout:       time.Minute,
			AssignmentTimeout:       time.Minute,
			MaxReplans:              1,
			OperatorDecisionTimeout: time.Minute,
			PreparationTimeout:      time.Minute,
			PickupTimeout:           time.Minute,
			DeliveryTimeout:         time.Minute,
			PublishStage:            true,
		},
		Shutdown: config.ShutdownConfig{DrainTimeout: 5 * time.Second},
	}

	orderID := order.ID("order-" + time.Now().UTC().Format("20060102T150405") + "-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	pg := config.PostgresConfig{DSN: dsn, MaxConns: 5}
	placing, err := repository.Open(ctx, pg)
	require.NoError(t, err)
	defer placing.Close()
	require.NoError(t, placing.Add(ctx, ordertest.New(t, orderID)))

	// The worker process has its own pool on the same database.
	store, err := repository.Open(ctx, pg)
	require.NoError(t, err)
	defer store.Close()

	worker := NewTemporalWorker(cfg, fulfillmentdi.NewActivities(orderdi.NewProcessStore(store, nil), testNeighbours(), observability.UseCases()))
	require.NoError(t, worker.Start(ctx))
	runErr := make(chan error, 1)
	go func() { runErr <- worker.Run() }()
	defer func() {
		require.NoError(t, worker.Shutdown(context.Background()))
		require.NoError(t, <-runErr)
	}()

	temporalClient, err := client.DialContext(ctx, client.Options{HostPort: address, Namespace: "default"})
	require.NoError(t, err)
	defer temporalClient.Close()
	port := fulfillmenttemporal.NewClient(temporalClient, NewFulfillmentPolicy(cfg.Fulfillment))
	workflowID := fulfillmenttemporal.WorkflowID(string(orderID))

	waitActivity := func(name string) {
		require.Eventually(t, func() bool { return activityCompleted(ctx, t, temporalClient, workflowID, name) },
			20*time.Second, 100*time.Millisecond, "activity %s did not complete", name)
	}
	type fact func(ctx context.Context, eventID string) error
	confirm := confirmorder.NewHandler(store, store, store, port)
	assign := recordcourierassignment.NewHandler(store, store, store, port, store, fake.NewAuthorizer())
	ready := recordorderready.NewHandler(store, store, store, port)
	pickup := recordpickup.NewHandler(store, store, store, port)
	complete := recorddeliverycompletion.NewHandler(store, store, store, port)
	// Inbox в PostgreSQL переживает прогоны: event_id уникален для заказа, иначе
	// повторный прогон на той же базе принимает факты за duplicates.
	send := func(eventID string, f fact) { require.NoError(t, f(ctx, string(orderID)+":"+eventID)) }
	now := time.Now().UTC()

	require.NoError(t, port.Start(ctx, fulfillment.StartRequest{OrderID: orderID}))
	waitActivity(fulfillmenttemporal.ActivityRequestRestaurantOrder)
	send("accepted", func(ctx context.Context, id string) error {
		_, err := confirm.Handle(ctx, confirmorder.Input{EventID: id, OrderID: orderID, OccurredAt: now})
		return err
	})
	waitActivity(fulfillmenttemporal.ActivityCreateDelivery)
	send("assigned", func(ctx context.Context, id string) error {
		_, err := assign.Handle(ctx, recordcourierassignment.Input{EventID: id, OrderID: orderID, OccurredAt: now})
		return err
	})
	send("ready", func(ctx context.Context, id string) error {
		_, err := ready.Handle(ctx, recordorderready.Input{EventID: id, OrderID: orderID, OccurredAt: now})
		return err
	})
	send("picked-up", func(ctx context.Context, id string) error {
		_, err := pickup.Handle(ctx, recordpickup.Input{EventID: id, OrderID: orderID, OccurredAt: now})
		return err
	})
	waitActivity(fulfillmenttemporal.ActivityCapturePayment)
	send("delivered", func(ctx context.Context, id string) error {
		_, err := complete.Handle(ctx, recorddeliverycompletion.Input{EventID: id, OrderID: orderID, OccurredAt: now})
		return err
	})

	var result fulfillmenttemporal.Result
	require.NoError(t, temporalClient.GetWorkflow(ctx, workflowID, "").Get(ctx, &result))
	assert.Equal(t, fulfillmenttemporal.Result{Outcome: fulfillmenttemporal.OutcomeCompleted}, result)

	completed, err := placing.Get(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, order.StateCompleted, completed.State())

	description, err := temporalClient.DescribeWorkflow(ctx, workflowID, "")
	require.NoError(t, err)
	stage, ok := description.TypedSearchAttributes.GetKeyword(fulfillmenttemporal.StageSearchAttribute)
	assert.True(t, ok)
	assert.Equal(t, string(fulfillmenttemporal.StageCompleted), stage)
}

// activityCompleted reports whether an Activity of the given type completed in
// the workflow history.
func activityCompleted(ctx context.Context, t *testing.T, c client.Client, workflowID, name string) bool {
	t.Helper()
	scheduled := map[int64]string{}
	history := c.GetWorkflowHistory(ctx, workflowID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for history.HasNext() {
		event, err := history.Next()
		require.NoError(t, err)
		if attributes := event.GetActivityTaskScheduledEventAttributes(); attributes != nil {
			scheduled[event.GetEventId()] = attributes.GetActivityType().GetName()
		}
		if attributes := event.GetActivityTaskCompletedEventAttributes(); attributes != nil &&
			scheduled[attributes.GetScheduledEventId()] == name {
			return true
		}
	}
	return false
}
