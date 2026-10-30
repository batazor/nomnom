# Kafka inbound adapter слайса order

Watermill `EventGroupProcessor` по [ADR 28](../../../../../../docs/adr/0028-selective-use-of-watermill-cqrs.md): typed dispatch входящих integration events в use cases слайса. Domain logic здесь нет; Router и lifecycle — в `cmd/orders-consumer` ([`di/consumer.go`](../../../../di/consumer.go)).

```text
Kafka → Subscriber → EventGroupProcessor → watermill_handler → mapper → Command
→ application handler (Inbox → UoW → Domain/FSM → commit) → nil → ack
```

## Стримы

| Handler group | Топик | Событие → use case |
| --- | --- | --- |
| `orders.restaurant-order-events` | `restaurants.v1.restaurant-order-events` | `RestaurantOrderAccepted` → `confirmorder`; `RestaurantOrderRejected` → `rejectorder`; `PreparationStarted` → `recordpreparationstarted`; `RestaurantOrderReady` → `recordorderready`; `PreparationDelayed` → `recordpreparationdelay`; `PreparationFailed` → `relaypreparationissue`; `RestaurantOrderCancelled` — подтверждается без действия: отмену запросил сам workflow |
| `orders.delivery-events` | `delivery.v1.delivery-events` | `CourierAssignedToDelivery` → `recordcourierassignment`; `CourierUnassignedFromDelivery`, `DeliveryCancelled` → `recordcourierrelease`; `DeliveryPickedUp` → `recordpickup`; `DeliveryCompleted` → `recorddeliverycompletion`; `DeliveryFailed` → `recorddeliveryfailure` |
| `orders.order-events` | `orders.v1.order-events` (собственный, [ADR 5](../../../../docs/adr/0005-order-fulfillment-orchestration.md#старт)) | `OrderPlaced` → `startfulfillment` (`SignalWithStart` workflow; ошибка Temporal — transient); `OrderCompleted`, `OrderCancelled` — явный no-op ack, иначе `rejectUnknownEvents` отправил бы их в `orders.v1.order-events.dlq` |

Имена топиков предварительные до integration specification `restaurants` и `delivery`. Группа `orders.order-events` впервые читает топик с `KAFKA__INITIAL_OFFSET: oldest`: завершённые заказы пропускаются, заказы без workflow запускаются. Контракты — `events.proto` модулей publisher'ов; Go-типы генерируются в `gen/` (`make proto-generate`).

## Файлы

- [`watermill_handler.go`](watermill_handler.go) — handler groups, чтение envelope, классификация ошибок.
- [`mapper.go`](mapper.go) — Transport DTO + envelope → Command, чистые функции.
- `gen/` — Protobuf DTO событий.

## Ошибки

Вид ошибки use case даёт `errorkind.Classify`; класс consumer'а — таблица `classByKind` в [`watermill_handler.go`](watermill_handler.go), покрывающая все виды.

| Ошибка | Класс | Действие |
| --- | --- | --- |
| Битый record, невалидный envelope, value вне wire format Schema Registry (`GuardedUnmarshaler`) | deserialization | `<stream>.dlq` + ack до handler |
| Id схемы неизвестен Registry или её subjects не содержат `event_type` (FR-715) | deserialization | `<stream>.dlq` + ack до handler |
| Schema Registry недоступен | transient | nack и повторная доставка: partition ждёт Registry |
| Вид `invalid_input`, `not_found`, `permission`; битый Protobuf payload, неизвестный `event_type` | permanent | `<stream>.dlq` + ack |
| Вид `domain_rule` (`order.InvalidTransition`, `order.GuardFailed`, …), `already_exists` | business | ack, лог |
| Вид `conflict`, `unavailable`, `internal` (store, Temporal) | transient | retry с backoff (`messaging.DefaultRetryPolicy`), затем nack и повторная доставка |
| Duplicate по Inbox | — | ack, no-op, `nomnom.inbox.duplicates` (workflow уведомляется повторно с тем же `event_id`) |

## Тесты

[`watermill_handler_test.go`](watermill_handler_test.go) — wiring на Watermill `gochannel`: маршрутизация по `event_type`, handler groups, порядок событий одного aggregate, duplicate, poison. Политика retry/poison/business — [`infrastructure/messaging/consumer_test.go`](../../../../infrastructure/messaging/consumer_test.go). Схемы событий тесты регистрируют в Confluent Schema Registry из testcontainers и оформляют payload в wire format, как SMT `SchemaFraming` коннектора (`main_test.go`). [`kafka_integration_test.go`](kafka_integration_test.go) (build tag `integration`, `make test-integration-kafka`, нужен Docker) — Testcontainers Kafka `apache/kafka:4.3.1`: применение события и повторная доставка, порядок событий одного `aggregate_id` в топиках с 3 partitions, retry при временной недоступности workflow, poison topic, запись вне своей схемы.

## Развёртывание

Отдельный release chart с [`values-consumer.yaml`](../../../../ops/helm/values-consumer.yaml): `component: consumer`, `command: ["/orders-consumer"]`, без Service. В kind — release `orders-consumer` ([`ops/kind/values/orders-consumer.yaml`](../../../../../../ops/kind/values/orders-consumer.yaml)) против Kafka и Temporal dev server из [`manifests/messaging`](../../../../../../ops/kind/manifests/messaging/infrastructure.yaml).
