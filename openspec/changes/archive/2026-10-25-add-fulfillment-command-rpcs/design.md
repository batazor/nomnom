# Design

## Context

Командные use cases соседей уже реализованы в application-слоях (`authorize_payment`, `receive-order-request`, `create_delivery` и др.), но не опубликованы по gRPC. `delivery` не умеет отменять доставку, `restaurants` не хранит адрес ресторана, а `CreateDelivery` требует pickup address. Публикация в BSR недоступна, verified identity не спроектирована.

## Decisions

### Контракты ключуются order_id

Workflow знает только `order_id`. `payments.CreatePayment` идемпотентен по `order_id` и возвращает существующий `payment_id`, поэтому клиент `orders` перед каждой финансовой операцией вызывает `CreatePayment` и затем операцию с `idempotency_key` шага. Команды `restaurants` и `delivery` адресуются `order_id` (один Restaurant Order и одна Delivery на Order).

### Бизнес-результат против ошибки

| Ответ | Клиент `orders` |
| --- | --- |
| `PaymentOperation.status = DECLINED` | `PaymentDecision{Approved:false, Reason}` |
| `PaymentOperation.status = PENDING` | retryable ошибка: Activity повторит тот же ключ |
| `CancelRestaurantOrder`/`CancelDelivery` с `rejection_reason` | успех компенсации без эффекта, причина в log: владелец уже закрыл шаг |
| `INVALID_ARGUMENT`, `NOT_FOUND`, `FAILED_PRECONDITION`, `ALREADY_EXISTS` | non-retryable (`application.ErrInvalidInput`) |
| `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `UNAUTHENTICATED`, `PERMISSION_DENIED`, прочие | retryable |

`UNAUTHENTICATED`/`PERMISSION_DENIED` retryable осознанно: до решения identity это конфигурационный отказ, который исправляется выкладкой, а не бизнес-решение.

### Pickup address

`Restaurant` получает обязательный для новых регистраций `address`; `GetRestaurant` его возвращает. Клиент `Delivery.Create` читает ресторан через `GetRestaurant` и передаёт `pickup_address`. Ресторан без адреса — `FAILED_PRECONDITION`.

### Cancel и Replan в delivery

Cancel Delivery: `Waiting`/`Assigned` → `Cancelled` (курьер освобождается), повтор — успех, после Pickup — бизнес-отказ `already_picked_up`, после `Delivered`/`Failed` — `already_closed`. Replan: если курьер назначен — Reassign Courier, затем Find Courier и Offer Delivery кандидату; без кандидата — успех без offer. Алгоритм остаётся в `delivery`.

### Генерация клиентов

До публикации в BSR `orders` генерирует Go-код соседних модулей из workspace (`buf generate` по путям модулей) в `slices/order/infrastructure/neighbours/grpcclient/gen`; переход на закреплённый BSR commit — вместе с публикацией (ADR 7).

### Транспорт и identity

Клиент подключается к `INTERNAL_GATEWAY_ADDR` (ADR 15) без TLS внутри кластера, deadline на вызов — из context Activity. Серверы используют существующий `SubjectResolver`; тесты подставляют resolver с `service` identity `orders-worker`.

## Risks / Trade-offs

- [В окружении все вызовы `UNAUTHENTICATED`] → retryable, workflow ждёт; решается change identity.
- [Локальная генерация вместо BSR] → временно; путь генерации зафиксирован в Makefile `orders`.
