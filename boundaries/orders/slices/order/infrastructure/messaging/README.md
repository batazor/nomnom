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
| `application.ErrInvalidInput`, `order.ErrOrderNotFound`, битый envelope/payload, неизвестный `event_type` | permanent | poison topic `kafka.poison_topic` + ack |
| `order.InvalidTransition`, `order.GuardFailed` | business | ack, лог |
| Прочие (store, Temporal) | transient | retry с backoff (`messaging.DefaultRetryPolicy`); после лимита — poison |
| Duplicate по Inbox | — | ack, no-op (workflow уведомляется повторно с тем же `event_id`) |

## Тесты

[`watermill_handler_test.go`](watermill_handler_test.go) — wiring на Watermill `gochannel`: маршрутизация по `event_type`, handler groups, порядок событий одного aggregate, duplicate, poison. Политика retry/poison/business — [`pkg/messaging/consumer_test.go`](../../../../pkg/messaging/consumer_test.go). Integration на Testcontainers Kafka и E2E в kind пока не реализованы.
