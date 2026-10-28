package archivemarket_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/archivemarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

var operator = application.Subject{Kind: application.SubjectUser, ID: "kind-operator", Service: "e2e-driver"}

// granted is a mock of the SpiceDB check granting manage_markets to the operator only.
type granted struct{}

func (granted) Check(_ context.Context, check application.AccessCheck) error {
	if check.Subject.ID != operator.ID {
		return application.ErrPermissionDenied
	}
	return nil
}

// markets is a mock of the repository: the known Markets and whether each is archived.
type markets map[vo.MarketID]bool

func (m markets) Archive(_ context.Context, id vo.MarketID) (bool, error) {
	archived, ok := m[id]
	if !ok {
		return false, market.ErrNotFound
	}
	m[id] = true
	return !archived, nil
}

// routes is a mock of route publication that counts publications.
type routes struct {
	published int
	err       error
}

func (r *routes) Publish(context.Context) error {
	r.published++
	return r.err
}

func TestArchivesAMarketOnceAndRepeatsIdempotently(t *testing.T) {
	stored := markets{"kind-e2e-1": false}
	published := &routes{}
	handler := archivemarket.New(granted{}, stored, published)

	first, err := handler.Handle(context.Background(), archivemarket.Command{Subject: operator, MarketID: "kind-e2e-1"})
	require.NoError(t, err)
	assert.True(t, first.Archived)
	assert.True(t, stored["kind-e2e-1"])

	again, err := handler.Handle(context.Background(), archivemarket.Command{Subject: operator, MarketID: "kind-e2e-1"})
	require.NoError(t, err)
	assert.False(t, again.Archived)
	assert.Equal(t, 2, published.published, "routes are published after the archiving and after the repeat")
}

func TestAnUnknownMarketIsNotFoundAndPublishesNothing(t *testing.T) {
	published := &routes{}
	_, err := archivemarket.New(granted{}, markets{}, published).Handle(context.Background(), archivemarket.Command{Subject: operator, MarketID: "nowhere"})
	assert.ErrorIs(t, err, market.ErrNotFound)
	assert.Zero(t, published.published)
}

func TestAnUnpublishedRouteKeepsTheArchive(t *testing.T) {
	stored := markets{"kind-e2e-1": false}
	_, err := archivemarket.New(granted{}, stored, &routes{err: publishroutes.ErrRoutesUnavailable}).Handle(context.Background(), archivemarket.Command{Subject: operator, MarketID: "kind-e2e-1"})
	assert.ErrorIs(t, err, publishroutes.ErrRoutesUnavailable)
	assert.True(t, stored["kind-e2e-1"])
}

func TestArchiveRequiresManageMarkets(t *testing.T) {
	stored := markets{"berlin": false}
	cmd := archivemarket.Command{Subject: application.Subject{Kind: application.SubjectService, ID: "bff", Service: "bff"}, MarketID: "berlin"}
	_, err := archivemarket.New(granted{}, stored, &routes{}).Handle(context.Background(), cmd)
	assert.ErrorIs(t, err, application.ErrPermissionDenied)
	assert.False(t, stored["berlin"])
}

func TestArchiveRejectsAnInvalidID(t *testing.T) {
	_, err := archivemarket.New(granted{}, markets{}, &routes{}).Handle(context.Background(), archivemarket.Command{Subject: operator, MarketID: "Berlin"})
	assert.ErrorIs(t, err, vo.ErrInvalidValue)
}
