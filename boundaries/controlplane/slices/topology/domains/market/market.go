// Package market is the Market of the control plane (ADR 41): a city served by
// one Cell, its Market Placement.
package market

import (
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

type Market struct {
	id       vo.MarketID
	name     vo.Name
	currency vo.Currency
	timeZone vo.TimeZone
	cellID   cellvo.CellID
}

func New(id vo.MarketID, name vo.Name, currency vo.Currency, timeZone vo.TimeZone, cellID cellvo.CellID) Market {
	return Market{id: id, name: name, currency: currency, timeZone: timeZone, cellID: cellID}
}

func (m Market) ID() vo.MarketID       { return m.id }
func (m Market) Name() vo.Name         { return m.name }
func (m Market) Currency() vo.Currency { return m.currency }
func (m Market) TimeZone() vo.TimeZone { return m.timeZone }
func (m Market) CellID() cellvo.CellID { return m.cellID }
