# Design

## Context

См. `proposal.md` и spec delta `order-use-cases`. Request Order Cancellation (`slices/order/applications/requestordercancellation`) получает окно через порт `CancellationPolicy.Window(ctx, placedAt)` и проверяет его доменным `Order.CheckCancellation` (`WithinCancellationWindow`, затем строка FSM для `cancel`). В `infrastructure/memory` есть `AlwaysOpen` (окно до 9999 года, тестовая заглушка) и `FixedWindow { Duration }` (окно от `placedAt`). В DI use case не подключён и в gRPC не опубликован.

Get Order (`applications/getorder`) проверяет `view` и читает `OrderView` из read model PostgreSQL; gRPC `GetOrder` переводит его в `nomnom.orders.v1.Order`. Customer contract BFF ждёт `Order.cancellation.availableUntil`, присутствующее только пока отмена допустима (`add-customer-graphql-contract`, решение 5).

## Goals / Non-Goals

**Goals:**

- одно правило окна для чтения и для Cancellation Request;
- окно в ответе `GetOrder` для заказов, которые по состоянию ещё можно отменить;
- конфигурируемая длительность вместо тестовых адаптеров в production.

**Non-Goals:**

- gRPC и DI для Request Order Cancellation;
- окончательная продуктовая политика окна;
- вычисление «открыто ли сейчас» на стороне `orders`.

## Decisions

### 1. Get Order публикует интервал, а не флаг «можно отменить»

Ответ содержит `opens_at` и `closes_at`, а не `cancellable: bool`. Флаг зависел бы от времени чтения и устаревал между polling-запросами; интервал стабилен, и BFF сам выводит `availableUntil = closes_at`, если `opens_at ≤ now ≤ closes_at`. Authoritative check по-прежнему выполняет Request Order Cancellation в момент запроса, поэтому расхождение часов BFF и `orders` даёт в худшем случае бизнес-отказ `CANCELLATION_WINDOW_EXPIRED`, а не ошибочную отмену.

Альтернатива — вычислять `availableUntil` в `orders` с clock. Отклонена: read model и Query остаются детерминированными, а представление «до какого времени показывать кнопку» — забота customer contract.

### 2. Окно только для состояний, допускающих Cancellation

Get Order запрашивает окно, только если FSM допускает `cancel` из текущего Order State (`placed`, `confirmed`). Для `cancelled` и `completed` поле отсутствует, поэтому BFF не покажет отмену завершённому заказу даже при открытом по времени окне. Проверка — чистый метод `State.AcceptsCancellation()` поверх transition table; `Order.CheckCancellation` читает ту же строку `cancel`, поэтому правила не расходятся.

### 3. Политика из конфигурации

Новый адаптер `slices/order/infrastructure/cancellationpolicy.Fixed` реализует `Window(ctx, placedAt)` как `[placedAt, placedAt + window]`. Конфигурация — секция `cancellation`:

```yaml
cancellation:
  window: 5m
```

`CANCELLATION__WINDOW` переопределяет значение. Значение по умолчанию `5m` — placeholder, как значения `fulfillment.*`; `window <= 0` или неразбираемая длительность — ошибка конфигурации на старте. Один экземпляр политики создаётся в DI и передаётся Get Order; при подключении Request Order Cancellation он получит тот же экземпляр. `memory.FixedWindow` и `memory.AlwaysOpen` остаются тестовыми.

Альтернатива — хранить окно в `Order` при Place Order. Отклонена до продуктового решения: смена политики тогда потребовала бы миграции данных, а сейчас окно выводится из `placed_at`.

### 4. Ошибка политики не ломает чтение

Get Order — основной источник экрана заказа. Если `Window` вернёт ошибку, handler отдаёт `OrderView` без окна; use cases не импортируют OpenTelemetry и не логируют (docs/observability.md), поэтому ошибку в log и span пишет декоратор порта `cancellationpolicy.Observed`; отказ authorization и «не найден» обрабатываются как раньше. Отсутствие окна безопасно: UI просто не предлагает отмену. Текущий адаптер из конфигурации ошибок не возвращает; поведение нужно для будущих политик с внешними данными.

### 5. Контракт gRPC

```proto
message CancellationWindow {
  google.protobuf.Timestamp opens_at = 1;
  google.protobuf.Timestamp closes_at = 2;
}

message Order {
  // ...
  CancellationWindow cancellation_window = 11;
}
```

Новое поле message-типа необязательно и обратно совместимо (`buf breaking` проходит). `OrderSummary` в List Customer Orders не меняется.

## Risks / Trade-offs

- [Placeholder `5m` может уйти в production как правило] → значение помечено placeholder в README и `config.example.yaml`; продуктовое решение меняет только конфигурацию.
- [Окно от `placed_at` не учитывает подтверждение ресторана] → допущение текущего change; правило «до подтверждения» потребует новой политики за тем же портом без изменения контракта.
- [Часы BFF и `orders` расходятся] → authoritative check в Request Order Cancellation; клиент получает бизнес-отказ.

## Migration Plan

1. Доменная функция и тесты FSM.
2. Адаптер политики, конфигурация и DI.
3. Порт в Get Order, тесты handler.
4. Proto, генерация, mapping в gRPC и тест сервиса.
5. README, `config.example.yaml`, Helm values.
6. Rollback: удалить поле из ответа (proto поле остаётся зарезервированным при удалении) и секцию конфигурации.
