# Design

## Context

См. [proposal.md](proposal.md) — Why. `boundaries/restaurants` — TypeScript (Node.js ≥ 22.6, запуск через type stripping, `tsc` 7, Biome 2), сейчас содержит HTTP-каркас, `pkg/config` и две чистые Specifications. Type stripping запрещает синтаксис, требующий трансформации (enums, parameter properties, namespaces), поэтому константы перечислений выражаются `as const`-объектами, а зависимости — явными полями классов. Архитектурные ограничения заданы [boundaries/README.md](../../../boundaries/README.md) (DDD внутри vertical slice, направление зависимостей), [Unit of Work](../../../docs/unit-of-work.md), [FSM](../../../docs/fsm.md), [Specification Pattern](../../../docs/specification-pattern.md), [Domain Events](../../../docs/domain-events.md), [application models](../../../docs/application-models.md) и [security](../../../docs/security.md). Требования — в specs этого change.

## Goals / Non-Goals

**Goals:**

- один handler на use case с тем же application interface, которым позже воспользуются gRPC-адаптер, Temporal Activity и Kafka consumer;
- инварианты и допустимость переходов — только в доменной модели и transition table; handler координирует authorization → load → domain method → persist → Outbox;
- все внешние зависимости — через порты; in-memory адаптеры честно моделируют commit/rollback, optimistic concurrency и уникальность `orderId`;
- открытые продуктовые решения не зашиты в код: они выражены портом или обязательным параметром composition root.

**Non-Goals:**

- persistence MongoDB, SpiceDB, Kafka/Outbox publisher, Integration Event mappers и Protobuf;
- wiring handlers в `cmd/` и Awilix composition root (появится вместе с первым transport adapter);
- BDD `.feature` и Cucumber.js: acceptance criteria получили `AC-*`, но executable scenarios — отдельный шаг по [testing](../../../docs/testing.md);
- item-level model, Reject After Acceptance, courier projection, tombstone для Cancel до Receive.

## Decisions

### Размещение кода и портов

```text
pkg/application/            UnitOfWork, Outbox, Clock, IdGenerator, Subject, application errors
pkg/infrastructure/memory/  InMemoryUnitOfWork, InMemoryOutbox, документное хранилище, FixedClock, SequentialIdGenerator
slices/<slice>/domains/<domain>/   aggregate, FSM, Domain Events, доменные ошибки, контракт repository, vo/
slices/<slice>/applications/access-control.ts   узкий authorization port слайса
slices/<slice>/applications/<usecase>/<usecase>.ts   Command/Query, Result DTO, use-case ports, Handler
slices/<slice>/infrastructure/memory/            in-memory repositories, read models, access control
```

- `UnitOfWork` (`run<T>(work: () => Promise<T>): Promise<T>`) и `Outbox` — shared application ports в `pkg/application`, как предписывает [Unit of Work](../../../docs/unit-of-work.md#размещение).
- Контракты repositories находятся в `domains/` по правилу [направления зависимостей](../../../boundaries/README.md#направление-зависимостей); остальные порты — в `applications/`. Альтернатива «все порты в applications» отклонена: она расходится с принятым ADR 6.
- In-memory адаптеры — production-исходники в `infrastructure/memory`, а не тестовые helpers: их же переиспользуют будущие BDD step definitions и локальный запуск без MongoDB.
- Тесты остаются в `test/` по существующей конвенции `node --test test/*.test.ts`.

### Handler shape

Handler — класс с одним методом `execute(input)` и зависимостями, переданными объектом в конструктор (совместимо с Awilix `PROXY` injection, без `container.resolve()`). Commands и Queries — неизменяемые plain types; `Subject` (`{ type: "user" | "service", id }`) приходит в Command уже verified адаптером транспорта. Результат — Application DTO; доменные объекты наружу не отдаются.

### Ошибки

Доменные ошибки (`InvalidTransitionError`, `GuardFailedError{reasons}`, `AlreadyInStateError`, `InvalidRestaurantOrderError`, `InvalidMenuError`, `InvalidMoneyError`, `InvalidRestaurantError`) живут в `domains/`; application errors (`NotFoundError`, `PermissionDeniedError`, `AlreadyExistsError`, `ConcurrencyConflictError`) — в `pkg/application`. Все ошибки — классы со стабильным полем `code`, которое infrastructure сопоставит с таблицей gRPC-кодов из README `restaurants` (`NOT_FOUND`, `INVALID_ARGUMENT`, `FAILED_PRECONDITION`, `ALREADY_EXISTS`). Альтернатива Result-типов везде отклонена: она удваивает код handlers без выгоды до появления transport mapping. Исключение — Cancel Restaurant Order, где отказ по стадии является бизнес-результатом (`Cancelled{stage}` | `CancellationRejected{reason}`), как требует [Kitchen](../../../docs/kitchen.md#cancel-restaurant-order).

### Authorization

У каждого слайса один узкий port в `applications/access-control.ts`, методы которого выражают нужду use case, а не имя permission в будущей `schema.zed` (имена permissions ещё не утверждены):

| Слайс | Методы |
| --- | --- |
| `restaurant` | `canRegisterRestaurant(subject)`, `canManageRestaurant(subject, restaurantId)` |
| `menu` | `canManageMenu(subject, restaurantId)` |
| `kitchenorder` | `canOperateKitchen(subject, restaurantId)` (relation `staff`), `canCoordinateFulfillment(subject, restaurantId)` (`service` identity `orders`), `canViewRestaurantOrders(subject, restaurantId)` |

Проверка выполняется первой; `false` → `PermissionDeniedError`, исключение порта пробрасывается как есть (fail closed). Команды кухни адресуются парой `restaurantId` + `restaurantOrderId`, команды `orders` — `restaurantId` + `orderId`: authorization проверяется по ресторану из Command, а несовпадение ресторана загруженного заказа даёт `NotFoundError` и не раскрывает State чужого заказа. Get Restaurant, List Restaurants и Get Menu — публичные данные без authorization (допущение: клиент может быть анонимным; BFF отвечает за coarse-grained ограничения).

Register Restaurant пишет creation relationship владельца через port `OwnerRelationshipWriter.touchOwner(restaurantId, ownerId)` до сохранения ресторана, с id, сгенерированным заранее, по [dual write rules](../../../docs/security.md#dual-write-rules). Сохранение ZedToken рядом с ресурсом откладывается до SpiceDB adapter.

### Unit of Work, Outbox и concurrency

- UoW используется только там, где сохраняется state + Outbox: Open/Close Restaurant, Update Menu Item, Set Menu Item Availability, Accept, Reject, Start Preparation, Mark Ready, Report Delay, Report Failure, Cancel. Остальные команды выполняют один save (autocommit), что соответствует таблице [«Где появится UoW»](../../../docs/unit-of-work.md#restaurants).
- Handler передаёт в `Outbox.add(aggregateRef, events)` только публикуемые Domain Events из `pullEvents()`; локальные факты (`RestaurantOrderReceived`, `RestaurantOrderQueued`, `RestaurantOrderAssembled`, `RestaurantRegistered`, `MenuCreated`, `MenuItemAdded`, …) отбрасываются. `aggregate_version` — версия, возвращённая `save`.
- Repository `save(aggregate)` проверяет загруженную `version`, записывает `version + 1` и возвращает её; несовпадение → `ConcurrencyConflictError`. Retry транзакции — ответственность production-реализации UoW; in-memory UoW не повторяет.
- `InMemoryUnitOfWork` снимает snapshot всех enlisted хранилищ при входе в внешний scope и восстанавливает их при исключении; вложенный `run` участвует во внешнем scope. `InMemoryOutbox` отказывает в записи вне активной UoW, что проверяет инвариант «Outbox только в транзакции со state».
- Идентификаторы создаются `IdGenerator` до UoW, время — `Clock` и передаётся в domain methods значением.

### RestaurantOrder и FSM

Transition table — данные (`from`, `trigger`, `to`, опциональный guard) плюс явный список строгих повторов `AlreadyInState`; все прочие пары возвращают `InvalidTransition`. Таблица повторяет [FSM: Kitchen / Preparation](../../../docs/fsm.md#kitchen--preparation) без `ChangeItem` (item-level model вне scope). Guards: Accept — результат domain service проверки Acceptance; ReportDelay — `not_later`; Cancel из `PREPARING`/`ASSEMBLED` — переданная политика отмены. Domain method вызывает engine, применяет `to`, обновляет timeline и явно записывает Domain Event. Состояния: `RECEIVED`, `ACCEPTED`, `QUEUED`, `PREPARING`, `ASSEMBLED`, `READY`, `REJECTED`, `CANCELLED`, `FAILED`.

Проверку Acceptance выполняет domain service `evaluateOrderAcceptance` в `kitchenorder/domains/restaurantorder`: он получает уже загруженные `Restaurant` (открытость), `Menu` (существование и `isAvailable` позиций) и snapshot условий, вызывает существующие `RestaurantAcceptingOrders` и `MenuItemAvailable` и возвращает `satisfied | reasons`. Причины: `restaurant_closed`, причины `RestaurantAcceptingOrders`, `menu_item_not_found{menuItemId}`, причины `MenuItemAvailable` с `menuItemId`.

`Preparation Item` — `menuItemId`, `Quantity`, `options: string[]` (непрозрачные идентификаторы выбранных options; модель Modifier не согласована). Дубликат определяется по `menuItemId` + отсортированному набору options. Цена в Preparation Item не хранится.

### Допущения по открытым продуктовым вопросам

| Вопрос | Решение в этом change |
| --- | --- |
| Расчёт начального `expected_ready_at` | Port `ReadyTimeEstimator.estimate({ restaurantId, items, acceptedAt })`; in-memory — фиксированная длительность, заданная в тесте |
| Часы работы, schedule exceptions, capacity, окно и остаток Menu Item | Port `AcceptanceConditionsProvider.load({ restaurantId, menuItemIds, now })` возвращает snapshot для существующих Specifications; модель хранения не вводится. Port читает только локальные данные и вызывается внутри UoW |
| Порядок Kitchen Queue | Port `KitchenQueueOrdering.order(entries)`; in-memory — FIFO по `accepted_at`, затем по id, только как тестовая реализация |
| `priority` | Не моделируется: значения не согласованы |
| Семантика повтора Start Preparation / Mark Ready | Обязательный параметр handler `onRepeat: "succeed-without-event" \| "reject"`; значения по умолчанию нет, выбор — в composition root |
| Отмена во время Preparation | Обязательный параметр `cancellationDuringPreparation: "allow" \| "reject"`, передаётся в guard FSM как политика |
| Reject After Acceptance | Не разрешён: `InvalidTransition` |
| Cancel до Receive | `NotFoundError`; tombstone не вводится |
| Повтор Receive с другим составом | `AlreadyExistsError`; с тем же составом — существующий заказ. Открытость ресторана при Receive не проверяется — только при Accept |
| Повтор Queue | Успешный no-op (системная команда, повтор без бизнес-эффекта) |
| Повтор Reject, Mark Assembled, Report Failure | Строго: `AlreadyInStateError` |
| Причины Reject, Delay, Failure | Непрозрачная строка; Reject и Delay — необязательная, Failure — обязательная. Каталог причин не вводится |
| Меню | Отдельный aggregate `Menu` в `slices/menu`, одно меню на ресторан; `MenuItem` — entity `Menu`. Это уточняет минимальную модель README, где `MenuItem` вложен в `Restaurant`: владельцем меню является slice `menu` |
| Данные ресторана | Только `name` и `ownerId`; адрес, часы работы и прочее требуют SDD. Новый ресторан закрыт |
| Open/Close повтор | Успешный no-op без события |
| Публикация Open/Close/Availability | `RestaurantOpened`, `RestaurantClosed`, `MenuItemAvailabilityChanged` передаются в Outbox (матрица use cases: «publishes business fact»); состав Integration Event и consumers — при создании контракта |
| Update Menu Item меняет доступность | Записывает тот же `MenuItemAvailabilityChanged`, что и Set Menu Item Availability, чтобы факт не зависел от команды |
| List Restaurants: «доступные» | По умолчанию только открытые; фильтр `includeClosed`; страница `limit` (1–100, по умолчанию 50) с курсором по id. Часы работы, зона и capacity не учитываются |
| List Ready Orders | Все заказы в `READY` ресторана; без данных курьера и без отсечения по Pickup до projection `delivery` |

### Money

`Money` — value object в `slices/menu/domains/menu/vo/money.ts` на `Decimal` из `decimal.js`: создаётся только из строки формата `^\d+(\.\d+)?$`, валюта — `^[A-Z]{3}$`, сумма неотрицательна, равенство по значению, наружу — каноническая строка `toFixed()` без потери точности. Арифметика не добавляется, пока её не требует сценарий. Альтернатива `bigint` в minor units отклонена: [boundaries/README.md](../../../boundaries/README.md#денежные-значения) фиксирует `decimal.js` для `restaurants`.

### Biome

`files.includes` в `biome.json` сейчас указывает на несуществующий `src/**`, поэтому production-код не проверяется. Значение меняется на `cmd/**`, `pkg/**`, `slices/**`, `test/**`, `*.json`.

## Risks / Trade-offs

- [Порты открытых вопросов могут не совпасть с будущим продуктовым решением] → они узкие и находятся рядом с use case; замена реализации или сигнатуры локальна.
- [In-memory snapshot UoW не моделирует изоляцию конкурентных транзакций] → конкурентность проверяется optimistic version check в unit tests и будущими Testcontainers-тестами MongoDB.
- [Кросс-слайсовые импорты домена: `kitchenorder` читает `Restaurant` и `Menu`] → импортируются только типы и чтение состояния; Acceptance не меняет их атомарно с заказом, как описано в [Kitchen](../../../docs/kitchen.md#temporal-и-persistence).
- [List Ready Orders растёт без ограничения, пока нет Pickup projection] → допустимо для in-memory; ограничение окна или Pickup projection — до production read model.
- [Domain Events без Integration Event mapper] → handler передаёт события в port; отсутствие mapper станет ошибкой Outbox adapter, как требует [Domain Events](../../../docs/domain-events.md#цепочка-публикации).

## Migration Plan

Данные и deployment не меняются; handlers не подключены к транспорту. Rollback — удаление добавленных модулей, тестов, зависимости `decimal.js` и возврат `biome.json`.

## Open Questions

- Конкретные имена permissions в `authorization/schema.zed` для методов access-control ports.
- Состав payload Integration Events и маппинг Domain Event → `nomnom.restaurants.v1.*` (в том числе `RestaurantOrderFailed` → `PreparationFailed`).
