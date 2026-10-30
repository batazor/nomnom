package publishroutes

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

var (
	cell1 = DeclaredCell{ID: "cell-1", RouterNamespace: "nomnom-bff", RouterName: "bff", RouterPort: 3010}
	// berlin and hamburg are declared out of the order of the list of Markets.
	hamburg = DeclaredMarket{ID: "hamburg", Name: "Hamburg", Currency: "EUR", TimeZone: "Europe/Berlin", CellID: "cell-1"}
	berlin  = DeclaredMarket{ID: "berlin", Name: "Berlin", Currency: "EUR", TimeZone: "Europe/Berlin", CellID: "cell-1"}
)

func TestPublishAppliesTheMarketsByNameAndRecordsTheOutcome(t *testing.T) {
	registry := NewMockRegistry(t)
	applier := NewMockApplier(t)
	registry.EXPECT().Read(mock.Anything).Return(Topology{Cells: []DeclaredCell{cell1}, Markets: []DeclaredMarket{hamburg, berlin}}, nil).Once()
	applier.EXPECT().Apply(mock.Anything, mock.MatchedBy(func(routes []MarketRoute) bool {
		return len(routes) == 2 && routes[0].Market.ID() == vo.MarketID("berlin") && routes[1].Market.ID() == vo.MarketID("hamburg") &&
			routes[0].Router.Name() == "bff"
	})).Return(nil).Once()
	registry.EXPECT().Record(mock.Anything, Outcomes{
		Cells: map[string]Outcome{"cell-1": {Ready: true, Reason: ReasonAccepted, Message: "the Cell is accepted"}},
		Markets: map[string]Outcome{
			"berlin":  {Ready: true, Reason: ReasonRoutesApplied, Message: "the routes of the Market are applied"},
			"hamburg": {Ready: true, Reason: ReasonRoutesApplied, Message: "the routes of the Market are applied"},
		},
	}).Return(nil).Once()

	require.NoError(t, New(registry, applier).Publish(t.Context()))
}

// AC-1010, AC-1011: a Market that cannot be served stays off the gateway and
// does not stop the others.
func TestPublishLeavesOutAMarketItCannotServe(t *testing.T) {
	registry := NewMockRegistry(t)
	applier := NewMockApplier(t)
	noCell := DeclaredMarket{ID: "paris", Name: "Paris", Currency: "EUR", TimeZone: "Europe/Paris", CellID: "cell-2"}
	mars := DeclaredMarket{ID: "olympus", Name: "Olympus", Currency: "EUR", TimeZone: "Mars/Olympus", CellID: "cell-1"}
	archived := DeclaredMarket{ID: "bremen", Name: "Bremen", Currency: "EUR", TimeZone: "Europe/Berlin", CellID: "cell-1", Archived: true}
	registry.EXPECT().Read(mock.Anything).Return(Topology{Cells: []DeclaredCell{cell1}, Markets: []DeclaredMarket{berlin, noCell, mars, archived}}, nil).Once()
	applier.EXPECT().Apply(mock.Anything, mock.MatchedBy(func(routes []MarketRoute) bool {
		return len(routes) == 1 && routes[0].Market.ID() == vo.MarketID("berlin")
	})).Return(nil).Once()
	registry.EXPECT().Record(mock.Anything, mock.MatchedBy(func(outcomes Outcomes) bool {
		return outcomes.Markets["berlin"].Ready &&
			outcomes.Markets["paris"].Reason == ReasonCellNotFound && !outcomes.Markets["paris"].Ready &&
			outcomes.Markets["olympus"].Reason == ReasonInvalidTimeZone && !outcomes.Markets["olympus"].Ready &&
			outcomes.Markets["bremen"].Reason == ReasonArchived && !outcomes.Markets["bremen"].Ready
	})).Return(nil).Once()

	require.NoError(t, New(registry, applier).Publish(t.Context()))
}

// An empty registry still publishes: the list of Markets exists and is empty.
func TestPublishAppliesAnEmptyRegistry(t *testing.T) {
	registry := NewMockRegistry(t)
	applier := NewMockApplier(t)
	registry.EXPECT().Read(mock.Anything).Return(Topology{}, nil).Once()
	applier.EXPECT().Apply(mock.Anything, []MarketRoute{}).Return(nil).Once()
	registry.EXPECT().Record(mock.Anything, Outcomes{Cells: map[string]Outcome{}, Markets: map[string]Outcome{}}).Return(nil).Once()

	require.NoError(t, New(registry, applier).Publish(t.Context()))
}

// AC-807: the failure reaches the operator in the status and asks for a repeat.
func TestPublishRecordsAFailedApply(t *testing.T) {
	registry := NewMockRegistry(t)
	applier := NewMockApplier(t)
	refused := errors.New("kubernetes API refused")
	registry.EXPECT().Read(mock.Anything).Return(Topology{Cells: []DeclaredCell{cell1}, Markets: []DeclaredMarket{berlin}}, nil).Once()
	applier.EXPECT().Apply(mock.Anything, mock.Anything).Return(refused).Once()
	registry.EXPECT().Record(mock.Anything, mock.MatchedBy(func(outcomes Outcomes) bool {
		return outcomes.Markets["berlin"] == Outcome{Reason: ReasonRoutesUnavailable, Message: refused.Error()} && outcomes.Cells["cell-1"].Ready
	})).Return(nil).Once()

	err := New(registry, applier).Publish(t.Context())
	require.ErrorIs(t, err, ErrRoutesUnavailable)
	assert.ErrorIs(t, err, refused)
}

func TestPublishStopsWithoutTheRegistry(t *testing.T) {
	registry := NewMockRegistry(t)
	applier := NewMockApplier(t)
	unreadable := errors.New("cache is not synced")
	registry.EXPECT().Read(mock.Anything).Return(Topology{}, unreadable).Once()

	require.ErrorIs(t, New(registry, applier).Publish(t.Context()), unreadable)
}

func TestPublishRepeatsWhenTheOutcomeIsNotRecorded(t *testing.T) {
	registry := NewMockRegistry(t)
	applier := NewMockApplier(t)
	conflict := errors.New("status not patched")
	registry.EXPECT().Read(mock.Anything).Return(Topology{}, nil).Once()
	applier.EXPECT().Apply(mock.Anything, mock.Anything).Return(nil).Once()
	registry.EXPECT().Record(mock.Anything, mock.Anything).Return(conflict).Once()

	require.ErrorIs(t, New(registry, applier).Publish(t.Context()), conflict)
}
