// Package market is the Market of the control plane (ADR 41): a city served by
// one Cell. The Market Placement — the Cell of the Market — is set when the
// Market is created, in a registered Cell; the API does not move a Market.
// An archived Market leaves the client list and the public gateway; its id
// stays taken, so it is never created again.
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
	// ErrArchived: the Market with the id is archived; its id is not reused.
	ErrArchived = errors.New("market archived")
)

type Market struct {
	id       vo.MarketID
	name     vo.Name
	currency vo.Currency
	timeZone vo.TimeZone
	cellID   cellvo.CellID
	archived bool
}

func New(id vo.MarketID, name vo.Name, currency vo.Currency, timeZone vo.TimeZone, cellID cellvo.CellID) Market {
	return Market{id: id, name: name, currency: currency, timeZone: timeZone, cellID: cellID}
}

func (m Market) ID() vo.MarketID       { return m.id }
func (m Market) Name() vo.Name         { return m.name }
func (m Market) Currency() vo.Currency { return m.currency }
func (m Market) TimeZone() vo.TimeZone { return m.timeZone }
func (m Market) CellID() cellvo.CellID { return m.cellID }
func (m Market) Archived() bool        { return m.archived }

// Archive returns the Market archived.
func (m Market) Archive() Market {
	m.archived = true
	return m
}

// Repeat decides a creation of a Market whose id exists already: the same
// values are the same Market (an idempotent repeat), other values — another
// name, currency, time zone or Cell — conflict, and an archived Market is
// not created again.
func (m Market) Repeat(existing Market) error {
	if existing.archived {
		return fmt.Errorf("%w: %s", ErrArchived, existing.id)
	}
	if m != existing {
		return fmt.Errorf("%w: %s is %q in %s and %s, placed in %s", ErrAlreadyCreated, existing.id, existing.name, existing.currency, existing.timeZone, existing.cellID)
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
	// List returns every Market that is not archived, by name, then by id.
	List(ctx context.Context) ([]Market, error)
	// Archive marks the Market archived; archived is false when it was
	// archived already. An unknown id is ErrNotFound.
	Archive(ctx context.Context, id vo.MarketID) (archived bool, err error)
}
