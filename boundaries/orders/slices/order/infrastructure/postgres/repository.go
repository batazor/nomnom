package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

var _ order.Repository = (*Store)(nil)

func (s *Store) Get(ctx context.Context, id order.ID) (*order.Order, error) {
	snapshot, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return order.Restore(snapshot)
}

// Add inserts a new Order with version Version()+1; a duplicate id is
// order.ErrOrderAlreadyExists.
func (s *Store) Add(ctx context.Context, created *order.Order) error {
	snap := created.Snapshot()
	outcome, code := cancellation(snap.CancellationReason)
	_, err := s.exec(ctx, psql.Insert("orders").
		Columns("id", "customer_id", "restaurant_id", "total_amount", "total_currency",
			"state", "placed_at", "cancelled_at", "negative_outcome", "cancellation_code", "version").
		Values(snap.ID, snap.CustomerID, snap.RestaurantID,
			created.Total().Amount().String(), created.Total().Currency(),
			snap.State, snap.PlacedAt, nullTime(snap.CancelledAt), outcome, code, snap.Version+1))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: %q", order.ErrOrderAlreadyExists, snap.ID)
	}
	if err != nil {
		return fmt.Errorf("insert order %s: %w", snap.ID, err)
	}
	for position, item := range snap.Items {
		_, err := s.exec(ctx, psql.Insert("order_items").
			Columns("order_id", "position", "menu_item_id", "quantity", "unit_price_amount", "unit_price_currency").
			Values(snap.ID, position, item.MenuItemID(), item.Quantity().Value(),
				item.UnitPrice().Amount().String(), item.UnitPrice().Currency()))
		if err != nil {
			return fmt.Errorf("insert order item %s/%s: %w", snap.ID, item.MenuItemID(), err)
		}
	}
	return s.saveFacts(ctx, snap)
}

// Save updates the Order only if its stored version is the loaded Version();
// otherwise application.ErrConcurrencyConflict.
func (s *Store) Save(ctx context.Context, changed *order.Order) error {
	snap := changed.Snapshot()
	outcome, code := cancellation(snap.CancellationReason)
	tag, err := s.exec(ctx, psql.Update("orders").
		Set("state", snap.State).
		Set("cancelled_at", nullTime(snap.CancelledAt)).
		Set("negative_outcome", outcome).
		Set("cancellation_code", code).
		Set("version", sq.Expr("version + 1")).
		Where(sq.Eq{"id": snap.ID, "version": snap.Version}))
	if err != nil {
		return fmt.Errorf("update order %s: %w", snap.ID, err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		stored := psql.Select("1").From("orders").Where(sq.Eq{"id": snap.ID})
		if err := s.queryRow(ctx, psql.Select().Column(sq.Expr("EXISTS (?)", stored)), &exists); err != nil {
			return fmt.Errorf("check order %s: %w", snap.ID, err)
		}
		if !exists {
			return fmt.Errorf("%w: %q", order.ErrOrderNotFound, snap.ID)
		}
		return fmt.Errorf("%w: order %s version %d", application.ErrConcurrencyConflict, snap.ID, snap.Version)
	}
	return s.saveFacts(ctx, snap)
}

func (s *Store) saveFacts(ctx context.Context, snap order.Snapshot) error {
	for _, fact := range snap.Facts {
		_, err := s.exec(ctx, psql.Insert("order_facts").
			Columns("order_id", "fact", "occurred_at", "reason").
			Values(snap.ID, fact.Fact, fact.OccurredAt, fact.Reason).
			Suffix("ON CONFLICT (order_id, fact) DO NOTHING"))
		if err != nil {
			return fmt.Errorf("insert fact %s of %s: %w", fact.Fact, snap.ID, err)
		}
	}
	return nil
}

func (s *Store) load(ctx context.Context, id order.ID) (order.Snapshot, error) {
	var (
		snap             order.Snapshot
		amount, currency string
		cancelledAt      *time.Time
		outcome, code    *string
	)
	err := s.queryRow(ctx, psql.
		Select("customer_id", "restaurant_id", "total_amount::text", "total_currency",
			"state", "placed_at", "cancelled_at", "negative_outcome", "cancellation_code", "version").
		From("orders").Where(sq.Eq{"id": id}),
		&snap.CustomerID, &snap.RestaurantID, &amount, &currency,
		&snap.State, &snap.PlacedAt, &cancelledAt, &outcome, &code, &snap.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return order.Snapshot{}, fmt.Errorf("%w: %q", order.ErrOrderNotFound, id)
	}
	if err != nil {
		return order.Snapshot{}, fmt.Errorf("load order %s: %w", id, err)
	}
	snap.ID = id
	snap.PlacedAt = snap.PlacedAt.UTC()
	if cancelledAt != nil {
		snap.CancelledAt = cancelledAt.UTC()
	}
	if outcome != nil && code != nil {
		if snap.CancellationReason, err = order.NewCancellationReason(order.NegativeOutcome(*outcome), *code); err != nil {
			return order.Snapshot{}, fmt.Errorf("order %s cancellation: %w", id, err)
		}
	}
	if snap.Items, err = s.items(ctx, id); err != nil {
		return order.Snapshot{}, err
	}
	if snap.Facts, err = s.facts(ctx, id); err != nil {
		return order.Snapshot{}, err
	}
	return snap, nil
}

func (s *Store) items(ctx context.Context, id order.ID) ([]order.Item, error) {
	rows, err := s.query(ctx, psql.
		Select("menu_item_id", "quantity", "unit_price_amount::text", "unit_price_currency").
		From("order_items").Where(sq.Eq{"order_id": id}).OrderBy("position"))
	if err != nil {
		return nil, fmt.Errorf("load items of %s: %w", id, err)
	}
	defer rows.Close()
	var items []order.Item
	for rows.Next() {
		var (
			menuItemID       order.MenuItemID
			quantity         int
			amount, currency string
		)
		if err := rows.Scan(&menuItemID, &quantity, &amount, &currency); err != nil {
			return nil, fmt.Errorf("scan item of %s: %w", id, err)
		}
		q, err := vo.NewQuantity(quantity)
		if err != nil {
			return nil, fmt.Errorf("item %s/%s quantity: %w", id, menuItemID, err)
		}
		price, err := vo.ParseMoney(amount, currency)
		if err != nil {
			return nil, fmt.Errorf("item %s/%s price: %w", id, menuItemID, err)
		}
		items = append(items, order.NewItem(menuItemID, q, price))
	}
	return items, rows.Err()
}

func (s *Store) facts(ctx context.Context, id order.ID) ([]order.RecordedFact, error) {
	rows, err := s.query(ctx, psql.Select("fact", "occurred_at", "reason").
		From("order_facts").Where(sq.Eq{"order_id": id}).OrderBy("occurred_at", "fact"))
	if err != nil {
		return nil, fmt.Errorf("load facts of %s: %w", id, err)
	}
	defer rows.Close()
	var facts []order.RecordedFact
	for rows.Next() {
		var fact order.RecordedFact
		if err := rows.Scan(&fact.Fact, &fact.OccurredAt, &fact.Reason); err != nil {
			return nil, fmt.Errorf("scan fact of %s: %w", id, err)
		}
		fact.OccurredAt = fact.OccurredAt.UTC()
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func cancellation(reason order.CancellationReason) (*string, *string) {
	if reason.IsZero() {
		return nil, nil
	}
	outcome, code := string(reason.Outcome()), reason.Code()
	return &outcome, &code
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
