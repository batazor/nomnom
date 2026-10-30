package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/simulator/applications"
)

func simulatorService(t *testing.T, get *mockGetSimulatorHandler, set *mockSetSimulatorEnabledHandler) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{
		accessTokens: verifier,
		simulator:    &simulator{getSimulator: get, setSimulatorEnabled: set},
	}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func TestSimulatorRunsOnBehalfOfTheUser(t *testing.T) {
	get := newMockGetSimulatorHandler(t)
	// the user reaches the contexts as a user assertion.
	get.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool { user, ok := identity.UserFrom(ctx); return ok && user.ID != "" })).Return(true, nil).Once()
	svc, ctx := simulatorService(t, get, newMockSetSimulatorEnabledHandler(t))

	response, err := svc.QuerySimulator(ctx, &service.QuerySimulatorRequest{})

	require.NoError(t, err)
	assert.True(t, response.GetSimulator().GetEnabled())
}

// AC-766: the operator switches the Simulator on.
func TestSetSimulatorEnabledAnswersTheNewState(t *testing.T) {
	set := newMockSetSimulatorEnabledHandler(t)
	set.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool { user, ok := identity.UserFrom(ctx); return ok && user.ID != "" }), true).Return(true, nil).Once()
	svc, ctx := simulatorService(t, newMockGetSimulatorHandler(t), set)

	response, err := svc.MutationSetSimulatorEnabled(ctx, &service.MutationSetSimulatorEnabledRequest{Enabled: true})

	require.NoError(t, err)
	assert.True(t, response.GetSetSimulatorEnabled().GetEnabled())
}

func TestSimulatorRequiresAccessToken(t *testing.T) {
	svc, _ := simulatorService(t, newMockGetSimulatorHandler(t), newMockSetSimulatorEnabledHandler(t))

	_, err := svc.QuerySimulator(context.Background(), &service.QuerySimulatorRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = svc.MutationSetSimulatorEnabled(context.Background(), &service.MutationSetSimulatorEnabledRequest{Enabled: true})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// AC-767: a user who is not the operator gets PermissionDenied; other
// failures hide internal details.
func TestSimulatorMapsRefusalsToErrors(t *testing.T) {
	for refusal, want := range map[error]codes.Code{
		fmt.Errorf("set simulator enabled: %w", applications.ErrNotPermitted): codes.PermissionDenied,
		errors.New("get delivery simulator: rpc error: spicedb down"):         codes.Unavailable,
	} {
		get, set := newMockGetSimulatorHandler(t), newMockSetSimulatorEnabledHandler(t)
		get.EXPECT().Handle(mock.Anything).Return(false, refusal).Once()
		set.EXPECT().Handle(mock.Anything, false).Return(false, refusal).Once()
		svc, ctx := simulatorService(t, get, set)

		_, err := svc.QuerySimulator(ctx, &service.QuerySimulatorRequest{})
		assert.Equal(t, want, status.Code(err), refusal)
		assert.NotContains(t, status.Convert(err).Message(), "spicedb")

		_, err = svc.MutationSetSimulatorEnabled(ctx, &service.MutationSetSimulatorEnabledRequest{Enabled: false})
		assert.Equal(t, want, status.Code(err), refusal)
		assert.NotContains(t, status.Convert(err).Message(), "spicedb")
	}
}
