# Plan Schedule

## Сценарий

- **Тип:** Query.
- **Actor:** Order Fulfillment Workflow через Activity `PlanSchedule`.
- **Намерение:** Рассчитать обратный план Scheduled Order по свежим оценкам.
- **Результат:** Возвращён `order.SchedulePlan`. Order план не хранит: он остаётся в Event History workflow.

## Границы

- **Владелец:** `orders/slices/order`.
- **Домен:** `domains/order`, политика `order.PlanSchedule`.
- **Взаимодействие:** Читает базовое время приготовления из `restaurants` и Delivery Duration Estimate из `delivery`.

## Реализация

Handler: [`planschedule.go`](planschedule.go), сценарии: [`acceptance_test.go`](acceptance_test.go), остальные случаи: [`planschedule_test.go`](planschedule_test.go).

- **Вход:** `Input{OrderID, PlannedReadyAt}`.
- **Порядок:** загрузка Order → отказ `order.ErrNotScheduled` для ASAP-заказа → базовое время приготовления → оценка доставки → `order.PlanSchedule`. Оценки читаются при каждом вызове.
- **Порты:** `Orders`, `Preparation`, `DeliveryDuration`.
- **Требования:** FR-677; AC-906, AC-936 в OpenSpec change `add-scheduled-orders`.

## Acceptance

Executable-сценарии: [`planschedule.feature`](acceptance/planschedule.feature) исполняются `godog` под `go test` против PostgreSQL (testcontainers); все сценарии `@pending`: AC-906 и AC-909 ещё не перенесены из change `add-scheduled-orders` в `openspec/specs`.

Общий каталог: [Orders README](../../../../README.md).
