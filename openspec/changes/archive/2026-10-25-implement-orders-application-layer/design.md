# Design

## Context

См. `proposal.md` — Why. Текущий код `orders` содержит агрегат `Order` (`placed`, `confirmed`, `cancelled`, `completed`), табличную FSM со строгими `InvalidTransition`/`AlreadyInState`/`GuardFailed`, Specifications `WithinCancellationWindow` и `OrderItemsOrderable` и value objects `Money`, `Quantity`, `DeliveryAddress`. Repository, UoW, Inbox/Outbox, authorization и Temporal существуют только как документированные контракты ([UoW](../../../docs/unit-of-work.md), [event-driven architecture](../../../docs/event-driven-architecture.md), [security](../../../docs/security.md), [ADR 6](../../../docs/adr/0006-ddd-structure-inside-vertical-slices.md)). Change `specify-order-fulfillment-orchestration` описывает протокол workflow, но ещё не применён; его решения используются как ориентир, а не как реализованная зависимость.

Требования — в `specs/order-use-cases/spec.md` (FR-100…FR-117, AC-100…AC-130).

## Goals / Non-Goals

**Goals:**

- один Handler на use case, вызываемый одинаково из будущих gRPC, Kafka consumer и Temporal Activity адаптеров;
- явная транзакционная граница: внешние вызовы до `UnitOfWork.Do` или после commit, внутри — только локальное хранилище;
- воспроизводимые unit-тесты на in-memory адаптерах без Docker, сети и wall clock.

**Non-Goals:**

- реальные PostgreSQL, Watermill, SpiceDB, Temporal и gRPC адаптеры, DI providers и composition root;
- consumer собственного `OrderPlaced` с `SignalWithStart`, Workflow и Activities;
- решение открытых продуктовых вопросов (см. «Допущения»).

## Decisions

### Размещение портов

- `pkg/application` — shared технические порты boundary: `UnitOfWork` (форма из документации UoW), `Inbox`, `Clock`, verified `Subject`, `AccessCheck` и application errors (`ErrInvalidInput`, `ErrPermissionDenied`, `ErrConcurrencyConflict`). Бизнес-правил и зависимостей от `domains` здесь нет.
- `domains/order` — контракт `Repository` (`Get`, `Add`, `Save`) и ошибки `ErrOrderNotFound`, `ErrOrderAlreadyExists` по ADR 6. Persisted version после `Add`/`Save` равна загруженной `version + 1`; это значение передаётся в Outbox как `aggregate_version`.
- `slices/order/applications/fulfillment` — единственный порт Order Fulfillment Workflow (`Starter`, `Notifier`, `CancellationRequester`), типы запросов и общий `Reflector` входящих фактов. Package не является use case; он объявлен один раз, потому что workflow общий для 10 сценариев.
- Узкие порты конкретных сценариев объявляются в их package: authorization, catalog snapshot, relationships, idempotency keys и Outbox у `placeorder`; cancellation policy у `requestordercancellation`; read models у `getorder` и `listcustomerorders`. Go structural typing позволяет одному in-memory store реализовать порты нескольких сценариев без общего horizontal-пакета.

Альтернатива — один `ports` package со всеми интерфейсами — отклонена: она превращает application в горизонтальный слой и противоречит ADR 6. Альтернатива — копировать порт workflow и логику Inbox в 8 handlers — отклонена из-за дублирования одного протокола.

### Inbox как порт внутри транзакции handler'а

`fulfillment.Reflector` выполняет insert-first протокол из документации UoW: внутри `Do` сначала `Inbox.Accept(consumer, event_id)`, затем загрузка `Order`, доменный метод и `Save`; после commit — `Notifier.Notify` с исходным `event_id`. Duplicate не меняет состояние и повторяет уведомление: Signal идемпотентен по `event_id` в workflow, поэтому сбой между commit и уведомлением восстанавливается обычной повторной доставкой (первый вариант из «Действия после commit в Inbox-сценариях»).

[Event-driven architecture](../../../docs/event-driven-architecture.md#размещение-в-коде) описывает Inbox как инфраструктурную обёртку вокруг handler. Здесь dedupe остаётся за портом, а реализация (insert `ON CONFLICT DO NOTHING`) — в infrastructure, но вызов порта находится в handler, потому что только handler знает, что делать после commit и в duplicate-ветке. Handler не читает таблицу Inbox напрямую. Расхождение предлагается отразить в общей документации отдельной правкой.

Каждый use case передаёт стабильное логическое имя consumer (`orders.<usecase>`), поэтому одно событие может обрабатываться разными сценариями независимо.

### Классификация результата входящего факта

| Результат доменного метода | Outcome | Сохранение | Уведомление workflow |
| --- | --- | --- | --- |
| успех | `applied` | `Order` + Inbox | да |
| `AlreadyInState`, `FactAlreadyRecorded` | `already_applied` | только Inbox | да |
| `InvalidTransition` (Order State `completed`/`cancelled`) | `ignored_late` | только Inbox | да; закрытый workflow (`ErrWorkflowNotFound`) не ошибка |
| Inbox duplicate | `duplicate` | ничего | да, повторно |
| прочие ошибки | — | откат | нет |

Уведомление выполняется во всех успешных ветках: workflow сам дедуплицирует `event_id` и игнорирует неожиданный факт для своей stage (orchestration SDD). Late-event policy этого change — диагностируемое игнорирование; отдельный compensation flow не вводится.

### Расширение агрегата `Order`

- `Place(...)` — фабрика оформления: создаёт `Order`, вычисляет `OrderItemsOrderable` над snapshot и возвращает `NotOrderable{Reasons}` либо записывает `OrderPlaced`. `New` остаётся чистым конструктором без событий для `Restore` и тестов.
- `placedAt` хранится в агрегате: он нужен истории заказов и cancellation policy.
- Факты fulfillment (`restaurant_rejected`, `preparation_started`, `ready_for_pickup`, `courier_assigned`, `picked_up`, `delivery_completed`, `delivery_failed`) — данные `Order`, а не Order State: по [FSM](../../../docs/fsm.md) ожидания и stages принадлежат workflow. Допустимость записи по state выражена одной строкой FSM на state — trigger `record_fact` с target state, равным source (разрешён в `placed`/`confirmed`, `InvalidTransition` в terminal states). Повтор факта — инвариант агрегата `FactAlreadyRecorded`, а не `AlreadyInState`.
- `CheckCancellation(requestedAt, window)` — чистая проверка Cancellation Request без изменения state: `WithinCancellationWindow`, затем dry-run строки FSM `cancel`.
- `Cancel(requestedAt, reason)` сохраняет `CancellationReason{Outcome, Code}` и записывает `OrderCancelled` с причиной. `Complete()` записывает `OrderCompleted`.
- `Snapshot()`/`Restore(Snapshot)` — полный mapping для repository без доступа к приватным полям.

Альтернатива — исключить 7 slices отражения фактов из scope — отклонена: README `orders` требует отражать результаты в `Order`, а workflow-only обработка потеряла бы данные для Get Order. Альтернатива — новые Order States на каждый факт — отклонена FSM-документацией.

### Place Order

Порядок: validation Input → поиск idempotency key → catalog snapshot (вне транзакции) → генерация `order_id` → relationship Customer → `Order` (fail closed) → `UoW { повторная проверка key, Add, сохранить key, Outbox(OrderPlaced) }` → `Starter.Start` после commit. Ошибка старта не возвращается как ошибка use case: результат содержит `FulfillmentStarted=false`, повтор с тем же key повторяет идемпотентный старт (FR-103). Конфликт key внутри транзакции (параллельный повтор) возвращает уже сохранённый `order_id`; осиротевший relationship оставляется reconciliation по [security](../../../docs/security.md#relationship-lifecycle).

### Request Order Cancellation

Без UoW: запрос отмены не сохраняется локально, а передаётся workflow. Порядок: `cancel` authorization → загрузка `Order` → `CancellationPolicy.Window(placedAt)` → `order.CheckCancellation` → `CancellationRequester.RequestCancellation` с `RequestID` (idempotency key запроса) и исходным subject для audit. Выбор Signal или Update — деталь адаптера; бизнес-отказ workflow возвращается как ошибка порта.

### Complete Order и Fail or Cancel Order

Вызываются Activity от `service` identity внутри `orders`, поэтому отдельной authorization не выполняют. Идемпотентность — через строгую FSM: `AlreadyInState` после commit превращается в `already_applied` без записи state и Outbox, что исключает второй логический event при повторе Activity.

### Queries

Без UoW. `getorder`: authorization `view` на `order:<id>` до чтения, затем read port. `listcustomerorders`: собственная история Customer фильтруется по `customer_id` без authorization graph (security: владелец известен domain storage); другой subject проходит `Check(view_orders, customer:<id>)`. Пагинация — `Limit` (по умолчанию 20, максимум 100) и непрозрачный cursor адаптера; порядок — от новых к старым по `placedAt`, затем по `order_id`.

### In-memory адаптеры

`infrastructure/memory.Store` хранит snapshots агрегатов, Inbox, Outbox, idempotency keys и read models в одном хранилище и реализует `UnitOfWork` с глобальной блокировкой, полным откатом при ошибке или panic и участием вложенного `Do` во внешней транзакции. Остальные адаптеры (`Authorizer`, `Catalog`, `Workflow`, `Clock`, `IDs`, cancellation policies) управляемы из тестов: allow/deny, ошибки, закрытые workflows. Retry транзакции не моделируется — это ответственность PostgreSQL-реализации.

## Допущения по открытым вопросам

- Окно отмены — за портом `CancellationPolicy`; in-memory `AlwaysOpen` и `FixedWindow` не являются продуктовым решением.
- Минимальная/максимальная сумма заказа и доступность приходят в catalog snapshot; какой контекст владеет лимитами, не решается. В `Order` сохраняется только цена позиции, как в текущей модели; расширенный Order Snapshot (названия, опции) — открытый вопрос.
- Отрицательные итоги Cancellation и Failure используют один Order State `cancelled` с сохранённым видом итога; отдельный `failed` State не согласован и может быть добавлен без потери данных.
- Отказ ресторана после Order Acceptance и факты в любом порядке записываются как сообщённые владельцем: правила допустимости принадлежат `restaurants` и `delivery`.
- Courier ID и прочие детали фактов не сохраняются; Get Order может обогащаться BFF из `delivery`.
- Completion не требует факта Delivery Completion в агрегате: условие успешного итога определяет workflow.
- Permissions `view`, `cancel`, `view_orders` и resource types — application-константы; окончательная `.zed` schema не создаётся.

## Risks / Trade-offs

- [Inbox-порт в handler расходится с текстом event-driven документации] → решение и мотивация записаны здесь; предлагается правка общих документов.
- [Signal повторяется на каждый duplicate] → workflow обязан дедуплицировать `event_id` (orchestration SDD); стоимость — лишний RPC при повторной доставке.
- [Изменение сигнатур `Cancel`/`Restore`] → API внутренний, используется только тестами и новым кодом этого change.
- [Глобальная блокировка in-memory UoW] → только для тестов; конкурентность PostgreSQL проверяется integration tests.

## Migration Plan

Данных и deployment нет: код используется только тестами. Откат — удаление новых packages и возврат сигнатур домена. Следующие changes подключают PostgreSQL/Watermill/Temporal/SpiceDB адаптеры к тем же портам и регистрируют handlers в DI.
