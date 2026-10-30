# Слайс fulfillment

Процесс Order Fulfillment — самостоятельная бизнес-возможность `orders` со своей моделью: этапами (Workflow Stage), сроками (`Policy`) и порядком компенсаций Saga; Courier Assignment срока не имеет (FR-709) ([ADR 5](../../docs/adr/0005-order-fulfillment-orchestration.md), [ADR 6](../../../../docs/adr/0006-ddd-structure-inside-vertical-slices.md), спецификация [`order-fulfillment-orchestration`](../../../../openspec/specs/order-fulfillment-orchestration/spec.md), `FR-483`, `FR-484`).

```text
slices/fulfillment/
  domains/fulfillment/        Process, Policy, Stage, Outcome, Signal, Step, Effect
  applications/neighbours/    client-порты payments, restaurants, delivery, их DTO и построение запросов из Order
  applications/orders/        порт Order: что Activities нужно от Order на языке процесса
  infrastructure/temporal/    workflow, signals, activities, client, search attribute
  infrastructure/neighbours/  client-адаптеры payments, restaurants, delivery для Activities
  di/                         WorkerSet: адаптеры соседей и Activities orders-worker
```

## Доменная модель

`domains/fulfillment` импортирует только стандартную библиотеку, не читает часы и не генерирует случайности: время передаёт вызывающий (workflow — `workflow.Now`). Операции возвращают решения, а не выполняют побочные эффекты.

| Файл | Операции |
| --- | --- |
| `fulfillment.go` | `Process` (журнал подтверждённых шагов, факты соседей, сроки), `New`, `NewScheduled`, `Outcome`, коды причин |
| `schedule.go` | Маршрут запланированного заказа: `Plan`, `ConfirmPlan`, подтверждение и освобождение резервации окна, моменты плана как due-шаги, deadline Preparation от `QueueRestaurantOrder` |
| `signals.go` | `Apply(Signal, at) Effect` — сигнал соседа с дедупликацией по `EventID` |
| `cancellation.go` | `AdmitCancellation` (validator Update, без изменений), `AcceptCancellation`, `RequestCancellation` |
| `route.go` | `Next() (Step, bool)` — следующая операция маршрута (`RequestRestaurantOrder` — только после `AuthorizePayment` и `RecordPaymentAuthorization`); `Confirm(Step, approved, at)` — результат выполненного шага |
| `deadlines.go` | `NextDeadline()`, `Expire(now) Expiry` — отказ по сроку, шаги плана запланированного заказа |
| `compensation.go` | `Compensation() []Step` — Saga по журналу шагов и закрытие заказа; `Compensate()` переводит процесс в `compensating` |
| `stage.go` | `Stage` и его вычисление |
| `policy.go` | `Policy`, `DefaultPolicy` (placeholders) |

`Step.Key` — логическое имя шага (`authorize_payment`, `create_delivery`, …), из которого адаптер строит idempotency key `order:<order_id>:<key>`. Unit-тесты модели табличные и покрывают маршрут, факты и поздние события, ожидание курьера без срока (`AC-969`), сроки, Cancellation Request до и после Pickup и компенсации (`AC-547`).

## Зависимости

`infrastructure` → `domains/fulfillment`, `applications/neighbours` (порты соседей: Activities их вызывают, `infrastructure/neighbours` и `grpcclient` реализуют, не завися от `temporal`) и `applications/orders` (порт Order: Activities его вызывают).

Слайс не импортирует `slices/order`, это проверяет `depguard`. Оба направления связывает composition root `nomnom/orders/di`, единственное место, где встречаются два словаря: `di/fulfillment_orders.go` реализует порт Order поверх use cases слайса `order` и назначает их ошибкам `usecase.ErrorKind`, а `di/workflow.go` реализует порт `fulfillment` слайса `order` на Temporal-клиенте этого слайса.

Temporal-адаптер и его внешние имена описаны в [`infrastructure/temporal`](infrastructure/temporal/README.md).
