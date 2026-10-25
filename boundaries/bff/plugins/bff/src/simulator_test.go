package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/simulator/applications"
)

type fakeSimulatorScenario struct {
	enabled bool
	err     error
	called  bool
	got     bool
	hasUser bool
}

func (f *fakeSimulatorScenario) handle(ctx context.Context) (bool, error) {
	f.called = true
	_, f.hasUser = identity.UserFrom(ctx)
	return f.enabled, f.err
}

type fakeGetSimulator struct{ fakeSimulatorScenario }

func (f *fakeGetSimulator) Handle(ctx context.Context) (bool, error) { return f.handle(ctx) }

type fakeSetSimulator struct{ fakeSimulatorScenario }

func (f *fakeSetSimulator) Handle(ctx context.Context, enabled bool) (bool, error) {
	f.got = enabled
	return f.handle(ctx)
}

func simulatorService(t *testing.T, get *fakeGetSimulator, set *fakeSetSimulator) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{
		accessTokens: verifier,
		simulator:    &simulator{getSimulator: get, setSimulatorEnabled: set},
	}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func TestSimulatorRunsOnBehalfOfTheUser(t *testing.T) {
	get := &fakeGetSimulator{fakeSimulatorScenario{enabled: true}}
	svc, ctx := simulatorService(t, get, &fakeSetSimulator{})

	response, err := svc.QuerySimulator(ctx, &service.QuerySimulatorRequest{})

	require.NoError(t, err)
	assert.True(t, get.hasUser, "the user reaches the contexts as a user assertion")
	assert.True(t, response.GetSimulator().GetEnabled())
}

// AC-766: the operator switches the Simulator on.
func TestSetSimulatorEnabledAnswersTheNewState(t *testing.T) {
	set := &fakeSetSimulator{fakeSimulatorScenario{enabled: true}}
	svc, ctx := simulatorService(t, &fakeGetSimulator{}, set)

	response, err := svc.MutationSetSimulatorEnabled(ctx, &service.MutationSetSimulatorEnabledRequest{Enabled: true})

	require.NoError(t, err)
	assert.True(t, set.got)
	assert.True(t, set.hasUser)
	assert.True(t, response.GetSetSimulatorEnabled().GetEnabled())
}

func TestSimulatorRequiresAccessToken(t *testing.T) {
	get, set := &fakeGetSimulator{}, &fakeSetSimulator{}
	svc, _ := simulatorService(t, get, set)

	_, err := svc.QuerySimulator(context.Background(), &service.QuerySimulatorRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = svc.MutationSetSimulatorEnabled(context.Background(), &service.MutationSetSimulatorEnabledRequest{Enabled: true})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.False(t, get.called)
	assert.False(t, set.called)
}

// AC-767: a user who is not the operator gets PermissionDenied; other
// failures hide internal details.
func TestSimulatorMapsRefusalsToErrors(t *testing.T) {
	for refusal, want := range map[error]codes.Code{
		fmt.Errorf("set simulator enabled: %w", applications.ErrNotPermitted): codes.PermissionDenied,
		errors.New("get delivery simulator: rpc error: spicedb down"):         codes.Unavailable,
	} {
		svc, ctx := simulatorService(t, &fakeGetSimulator{fakeSimulatorScenario{err: refusal}}, &fakeSetSimulator{fakeSimulatorScenario{err: refusal}})

		_, err := svc.QuerySimulator(ctx, &service.QuerySimulatorRequest{})
		assert.Equal(t, want, status.Code(err), refusal)
		assert.NotContains(t, status.Convert(err).Message(), "spicedb")

		_, err = svc.MutationSetSimulatorEnabled(ctx, &service.MutationSetSimulatorEnabledRequest{Enabled: false})
		assert.Equal(t, want, status.Code(err), refusal)
		assert.NotContains(t, status.Convert(err).Message(), "spicedb")
	}
}
