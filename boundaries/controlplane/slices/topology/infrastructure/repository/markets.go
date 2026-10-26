package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

// Markets is the market.Repository on table markets.
type Markets struct{ store *Store }

var _ market.Repository = Markets{}

func NewMarkets(store *Store) Markets { return Markets{store: store} }

var marketColumns = []string{"market_id", "name", "currency", "cell_id"}

// Add relies on the foreign key to cells: a Market of an unregistered Cell is
// refused by the database, so a concurrent registration cannot slip between a
// check and the insert.
func (m Markets) Add(ctx context.Context, candidate market.Market) (bool, error) {
	added, err := m.store.insertOnce(ctx, psql.Insert("markets").
		Columns(append(marketColumns, "created_at")...).
		Values(string(candidate.ID()), string(candidate.Name()), string(candidate.Currency()), string(candidate.CellID()), m.store.now().UTC()))
	switch {
	case isForeignKeyViolation(err):
		return false, fmt.Errorf("%w: %s", market.ErrUnknownCell, candidate.CellID())
	case err != nil:
		return false, fmt.Errorf("insert market: %w", err)
	}
	return added, nil
}

func (m Markets) Get(ctx context.Context, id vo.MarketID) (market.Market, error) {
	var row marketRow
	err := m.store.queryRow(ctx, psql.Select(marketColumns...).From("markets").Where("market_id = ?", string(id)), row.dest()...)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return market.Market{}, fmt.Errorf("%w: %s", market.ErrNotFound, id)
	case err != nil:
		return market.Market{}, fmt.Errorf("select market: %w", err)
	}
	return row.restore()
}

func (m Markets) List(ctx context.Context) ([]market.Market, error) {
	rows, err := m.store.query(ctx, psql.Select(marketColumns...).From("markets").OrderBy("name", "market_id"))
	if err != nil {
		return nil, fmt.Errorf("select markets: %w", err)
	}
	defer rows.Close()
	var markets []market.Market
	for rows.Next() {
		var row marketRow
		if err := rows.Scan(row.dest()...); err != nil {
			return nil, fmt.Errorf("scan market: %w", err)
		}
		restored, err := row.restore()
		if err != nil {
			return nil, err
		}
		markets = append(markets, restored)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read markets: %w", err)
	}
	return markets, nil
}

type marketRow struct{ id, name, currency, cellID string }

func (r *marketRow) dest() []any { return []any{&r.id, &r.name, &r.currency, &r.cellID} }

// restore rebuilds a stored Market through its value objects.
func (r marketRow) restore() (market.Market, error) {
	id, err := vo.NewMarketID(r.id)
	if err != nil {
		return market.Market{}, fmt.Errorf("stored market: %w", err)
	}
	name, errName := vo.NewName(r.name)
	currency, errCurrency := vo.NewCurrency(r.currency)
	cellID, errCell := cellvo.NewCellID(r.cellID)
	if err := errors.Join(errName, errCurrency, errCell); err != nil {
		return market.Market{}, fmt.Errorf("stored market %s: %w", id, err)
	}
	return market.New(id, name, currency, cellID), nil
}
