# Слайс fulfillment

Процесс Order Fulfillment — самостоятельная бизнес-возможность `orders` со своей моделью: этапами (Workflow Stage), сроками (`Policy`), правилами перепланирования доставки, эскалации оператору и порядком компенсаций Saga ([ADR 5](../../docs/adr/0005-order-fulfillment-orchestration.md), [ADR 6](../../../../docs/adr/0006-ddd-structure-inside-vertical-slices.md), спецификация [`order-fulfillment-orchestration`](../../../../openspec/specs/order-fulfillment-orchestration/spec.md), `FR-483`, `FR-484`).

```text
slices/fulfillment/
  domains/fulfillment/        Process, Policy, Stage, Outcome, Fact, Step, Effect
  applications/neighbours/    client-порты payments, restaurants, delivery, их DTO и построение запросов из Order
  infrastructure/temporal/    workflow, signals, activities, client, search attribute
  infrastructure/neighbours/  client-адаптеры payments, restaurants, delivery для Activities
  di/                         WorkerSet: адаптеры соседей и Activities orders-worker
```

## Доменная модель

`domains/fulfillment` импортирует только стандартную библиотеку, не читает часы и не генерирует случайности: время передаёт вызывающий (workflow — `workflow.Now`). Операции возвращают решения, а не выполняют побочные эффекты.

| Файл | Операции |
| --- | --- |
| `fulfillment.go` | `Process` (журнал подтверждённых шагов, факты соседей, replan, эскалация, сроки), `New`, `Outcome`, коды причин |
| `facts.go` | `Apply(Fact, at) Effect` — факт соседа с дедупликацией по `EventID`; `Decide(OperatorDecision) Effect` |
| `cancellation.go` | `AdmitCancellation` (validator Update, без изменений), `AcceptCancellation`, `RequestCancellation` |
| `route.go` | `Next() (Step, bool)` — следующая операция маршрута (`RequestRestaurantOrder` — только после `AuthorizePayment` и `RecordPaymentAuthorization`); `Confirm(Step, approved, at)` — результат выполненного шага |
| `deadlines.go` | `NextDeadline()`, `Expire(now) Expiry` — replan до `MaxReplans`, эскалация, отказ по сроку |
| `compensation.go` | `Compensation() []Step` — Saga по журналу шагов и закрытие заказа; `Compensate()` переводит процесс в `compensating` |
| `stage.go` | `Stage` и его вычисление |
| `policy.go` | `Policy`, `DefaultPolicy` (placeholders) |

`Step.Key` — логическое имя шага (`authorize_payment`, `replan_delivery_2`, …), из которого адаптер строит idempotency key `order:<order_id>:<key>`. Unit-тесты модели табличные и покрывают маршрут, факты и поздние события, replan до `MaxReplans` (`AC-546`), эскалацию и решение оператора, сроки, Cancellation Request до и после Pickup и компенсации (`AC-547`).

## Зависимости

`infrastructure` → `domains/fulfillment`, `applications/neighbours` (порты соседей: Activities их вызывают, `infrastructure/neighbours` и `grpcclient` реализуют, не завися от `temporal`), порт `slices/order/applications/fulfillment` (его реализует `Client`) и use cases `order/applications/{recordpaymentauthorization,completeorder,failorcancelorder}` (их вызывают Activities). `di` объявляет роль хранилища Activities (`Store`: Unit of Work, repository, Outbox) и использует телеметрию слайса `order`; composition root связывает роль с `ProcessStore` слайса `order`. Слайс `order` не импортирует `fulfillment`.

Temporal-адаптер и его внешние имена описаны в [`infrastructure/temporal`](infrastructure/temporal/README.md).
