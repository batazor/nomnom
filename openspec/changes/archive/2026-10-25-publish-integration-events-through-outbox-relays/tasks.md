# Tasks

Ретроспективный change: все задачи выполнены коммитами, указанными в скобках.

## 1. payments (`77bf22b`)

- [x] 1.1 Контракт `infrastructure/grpc/nomnom/payments/v1/events.proto` и сгенерированный код; `PaymentEventsMapper` (`infrastructure/messaging/events.py`) с allowlist без `PaymentOperationDeclined`, подключён в `di/payments.py` (`FR-521`, `FR-522`, `AC-602`, `AC-605`, `AC-606`); тест `tests/test_payment_events_mapper.py`.
- [x] 1.2 UUIDv5 `event_id`, `traceparent` в `metadata` (`infrastructure/postgres/outbox.py`) и таблица `outbox` с unique `event_id` и индексом неопубликованных по `seq` (`FR-520`, `FR-525`, `FR-536`, `AC-600`, `AC-601`, `AC-612`, `AC-613`).
- [x] 1.3 `payments-relay`: `PostgresOutboxStore` с advisory lock, `KafkaPublisher` (`confluent-kafka`, idempotent, `acks=all`), `OutboxRelay`/`RelayWorker` с head-of-line, retention и `RelayMetrics`; `entrypoints/payments_relay`, `pkg/config/relay.py` (`FR-526`…`FR-533`, `FR-537`, `AC-614`…`AC-623`); тесты `tests/test_outbox_relay.py`, `tests/test_kafka_relay_integration.py`.

## 2. delivery (`cad1e19`, `c25624c`)

- [x] 2.1 Контракт `nomnom/delivery/v1/events.proto`; `DeliveryIntegrationEvents` (`slices/delivery/infrastructure/integration_events.rs`) — исчерпывающий match по `DomainEvent`, подключён в `di::postgres` (`FR-521`, `FR-523`, `FR-534`, `AC-604`, `AC-607`).
- [x] 2.2 `CourierUnassignedFromDelivery` и `DeliveryCancelled` в контракте и mapper (`FR-523`, `AC-608`; контракт также закреплён `FR-475`).
- [x] 2.3 `delivery-relay`: `infrastructure/outbox` (`PgOutbox` с advisory lock `dlvrelay`, `KafkaPublisher` на `rdkafka`, envelope, `relay_batch`/`RelayWorker`, `RelayMetrics`), `cmd/delivery-relay`, `pkg/config`, `pkg/telemetry` (`FR-525`…`FR-533`, `FR-536`, `FR-537`, `AC-612`, `AC-614`…`AC-623`); тест `tests/outbox_relay.rs`.

## 3. restaurants (`1790e29`)

- [x] 3.1 `RestaurantOrderCancelled` в `nomnom/restaurants/v1/events.proto`; mapper `slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts` с allowlist событий `RestaurantOrder`, подключён к `MongoOutbox` в `cmd/restaurants` (`FR-521`, `FR-524`, `FR-535`, `AC-603`, `AC-609`…`AC-611`); тест `test/restaurant-order-events.test.ts`.
- [x] 3.2 `restaurants-relay`: `MongoOutboxRelayStore` (lease `outbox_leases`, выборка по `seq`, retention), `KafkaPublisher` (`@confluentinc/kafka-javascript`), `OutboxRelay` с envelope и метриками, `cmd/restaurants-relay`, конфигурация `OUTBOX__*`/`KAFKA__*` (`FR-525`…`FR-533`, `FR-536`, `FR-537`, `AC-614`…`AC-623`, `AC-625`); тесты `test/outbox-relay.test.ts`, `test/outbox-relay-integration.test.ts`, `test/config.test.ts`.

## 4. Развёртывание (`8c22cb0`)

- [x] 4.1 Helm: `command` и `service.enabled` в charts, `values-relay.yaml` трёх контекстов (`replicaCount: 1`, без Service, без миграций) (`FR-538`).
- [x] 4.2 kind: `ops/kind/values/{payments,delivery,restaurants}-relay.yaml`, releases в `ops/kind/skaffold/skaffold.yaml`, раздел в `ops/kind/README.md` (`FR-538`, `AC-624`).

## 5. Документация и проверка

- [x] 5.1 README `payments`, `delivery`, `restaurants`: разделы публикации Integration Events и конфигурации relay.
- [x] 5.2 Ретроспективный change: `openspec validate publish-integration-events-through-outbox-relays --strict` и `openspec validate --all --strict`.
