package grpcadapter_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/infrastructure/mocks"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
	"nomnom/orders/slices/simulator/applications/switchsimulator"
	simulatorrepository "nomnom/orders/slices/simulator/infrastructure/repository"
)

var (
	operator = application.Subject{Kind: application.SubjectUser, ID: "operator-1"}
	platform = application.Resource{Type: switchsimulator.ResourcePlatform, ID: switchsimulator.PlatformID}
)

// AC-761: the operator enables the Simulator; the state survives a restart
// (a new adapter over the same database).
func TestSetSimulatorEnabledPersistsForOperator(t *testing.T) {
	store := repositorytest.Open(t)
	// Three reads and two writes, each checked in SpiceDB.
	authorizer := mocks.NewMockAuthorizer(t)
	authorizer.EXPECT().Check(mock.Anything, application.AccessCheck{
		Subject: operator, Permission: switchsimulator.PermissionOperateSimulator, Resource: platform,
	}).Return(nil).Times(5)
	client := newClientWith(t, fixedSubject{subject: operator}, store, authorizer)
	ctx := context.Background()

	initial, err := client.GetSimulator(ctx, &ordersv1.GetSimulatorRequest{})
	require.NoError(t, err)
	assert.False(t, initial.GetEnabled(), "a new service starts with the Simulator disabled")

	set, err := client.SetSimulatorEnabled(ctx, &ordersv1.SetSimulatorEnabledRequest{Enabled: true})
	require.NoError(t, err)
	assert.True(t, set.GetEnabled())

	restarted, err := simulatorrepository.NewState(store.Pool()).Enabled(ctx)
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
	store := repositorytest.Open(t)
	customer := application.Subject{Kind: application.SubjectUser, ID: "customer-1"}
	authorizer := mocks.NewMockAuthorizer(t)
	authorizer.EXPECT().Check(mock.Anything, application.AccessCheck{
		Subject: customer, Permission: switchsimulator.PermissionOperateSimulator, Resource: platform,
	}).Return(application.ErrPermissionDenied).Times(2)
	client := newClientWith(t, fixedSubject{subject: customer}, store, authorizer)
	ctx := context.Background()

	_, err := client.GetSimulator(ctx, &ordersv1.GetSimulatorRequest{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = client.SetSimulatorEnabled(ctx, &ordersv1.SetSimulatorEnabledRequest{Enabled: true})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	enabled, err := simulatorrepository.NewState(store.Pool()).Enabled(ctx)
	require.NoError(t, err)
	assert.False(t, enabled)
}
