package repository

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/getplacementmap"
)

// Placements is the Placement Map read model: markets joined with cells.
type Placements struct{ store *Store }

var _ getplacementmap.Reader = Placements{}

func NewPlacements(store *Store) Placements { return Placements{store: store} }

func (p Placements) Placements(ctx context.Context) ([]getplacementmap.Placement, error) {
	rows, err := p.store.query(ctx, placementQuery())
	if err != nil {
		return nil, fmt.Errorf("select placements: %w", err)
	}
	defer rows.Close()
	var placements []getplacementmap.Placement
	for rows.Next() {
		var placement getplacementmap.Placement
		if err := rows.Scan(&placement.MarketID, &placement.CellID, &placement.GatewayAddress); err != nil {
			return nil, fmt.Errorf("scan placement: %w", err)
		}
		placements = append(placements, placement)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read placements: %w", err)
	}
	return placements, nil
}

// placementColumns join a Market with the gateway of its Cell.
var placementColumns = []string{"m.market_id", "m.cell_id", "c.gateway_address"}

func placementQuery() sq.SelectBuilder {
	return psql.Select(placementColumns...).From("markets m").Join("cells c ON c.cell_id = m.cell_id").OrderBy("m.market_id")
}
