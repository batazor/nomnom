// Package memory contains in-memory adapters of the order slice ports for unit
// tests. Store keeps every local record of the orders database in one place so
// that its Unit of Work can commit or roll back all of them together.
package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
)

const aggregateType = "order"

// OutboxRecord is the stored Outbox entry; mapping to an Integration Event and
// event_id assignment belong to the real Outbox adapter.
type OutboxRecord struct {
	AggregateType    string
	AggregateID      order.ID
	AggregateVersion uint64
	Event            order.DomainEvent
}

type inboxKey struct {
	consumer string
	eventID  string
}

type idempotencyKey struct {
	customerID order.CustomerID
	key        string
}

type state struct {
	orders      map[order.ID]order.Snapshot
	inbox       map[inboxKey]struct{}
	outbox      []OutboxRecord
	idempotency map[idempotencyKey]order.ID
	revisions   map[order.ID]application.AuthorizationRevision
	assignments map[order.ID]recordcourierassignment.Assignment
}

// clone copies the containers; stored snapshots are replaced, never mutated.
func (s state) clone() state {
	cloned := state{
		orders:      make(map[order.ID]order.Snapshot, len(s.orders)),
		inbox:       make(map[inboxKey]struct{}, len(s.inbox)),
		outbox:      append([]OutboxRecord(nil), s.outbox...),
		idempotency: make(map[idempotencyKey]order.ID, len(s.idempotency)),
		revisions:   make(map[order.ID]application.AuthorizationRevision, len(s.revisions)),
		assignments: make(map[order.ID]recordcourierassignment.Assignment, len(s.assignments)),
	}
	for id, snapshot := range s.orders {
		cloned.orders[id] = snapshot
	}
	for key := range s.inbox {
		cloned.inbox[key] = struct{}{}
	}
	for key, id := range s.idempotency {
		cloned.idempotency[key] = id
	}
	for id, revision := range s.revisions {
		cloned.revisions[id] = revision
	}
	for id, assignment := range s.assignments {
		cloned.assignments[id] = assignment
	}
	return cloned
}

// Store implements order.Repository, application.UnitOfWork, application.Inbox,
// the Outbox and idempotency ports and the read models of getorder and
// listcustomerorders.
type Store struct {
	mu        sync.Mutex
	state     state
	commits   int
	rollbacks int
}

func NewStore() *Store {
	return &Store{state: state{}.clone()}
}

type txKey struct{}

func (s *Store) inTransaction(ctx context.Context) bool {
	active, _ := ctx.Value(txKey{}).(*Store)
	return active == s
}

// lock serializes access outside a transaction; inside one the lock is held by Do.
func (s *Store) lock(ctx context.Context) func() {
	if s.inTransaction(ctx) {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Do runs fn as one serializable transaction. A nested Do joins the outer one.
// Any error or panic restores every record changed by fn.
func (s *Store) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.inTransaction(ctx) {
		return fn(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	backup := s.state.clone()
	committed := false
	defer func() {
		if !committed {
			s.state = backup
			s.rollbacks++
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{}, s)); err != nil {
		return err
	}
	committed = true
	s.commits++
	return nil
}

func (s *Store) Get(ctx context.Context, id order.ID) (*order.Order, error) {
	defer s.lock(ctx)()
	snapshot, ok := s.state.orders[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", order.ErrOrderNotFound, id)
	}
	return order.Restore(snapshot)
}

func (s *Store) Add(ctx context.Context, created *order.Order) error {
	defer s.lock(ctx)()
	if _, exists := s.state.orders[created.ID()]; exists {
		return fmt.Errorf("%w: %q", order.ErrOrderAlreadyExists, created.ID())
	}
	s.state.orders[created.ID()] = persisted(created)
	return nil
}

func (s *Store) Save(ctx context.Context, changed *order.Order) error {
	defer s.lock(ctx)()
	stored, exists := s.state.orders[changed.ID()]
	if !exists {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, changed.ID())
	}
	if stored.Version != changed.Version() {
		return fmt.Errorf("%w: order %q has version %d, loaded %d",
			application.ErrConcurrencyConflict, changed.ID(), stored.Version, changed.Version())
	}
	s.state.orders[changed.ID()] = persisted(changed)
	return nil
}

func persisted(o *order.Order) order.Snapshot {
	snapshot := o.Snapshot()
	snapshot.Version = o.Version() + 1
	return snapshot
}

func (s *Store) Accept(ctx context.Context, consumer, eventID string) (bool, error) {
	defer s.lock(ctx)()
	key := inboxKey{consumer: consumer, eventID: eventID}
	if _, processed := s.state.inbox[key]; processed {
		return false, nil
	}
	s.state.inbox[key] = struct{}{}
	return true, nil
}

func (s *Store) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	defer s.lock(ctx)()
	for _, event := range events {
		s.state.outbox = append(s.state.outbox, OutboxRecord{
			AggregateType:    aggregateType,
			AggregateID:      aggregateID,
			AggregateVersion: aggregateVersion,
			Event:            event,
		})
	}
	return nil
}

var ErrIdempotencyKeyTaken = errors.New("idempotency key already identifies another order")

func (s *Store) FindOrder(ctx context.Context, customerID order.CustomerID, key string) (order.ID, bool, error) {
	defer s.lock(ctx)()
	id, ok := s.state.idempotency[idempotencyKey{customerID: customerID, key: key}]
	return id, ok, nil
}

func (s *Store) RememberOrder(ctx context.Context, customerID order.CustomerID, key string, orderID order.ID) error {
	defer s.lock(ctx)()
	mapKey := idempotencyKey{customerID: customerID, key: key}
	if existing, ok := s.state.idempotency[mapKey]; ok && existing != orderID {
		return ErrIdempotencyKeyTaken
	}
	s.state.idempotency[mapKey] = orderID
	return nil
}

func (s *Store) OrderView(ctx context.Context, id order.ID) (getorder.OrderView, error) {
	defer s.lock(ctx)()
	snapshot, ok := s.state.orders[id]
	if !ok {
		return getorder.OrderView{}, fmt.Errorf("%w: %q", order.ErrOrderNotFound, id)
	}
	restored, err := order.Restore(snapshot)
	if err != nil {
		return getorder.OrderView{}, err
	}
	view := getorder.OrderView{
		OrderID:         snapshot.ID,
		CustomerID:      snapshot.CustomerID,
		RestaurantID:    snapshot.RestaurantID,
		DeliveryAddress: snapshot.DeliveryAddress.String(),
		Total:           restored.Total(),
		State:           snapshot.State,
		PlacedAt:        snapshot.PlacedAt,
	}
	for _, item := range snapshot.Items {
		view.Items = append(view.Items, getorder.ItemView{
			MenuItemID: item.MenuItemID(),
			Quantity:   item.Quantity().Value(),
			UnitPrice:  item.UnitPrice(),
		})
	}
	for _, fact := range snapshot.Facts {
		view.Facts = append(view.Facts, getorder.FactView{Fact: fact.Fact, OccurredAt: fact.OccurredAt, Reason: fact.Reason})
	}
	if !snapshot.CancellationReason.IsZero() {
		view.Cancellation = &getorder.CancellationView{
			Outcome:     snapshot.CancellationReason.Outcome(),
			ReasonCode:  snapshot.CancellationReason.Code(),
			CancelledAt: snapshot.CancelledAt,
		}
	}
	return view, nil
}

// ListCustomerOrders orders by placement time descending, then by id; the
// cursor is the offset of the next page.
func (s *Store) ListCustomerOrders(ctx context.Context, request listcustomerorders.PageRequest) (listcustomerorders.Page, error) {
	defer s.lock(ctx)()
	offset := 0
	if request.Cursor != "" {
		parsed, err := strconv.Atoi(request.Cursor)
		if err != nil || parsed < 0 {
			return listcustomerorders.Page{}, application.InvalidInput("cursor", "is malformed")
		}
		offset = parsed
	}

	var rows []listcustomerorders.OrderSummary
	for _, snapshot := range s.state.orders {
		if snapshot.CustomerID != request.CustomerID {
			continue
		}
		restored, err := order.Restore(snapshot)
		if err != nil {
			return listcustomerorders.Page{}, err
		}
		rows = append(rows, listcustomerorders.OrderSummary{
			OrderID:      snapshot.ID,
			RestaurantID: snapshot.RestaurantID,
			State:        snapshot.State,
			Total:        restored.Total(),
			PlacedAt:     snapshot.PlacedAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].PlacedAt.Equal(rows[j].PlacedAt) {
			return rows[i].PlacedAt.After(rows[j].PlacedAt)
		}
		return rows[i].OrderID > rows[j].OrderID
	})

	page := listcustomerorders.Page{Orders: []listcustomerorders.OrderSummary{}}
	if offset >= len(rows) {
		return page, nil
	}
	end := min(offset+request.Limit, len(rows))
	page.Orders = rows[offset:end]
	if end < len(rows) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

// OutboxRecords returns committed and in-transaction Outbox entries.
func (s *Store) OutboxRecords() []OutboxRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]OutboxRecord(nil), s.state.outbox...)
}

func (s *Store) InboxSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.state.inbox)
}

func (s *Store) Commits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits
}

func (s *Store) Rollbacks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rollbacks
}

// Snapshot returns the persisted form of an Order, or false when it is missing.
func (s *Store) Snapshot(id order.ID) (order.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.state.orders[id]
	return snapshot, ok
}

// RememberRevision keeps the authorization revision of an Order.
func (s *Store) RememberRevision(ctx context.Context, orderID order.ID, revision application.AuthorizationRevision) error {
	defer s.lock(ctx)()
	if _, ok := s.state.orders[orderID]; !ok {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	s.state.revisions[orderID] = revision
	return nil
}

// Revision returns the authorization revision of an Order, if any.
func (s *Store) Revision(ctx context.Context, orderID string) (application.AuthorizationRevision, bool, error) {
	defer s.lock(ctx)()
	revision, ok := s.state.revisions[order.ID(orderID)]
	return revision, ok, nil
}

// CourierAssignment returns the stored Courier Assignment of an Order, if any.
func (s *Store) CourierAssignment(ctx context.Context, orderID order.ID) (recordcourierassignment.Assignment, bool, error) {
	defer s.lock(ctx)()
	assignment, ok := s.state.assignments[orderID]
	return assignment, ok, nil
}

// RememberCourierAssignment replaces the Courier Assignment and the
// authorization revision of an Order.
func (s *Store) RememberCourierAssignment(ctx context.Context, orderID order.ID, assignment recordcourierassignment.Assignment, revision application.AuthorizationRevision) error {
	defer s.lock(ctx)()
	if _, ok := s.state.orders[orderID]; !ok {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	s.state.assignments[orderID] = assignment
	if revision != "" {
		s.state.revisions[orderID] = revision
	}
	return nil
}

// CountByState — число Orders в каждом состоянии.
func (s *Store) CountByState(ctx context.Context) (map[order.State]int64, error) {
	defer s.lock(ctx)()
	counts := map[order.State]int64{}
	for _, snapshot := range s.state.orders {
		counts[snapshot.State]++
	}
	return counts, nil
}
