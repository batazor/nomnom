package di

import (
	"context"
	"time"

	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository"
)

// ProcessStore — общий PostgreSQL store процесса (ADR 8) во всех ролях портов
// слайса (PlacementStore, ReadModels, InboxStore, Activities fulfillment):
// Unit of Work и Outbox (messaging/outbound) — под декоратором бизнес-метрик,
// остальные порты — PostgreSQL напрямую. Роли объявлены рядом с потребителями,
// Wire связывает их с одним ProcessStore. Каждая запись Order (Add, Save)
// кладёт в тот же Outbox Order Change Notification для подписок BFF (ADR 36).
type ProcessStore struct {
	*repository.Store
	observed observability.ObservedStore
	changes  outbound.ChangeNotifier
}

// NewProcessStore собирает store процесса поверх PostgreSQL.
func NewProcessStore(store *repository.Store, metrics *observability.DomainMetrics) *ProcessStore {
	return &ProcessStore{
		Store:   store,
		changes: outbound.NewChangeNotifier(store),
		observed: observability.ObservedStore{
			UnitOfWork: store,
			Outbox:     outbound.NewOutbox(store),
			Metrics:    metrics,
		},
	}
}

func (s *ProcessStore) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.observed.Do(ctx, fn)
}

func (s *ProcessStore) Add(ctx context.Context, created *order.Order) error {
	if err := s.Store.Add(ctx, created); err != nil {
		return err
	}
	// Add stores Version()+1, like Save.
	return s.changes.Notify(ctx, created.ID(), created.Version()+1, time.Now())
}

func (s *ProcessStore) Save(ctx context.Context, changed *order.Order) error {
	if err := s.Store.Save(ctx, changed); err != nil {
		return err
	}
	// Save stores the loaded Version()+1.
	return s.changes.Notify(ctx, changed.ID(), changed.Version()+1, time.Now())
}

func (s *ProcessStore) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	return s.observed.Append(ctx, aggregateID, aggregateVersion, events)
}
