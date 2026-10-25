# Tasks

## 1. Shared application ports

- [x] 1.1 Добавить `pkg/application` с `UnitOfWork`, `Inbox`, `Clock`, `Subject`, `AccessCheck` и application errors; проверить `go build ./pkg/...` и unit-тест валидации `Subject`.

## 2. Расширение агрегата `Order`

- [x] 2.1 Добавить `Place` с `OrderItemsOrderable`, `placedAt`, `OrderPlaced`, `OrderCompleted`, `CancellationReason` в `Cancel`/`OrderCancelled`, `Snapshot`/`Restore` и контракт `Repository`; обновить существующие тесты и проверить `go test ./slices/order/domains/...`.
- [x] 2.2 Добавить trigger `record_fact`, факты fulfillment с `FactAlreadyRecorded` и `CheckCancellation`; дополнить табличные тесты FSM (полнота таблицы, terminal states, повтор факта, окно отмены) и проверить `go test ./slices/order/domains/...`.

## 3. In-memory адаптеры

- [x] 3.1 Добавить `slices/order/infrastructure/memory` (Store с UoW/rollback/nested, repository с optimistic concurrency, Inbox, Outbox, idempotency keys; Authorizer, Catalog, Workflow, Clock, IDs, cancellation policies) и unit-тесты commit/rollback/panic/nested/version conflict; проверить `go test ./slices/order/infrastructure/...`.

## 4. Порт workflow и входящие факты

- [x] 4.1 Добавить package `applications/fulfillment` (порты `Starter`, `Notifier`, `CancellationRequester`, `Reflector` с классификацией outcome) и README; покрыть тестами duplicate, already applied, ignored late, ошибку уведомления и откат (AC-106, AC-107, AC-112…AC-114, AC-130).
- [x] 4.2 Реализовать `confirmorder` и `rejectorder` с табличными тестами (AC-108, AC-109, `confirmorder.feature` AC-001/AC-003) и обновить их README.
- [x] 4.3 Реализовать `recordpreparationstarted`, `recordorderready`, `recordcourierassignment`, `recordpickup`, `recorddeliverycompletion`, `recorddeliveryfailure` с табличными тестами (AC-110, AC-111, `recordcourierassignment.feature` AC-002) и обновить их README.

## 5. Команды клиента и workflow

- [x] 5.1 Реализовать `placeorder` (validation, idempotency, catalog snapshot, relationship, UoW + Outbox, старт после commit) с тестами AC-100…AC-105 и обновить README.
- [x] 5.2 Реализовать `requestordercancellation` (authorization, cancellation policy, доменная проверка, запрос workflow) с тестами AC-115…AC-119 и обновить README.
- [x] 5.3 Реализовать `completeorder` и `failorcancelorder` с тестами AC-120…AC-125 и обновить README.

## 6. Queries

- [x] 6.1 Реализовать `getorder` и `listcustomerorders` с read-портами, authorization и пагинацией; тесты AC-126…AC-129; обновить README.

## 7. Документация и проверка

- [x] 7.1 Обновить раздел «Статус реализации» и описание доменной модели в `boundaries/orders/README.md`; проверить, что ADR 0001–0004 не изменены и ADR 0005 не создан.
- [x] 7.2 Выполнить `make check` в `boundaries/orders` и `openspec validate implement-orders-application-layer --strict`; подтвердить отсутствие новых зависимостей в `go.mod` и изменений вне `boundaries/orders/**` и этого change.

## Workflow follow-up

- Предложить правки общих документов (`docs/event-driven-architecture.md`, `docs/unit-of-work.md`, `docs/use-cases.md`) по Inbox-порту и допущениям.
- Подключить handlers к DI и реальным адаптерам отдельными changes; архивировать change после ревью.
