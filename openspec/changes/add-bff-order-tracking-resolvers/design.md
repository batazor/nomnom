# Design

## Context

- Схема BFF и правила представления зафиксированы в `openspec/specs/customer-graphql-contract` (решения в archive change `add-customer-graphql-contract`). Таблица `OrderStatus` и правило `availableUntil` — в README BFF.
- `orders.v1.Order`: `state`, `items` (`menu_item_id`, `quantity`, `unit_price`), `total`, `delivery_address`, `placed_at`, `fulfillment_facts` (`kind`, `occurred_at`, `reason`), `cancellation` (`outcome`, `reason_code`, `cancelled_at`), `cancellation_window`.
- `restaurants.v1`: `GetRestaurant` публичен; use case Get Menu публичен (FR-216), но RPC нет.
- BFF — Cosmo Router и Go plugin; identity — пакет `plugins/bff/identity` (ADR 32).

## Decisions

### 1. Слайс `order-tracking` в плагине

По BFF ADR 2 слайсы соответствуют клиентским сценариям:

```text
plugins/bff/
  ordertracking/
    applications/     сценарии GetOrder и RequestCancellation, порты Orders и Catalog, представление
    infrastructure/   gRPC-адаптеры orders и restaurants, перевод proto ↔ applications
  infrastructure/grpc/gen/   сгенерированные клиенты (buf.gen.yaml из workspace)
  src/main.go          resolvers QueryOrder / MutationRequestOrderCancellation вызывают сценарии
```

`applications` не импортирует Protobuf ни BFF-схемы, ни контекстов: сценарий возвращает прикладное представление, resolver переводит его в типы GraphQL plugin.

### 2. Представление заказа

- **Status** — по таблице README BFF: `placed` → `RECEIVED`; `confirmed` без фактов → `CONFIRMED`; есть `preparation_started` → `PREPARING`; `ready_for_pickup` → `READY_FOR_PICKUP`; `picked_up` → `PICKED_UP`; `completed` → `DELIVERED`; `cancelled` + `outcome=cancellation` → `CANCELLED`; `cancelled` + `outcome=failure` → `FAILED`. Неизвестное состояние — ошибка resolver, а не догадка.
- **endReason** по `reason_code`: `customer_requested` → `CUSTOMER_CANCELLED`; `restaurant_rejected`, `restaurant_acceptance_timeout` → `RESTAURANT_REJECTED`; `delivery_failed`, `delivery_timeout`, `pickup_timeout` → `DELIVERY_FAILED`; остальное → `NOT_FULFILLED`.
- **Milestones**: `PLACED` — `placed_at`; факты `preparation_started`, `ready_for_pickup`, `courier_assigned`, `picked_up`, `delivery_completed` → `PREPARATION_STARTED`, `READY_FOR_PICKUP`, `COURIER_ASSIGNED`, `PICKED_UP`, `DELIVERED`; `ENDED` — `cancelled_at`. `CONFIRMED` не публикуется: у `orders` нет момента подтверждения. Порядок — по времени.
- **Позиции**: название из `GetMenu` того же ресторана по `menu_item_id`; если позиции уже нет в меню, `name` — пустая строка, UI показывает нейтральную подпись. `lineTotal = unit_price × quantity` считается десятичной арифметикой (`math/big`) с масштабом цены, без float.
- **cancellation**: `availableUntil = closes_at`, если статус не финальный и `opens_at ≤ now ≤ closes_at` по часам BFF; иначе `null`.
- **Ресторан**: `id`, `name`, `address` из `GetRestaurant`; `acceptingOrders` — `is_open`; `menu` в этом resolver не заполняется (поле nullable).

### 3. Identity и вызовы

Resolver проверяет access token (`identity.AccessTokenVerifier.FromIncomingContext`), кладёт покупателя в context (`identity.WithUser`). Клиент `orders` использует interceptor с audience `nomnom.orders` — вызов уходит с service token BFF и user assertion. Клиент `restaurants` вызывает публичные `GetRestaurant` и `GetMenu` с audience `nomnom.restaurants` без пользователя (interceptor сам не добавляет assertion, если покупателя нет; для каталога context очищается от пользователя). Без настроенной identity resolver отвечает ошибкой аутентификации.

Адрес internal gateway — `BFF__INTERNAL_GATEWAY_ADDR`; без него customer-resolvers отвечают ошибкой «не настроено», `bffInfo` работает. Таймаут вызова — deadline входящего запроса.

### 4. Ошибки

| Источник | Результат GraphQL |
| --- | --- |
| access token отсутствует или невалиден | GraphQL error `UNAUTHENTICATED` |
| `GetOrder`: `NOT_FOUND`, `PERMISSION_DENIED` | `order = null` |
| `RequestOrderCancellation`: `CANCELLATION_WINDOW_NOT_OPEN` / `_EXPIRED` | `problem` с тем же кодом |
| `RequestOrderCancellation`: `ORDER_STATE_CONFLICT` | `problem = ORDER_ALREADY_FINAL` |
| `RequestOrderCancellation`: `NOT_FOUND`, `PERMISSION_DENIED` | GraphQL error «заказ не найден» |
| `UNAVAILABLE`, `INTERNAL`, сбой restaurants | GraphQL error без внутренних деталей |

### 5. orders: причина отказа отмены

`transportError` для `order.CancellationNotAllowed` берёт код первой причины политики (`cancellation_window_not_open`, `cancellation_window_expired`) и ставит его в `ErrorInfo.reason` в верхнем регистре; остальные случаи без изменений. Это новое значение того же поля — обратно совместимо.

### 6. restaurants: GetMenu

`rpc GetMenu(GetMenuRequest{restaurant_id}) returns (GetMenuResponse{repeated MenuItem items})`, `MenuItem { menu_item_id, name, description, Money price, bool available }`. Отсутствующее меню — `NOT_FOUND`. Без authorization, как Get Restaurant.

## Risks / Trade-offs

- [Три вызова на один `order(id)` при polling раз в 10 s] → допустимо для MVP; кеш меню — отдельное решение.
- [Нет e2e через gateway, пока BFF не развёрнут в kind] → unit-тесты сценариев и адаптеров на bufconn-серверах с сгенерированными stubs; e2e — вместе с развёртыванием BFF.
- [Расхождение часов BFF и `orders` у границы окна] → authoritative check в `orders`, клиент получает `CANCELLATION_WINDOW_EXPIRED`.

## Migration Plan

1. `orders` — причина отказа; `restaurants` — `GetMenu`.
2. BFF: генерация клиентов, слайс, resolvers, конфигурация.
3. Документация. Rollback — revert; контракты расширены обратно совместимо.
