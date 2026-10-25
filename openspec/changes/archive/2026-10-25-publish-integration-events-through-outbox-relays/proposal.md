# Proposal

> **Ретроспективная документация.** Change описывает уже реализованную и закоммиченную работу (`77bf22b` — payments relay, `cad1e19` — delivery relay, `1790e29` — restaurants relay, `c25624c` — `CourierUnassignedFromDelivery`/`DeliveryCancelled`, `8c22cb0` — Helm/kind releases relay). Требования фиксируют фактическое поведение кода, а не план; все задачи в `tasks.md` выполнены.

## Why

[ADR 13](../../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md) требует Transactional Outbox во всех publishers, а [ADR 31](../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) выбирает для `payments`, `delivery` и `restaurants` хранилища, polling relay и клиентские библиотеки. До этой работы Domain Events трёх контекстов записывались в Outbox без опубликованных контрактов (mapper по умолчанию ничего не писал), и ни один процесс не доставлял их в Kafka. Consumers `orders` (факты Payment, Courier Assignment, Restaurant Order) не могли получать события надёжно. Поведение публикации описано только в ADR и README контекстов; OpenSpec не содержит проверяемых требований к нему.

## What Changes

- Protobuf-контракты Integration Events: `nomnom/payments/v1/events.proto` (topic `payments.v1.payment-events`), `nomnom/delivery/v1/events.proto` (`delivery.v1.delivery-events`), `nomnom/restaurants/v1/events.proto` (`restaurants.v1.restaurant-order-events`, добавлен `RestaurantOrderCancelled`).
- Mapper Domain Event → Integration Event в `infrastructure` каждого контекста — явный allowlist; события без контракта в Outbox не пишутся. Mapper подключён к Outbox основного процесса, поэтому запись идёт в транзакции бизнес-изменения.
- Polling relay — отдельный процесс того же образа: `payments-relay` (Python, `psycopg`, `confluent-kafka`), `delivery-relay` (Rust, `sqlx`, `rdkafka`), `restaurants-relay` (TypeScript, `mongodb`, `@confluentinc/kafka-javascript`). Единственный активный экземпляр (advisory lock / lease), порядок `seq`, публикация с подтверждением брокера до отметки `published_at`, head-of-line остановка пачки, retention опубликованных записей, метрики Outbox.
- Kafka message key — `aggregate_id`; Kafka headers Event Envelope совпадают с `orders` (`pkg/messaging/envelope.go`).
- Конфигурация relay с fail fast на пустом хранилище и `KAFKA__BROKERS`; основной процесс к Kafka не подключается.
- Helm: `values-relay.yaml` каждого chart (отдельный release, `replicaCount: 1`, без Service и без миграций) и releases relay в kind (`ops/kind/values/*-relay.yaml`, Skaffold).

Не входит: CDC, Kafka consumers и Transactional Inbox `payments`/`delivery`, публикация событий `Restaurant`/`Menu` и курьерского слайса `delivery`, изменение Outbox `orders` (`orders-forwarder`), правки ADR и общих документов.

## Capabilities

### New Capabilities

- `integration-event-publication`: публикация Integration Events `payments`, `delivery` и `restaurants` через Transactional Outbox и polling relay — состав публикуемых событий каждого контекста, идентичность записи, единственный активный relay, порядок, подтверждение, head-of-line, Event Envelope, retention, метрики, конфигурация и развёртывание relay.

### Modified Capabilities

Нет. Существующие требования об атомарной записи бизнес-факта в Outbox (`payment-operations`, `delivery-assignment`, `delivery-execution`, `restaurant-order-lifecycle`, `kitchen-command-api`) и `FR-475` (`delivery-assignment`, контракт `CourierUnassignedFromDelivery`/`DeliveryCancelled`) не меняются; новая capability описывает, что происходит с записью Outbox после commit.

## Impact

- `boundaries/payments`: `infrastructure/messaging/` (mapper, relay, store, Kafka publisher, метрики), `infrastructure/postgres/outbox.py`, `entrypoints/payments_relay/`, `pkg/config/relay.py`, контракт `events.proto`, тесты `test_outbox_relay.py`, `test_payment_events_mapper.py`, `test_kafka_relay_integration.py`, Helm `values-relay.yaml`.
- `boundaries/delivery`: `infrastructure/outbox/` (relay, envelope, Kafka, PostgreSQL, метрики), `slices/delivery/infrastructure/integration_events.rs`, `cmd/delivery-relay`, `pkg/config`, `pkg/telemetry`, контракт `events.proto`, тест `tests/outbox_relay.rs`, Helm `values-relay.yaml`.
- `boundaries/restaurants`: `pkg/infrastructure/outbox/`, `pkg/infrastructure/mongodb/outbox-relay-store.ts`, `pkg/infrastructure/kafka/publisher.ts`, `slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts`, `cmd/restaurants-relay`, `pkg/config`, контракт `events.proto`, тесты `outbox-relay*.test.ts`, `restaurant-order-events.test.ts`, Helm `values-relay.yaml`.
- `ops/kind`: values и Skaffold releases `payments-relay`, `delivery-relay`, `restaurants-relay`.
