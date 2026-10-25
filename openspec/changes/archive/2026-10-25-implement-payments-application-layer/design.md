# Design

## Context

См. proposal.md — Why. Текущее состояние `boundaries/payments`:

- `slices/payment/domains/payment/` содержит агрегат `Payment` с чистой табличной FSM (`pending`, `authorized`, `captured`, `refunded`, `failed`), `Money` на `decimal.Decimal`, policies `PaymentRefundable`/`PaymentCapturable` и единственный domain event `PaymentRefunded`.
- В `slices/payment/applications/<usecase>/` лежат только README; handlers, порты и adapters отсутствуют. Хранилище, Kafka-клиент, provider и DI-контейнер не выбраны.
- Проверки — `make check`: `unittest`, `ruff`, `ty` (типизация на уровне Python 3.9), `ruff format --check`. Новые зависимости не нужны.

Ограничения из документации: UoW — port в `pkg/application` с идиоматичным transaction scope ([UoW](../../../docs/unit-of-work.md)); provider call вне транзакции по схеме `UoW { pending operation } → provider → UoW { result + Payment + Outbox }`; authorization port — в `applications/`, проверка до доменной операции, fail closed ([security](../../../docs/security.md)); контракты repositories — в `domains/`; application не создаёт Domain Event вместо домена ([domain events](../../../docs/domain-events.md)).

## Goals / Non-Goals

**Goals:**

- Исполняемые handlers всех девяти use cases со строгими границами domain/application и портами, заменяемыми in-memory adapters в тестах.
- Не более одного финансового эффекта на idempotency key при повторе Activity, потере ответа и конкурентном вызове.
- Отдельная дедупликация provider callbacks и применение результата операции не более одного раза.

**Non-Goals:**

- Выбор момента Authorization и Capture, политики компенсаций и реакции на отказ: `payments` выполняет операцию по запросу оркестратора, решение о вызове остаётся у `orders` (open questions в `docs/use-cases.md`).
- Partial capture/refund и несколько попыток платежа на один `Order`: Capture и Refund всегда на полную сумму `Payment`.
- Применение `PaymentCapturable`/`PaymentRefundable`: продуктовые сроки authorization window и refund window не определены; при полной сумме amount-часть этих policies всегда выполнена. Подключение — отдельный change после решения о сроках.
- Реальный provider client, проверка подписи webhook, gRPC/HTTP transport, хранилище, Outbox publisher, Kafka, Dependency Injector, Helm/ops.

## Decisions

### 1. Размещение кода

```text
pkg/application/                 # shared application-уровень boundary
  unit_of_work.py                # UnitOfWork port
  clock.py                       # Clock port
  subject.py                     # Subject (user | service)
  errors.py                      # ApplicationError, PermissionDenied, AuthorizationUnavailable, ConcurrencyConflict
  outbox.py                      # AggregateRef
slices/payment/domains/payment/
  payment.py                     # Payment (+ ensure_can, события переходов, fail(reason))
  events.py                      # domain events Payment и Payment Operation
  operation.py                   # PaymentOperation, его FSM, complete_operation
  repository.py                  # контракты PaymentRepository, PaymentOperationRepository
slices/payment/applications/
  ports.py                       # порты слайса: permissions, provider gateway, outbox, provider message inbox, identifiers
  errors.py                      # application errors слайса
  access.py                      # require_permission: authoritative check, fail closed
  operation_flow.py              # общий поток финансовой операции и применения результата provider
  <usecase>/__init__.py          # Input, Result, Handler одного use case
tests/
  payment_fakes.py               # in-memory store/UoW/repositories/outbox/inbox, fake provider, permissions, clock, ids
  test_<usecase>.py              # unit-тесты handler'ов
```

Порты, общие для нескольких use cases одного слайса (provider gateway используют пять handlers), объявлены один раз в `applications/ports.py` рядом с потребляющим кодом, а не дублируются по packages; это не выходит за `applications/` и согласуется с ADR 6. UoW, clock, subject и generic errors не зависят от платёжного языка и находятся в `pkg/application`, как предписывает документация UoW. Альтернатива — порт в каждом use-case package — отвергнута: пять одинаковых Protocol без различий.

In-memory adapters размещены в `tests/`, а не в `infrastructure/`: хранилище не выбрано, а документация запрещает пустые инфраструктурные scaffolds. Они реализуют контракты так, как обязано будущее хранилище: snapshot/rollback UoW, optimistic concurrency по `version`, уникальность idempotency key и `order_id`, не более одной незавершённой операции на `Payment`.

### 2. Синхронный API

Handlers, порты и UoW синхронные: `UnitOfWork.run(work: Callable[[], T]) -> T`. Текущий runtime синхронный (`http.server`), а transport и Temporal worker для Python ещё не выбраны. Переход на `async` при выборе асинхронного transport меняет только сигнатуры портов и handlers, но не поток. Альтернатива `async with uow()` отложена до выбора runtime.

### 3. Payment Operation как отдельная доменная запись

`PaymentOperation` (термин словаря «Payment Operation») — запись одной логической попытки Authorization, Capture, Cancel Authorization или Refund: `id`, `payment_id`, `kind`, `idempotency_key`, `amount`, `requested_at`, `state` (`pending → succeeded | declined`), `provider_reference`, `decline_reason`, `completed_at`, `version`. Её жизненный цикл — отдельная маленькая табличная FSM со строгими повторами. История операций нужна List Payment Operations, а pending-запись — защите от повторного provider call.

Альтернатива — коллекция операций внутри агрегата `Payment` — отвергнута: меняет persisted form `Payment`, увеличивает загрузку агрегата и смешивает две независимые FSM. Документация UoW явно допускает транзакцию `operation record + Payment + Outbox`.

Правило «какой переход `Payment` следует из результата операции» — доменное и реализовано доменной функцией `complete_operation(payment, operation, outcome, completed_at)`:

| Операция | Успех | Отказ |
| --- | --- | --- |
| Authorization | `pending → authorized`, `PaymentAuthorized` | `pending → failed`, `PaymentFailed` + `PaymentOperationDeclined` |
| Capture | `authorized → captured`, `PaymentCaptured` | `Payment` остаётся `authorized`, `PaymentOperationDeclined` |
| Cancel Authorization | `authorized → pending`, `PaymentAuthorizationCancelled` | `Payment` остаётся `authorized`, `PaymentOperationDeclined` |
| Refund | `captured → refunded`, `PaymentRefunded` | `Payment` остаётся `captured`, `PaymentOperationDeclined` |

Отказ в Authorization переводит `Payment` в терминальный `failed`, потому что несколько попыток платежа не моделируются. Отказ в Capture не переводит `Payment` в `failed`: иначе FSM запретила бы Cancel Authorization, и удержание средств осталось бы до истечения у provider; что делать дальше, решает оркестратор.

### 4. Поток финансовой операции

```text
validate input → check permission (fail closed)
UoW#1 { load Payment; find operation by idempotency key
        ├─ найдена, завершена → вернуть сохранённый результат (replayed), provider не вызывается
        ├─ найдена, pending   → продолжить с ней
        ├─ найдена для другого Payment/kind → IdempotencyKeyConflict
        └─ не найдена → Payment.ensure_can(trigger); нет другой pending; add PaymentOperation(pending) }
provider.execute(request с тем же idempotency key)           # вне транзакции
  ├─ ProviderUnavailable → ошибка вызывающему, операция остаётся pending
  ├─ accepted            → результат pending, состояние не меняется
  └─ succeeded/declined  → UoW#2 { reload; если уже завершена callback'ом — вернуть её результат;
                                   complete_operation; save PaymentOperation; save Payment;
                                   Outbox.add(AggregateRef(Payment, version), events) }
```

- Идентификатор операции генерируется до UoW#1, время берётся из `Clock`.
- Повтор с тем же ключом при pending вызывает provider с тем же ключом: provider обязан дедуплицировать по нему (требование к будущему adapter). Это закрывает сбой между provider call и локальным commit (AC-408, AC-432).
- Конкурентная вставка того же ключа или второй pending-операции обнаруживается уникальностью хранилища (`DuplicateIdempotencyKey`, `OperationAlreadyPending`); handler один раз перечитывает состояние в новом UoW#1 и продолжает с найденной операцией либо возвращает `OperationInProgress`.
- `Payment` сохраняется в UoW#2 и при отказе без смены state: факт получает собственную `aggregate_version`, поэтому детерминированный `event_id` не совпадает с предыдущими событиями `Payment`.
- `AlreadyInState` и `InvalidTransition` из `ensure_can` для нового ключа превращаются в `PaymentStateConflict`: другой ключ означает другую логическую операцию, повтор той же — только по тому же ключу.

### 5. Create Payment

Естественный ключ идемпотентности — `order_id`: один `Payment` на `Order`. Повтор с той же суммой и валютой возвращает существующий `payment_id` (`created=False`), с другими — `PaymentAlreadyExists`. Отдельный idempotency key не хранится, поэтому UoW не нужен: одиночный `add` в autocommit, гонка обнаруживается `DuplicatePayment` и разрешается перечитыванием. `payment_id` генерируется до проверки permission, чтобы resource был известен adapter'у. Событие не публикуется (`docs/use-cases.md`: Kafka — no).

### 6. Provider results: callback и operation result

Оба use cases используют общий шаг применения результата (`operation_flow.apply_provider_result`):

- корреляция по `operation_id`, который `payments` передаёт provider как merchant reference; adapter отображает payload provider на него;
- результат `succeeded`/`declined`; `accepted` во входе отклоняется как invalid request;
- завершённая операция с тем же статусом — no-op (`already_applied`); с другим — `ProviderResultConflict` без изменений (сигнал для reconciliation).

Apply Provider Callback дополнительно в той же UoW регистрирует `provider_message_id` через порт `ProviderMessageInbox.register()` (insert-first, аналог Inbox); повторный message — no-op (`duplicate_message`). Ошибка (неизвестная операция, конфликт) откатывает регистрацию, чтобы provider мог повторить доставку. Подлинность callback проверяет инфраструктура; SpiceDB-check для callback и для внутреннего async adapter не выполняется — actor не является subject authorization graph `payments`.

### 7. Authorization port

`PaymentPermissionChecker.is_allowed(subject, permission, resource) -> bool`, `PaymentPermission` — `create`, `authorize`, `capture`, `cancel_authorization`, `refund`, `view`, `view_operations`; `PaymentResource(payment_id, order_id)`. Mapping на SpiceDB definitions — задача будущего adapter. `False` → `PermissionDenied`; adapter выражает ошибку/timeout исключением `AuthorizationUnavailable`, которое handler не перехватывает (fail closed). Проверка выполняется до загрузки `Payment`, поэтому отказ не раскрывает существование resource.

### 8. Queries

Отдельная read model не вводится, пока нет CQRS-требования: Get Payment Status и List Payment Operations читают через методы repositories без UoW и отображают в Application DTO без `provider_reference`. List возвращает операции в порядке `requested_at`, включая idempotency key как диагностический идентификатор вызывающей стороны.

### 9. Ошибки

Application errors слайса типизированы и наследуют `ApplicationError`; transport mapping — задача будущего gRPC adapter:

| Ошибка | Ожидаемый gRPC code |
| --- | --- |
| `PaymentNotFound`, `PaymentOperationNotFound` | `NOT_FOUND` |
| `InvalidPaymentRequest` | `INVALID_ARGUMENT` |
| `PaymentStateConflict`, `OperationInProgress`, `IdempotencyKeyConflict` | `FAILED_PRECONDITION` |
| `PaymentAlreadyExists` | `ALREADY_EXISTS` |
| `ProviderUnavailable` | `UNAVAILABLE` |
| `PermissionDenied` | `PERMISSION_DENIED` |
| `ProviderResultConflict`, `ConcurrencyConflict` | `ABORTED` / `INTERNAL` — выбрать при реализации adapter |

## Risks / Trade-offs

- [Provider не дедуплицирует по idempotency key] → повтор pending-операции создаст второй эффект. Требование зафиксировано для будущего adapter; без такой гарантии adapter обязан сначала запрашивать статус операции у provider.
- [Pending-операция без ответа блокирует другие операции `Payment`] → вызывающая сторона повторяет с тем же ключом либо результат приходит callback'ом/operation result; reconciliation pending-операций — отдельный change.
- [In-memory adapters не доказывают атомарность реального хранилища] → они повторяют контракт (rollback, уникальность, version); integration tests с выбранным хранилищем обязательны при его появлении.
- [Синхронные порты] → смена на async затронет сигнатуры; поток и тесты сохраняются.
- [Отказ Authorization публикует два факта: `PaymentOperationDeclined` и `PaymentFailed`] → оба истинны и различаются `event_type`; integration mapping может выбрать публикуемый контракт.

## Migration Plan

Не требуется: внешних contracts, данных и deployment изменений нет. Откат — удаление добавленных модулей; доменные изменения обратно совместимы, кроме сигнатуры `Payment.fail(reason)` и состава полей `PaymentRefunded`, у которых нет внешних потребителей.

## Follow-up для общих документов

Change не редактирует общие документы; предлагаемые правки:

- `docs/unit-of-work.md`, раздел «Размещение»: для `payments` port реализован в `pkg/application/unit_of_work.py` как синхронный `UnitOfWork.run(work)`.
- `docs/security.md`, таблица owners resources: добавить `payment` с owner `payments` и permissions из решения 7.
- `docs/ubiquitous-language.md`: зафиксировать `Cancel Authorization` как public имя (Void — provider mapping), добавить domain events `PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentFailed`, `PaymentOperationDeclined`.
- `docs/use-cases.md`, Open questions: дополнить вопросом о сроках authorization/refund windows для `PaymentCapturable`/`PaymentRefundable`.
- `boundaries/payments/GLOSSARY.md` (в границах `payments`) обновляется этим change термином Payment Operation.
