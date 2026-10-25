# Kafka inbound adapter слайса order

Watermill `EventGroupProcessor` по [ADR 28](../../../../../../docs/adr/0028-selective-use-of-watermill-cqrs.md): typed dispatch входящих integration events в use cases слайса. Domain logic здесь нет; Router и lifecycle — в `cmd/orders-consumer` ([`di/consumer.go`](../../../../di/consumer.go)).

```text
Kafka → Subscriber → EventGroupProcessor → watermill_handler → mapper → Command
→ application handler (Inbox → UoW → Domain/FSM → commit) → nil → ack
```

## Стримы

| Handler group | Топик | Событие → use case |
| --- | --- | --- |
| `orders.restaurant-order-events` | `restaurants.v1.restaurant-order-events` | `RestaurantOrderAccepted` → `confirmorder`; `RestaurantOrderRejected` → `rejectorder`; `PreparationStarted` → `recordpreparationstarted`; `RestaurantOrderReady` → `recordorderready`; `PreparationDelayed`, `PreparationFailed` → `relaypreparationissue` |
| `orders.delivery-events` | `delivery.v1.delivery-events` | `CourierAssignedToDelivery` → `recordcourierassignment`; `DeliveryPickedUp` → `recordpickup`; `DeliveryCompleted` → `recorddeliverycompletion`; `DeliveryFailed` → `recorddeliveryfailure` |

Имена топиков предварительные до integration specification `restaurants` и `delivery`. Контракты — `events.proto` модулей publisher'ов; Go-типы генерируются в `gen/` (`make proto-generate`).

## Файлы

- [`watermill_handler.go`](watermill_handler.go) — handler groups, чтение envelope, классификация ошибок.
- [`mapper.go`](mapper.go) — Transport DTO + envelope → Command, чистые функции.
- `gen/` — Protobuf DTO событий.

## Ошибки

| Ошибка | Класс | Действие |
| --- | --- | --- |
| Битый record, невалидный envelope (`GuardedUnmarshaler`) | deserialization | `<stream>.dlq` + ack до handler |
| `application.ErrInvalidInput`, `order.ErrOrderNotFound`, битый Protobuf payload, неизвестный `event_type` | permanent | `<stream>.dlq` + ack |
| `order.InvalidTransition`, `order.GuardFailed` | business | ack, лог |
| Прочие (store, Temporal) | transient | retry с backoff (`messaging.DefaultRetryPolicy`), затем nack и повторная доставка |
| Duplicate по Inbox | — | ack, no-op, `nomnom.inbox.duplicates` (workflow уведомляется повторно с тем же `event_id`) |

## Тесты

[`watermill_handler_test.go`](watermill_handler_test.go) — wiring на Watermill `gochannel`: маршрутизация по `event_type`, handler groups, порядок событий одного aggregate, duplicate, poison. Политика retry/poison/business — [`pkg/messaging/consumer_test.go`](../../../../pkg/messaging/consumer_test.go). [`kafka_integration_test.go`](kafka_integration_test.go) (build tag `integration`, `make test-integration-kafka`, нужен Docker) — Testcontainers Kafka `apache/kafka:4.4.0`: применение события и повторная доставка, порядок событий одного `aggregate_id` в топиках с 3 partitions, retry при временной недоступности workflow, poison topic.

## Развёртывание

Отдельный release chart с [`values-consumer.yaml`](../../../../ops/helm/values-consumer.yaml): `component: consumer`, `command: ["/orders-consumer"]`, без Service. В kind — release `orders-consumer` ([`ops/kind/values/orders-consumer.yaml`](../../../../../../ops/kind/values/orders-consumer.yaml)) против Kafka и Temporal dev server из [`manifests/messaging`](../../../../../../ops/kind/manifests/messaging/infrastructure.yaml).
