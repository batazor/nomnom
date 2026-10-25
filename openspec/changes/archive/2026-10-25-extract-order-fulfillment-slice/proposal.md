# Proposal

## Why

Процесс Order Fulfillment — самостоятельная бизнес-возможность `orders`: у него свои этапы (Workflow Stage), сроки (`Policy`), правила перепланирования доставки, эскалации оператору и порядок компенсаций Saga. Сейчас все эти правила живут в `slices/order/infrastructure/temporal/fulfillorder` (около 2100 строк вместе с тестами), то есть в слое адаптеров, и смешаны с Temporal SDK: решения нельзя проверить без test environment Temporal, а [ADR 6](../../../docs/adr/0006-ddd-structure-inside-vertical-slices.md) требует, чтобы бизнес-правила реализовывала доменная модель, а инфраструктура только адаптировала транспорт. По тому же ADR новая бизнес-возможность с собственной моделью выделяется в отдельный слайс.

## What Changes

- Новый слайс `slices/fulfillment` с доменной моделью процесса `domains/fulfillment`: состояние процесса (журнал подтверждённых шагов, факты соседей, счётчик replan, эскалация, сроки), `Policy`, `Stage`, `Outcome` и чистые функции решений:
  - применение входящего факта, решения оператора и Cancellation Request к состоянию;
  - следующий шаг маршрута (какую Activity выполнить) по состоянию;
  - ближайший срок и реакция на его истечение (replan, эскалация, отказ);
  - текущий Workflow Stage;
  - план компенсации по журналу шагов.
  Модель не импортирует Temporal SDK и проверяется обычными unit-тестами, включая все сценарии, которые сейчас покрыты `workflow_test.go`.
- Temporal workflow становится тонким адаптером в `slices/fulfillment/infrastructure/temporal`: Signal/Update handlers переводят payload в вызовы модели, main loop исполняет решения модели как Activities и таймеры, публикует Search Attribute. Activities, client и регистрация переезжают туда же без изменения поведения.
- Порт `slices/order/applications/fulfillment` (старт, уведомления, Cancellation Request) остаётся в слайсе `order` как контракт, который используют его use cases; реализация на Temporal — в новом слайсе. Use cases `completeorder` и `failorcancelorder`, которые вызывают Activities, остаются в `order`: они меняют агрегат `Order`.
- Имена workflow, Signal, Update, Query, Activity, Task Queue и Search Attribute не меняются, поведение процесса и его спецификация `order-fulfillment-orchestration` — тоже.
- DI (`di/worker.go`, `di/consumer.go`, wire) и README переводятся на новые пакеты.

Не входит: изменение правил процесса, значений `Policy`, протоколов соседей, хранения; versioning уже запущенных workflow (production-развёртываний нет).

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `order-fulfillment-orchestration`: добавляется требование, что решения процесса принимает доменная модель без зависимости от Temporal.

## Impact

- `boundaries/orders/slices/fulfillment/` (новый), `slices/order/infrastructure/temporal/` (удаляется после переноса), `di/`, `cmd/orders-worker` при необходимости, README `orders` и `slices/order/applications/fulfillment/README.md`.
- Внешние контракты (gRPC, Kafka, Temporal names) не меняются.
