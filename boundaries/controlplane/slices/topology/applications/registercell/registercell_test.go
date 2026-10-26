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

// routes is a mock of route publication that counts publications.
type routes struct{ published int }

func (r *routes) Publish(context.Context) error {
	r.published++
	return nil
}

func command(port int32) registercell.Command {
	return registercell.Command{Subject: operator, CellID: "cell-1", Router: registercell.Router{Namespace: "nomnom-bff", Name: "bff", Port: port}}
}

func TestRegistersACellOnceAndRepeatsIdempotently(t *testing.T) {
	auth, repo, published := &authorizer{granted: operator.ID}, &cells{}, &routes{}
	handler := registercell.New(auth, repo, published)

	first, err := handler.Handle(context.Background(), command(3010))
	require.NoError(t, err)
	assert.True(t, first.Created)
	assert.Equal(t, vo.CellID("cell-1"), first.Cell.ID())

	assert.Equal(t, "nomnom-bff/bff:3010", first.Cell.Router().String())

	again, err := handler.Handle(context.Background(), command(3010))
	require.NoError(t, err)
	assert.False(t, again.Created)
	assert.Equal(t, first.Cell, again.Cell)
	assert.Equal(t, 2, published.published, "routes are published after the registration and after the repeat")

	assert.Equal(t, "manage_markets", string(auth.checks[0].Permission))
	assert.Equal(t, "platform:nomnom", string(auth.checks[0].Resource.Type)+":"+auth.checks[0].Resource.ID)
}

func TestRefusesOtherValuesForARegisteredCell(t *testing.T) {
	published := &routes{}
	handler := registercell.New(&authorizer{granted: operator.ID}, &cells{}, published)
	_, err := handler.Handle(context.Background(), command(3010))
	require.NoError(t, err)

	_, err = handler.Handle(context.Background(), command(3011))
	assert.ErrorIs(t, err, cell.ErrAlreadyRegistered)
	assert.Equal(t, 1, published.published, "a refused registration publishes nothing")
}

func TestRequiresManageMarketsBeforeAnyChange(t *testing.T) {
	repo := &cells{}
	_, err := registercell.New(&authorizer{granted: "someone-else"}, repo, &routes{}).Handle(context.Background(), command(3010))
	assert.ErrorIs(t, err, application.ErrPermissionDenied)

	unavailable := errors.Join(errors.New("spicedb down"), application.ErrDependencyUnavailable)
	_, err = registercell.New(&authorizer{err: unavailable}, repo, &routes{}).Handle(context.Background(), command(3010))
	assert.ErrorIs(t, err, application.ErrPermissionDenied, "fails closed")
	assert.Zero(t, repo.adds)
}

func TestRejectsInvalidValues(t *testing.T) {
	handler := registercell.New(&authorizer{granted: operator.ID}, &cells{}, &routes{})
	invalidID := command(3010)
	invalidID.CellID = "Cell 1"
	_, err := handler.Handle(context.Background(), invalidID)
	assert.ErrorIs(t, err, vo.ErrInvalidValue)
	_, err = handler.Handle(context.Background(), command(0))
	assert.ErrorIs(t, err, vo.ErrInvalidValue)
}
