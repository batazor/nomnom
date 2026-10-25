# Tasks

## 1. Порт и зависимость

- [x] 1.1 Добавить `go.temporal.io/sdk` v1.50.0; расширить порт `fulfillment` (`KindPreparationDelayed`, `KindPreparationFailed`, `ExpectedReadyAt`, `ErrCancellationRefused`) и README порта; проверить `go build ./...`.

## 2. Workflow

- [x] 2.1 Реализовать `fulfillorder` workflow: вход, `Policy`/`DefaultPolicy`, Workflow Stage и Search Attribute, Signals с дедупликацией и проверкой stage, deadlines, replan/эскалацию, Update `CancellationRequested`, Saga.
- [x] 2.2 Покрыть workflow тестами `testsuite`: AC-433…AC-443.

## 3. Activities и client-адаптер

- [x] 3.1 Реализовать Activities и client-порты `Payments`, `Restaurants`, `Delivery`; идемпотентные ключи, non-retryable ошибки; тесты на in-memory адаптерах.
- [x] 3.2 Реализовать client-адаптер `Starter`/`Notifier`/`CancellationRequester` и `StartFromOrderPlaced`, функцию `Register`; тесты на `mocks.Client` (AC-444, AC-445).

## 4. Документация и проверка

- [x] 4.1 Добавить README `fulfillorder`, обновить «Статус реализации» `orders`.
- [x] 4.2 Выполнить `make check` в `boundaries/orders` и `openspec validate implement-order-fulfillment-temporal-adapter --strict`.
