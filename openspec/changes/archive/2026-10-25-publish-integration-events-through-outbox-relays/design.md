# Design

## Context

Ретроспективный design уже реализованной публикации Integration Events `payments`, `delivery` и `restaurants`. Архитектурные решения приняты в [ADR 13](../../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md) и [ADR 31](../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md); ниже — как они воплощены в коде и где реализация уточняет ADR.

| Context | Хранилище Outbox | Relay | Topic | Aggregate type |
| --- | --- | --- | --- | --- |
| `payments` | PostgreSQL, таблица `outbox` | `payments-relay` (`entrypoints/payments_relay`) | `payments.v1.payment-events` | `Payment` |
| `delivery` | PostgreSQL, таблица `delivery_outbox` | `delivery-relay` (`cmd/delivery-relay`) | `delivery.v1.delivery-events` | `delivery` |
| `restaurants` | MongoDB, коллекции `outbox`, `outbox_counters`, `outbox_leases` | `restaurants-relay` (`cmd/restaurants-relay`) | `restaurants.v1.restaurant-order-events` | `RestaurantOrder` |

## Goals / Non-Goals

**Goals:** зафиксировать проверяемое поведение публикации — какие события публикуются и какие нет, гарантии доставки и порядка, формат Event Envelope, эксплуатационные свойства relay.

**Non-Goals:** consumers и Inbox, CDC, Outbox `orders`, события `Restaurant`/`Menu` и курьерского слайса.

## Decisions

### 1. Polling relay, не CDC

Как в ADR 31: отдельный процесс `<context>-relay` того же образа, по аналогии с `orders-forwarder`. Debezium, logical replication и MongoDB change streams не используются — они добавили бы Kafka Connect или resume tokens как эксплуатируемую инфраструктуру. Цена — задержка не меньше `OUTBOX__POLL_INTERVAL` (по умолчанию `1s`) и постоянные запросы к Outbox. В `payments` и `delivery` после полной успешной пачки следующая читается без паузы, поэтому backlog разгребается быстрее интервала; `restaurants-relay` выполняет одну пачку за интервал (`PeriodicLoop`).

### 2. Единственный активный relay

- PostgreSQL (`payments`, `delivery`): session-level `pg_try_advisory_lock` с константным ключом контекста (`payments` — ASCII `"payments"`, `delivery` — ASCII `"dlvrelay"`) на выделенном соединении. Потеря соединения снимает lock; реплика переподключается и пробует снова. При shutdown lock освобождается явно `pg_advisory_unlock`.
- MongoDB (`restaurants`): lease-документ `_id: "restaurants-relay"` в `outbox_leases` с `owner` (`hostname/pid`) и `expiresAt`. Захват — upsert с условием «владелец я или срок истёк»; конфликт `_id` (duplicate key) означает чужой lease. Lease продлевается каждый цикл на `OUTBOX__LEASE_TTL` (`15s`, должен быть больше интервала опроса) и удаляется при shutdown, чтобы standby не ждал истечения.
- Без lock/lease relay не читает Outbox и сообщает пустой backlog; replicas служат failover. Helm задаёт `replicaCount: 1`.

### 3. Порядок `seq`, без offset

Выборка — до `OUTBOX__BATCH_SIZE` (`100`) записей с пустым `published_at` по возрастанию `seq` (`BIGSERIAL` в PostgreSQL; в MongoDB — счётчик `outbox_counters`, выделяемый в той же транзакции, unique index `outbox_seq_unique`, индекс выборки `outbox_pending_by_seq`). Offset не хранится: запись, закоммиченная позже записи с большим `seq`, попадёт в следующий цикл. Порядок записей одного aggregate гарантирует optimistic concurrency, сериализующая их транзакции.

### 4. Publish-then-mark

Запись публикуется idempotent producer'ом с `acks=all` (librdkafka во всех трёх клиентах), key — `aggregate_id`. `published_at` выставляется только после подтверждения брокера (delivery report / resolved `send`). Сбой между подтверждением и отметкой даёт повторную публикацию с тем же `event_id` — штатно по ADR 13, consumers дедуплицируют по `event_id`.

### 5. Head-of-line остановка

Первая неудачная запись останавливает пачку: `attempts` увеличивается, `last_error` сохраняется (усечён до 1000 символов в `payments`, 1024 в `restaurants`), записи после неё в этом цикле не публикуются; следующий цикл начинает с неё же. Это осознанная плата за порядок; задержку видно по `nomnom_outbox_oldest_pending_age_seconds`. Классы ошибок для метрики различаются по клиенту: `payments` — `transient | permanent` (по `retriable` ошибки брокера), `delivery` — `timeout | queue_full | broker | client`, `restaurants` — `timeout | broker | unknown`.

### 6. Retention

Опубликованные записи старше `OUTBOX__RETENTION` (по умолчанию 7 дней) удаляет активный relay; неопубликованные не удаляются никогда. Частота очистки: `payments` и `restaurants` — не чаще раза в минуту, `delivery` — `OUTBOX__CLEANUP_INTERVAL` (по умолчанию `1h`).

### 7. Идентичность записи: UUIDv5 `event_id`

`event_id` = UUIDv5 в namespace `8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13` от `"{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}"`, где `event_type` — полное имя Protobuf-сообщения. Повторная запись того же события той же версии даёт тот же `event_id`; unique index по `event_id` откатывает такую транзакцию. События одной транзакции получают один `aggregate_version`, а различаются через `event_type` (например, `CourierUnassignedFromDelivery` и `CourierAssignedToDelivery` в одной Reassign — см. `FR-475`/`AC-533`).

### 8. Event Envelope как у `orders`

Kafka headers: `event_id`, `event_type`, `occurred_at` (UTC, формат Go `time.RFC3339Nano`: без хвостовых нулей дробной части, суффикс `Z`), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent` (только если записан в `metadata`), плюс `_watermill_message_uuid` = `event_id` для Watermill consumers `orders`. Формат совпадает с `orders/pkg/messaging/envelope.go`, поэтому consumers не зависят от языка publisher. `traceparent` берётся из активного span при записи (`payments`, `restaurants`); процесс `delivery` пока trace context не имеет и пишет пустой `metadata`.

### 9. Явные allowlist mappers

Mapper находится в `infrastructure` слайса (`payments` — `infrastructure/messaging/events.py`) и возвращает «нет контракта» для любого события вне списка; Outbox тогда ничего не пишет. По умолчанию фабрики хранилищ используют mapper без контрактов (`NoPublishedContracts`, `NoIntegrationEvents`, `noIntegrationEvents`) — для тестовых сборок; основной процесс подключает реальный mapper.

| Context | Публикуется | Сознательно не публикуется |
| --- | --- | --- |
| `payments` | `PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded` (`payment_id`, `order_id`, `Money amount`); `PaymentFailed` (`reason`) | `PaymentOperationDeclined` — внутренний факт отказа отдельной операции провайдера |
| `delivery` | `CourierAssignedToDelivery`, `CourierUnassignedFromDelivery` (`courier_id`); `DeliveryPickedUp`, `DeliveryCompleted`; `DeliveryFailed` (`reason`); `DeliveryCancelled` (`reason`, пустой при отсутствии) | `courier_id` в Pickup/Completion/Failure и `released_courier_id` отмены не входят в контракт; события курьерского слайса не публикуются. Match без wildcard: новый вариант `DomainEvent` не компилируется, пока не сопоставлен |
| `restaurants` | `RestaurantOrderAccepted`, `RestaurantOrderRejected` (`reason`, пустой при отсутствии), `PreparationStarted`, `PreparationDelayed` (`expected_ready_at`), `RestaurantOrderFailed` → `PreparationFailed` (`reason`), `RestaurantOrderReady`, `RestaurantOrderCancelled` (`reason`, `preparation_started`) | `RestaurantOrderReceived`, `RestaurantOrderQueued`, `RestaurantOrderAssembled` (локальные факты) и все события aggregates `Restaurant`/`Menu` |

### 10. Метрики

OpenTelemetry instruments с именами и attributes из registry `ops/semconv`: `nomnom.outbox.pending.messages` и `nomnom.outbox.oldest_pending.age` (`messaging.destination.name`; только у активного экземпляра), `nomnom.outbox.publish.retries` (+ `nomnom.event.type`, `nomnom.error.class`), histograms `nomnom.outbox.publish.duration` (+ `nomnom.outcome`) и `nomnom.outbox.publish.lag` (`occurred_at` → подтверждение брокера). В Prometheus — `nomnom_outbox_pending_messages`, `nomnom_outbox_oldest_pending_age_seconds`, `nomnom_outbox_publish_retries_total`, `nomnom_outbox_publish_duration_seconds`, `nomnom_outbox_publish_lag_seconds`. ADR 31 упоминает label `boundary`; реализация различает контекст по topic (`messaging.destination.name`) и `service.name` ресурса.

### 11. Конфигурация и развёртывание

Relay требует хранилище (`DATABASE__URL` / `MONGODB__URI`) и `KAFKA__BROKERS`; пустое значение — ошибка на старте (fail fast). Прочие параметры: `KAFKA__CLIENT_ID` (`<context>-relay`), `OUTBOX__POLL_INTERVAL`, `OUTBOX__BATCH_SIZE`, `OUTBOX__RETENTION`, у `delivery` — `OUTBOX__PUBLISH_TIMEOUT` и `OUTBOX__CLEANUP_INTERVAL`, у `restaurants` — `OUTBOX__LEASE_TTL`. Основной процесс к Kafka не подключается. Relay следует runtime contract `pkg/lifecycle` (технический порт, drain при `SIGTERM`, освобождение lock/lease). Helm: отдельный release того же chart с `values-relay.yaml` (`component: relay`, `replicaCount: 1`, свой `command`, `service.enabled: false`, `database.migrate: false` — схему применяет release основного процесса); kind — `ops/kind/values/<context>-relay.yaml` с `KAFKA__BROKERS` кластера и Skaffold releases.

## Risks / Trade-offs

- [Head-of-line блокирует весь стрим при одной «ядовитой» записи] → осознанно ради порядка; alert на возраст старейшей записи, `attempts`/`last_error` для диагностики.
- [Повторная публикация после сбоя между ack и отметкой] → детерминированный `event_id` и Inbox consumers.
- [Single active relay ограничивает пропускную способность] → как у `orders`; пересматривается вместе с переходом на CDC.
- [Требования restaurant-management/restaurant-menu говорят о записи `RestaurantOpened`, `RestaurantClosed`, `MenuItemAvailabilityChanged` в Outbox] → на уровне application-порта это так, но MongoDB-адаптер с mapper Kitchen Order такие события не сохраняет: контракта нет. Расхождение формулировок не исправляется этим change; при появлении контракта нужен отдельный change.
- [Lease MongoDB зависит от часов реплик] → TTL `15s` значительно больше интервала опроса; при сильном расхождении часов возможна кратковременная двойная публикация, которую поглощает дедупликация по `event_id`.
