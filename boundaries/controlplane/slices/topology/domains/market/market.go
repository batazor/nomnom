// Package market is the Market of the control plane (ADR 41): a city served by
// one Cell. The Market Placement — the Cell of the Market — is set when the
// Market is created, in a registered Cell; the API does not move a Market.
package market

import (
	"context"
	"errors"
	"fmt"

	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

var (
	// ErrAlreadyCreated: a Market with the id exists with other values.
	ErrAlreadyCreated = errors.New("market already created with other values")
	// ErrUnknownCell: the Cell of the Market Placement is not registered.
	ErrUnknownCell = errors.New("market placement needs a registered cell")
	// ErrNotFound: no Market has the id.
	ErrNotFound = errors.New("market not found")
)

type Market struct {
	id       vo.MarketID
	name     vo.Name
	currency vo.Currency
	cellID   cellvo.CellID
}

func New(id vo.MarketID, name vo.Name, currency vo.Currency, cellID cellvo.CellID) Market {
	return Market{id: id, name: name, currency: currency, cellID: cellID}
}

func (m Market) ID() vo.MarketID       { return m.id }
func (m Market) Name() vo.Name         { return m.name }
func (m Market) Currency() vo.Currency { return m.currency }
func (m Market) CellID() cellvo.CellID { return m.cellID }

// Repeat decides a creation of a Market whose id exists already: the same
// values are the same Market (an idempotent repeat), other values — another
// name, currency or Cell — conflict.
func (m Market) Repeat(existing Market) error {
	if m != existing {
		return fmt.Errorf("%w: %s is %q in %s, placed in %s", ErrAlreadyCreated, existing.id, existing.name, existing.currency, existing.cellID)
	}
	return nil
}

// Repository stores Markets with their Market Placement.
type Repository interface {
	// Add creates the Market unless its id exists; added is false then. A
	// Market of an unregistered Cell is ErrUnknownCell.
	Add(ctx context.Context, market Market) (added bool, err error)
	// Get returns the Market or ErrNotFound.
	Get(ctx context.Context, id vo.MarketID) (Market, error)
	// List returns every Market by name, then by id.
	List(ctx context.Context) ([]Market, error)
}
