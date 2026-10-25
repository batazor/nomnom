# Tasks

## 1. Архитектурное решение `orders`

- [x] 1.1 Добавить `boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md` со статусом Accepted: зафиксировать scope Temporal, identity и старт workflow, Inbox/Signal idempotency, Activities, timers, Saga, state ownership и явные non-use cases; проверить, что ADR ссылается на локальные ADR 2/4 и общесистемные ADR 11/13/15/17/18 и не изменяет существующие ADR.
- [x] 1.2 В ADR отделить принятые решения от отложенных продуктовых решений (точные timeout, момент Authorization/Capture, параллелизм Courier Assignment, late-event policy); проверить, что ни один открытый вопрос не представлен как уже выбранное поведение.

## 2. Документация bounded context

- [x] 2.1 Расширить `boundaries/orders/README.md` разделом Order Fulfillment Workflow: добавить критерии применения Temporal, Workflow ID `order:<order_id>`, namespace/task queue, основной и альтернативный старт и базовую логическую схему; проверить согласованность терминов с `GLOSSARY.md` четырёх contexts.
- [x] 2.2 Описать в README входящие события `Kafka → Inbox → application handler → Signal/Update`, два уровня дедупликации, Signal/Update/Query, business cancellation, retry/timeout и Saga; проверить ссылки на Inbox/Outbox, gateway, SpiceDB, runtime и новый ADR.
- [x] 2.3 Добавить в README явный раздел «Где Temporal не используется» и таблицу разделения `Order State`, Workflow Stage, Kafka, CQRS, SpiceDB и task queue; проверить, что Temporal не назван source of truth, event bus, authorization engine или универсальной job queue.

## 3. Проверка согласованности

- [x] 3.1 Выполнить `openspec validate specify-order-fulfillment-orchestration --strict` и исправить все ошибки и предупреждения спецификации.
- [x] 3.2 Проверить `rg`-поиском, что документация содержит `SignalWithStart`, `USE_EXISTING`, `order:<order_id>`, `event_id`, `Start-To-Close`, `service` identity и Search Attribute, а существующие ADR не изменены по `git diff -- boundaries/orders/docs/adr/0001-record-architecture-decisions.md boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md boundaries/orders/docs/adr/0003-use-watermill-for-event-integration-in-orders.md boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md`.
- [x] 3.3 Просмотреть итоговый `git diff --check` и `git diff` для README, нового ADR и OpenSpec artifacts; проверить отсутствие исходного кода Workflow/Activities, зависимостей и инфраструктурных изменений.

## Workflow follow-up

- После ревью применённой документации архивировать change командой `openspec archive specify-order-fulfillment-orchestration`.
- Создать отдельные SDD changes для продуктовых timeout, точного payment flow, параллелизма Courier Assignment и event-specific late-signal policies до реализации соответствующих слайсов.
