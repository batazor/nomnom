package di

import (
	"context"

	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// ProcessStore — общий PostgreSQL store процесса (ADR 8) во всех ролях портов
// слайса (PlacementStore, ReadModels, InboxStore, Activities fulfillment):
// Unit of Work и Outbox (messaging/outbound) — под декоратором бизнес-метрик,
// остальные порты — PostgreSQL напрямую. Роли объявлены рядом с потребителями,
// Wire связывает их с одним ProcessStore.
type ProcessStore struct {
	*postgres.Store
	observed observability.ObservedStore
}

// NewProcessStore собирает store процесса поверх PostgreSQL.
func NewProcessStore(store *postgres.Store, metrics *observability.DomainMetrics) *ProcessStore {
	return &ProcessStore{
		Store: store,
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

func (s *ProcessStore) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	return s.observed.Append(ctx, aggregateID, aggregateVersion, events)
}
