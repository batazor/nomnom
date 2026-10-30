package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/usecase"
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
	var feeAmount, feeCurrency *string
	if fee, premium := snap.Service.PremiumFee(); premium {
		amount, currency := fee.Amount().String(), fee.Currency()
		feeAmount, feeCurrency = &amount, &currency
	}
	_, err := s.exec(ctx, psql.Insert("orders").
		Columns("id", "customer_id", "restaurant_id", "market_id", "total_amount", "total_currency",
			"state", "placed_at", "cancelled_at", "negative_outcome", "cancellation_code", "version",
			"expected_ready_at", "expected_pickup_at", "expected_delivered_at",
			"priority", "premium_fee_amount", "premium_fee_currency",
			"delivery_window_start", "delivery_window_end").
		Values(snap.ID, snap.CustomerID, snap.RestaurantID, snap.MarketID.String(),
			created.Total().Amount().String(), created.Total().Currency(),
			snap.State, snap.PlacedAt, nullTime(snap.CancelledAt), outcome, code, snap.Version+1,
			nullTime(snap.Expected.ReadyAt), nullTime(snap.Expected.PickupAt), nullTime(snap.Expected.DeliveredAt),
			string(snap.Service.Priority()), feeAmount, feeCurrency,
			nullTime(snap.DeliveryWindow.Start()), nullTime(snap.DeliveryWindow.End())))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: %q", order.ErrOrderAlreadyExists, snap.ID)
	}
	if err != nil {
		return fmt.Errorf("insert order %s: %w", snap.ID, err)
	}
	for position, item := range snap.Items {
		_, err := s.exec(ctx, psql.Insert("order_items").
			Columns("order_id", "position", "menu_item_id", "name", "quantity", "unit_price_amount", "unit_price_currency").
			Values(snap.ID, position, item.MenuItemID(), nullString(item.Name()), item.Quantity().Value(),
				item.UnitPrice().Amount().String(), item.UnitPrice().Currency()))
		if err != nil {
			return fmt.Errorf("insert order item %s/%s: %w", snap.ID, item.MenuItemID(), err)
		}
	}
	if err := s.saveFacts(ctx, snap); err != nil {
		return err
	}
	return s.projectDetails(ctx, snap, snap.Version+1, created.Total())
}

// Save updates the Order only if its stored version is the loaded Version();
// otherwise usecase.ErrConcurrencyConflict.
func (s *Store) Save(ctx context.Context, changed *order.Order) error {
	snap := changed.Snapshot()
	outcome, code := cancellation(snap.CancellationReason)
	tag, err := s.exec(ctx, psql.Update("orders").
		Set("state", snap.State).
		Set("cancelled_at", nullTime(snap.CancelledAt)).
		Set("negative_outcome", outcome).
		Set("cancellation_code", code).
		Set("expected_ready_at", nullTime(snap.Expected.ReadyAt)).
		Set("expected_pickup_at", nullTime(snap.Expected.PickupAt)).
		Set("expected_delivered_at", nullTime(snap.Expected.DeliveredAt)).
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
		return fmt.Errorf("%w: order %s version %d", usecase.ErrConcurrencyConflict, snap.ID, snap.Version)
	}
	if err := s.saveFacts(ctx, snap); err != nil {
		return err
	}
	return s.projectDetails(ctx, snap, snap.Version+1, changed.Total())
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
		marketID         string
		cancelledAt      *time.Time
		outcome, code    *string
		readyAt          *time.Time
		pickupAt         *time.Time
		deliveredAt      *time.Time
		priority         string
		feeAmount        *string
		feeCurrency      *string
		windowStart      *time.Time
		windowEnd        *time.Time
	)
	err := s.queryRow(ctx, psql.
		Select("customer_id", "restaurant_id", "market_id", "total_amount::text", "total_currency",
			"state", "placed_at", "cancelled_at", "negative_outcome", "cancellation_code", "version",
			"expected_ready_at", "expected_pickup_at", "expected_delivered_at",
			"priority", "premium_fee_amount::text", "premium_fee_currency",
			"delivery_window_start", "delivery_window_end").
		From("orders").Where(sq.Eq{"id": id}),
		&snap.CustomerID, &snap.RestaurantID, &marketID, &amount, &currency,
		&snap.State, &snap.PlacedAt, &cancelledAt, &outcome, &code, &snap.Version,
		&readyAt, &pickupAt, &deliveredAt, &priority, &feeAmount, &feeCurrency,
		&windowStart, &windowEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return order.Snapshot{}, fmt.Errorf("%w: %q", order.ErrOrderNotFound, id)
	}
	if err != nil {
		return order.Snapshot{}, fmt.Errorf("load order %s: %w", id, err)
	}
	snap.ID = id
	if snap.MarketID, err = vo.NewMarketID(marketID); err != nil {
		return order.Snapshot{}, fmt.Errorf("order %s market: %w", id, err)
	}
	var fee *vo.Money
	if feeAmount != nil && feeCurrency != nil {
		parsed, err := vo.ParseMoney(*feeAmount, *feeCurrency)
		if err != nil {
			return order.Snapshot{}, fmt.Errorf("order %s premium fee: %w", id, err)
		}
		fee = &parsed
	}
	if snap.Service, err = order.RestoreService(order.Priority(priority), fee); err != nil {
		return order.Snapshot{}, fmt.Errorf("order %s priority: %w", id, err)
	}
	snap.PlacedAt = snap.PlacedAt.UTC()
	snap.CancelledAt = utcTime(cancelledAt)
	snap.Expected = order.ExpectedTimes{ReadyAt: utcTime(readyAt), PickupAt: utcTime(pickupAt), DeliveredAt: utcTime(deliveredAt)}
	if snap.DeliveryWindow, err = deliveryWindow(windowStart, windowEnd); err != nil {
		return order.Snapshot{}, fmt.Errorf("order %s delivery window: %w", id, err)
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
		Select("menu_item_id", "name", "quantity", "unit_price_amount::text", "unit_price_currency").
		From("order_items").Where(sq.Eq{"order_id": id}).OrderBy("position"))
	if err != nil {
		return nil, fmt.Errorf("load items of %s: %w", id, err)
	}
	defer rows.Close()
	var items []order.Item
	for rows.Next() {
		var (
			menuItemID       order.MenuItemID
			name             *string
			quantity         int
			amount, currency string
		)
		if err := rows.Scan(&menuItemID, &name, &quantity, &amount, &currency); err != nil {
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
		items = append(items, order.NewItem(menuItemID, stringValue(name), q, price))
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

// deliveryWindow reads the nullable window columns: both NULL is an ASAP
// Order; the CHECK constraint keeps them NULL together.
func deliveryWindow(start, end *time.Time) (order.DeliveryWindow, error) {
	if start == nil && end == nil {
		return order.DeliveryWindow{}, nil
	}
	return order.NewDeliveryWindow(utcTime(start), utcTime(end))
}

func cancellation(reason order.CancellationReason) (*string, *string) {
	if reason.IsZero() {
		return nil, nil
	}
	outcome, code := string(reason.Outcome()), reason.Code()
	return &outcome, &code
}

// utcTime reads a nullable timestamp: NULL is the zero time.
func utcTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// nullString stores an empty string as NULL: the value was not kept.
func nullString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// stringValue reads a nullable text column: NULL is the empty string.
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
