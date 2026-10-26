package registercell_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/registercell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
)

var operator = application.Subject{Kind: application.SubjectUser, ID: "kind-operator", Service: "demo-seed"}

// authorizer is a mock of the SpiceDB check: it grants manage_markets to one user.
type authorizer struct {
	granted string
	err     error
	checks  []application.AccessCheck
}

func (a *authorizer) Check(_ context.Context, check application.AccessCheck) error {
	a.checks = append(a.checks, check)
	if a.err != nil {
		return a.err
	}
	if check.Subject.ID != a.granted {
		return application.ErrPermissionDenied
	}
	return nil
}

// cells is a mock of the repository with one registered Cell at most.
type cells struct {
	registered *cell.Cell
	adds       int
}

func (c *cells) Add(_ context.Context, candidate cell.Cell) (bool, error) {
	c.adds++
	if c.registered != nil {
		return false, nil
	}
	c.registered = &candidate
	return true, nil
}

func (c *cells) Get(_ context.Context, id vo.CellID) (cell.Cell, error) {
	if c.registered == nil || c.registered.ID() != id {
		return cell.Cell{}, cell.ErrNotFound
	}
	return *c.registered, nil
}

func command(gateway string) registercell.Command {
	return registercell.Command{Subject: operator, CellID: "cell-1", GatewayAddress: gateway}
}

func TestRegistersACellOnceAndRepeatsIdempotently(t *testing.T) {
	auth, repo := &authorizer{granted: operator.ID}, &cells{}
	handler := registercell.New(auth, repo)

	first, err := handler.Handle(context.Background(), command("gateway:8080"))
	require.NoError(t, err)
	assert.True(t, first.Created)
	assert.Equal(t, vo.CellID("cell-1"), first.Cell.ID())

	again, err := handler.Handle(context.Background(), command("gateway:8080"))
	require.NoError(t, err)
	assert.False(t, again.Created)
	assert.Equal(t, first.Cell, again.Cell)

	assert.Equal(t, "manage_markets", string(auth.checks[0].Permission))
	assert.Equal(t, "platform:nomnom", string(auth.checks[0].Resource.Type)+":"+auth.checks[0].Resource.ID)
}

func TestRefusesOtherValuesForARegisteredCell(t *testing.T) {
	handler := registercell.New(&authorizer{granted: operator.ID}, &cells{})
	_, err := handler.Handle(context.Background(), command("gateway:8080"))
	require.NoError(t, err)

	_, err = handler.Handle(context.Background(), command("other:8080"))
	assert.ErrorIs(t, err, cell.ErrAlreadyRegistered)
}

func TestRequiresManageMarketsBeforeAnyChange(t *testing.T) {
	repo := &cells{}
	_, err := registercell.New(&authorizer{granted: "someone-else"}, repo).Handle(context.Background(), command("gateway:8080"))
	assert.ErrorIs(t, err, application.ErrPermissionDenied)

	unavailable := errors.Join(errors.New("spicedb down"), application.ErrDependencyUnavailable)
	_, err = registercell.New(&authorizer{err: unavailable}, repo).Handle(context.Background(), command("gateway:8080"))
	assert.ErrorIs(t, err, application.ErrPermissionDenied, "fails closed")
	assert.Zero(t, repo.adds)
}

func TestRejectsInvalidValues(t *testing.T) {
	handler := registercell.New(&authorizer{granted: operator.ID}, &cells{})
	_, err := handler.Handle(context.Background(), registercell.Command{Subject: operator, CellID: "Cell 1", GatewayAddress: "gateway:8080"})
	assert.ErrorIs(t, err, vo.ErrInvalidValue)
	_, err = handler.Handle(context.Background(), command("gateway"))
	assert.ErrorIs(t, err, vo.ErrInvalidValue)
}
