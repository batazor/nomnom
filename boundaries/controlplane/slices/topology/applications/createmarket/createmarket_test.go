package createmarket_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/createmarket"
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

func newHandler() createmarket.Handler {
	return createmarket.New(granted{}, &markets{cells: map[string]bool{"cell-1": true}, created: map[vo.MarketID]market.Market{}})
}

func berlin(name, currency, cell string) createmarket.Command {
	return createmarket.Command{Subject: operator, MarketID: "berlin", Name: name, Currency: currency, CellID: cell}
}

func TestCreatesAMarketOnceAndRepeatsIdempotently(t *testing.T) {
	handler := newHandler()

	first, err := handler.Handle(context.Background(), berlin(" Berlin ", "EUR", "cell-1"))
	require.NoError(t, err)
	assert.True(t, first.Created)
	assert.Equal(t, vo.Name("Berlin"), first.Market.Name(), "stored trimmed")

	again, err := handler.Handle(context.Background(), berlin("Berlin", "EUR", "cell-1"))
	require.NoError(t, err)
	assert.False(t, again.Created)
	assert.Equal(t, first.Market, again.Market)
}

func TestRefusesOtherValuesForAnExistingMarket(t *testing.T) {
	handler := newHandler()
	_, err := handler.Handle(context.Background(), berlin("Berlin", "EUR", "cell-1"))
	require.NoError(t, err)

	_, err = handler.Handle(context.Background(), berlin("Berlin", "USD", "cell-1"))
	assert.ErrorIs(t, err, market.ErrAlreadyCreated)
}

func TestRefusesAMarketOfAnUnregisteredCell(t *testing.T) {
	_, err := newHandler().Handle(context.Background(), berlin("Berlin", "EUR", "cell-9"))
	assert.ErrorIs(t, err, market.ErrUnknownCell)
}

func TestRequiresManageMarkets(t *testing.T) {
	cmd := berlin("Berlin", "EUR", "cell-1")
	cmd.Subject = application.Subject{Kind: application.SubjectService, ID: "bff", Service: "bff"}
	_, err := newHandler().Handle(context.Background(), cmd)
	assert.ErrorIs(t, err, application.ErrPermissionDenied)
}

func TestRejectsInvalidValues(t *testing.T) {
	for _, cmd := range []createmarket.Command{
		berlin("", "EUR", "cell-1"),
		berlin("Berlin", "eur", "cell-1"),
		berlin("Berlin", "EUR", "Cell 1"),
		{Subject: operator, MarketID: "Berlin", Name: "Berlin", Currency: "EUR", CellID: "cell-1"},
	} {
		_, err := newHandler().Handle(context.Background(), cmd)
		assert.Error(t, err)
	}
}
