// Package memory contains in-memory adapters of the order slice ports for unit
// tests. Store keeps every local record of the orders database in one place so
// that its Unit of Work can commit or roll back all of them together.
package memory

import (
	"context"
	"sync"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
)

const aggregateType = "order"

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
