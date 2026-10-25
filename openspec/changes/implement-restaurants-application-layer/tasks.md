# Tasks

## 1. Shared application foundation

- [x] 1.1 Добавить `decimal.js` (последняя стабильная) в `boundaries/restaurants/package.json`, расширить `biome.json` на `cmd/**`, `pkg/**`, `slices/**`, `test/**`; проверить `npm install` и `npm run check` на существующем коде.
- [x] 1.2 Добавить `pkg/application` (UnitOfWork, Outbox, Clock, IdGenerator, Subject, application errors) и `pkg/infrastructure/memory` (UoW со snapshot/rollback и nested scope, Outbox только внутри UoW, документное хранилище с version check, FixedClock, SequentialIdGenerator); покрыть unit tests commit/rollback, nested scope, запрет Outbox вне UoW и конфликт версии.

## 2. Slice `restaurant`

- [x] 2.1 Добавить aggregate `Restaurant` (register, rename, open, close, Domain Events, snapshot/restore) и контракт repository в `domains/restaurant`; покрыть domain tests инвариантов названия и no-op повторов Open/Close.
- [x] 2.2 Добавить access-control port, handlers Register/Update/Open/Close Restaurant и Get/List Restaurants с read model port, in-memory адаптеры; покрыть tests AC-200…AC-214 (allow/deny/fail closed, owner relationship до save, Outbox в UoW и откат, пагинация).

## 3. Slice `menu`

- [x] 3.1 Добавить VO `Money` на `decimal.js` и aggregate `Menu` с entity `MenuItem` и контрактом repository в `domains/menu`; покрыть domain tests точности, отрицательной суммы, валюты и уникальности Menu Item.
- [x] 3.2 Добавить access-control port, handlers Create Menu, Add/Update Menu Item, Set Menu Item Availability, Get Menu и in-memory адаптеры; покрыть tests AC-220…AC-233.

## 4. Slice `kitchenorder`: domain

- [x] 4.1 Добавить VO `Quantity`, transition table и engine FSM `RestaurantOrder`; покрыть table-driven tests всех строк, guards, строгих повторов и полноты `state × trigger`.
- [x] 4.2 Добавить aggregate `RestaurantOrder` (receive, accept, reject, queue, startPreparation, markAssembled, markReady, reportDelay, reportFailure, cancel, Domain Events, snapshot/restore), domain service `evaluateOrderAcceptance` и контракт repository с уникальностью `orderId`; покрыть domain tests timeline, событий, состава и причин Acceptance.

## 5. Slice `kitchenorder`: application

- [x] 5.1 Добавить access-control port, in-memory repository/access control и handlers Receive Order Request, Accept Order (с Queue в одной UoW, ports `AcceptanceConditionsProvider` и `ReadyTimeEstimator`), Reject Order, Queue Restaurant Order; покрыть tests AC-240…AC-252.
- [x] 5.2 Добавить handlers Start Preparation, Mark Assembled, Mark Ready for Pickup (с обязательной политикой повтора), Report Preparation Delay, Report Preparation Failure; покрыть tests AC-253…AC-262 и AC-268.
- [x] 5.3 Добавить handler Cancel Restaurant Order с обязательной политикой отмены во время Preparation и бизнес-результатом по стадии; покрыть tests AC-263…AC-267.
- [x] 5.4 Добавить read model ports и in-memory read models, handlers Get Restaurant Order, Get Kitchen Queue (port `KitchenQueueOrdering`), List Active Preparations, List Ready Orders; покрыть tests AC-269 и AC-280…AC-283.
- [x] 5.5 Добавить README для новых use-case packages (`queue-restaurant-order`, `mark-assembled`, `report-preparation-delay`, `report-preparation-failure`, `get-restaurant-order`, `get-kitchen-queue`, `list-active-preparations`, `list-ready-orders`) и проверить, что у каждого каталога use case есть README.

## 6. Документация и общая проверка

- [x] 6.1 Обновить статус реализации в `boundaries/restaurants/README.md` (реализованные и исключённые use cases, порты, допущения со ссылкой на design.md) и проверить ссылки.
- [x] 6.2 Выполнить в `boundaries/restaurants` `npm test`, `npm run build`, `npm run check`, затем `openspec validate implement-restaurants-application-layer --strict` и `git diff --check` по изменённым путям; подтвердить, что вне `boundaries/restaurants/**` и каталога change файлы не изменены.

## Workflow follow-up

- После ревью архивировать change `implement-restaurants-application-layer`.
- Отдельными change: BDD `.feature` по выбранным `AC-*` с Cucumber.js, gRPC-адаптер, MongoDB repositories/UoW, Outbox mappers, SpiceDB adapter, правки общих документов из отчёта.
