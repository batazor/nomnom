package grpcadapter_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
	"nomnom/orders/slices/simulator/applications/switchsimulator"
	simulatorpostgres "nomnom/orders/slices/simulator/infrastructure/postgres"
)

var (
	operator = application.Subject{Kind: application.SubjectUser, ID: "operator-1"}
	platform = application.Resource{Type: switchsimulator.ResourcePlatform, ID: switchsimulator.PlatformID}
)

// AC-761: the operator enables the Simulator; the state survives a restart
// (a new adapter over the same database).
func TestSetSimulatorEnabledPersistsForOperator(t *testing.T) {
	store := postgrestest.Open(t)
	authorizer := fake.NewAuthorizer()
	authorizer.Allow(operator, switchsimulator.PermissionOperateSimulator, platform)
	client := newClientWith(t, fixedSubject{subject: operator}, store, authorizer)
	ctx := context.Background()

	initial, err := client.GetSimulator(ctx, &ordersv1.GetSimulatorRequest{})
	require.NoError(t, err)
	assert.False(t, initial.GetEnabled(), "a new service starts with the Simulator disabled")

	set, err := client.SetSimulatorEnabled(ctx, &ordersv1.SetSimulatorEnabledRequest{Enabled: true})
	require.NoError(t, err)
	assert.True(t, set.GetEnabled())

	restarted, err := simulatorpostgres.NewState(store.Pool()).Enabled(ctx)
	require.NoError(t, err)
	assert.True(t, restarted)
	got, err := client.GetSimulator(ctx, &ordersv1.GetSimulatorRequest{})
	require.NoError(t, err)
	assert.True(t, got.GetEnabled())

	_, err = client.SetSimulatorEnabled(ctx, &ordersv1.SetSimulatorEnabledRequest{Enabled: false})
	require.NoError(t, err)
	got, err = client.GetSimulator(ctx, &ordersv1.GetSimulatorRequest{})
	require.NoError(t, err)
	assert.False(t, got.GetEnabled())
}

// AC-762: a customer is denied and the state does not change.
func TestSimulatorDeniesNonOperator(t *testing.T) {
	store := postgrestest.Open(t)
	authorizer := fake.NewAuthorizer()
	customer := application.Subject{Kind: application.SubjectUser, ID: "customer-1"}
	client := newClientWith(t, fixedSubject{subject: customer}, store, authorizer)
	ctx := context.Background()

	_, err := client.GetSimulator(ctx, &ordersv1.GetSimulatorRequest{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = client.SetSimulatorEnabled(ctx, &ordersv1.SetSimulatorEnabledRequest{Enabled: true})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	enabled, err := simulatorpostgres.NewState(store.Pool()).Enabled(ctx)
	require.NoError(t, err)
	assert.False(t, enabled)
}
