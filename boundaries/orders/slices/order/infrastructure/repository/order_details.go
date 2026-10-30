package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// OrderDetailsProjectionVersion is the shape of order_details.details. A change
// of the document increments it: catch-up at process start projects every row
// of an older version again.
const OrderDetailsProjectionVersion = 4

// projectionBatch bounds the Orders projected again in one pass of catch-up
// and rebuild; each Order is projected in its own transaction.
const projectionBatch = 500

// projectionLockKey is the advisory lock of catch-up and rebuild: "orders_details".
const projectionLockKey int64 = 0x6f72645f64657461

// detailsDocument is the stored form of orderdetails.Details without the
// columns order_id, customer_id, market_id and placed_at.
type detailsDocument struct {
	RestaurantID string                `json:"restaurant_id"`
	Items        []itemDocument        `json:"items"`
	Priority     string                `json:"priority"`
	PremiumFee   *moneyDocument        `json:"premium_fee"`
	Total        moneyDocument         `json:"total"`
	State        string                `json:"state"`
	Facts        []factDocument        `json:"facts"`
	Expected     expectedDocument      `json:"expected"`
	Cancellation *cancellationDocument `json:"cancellation"`
	// DeliveryWindow is null for an ASAP Order (projection version 3).
	DeliveryWindow *windowDocument `json:"delivery_window"`
}

type windowDocument struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type itemDocument struct {
	MenuItemID string        `json:"menu_item_id"`
	Name       string        `json:"name"`
	Quantity   int           `json:"quantity"`
	UnitPrice  moneyDocument `json:"unit_price"`
}

type moneyDocument struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type factDocument struct {
	Fact       string    `json:"fact"`
	OccurredAt time.Time `json:"occurred_at"`
	Reason     string    `json:"reason"`
}

// expectedDocument keeps a deadline nobody named as null.
type expectedDocument struct {
	ReadyAt     *time.Time `json:"ready_at"`
	PickupAt    *time.Time `json:"pickup_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}

type cancellationDocument struct {
	Outcome     string    `json:"outcome"`
	ReasonCode  string    `json:"reason_code"`
	CancelledAt time.Time `json:"cancelled_at"`
}

func newDetailsDocument(snap order.Snapshot, total vo.Money) detailsDocument {
	doc := detailsDocument{
		RestaurantID: string(snap.RestaurantID),
		Items:        make([]itemDocument, 0, len(snap.Items)),
		Priority:     string(snap.Service.Priority()),
		Total:        moneyDocument{Amount: total.Amount().String(), Currency: total.Currency()},
		State:        string(snap.State),
		Facts:        make([]factDocument, 0, len(snap.Facts)),
		Expected: expectedDocument{
			ReadyAt:     nullTime(snap.Expected.ReadyAt),
			PickupAt:    nullTime(snap.Expected.PickupAt),
			DeliveredAt: nullTime(snap.Expected.DeliveredAt),
		},
	}
	if fee, premium := snap.Service.PremiumFee(); premium {
		doc.PremiumFee = &moneyDocument{Amount: fee.Amount().String(), Currency: fee.Currency()}
	}
	for _, item := range snap.Items {
		doc.Items = append(doc.Items, itemDocument{
			MenuItemID: string(item.MenuItemID()),
			Name:       item.Name(),
			Quantity:   item.Quantity().Value(),
			UnitPrice:  moneyDocument{Amount: item.UnitPrice().Amount().String(), Currency: item.UnitPrice().Currency()},
		})
	}
	for _, fact := range snap.Facts {
		doc.Facts = append(doc.Facts, factDocument{Fact: string(fact.Fact), OccurredAt: fact.OccurredAt, Reason: fact.Reason})
	}
	if !snap.DeliveryWindow.IsZero() {
		doc.DeliveryWindow = &windowDocument{Start: snap.DeliveryWindow.Start(), End: snap.DeliveryWindow.End()}
	}
	if !snap.CancellationReason.IsZero() {
		doc.Cancellation = &cancellationDocument{
			Outcome:     string(snap.CancellationReason.Outcome()),
			ReasonCode:  snap.CancellationReason.Code(),
			CancelledAt: snap.CancelledAt,
		}
	}
	return doc
}

func (d detailsDocument) details(id order.ID, customerID order.CustomerID, marketID string, placedAt time.Time) (orderdetails.Details, error) {
	market, err := vo.NewMarketID(marketID)
	if err != nil {
		return orderdetails.Details{}, fmt.Errorf("order %s market: %w", id, err)
	}
	total, err := vo.ParseMoney(d.Total.Amount, d.Total.Currency)
	if err != nil {
		return orderdetails.Details{}, fmt.Errorf("order %s total: %w", id, err)
	}
	var fee *vo.Money
	if d.PremiumFee != nil {
		parsed, err := vo.ParseMoney(d.PremiumFee.Amount, d.PremiumFee.Currency)
		if err != nil {
			return orderdetails.Details{}, fmt.Errorf("order %s premium fee: %w", id, err)
		}
		fee = &parsed
	}
	service, err := order.RestoreService(order.Priority(d.Priority), fee)
	if err != nil {
		return orderdetails.Details{}, fmt.Errorf("order %s priority: %w", id, err)
	}
	result := orderdetails.Details{
		OrderID:      id,
		CustomerID:   customerID,
		MarketID:     market,
		RestaurantID: order.RestaurantID(d.RestaurantID),
		Items:        make([]orderdetails.Item, 0, len(d.Items)),
		Service:      service,
		Total:        total,
		State:        order.State(d.State),
		PlacedAt:     placedAt.UTC(),
		Facts:        make([]orderdetails.Fact, 0, len(d.Facts)),
		Expected: order.ExpectedTimes{
			ReadyAt:     utcTime(d.Expected.ReadyAt),
			PickupAt:    utcTime(d.Expected.PickupAt),
			DeliveredAt: utcTime(d.Expected.DeliveredAt),
		},
	}
	if d.DeliveryWindow != nil {
		window, err := order.NewDeliveryWindow(d.DeliveryWindow.Start, d.DeliveryWindow.End)
		if err != nil {
			return orderdetails.Details{}, fmt.Errorf("order %s delivery window: %w", id, err)
		}
		result.DeliveryWindow = window
	}
	for _, item := range d.Items {
		price, err := vo.ParseMoney(item.UnitPrice.Amount, item.UnitPrice.Currency)
		if err != nil {
			return orderdetails.Details{}, fmt.Errorf("order %s item %s price: %w", id, item.MenuItemID, err)
		}
		result.Items = append(result.Items, orderdetails.Item{
			MenuItemID: order.MenuItemID(item.MenuItemID), Name: item.Name, Quantity: item.Quantity, UnitPrice: price,
		})
	}
	for _, fact := range d.Facts {
		result.Facts = append(result.Facts, orderdetails.Fact{
			Fact: order.FulfillmentFact(fact.Fact), OccurredAt: fact.OccurredAt.UTC(), Reason: fact.Reason,
		})
	}
	if d.Cancellation != nil {
		result.Cancellation = &orderdetails.Cancellation{
			Outcome:     order.NegativeOutcome(d.Cancellation.Outcome),
			ReasonCode:  d.Cancellation.ReasonCode,
			CancelledAt: d.Cancellation.CancelledAt.UTC(),
		}
	}
	return result, nil
}

// projectDetails writes the OrderDetails row of the Order stored at version in
// the active transaction. A row of a newer Order version is kept: catch-up of
// an older snapshot never overwrites a later change.
func (s *Store) projectDetails(ctx context.Context, snap order.Snapshot, version uint64, total vo.Money) error {
	document, err := json.Marshal(newDetailsDocument(snap, total))
	if err != nil {
		return fmt.Errorf("encode details of %s: %w", snap.ID, err)
	}
	_, err = s.exec(ctx, psql.Insert("order_details").
		Columns("order_id", "customer_id", "market_id", "placed_at", "order_version", "projection_version", "details").
		Values(snap.ID, snap.CustomerID, snap.MarketID.String(), snap.PlacedAt, version, OrderDetailsProjectionVersion, document).
		Suffix(`ON CONFLICT (order_id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			market_id = EXCLUDED.market_id,
			placed_at = EXCLUDED.placed_at,
			order_version = EXCLUDED.order_version,
			projection_version = EXCLUDED.projection_version,
			details = EXCLUDED.details,
			projected_at = now()
		WHERE order_details.order_version <= EXCLUDED.order_version`))
	if err != nil {
		return fmt.Errorf("project details of %s: %w", snap.ID, err)
	}
	return nil
}

// OrderDetails reads one row of the projection; a missing row is
// order.ErrOrderNotFound.
func (s *Store) OrderDetails(ctx context.Context, id order.ID) (orderdetails.Details, error) {
	var (
		customerID order.CustomerID
		marketID   string
		placedAt   time.Time
		raw        []byte
	)
	err := s.queryRow(ctx, psql.Select("customer_id", "market_id", "placed_at", "details").
		From("order_details").Where(sq.Eq{"order_id": id}), &customerID, &marketID, &placedAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return orderdetails.Details{}, fmt.Errorf("%w: %q", order.ErrOrderNotFound, id)
	}
	if err != nil {
		return orderdetails.Details{}, fmt.Errorf("read details of %s: %w", id, err)
	}
	return decodeDetails(id, customerID, marketID, placedAt, raw)
}

func decodeDetails(id order.ID, customerID order.CustomerID, marketID string, placedAt time.Time, raw []byte) (orderdetails.Details, error) {
	var doc detailsDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return orderdetails.Details{}, fmt.Errorf("decode details of %s: %w", id, err)
	}
	return doc.details(id, customerID, marketID, placedAt)
}

// ListCustomerOrders pages the projection of one Customer in one Market by
// (placed_at desc, order_id desc) with a keyset cursor, on the index
// order_details_customer_market_history.
func (s *Store) ListCustomerOrders(ctx context.Context, request listcustomerorders.PageRequest) (listcustomerorders.Rows, error) {
	query := psql.Select("order_id", "placed_at", "details").
		From("order_details").Where(sq.Eq{"customer_id": request.CustomerID, "market_id": request.MarketID.String()})
	if request.Cursor != "" {
		placedAt, id, err := decodeCursor(request.Cursor)
		if err != nil {
			return listcustomerorders.Rows{}, usecase.InvalidInput("cursor", "is malformed")
		}
		query = query.Where("(placed_at, order_id) < (?, ?)", placedAt, id)
	}
	rows, err := s.query(ctx, query.OrderBy("placed_at DESC", "order_id DESC").Suffix("LIMIT ?", request.Limit+1))
	if err != nil {
		return listcustomerorders.Rows{}, fmt.Errorf("list orders of %s: %w", request.CustomerID, err)
	}
	defer rows.Close()

	page := listcustomerorders.Rows{Rows: []listcustomerorders.Row{}}
	for rows.Next() {
		var (
			id       order.ID
			placedAt time.Time
			raw      []byte
		)
		if err := rows.Scan(&id, &placedAt, &raw); err != nil {
			return listcustomerorders.Rows{}, fmt.Errorf("scan order details: %w", err)
		}
		details, err := decodeDetails(id, request.CustomerID, request.MarketID.String(), placedAt, raw)
		if err != nil {
			return listcustomerorders.Rows{}, err
		}
		page.Rows = append(page.Rows, listcustomerorders.Row{Details: details, Cursor: encodeCursor(details.PlacedAt, string(id))})
	}
	if err := rows.Err(); err != nil {
		return listcustomerorders.Rows{}, err
	}
	if len(page.Rows) > request.Limit {
		page.Rows = page.Rows[:request.Limit]
		page.NextCursor = page.Rows[len(page.Rows)-1].Cursor
	}
	return page, nil
}

func encodeCursor(placedAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(placedAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	stamp, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return time.Time{}, "", errors.New("cursor without id")
	}
	placedAt, err := time.Parse(time.RFC3339Nano, stamp)
	return placedAt, id, err
}

// ProjectionScope selects the Orders that catch-up or rebuild project again.
type ProjectionScope string

const (
	// ScopeStale is Orders without a row, with a row of an older
	// projection_version or of another Order version.
	ScopeStale ProjectionScope = "stale"
	// ScopeAll is every Order: repair after a projection defect fixed without a
	// new projection_version.
	ScopeAll ProjectionScope = "all"
)

// CatchUpOrderDetails projects the stale Orders again; every process calls it
// at start after the migrations.
func (s *Store) CatchUpOrderDetails(ctx context.Context) (int, error) {
	return s.ReprojectOrderDetails(ctx, ScopeStale)
}

// ReprojectOrderDetails projects the Orders of scope again from the order
// tables, in batches by order id, each Order in its own transaction under a
// share lock of its row, so a concurrent change waits and is never overwritten.
// Processes starting at once serialize on an advisory lock: the second finds
// nothing stale. It returns the number of Orders projected.
func (s *Store) ReprojectOrderDetails(ctx context.Context, scope ProjectionScope) (int, error) {
	switch scope {
	case ScopeStale, ScopeAll:
	default:
		return 0, fmt.Errorf("unknown projection scope %q", scope)
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire connection for the projection lock: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", projectionLockKey); err != nil {
		return 0, fmt.Errorf("projection lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", projectionLockKey)
	}()

	projected := 0
	after := ""
	for {
		ids, err := s.projectionBatch(ctx, scope, after)
		if err != nil {
			return projected, err
		}
		for _, id := range ids {
			if err := s.Do(ctx, func(ctx context.Context) error { return s.reproject(ctx, id) }); err != nil {
				return projected, err
			}
			projected++
		}
		if len(ids) < projectionBatch {
			break
		}
		after = string(ids[len(ids)-1])
	}
	if projected > 0 {
		slog.InfoContext(ctx, "order details projected again",
			slog.String("scope", string(scope)), slog.Int("orders", projected),
			slog.Int("projection_version", OrderDetailsProjectionVersion))
	}
	return projected, nil
}

func (s *Store) projectionBatch(ctx context.Context, scope ProjectionScope, after string) ([]order.ID, error) {
	query := psql.Select("o.id").From("orders o").
		Where(sq.Gt{"o.id": after}).OrderBy("o.id").Limit(projectionBatch)
	if scope == ScopeStale {
		query = query.LeftJoin("order_details d ON d.order_id = o.id").
			Where(sq.Or{
				sq.Eq{"d.order_id": nil},
				sq.NotEq{"d.projection_version": OrderDetailsProjectionVersion},
				sq.Expr("d.order_version <> o.version"),
			})
	}
	rows, err := s.query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select orders to project: %w", err)
	}
	defer rows.Close()
	var ids []order.ID
	for rows.Next() {
		var id order.ID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan order to project: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) reproject(ctx context.Context, id order.ID) error {
	var amount, currency string
	err := s.queryRow(ctx, psql.Select("total_amount::text", "total_currency").
		From("orders").Where(sq.Eq{"id": id}).Suffix("FOR SHARE"), &amount, &currency)
	if err != nil {
		return fmt.Errorf("lock order %s: %w", id, err)
	}
	total, err := vo.ParseMoney(amount, currency)
	if err != nil {
		return fmt.Errorf("order %s total: %w", id, err)
	}
	snap, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	return s.projectDetails(ctx, snap, snap.Version, total)
}
