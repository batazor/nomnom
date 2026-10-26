package publishroutes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

// registry is a mock of the locked registry read.
type registry struct {
	routes []publishroutes.MarketRoute
	locked bool
}

func (r *registry) Locked(ctx context.Context, publish func(context.Context, []publishroutes.MarketRoute) error) error {
	r.locked = true
	defer func() { r.locked = false }()
	return publish(ctx, r.routes)
}

// applier is a mock of the gateway that records the applied set.
type applier struct {
	reg     *registry
	applied []publishroutes.MarketRoute
	err     error
}

func (a *applier) Apply(_ context.Context, routes []publishroutes.MarketRoute) error {
	if !a.reg.locked {
		return errors.New("applied without the lock")
	}
	a.applied = routes
	return a.err
}

func route(t *testing.T) publishroutes.MarketRoute {
	t.Helper()
	router, err := vo.NewRouter("nomnom-bff", "bff", 3010)
	require.NoError(t, err)
	return publishroutes.MarketRoute{Market: market.New("berlin", "Berlin", "EUR", "cell-1"), Router: router}
}

func TestAppliesTheSnapshotUnderTheLock(t *testing.T) {
	reg := &registry{routes: []publishroutes.MarketRoute{route(t)}}
	gateway := &applier{reg: reg}

	require.NoError(t, publishroutes.New(reg, gateway).Publish(context.Background()))
	assert.Equal(t, reg.routes, gateway.applied)
}

func TestAFailedApplyIsUnavailable(t *testing.T) {
	reg := &registry{routes: []publishroutes.MarketRoute{route(t)}}

	err := publishroutes.New(reg, &applier{reg: reg, err: errors.New("apiserver down")}).Publish(context.Background())
	assert.ErrorIs(t, err, publishroutes.ErrRoutesUnavailable)
	assert.ErrorIs(t, err, application.ErrDependencyUnavailable)
}
