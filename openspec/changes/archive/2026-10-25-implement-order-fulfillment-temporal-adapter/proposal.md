# Proposal

## Why

Application-слой `orders` уже обращается к Order Fulfillment Workflow через порт `fulfillment` (`Starter`, `Notifier`, `CancellationRequester`), а [ADR 5](../../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md) фиксирует контракт orchestration. Реализации за портом нет: заказ не проходит путь от оформления до Completion или компенсаций, а правила дедупликации, бизнес-таймеров и Saga существуют только в документации.

## What Changes

- Добавить Temporal-адаптер `slices/order/infrastructure/temporal/fulfillorder` на Temporal Go SDK `go.temporal.io/sdk` v1.50.0:
  - детерминированный workflow `OrderFulfillment` (Workflow ID `order:<order_id>`, task queue `orders`) с Workflow Stage, дедупликацией по `event_id`, бизнес-таймерами из `Policy`, replan и эскалацией Assignment, журналом выполненных шагов и Saga;
  - Signals с именами Integration Events и Update `CancellationRequested`;
  - Activities: финансовые операции, Restaurant Order и Delivery через узкие client-порты, `CompleteOrder` и `FailOrCancelOrder` через handlers слайса;
  - client-адаптер портов `Starter`, `Notifier`, `CancellationRequester` и `SignalWithStart` по `OrderPlaced` для будущего consumer;
  - функцию регистрации workflow и Activities в worker.
- Расширить порт `fulfillment`: `KindPreparationDelayed`, `KindPreparationFailed`, поле `ExpectedReadyAt` в `Notification`, ошибка `ErrCancellationRefused`.
- Начальные значения `Policy` — технические placeholders, а не продуктовые решения ADR 5.
- Unit-тесты workflow на `testsuite`, Activities на in-memory адаптерах, client-адаптера на `mocks.Client`.
- Не добавлять: worker-процесс и его конфигурацию, gRPC-клиенты `payments`/`restaurants`/`delivery`, Kafka consumers, PostgreSQL, Helm и регистрацию Search Attribute в кластере.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `order-fulfillment-orchestration`: добавляются требования к реализуемому маршруту workflow — Workflow Stage, шаги и их порядок, deadlines из Policy, replan/эскалация, Saga по журналу шагов, обработка Cancellation Update, контракт client-адаптера.

## Impact

- `boundaries/orders/go.mod`: новая зависимость `go.temporal.io/sdk` v1.50.0.
- `boundaries/orders/slices/order/applications/fulfillment`: два новых `Kind`, поле `ExpectedReadyAt`, `ErrCancellationRefused`.
- `boundaries/orders/slices/order/infrastructure/temporal/fulfillorder`: новый package.
- `boundaries/orders/README.md`, README слайса и порта: статус реализации.
- `cmd/orders`, DI, конфигурация, внешние API и deployment не изменяются.
