# Proposal

## Why

В `orders` уже есть канонический агрегат `Order` с FSM, Specifications и value objects, но ни один Command или Query из [матрицы use cases](../../../boundaries/orders/README.md#use-cases) не исполняется: нет handlers, транзакционной границы, портов внешних зависимостей и проверок идемпотентности. Без application-слоя будущие gRPC-, Kafka- и Temporal-адаптеры не на что подключать, а правила Inbox/Outbox, authorization и post-commit взаимодействия с workflow остаются только в документации.

## What Changes

- Добавить application handlers (Input, Result, Handler) для всех 12 Commands и 2 Queries `orders`: `placeorder`, `requestordercancellation`, `confirmorder`, `rejectorder`, `recordpreparationstarted`, `recordorderready`, `recordcourierassignment`, `recordpickup`, `recorddeliverycompletion`, `recorddeliveryfailure`, `completeorder`, `failorcancelorder`, `getorder`, `listcustomerorders`.
- Объявить порты в application-слое: shared `UnitOfWork`, `Inbox`, `Clock` и verified `Subject` в `pkg/application`; порт Order Fulfillment Workflow (start, уведомление о бизнес-факте, запрос отмены) в `slices/order/applications/fulfillment`; узкие use-case порты authorization, catalog snapshot, cancellation policy, Outbox, idempotency и read models рядом с потребителями. Контракт `order.Repository` — в `domains/order` по ADR 6.
- Расширить агрегат `Order` минимально необходимым для сценариев: оформление с проверкой `OrderItemsOrderable` и Domain Event `OrderPlaced`, время оформления, проверка допустимости Cancellation Request с `WithinCancellationWindow`, причина и вид отрицательного итога при Cancellation, `OrderCompleted`, отражённые факты fulfillment (Restaurant rejection, Preparation, готовность к Pickup, Courier Assignment, Pickup, Delivery Completion, delivery failure) и snapshot для repository. **BREAKING** для внутреннего Go API домена: `Cancel` принимает причину, `Restore` принимает `Snapshot`.
- Добавить in-memory адаптеры (store с UoW/rollback, Inbox, Outbox, repository, read models, idempotency keys; authorization, catalog, workflow, clock, id generator, cancellation policy) в `slices/order/infrastructure/memory` для unit-тестов.
- Добавить табличные unit-тесты на каждый handler, домен и in-memory адаптеры; обновить README слайсов и раздел «Статус реализации» `orders`.
- Не добавлять gRPC/protobuf, HTTP, PostgreSQL, Kafka/Watermill, Temporal SDK, Workflow/Activities, DI wiring, Helm и новые Go-зависимости.

## Capabilities

### New Capabilities

- `order-use-cases`: прикладное поведение Commands и Queries `orders` — оформление, отражение результатов соседних контекстов, Cancellation Request, финализация Order и чтение заказов — с транзакционной границей, идемпотентностью, authorization и передачей результатов Order Fulfillment Workflow.

### Modified Capabilities

Нет.

## Scope notes

- Все 14 slices входят в scope. Открытые продуктовые вопросы из [`docs/use-cases.md`](../../../docs/use-cases.md#open-questions) не решаются: окно отмены вынесено за порт cancellation policy, минимальная/максимальная сумма и состав snapshot — за catalog-порт, порядок шагов fulfillment и момент Authorization/Capture остаются за workflow. Принятые допущения перечислены в `design.md`.
- Основной путь старта workflow через собственное событие (`Outbox → Kafka → Inbox → SignalWithStart`) из change `specify-order-fulfillment-orchestration` не реализуется: здесь `placeorder` пишет `OrderPlaced` в Outbox и выполняет допустимый синхронный старт после commit через порт. Consumer собственного события — отдельный slice следующего change.
- Executable BDD-сценарии (`godog`) не подключаются, чтобы не вводить новую зависимость; поведение `confirmorder.feature` и `recordcourierassignment.feature` покрыто unit-тестами handlers.

## Impact

- `boundaries/orders/pkg/application`: новый shared package портов и application errors.
- `boundaries/orders/slices/order/domains/order`: расширение агрегата, FSM (новый trigger `record_fact`), Domain Events, контракт repository; существующие unit-тесты обновляются под новые сигнатуры.
- `boundaries/orders/slices/order/applications/*`: 14 use-case packages и package порта `fulfillment`.
- `boundaries/orders/slices/order/infrastructure/memory`: in-memory адаптеры.
- `boundaries/orders/README.md` и README слайсов: статус реализации и ссылки на порты.
- Внешние API, транспорт, схемы хранения, `cmd/orders`, DI и deployment не изменяются.
