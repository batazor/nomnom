# Tasks

## 1. orders

- [x] 1.1 `OrderPlaced.items` в `events.proto`, генерация, Domain Event и mapper Outbox; тесты домена, Place Order и Forwarder (`FR-498`, `AC-568`).

## 2. restaurants

- [x] 2.1 Envelope, `MessageProcessor` с классами ошибок, DLQ и метриками; in-memory Inbox; unit-тесты (`FR-496`).
- [x] 2.2 `MongoInbox`, коллекция `inbox` с unique и TTL индексами в миграции.
- [x] 2.3 Handler `OrderPlaced` → Receive Order Request, `OrdersEventSourceAccessControl`; тесты создания, повтора, схождения с RPC, DLQ и бизнес-исходов (`FR-495`, `AC-563`–`AC-566`).
- [x] 2.4 `KafkaConsumerWorker`, конфигурация `KAFKA__CONSUMER_GROUP`, `KAFKA__INITIAL_OFFSET`, `CONSUMER__*`, `di/consumer.ts`, `cmd/restaurants-consumer` с fail fast; тесты worker, drain по `SIGTERM`, конфигурации и контейнера (`FR-497`, `AC-567`).
- [x] 2.5 Integration-тест Kafka → consumer → Inbox + Restaurant Order + DLQ против MongoDB и Kafka (пропускается без `MONGODB_URI`, `KAFKA_BROKERS`).

## 3. ops и документация

- [x] 3.1 `values-consumer.yaml`, release `restaurants-consumer` в Skaffold (с профилем dev), `ops/kind/values/restaurants-consumer*.yaml` (`FR-499`); `helm lint` и `helm template`.
- [x] 3.2 README `restaurants`, ops README, локальный ADR 2, `docs/domain-events.md`, README `orders`, `ops/kind/README.md`.
- [x] 3.3 `npm test`, `npm run check`, `npm run build` в `restaurants`; `go test ./...` в `orders`; `openspec validate add-restaurants-order-placed-consumer --strict`.
