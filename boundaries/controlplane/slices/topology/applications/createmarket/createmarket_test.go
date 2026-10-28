package createmarket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/createmarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

var operator = application.Subject{Kind: application.SubjectUser, ID: "kind-operator", Service: "demo-seed"}

// granted is a mock of the SpiceDB check granting manage_markets to the operator only.
type granted struct{}

func (granted) Check(_ context.Context, check application.AccessCheck) error {
	if check.Subject.ID != operator.ID {
		return application.ErrPermissionDenied
	}
	return nil
}

// markets is a mock of the repository; Cells lists the registered Cells.
type markets struct {
	cells   map[string]bool
	created map[vo.MarketID]market.Market
}

func (m *markets) Add(_ context.Context, candidate market.Market) (bool, error) {
	if _, ok := m.created[candidate.ID()]; ok {
		return false, nil
	}
	if !m.cells[string(candidate.CellID())] {
		return false, market.ErrUnknownCell
	}
	m.created[candidate.ID()] = candidate
	return true, nil
}

func (m *markets) Get(_ context.Context, id vo.MarketID) (market.Market, error) {
	existing, ok := m.created[id]
	if !ok {
		return market.Market{}, market.ErrNotFound
	}
	return existing, nil
}

func (m *markets) List(context.Context) ([]market.Market, error) { return nil, nil }

// routes is a mock of route publication that counts publications.
type routes struct {
	published int
	err       error
}

func (r *routes) Publish(context.Context) error {
	r.published++
	return r.err
}

func newHandler() createmarket.Handler {
	return newHandlerPublishing(&routes{})
}

func newHandlerPublishing(published *routes) createmarket.Handler {
	return createmarket.New(granted{}, &markets{cells: map[string]bool{"cell-1": true}, created: map[vo.MarketID]market.Market{}}, published)
}

func berlin(name, currency, timeZone, cell string) createmarket.Command {
	return createmarket.Command{Subject: operator, MarketID: "berlin", Name: name, Currency: currency, TimeZone: timeZone, CellID: cell}
}

func TestCreatesAMarketOnceAndRepeatsIdempotently(t *testing.T) {
	published := &routes{}
	handler := newHandlerPublishing(published)

	first, err := handler.Handle(context.Background(), berlin(" Berlin ", "EUR", "Europe/Berlin", "cell-1"))
	require.NoError(t, err)
	assert.True(t, first.Created)
	assert.Equal(t, vo.Name("Berlin"), first.Market.Name(), "stored trimmed")
	assert.Equal(t, vo.TimeZone("Europe/Berlin"), first.Market.TimeZone())

	again, err := handler.Handle(context.Background(), berlin("Berlin", "EUR", "Europe/Berlin", "cell-1"))
	require.NoError(t, err)
	assert.False(t, again.Created)
	assert.Equal(t, first.Market, again.Market)
	assert.Equal(t, 2, published.published, "routes are published after the creation and after the repeat")
}

func TestAnUnpublishedRouteKeepsTheMarketAndARepeatPublishesIt(t *testing.T) {
	published := &routes{err: publishroutes.ErrRoutesUnavailable}
	handler := newHandlerPublishing(published)

	_, err := handler.Handle(context.Background(), berlin("Berlin", "EUR", "Europe/Berlin", "cell-1"))
	assert.ErrorIs(t, err, publishroutes.ErrRoutesUnavailable)

	published.err = nil
	again, err := handler.Handle(context.Background(), berlin("Berlin", "EUR", "Europe/Berlin", "cell-1"))
	require.NoError(t, err)
	assert.False(t, again.Created, "the market stayed in the registry")
	assert.Equal(t, 2, published.published)
}

func TestRefusesOtherValuesForAnExistingMarket(t *testing.T) {
	handler := newHandler()
	_, err := handler.Handle(context.Background(), berlin("Berlin", "EUR", "Europe/Berlin", "cell-1"))
	require.NoError(t, err)

	_, err = handler.Handle(context.Background(), berlin("Berlin", "USD", "Europe/Berlin", "cell-1"))
	assert.ErrorIs(t, err, market.ErrAlreadyCreated)
	_, err = handler.Handle(context.Background(), berlin("Berlin", "EUR", "Europe/Paris", "cell-1"))
	assert.ErrorIs(t, err, market.ErrAlreadyCreated)
}

func TestRefusesAnUnknownTimeZoneAndKeepsTheRegistry(t *testing.T) {
	stored := &markets{cells: map[string]bool{"cell-1": true}, created: map[vo.MarketID]market.Market{}}
	published := &routes{}
	handler := createmarket.New(granted{}, stored, published)

	for _, timeZone := range []string{"Mars/Olympus", ""} {
		_, err := handler.Handle(context.Background(), berlin("Berlin", "EUR", timeZone, "cell-1"))
		assert.ErrorIs(t, err, vo.ErrInvalidValue, timeZone)
	}
	assert.Empty(t, stored.created)
	assert.Zero(t, published.published)
}

func TestRefusesAMarketOfAnUnregisteredCell(t *testing.T) {
	_, err := newHandler().Handle(context.Background(), berlin("Berlin", "EUR", "Europe/Berlin", "cell-9"))
	assert.ErrorIs(t, err, market.ErrUnknownCell)
}

func TestRequiresManageMarkets(t *testing.T) {
	cmd := berlin("Berlin", "EUR", "Europe/Berlin", "cell-1")
	cmd.Subject = application.Subject{Kind: application.SubjectService, ID: "bff", Service: "bff"}
	published := &routes{}
	_, err := newHandlerPublishing(published).Handle(context.Background(), cmd)
	assert.ErrorIs(t, err, application.ErrPermissionDenied)
	assert.Zero(t, published.published)
}

func TestARefusedMarketPublishesNothing(t *testing.T) {
	published := &routes{}
	_, err := newHandlerPublishing(published).Handle(context.Background(), berlin("Berlin", "EUR", "Europe/Berlin", "cell-9"))
	assert.True(t, errors.Is(err, market.ErrUnknownCell))
	assert.Zero(t, published.published)
}

func TestRejectsInvalidValues(t *testing.T) {
	for _, cmd := range []createmarket.Command{
		berlin("", "EUR", "Europe/Berlin", "cell-1"),
		berlin("Berlin", "eur", "Europe/Berlin", "cell-1"),
		berlin("Berlin", "EUR", "Europe/Berlin", "Cell 1"),
		{Subject: operator, MarketID: "Berlin", Name: "Berlin", Currency: "EUR", TimeZone: "Europe/Berlin", CellID: "cell-1"},
	} {
		_, err := newHandler().Handle(context.Background(), cmd)
		assert.Error(t, err)
	}
}
