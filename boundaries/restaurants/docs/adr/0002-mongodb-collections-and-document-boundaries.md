# 2. Коллекции MongoDB и границы документов restaurants

Date: 2026-10-25

## Status

Accepted

## Context

[Общесистемный ADR 8](../../../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md) выбирает MongoDB основным хранилищем `restaurants`, но оставляет открытыми коллекции, границы документов, валидацию, индексы, эволюцию схемы и требования к multi-document atomicity. До этого решения все aggregate хранились в памяти процесса: после рестарта рестораны пропадали, а reconciler удалял их связи в SpiceDB как сироты.

Use cases уже работают через порты `RestaurantRepository`, `MenuRepository`, `RestaurantOrderRepository`, `UnitOfWork` и `Outbox` с optimistic concurrency по `version` ([ADR 20](../../../../docs/adr/0020-unit-of-work-for-local-transactions.md)). Commit d78df5b хранит ZedToken последней записи relationships рядом с рестораном.

## Decision

### Границы документов

Один документ — один aggregate. Вложенным хранится только то, что читается и изменяется вместе с aggregate и ограничено по размеру; соседние aggregate связаны идентификаторами.

| Коллекция | Документ | Вложено | Ссылки |
| --- | --- | --- | --- |
| `restaurants` | aggregate `Restaurant` | профиль, `marketId` (Market, [ADR 41](../../../../docs/adr/0041-cell-based-architecture-with-control-plane.md), неизменяем), `timeZone` (IANA-пояс, неизменяем), `openingHours` (недельное расписание `{ day, opens, closes }` в местном времени, пустое — не принимает заказы), `scheduledOrders` (`{ slotCapacity, openingLeadMs, closingGraceMs }`, задаются и убираются вместе; нет — запланированных заказов нет), `staff` (множество user id, ограничено командой ресторана), `authorizationRevision` (ZedToken); read model `waitTime` (Wait Time Statistics, пишется без `version`; `open[]` — заказы с `queuedAt`) | — |
| `menus` | aggregate `Menu` | `marketId` (Market ресторана), `items` — Menu Item с ценой `{ amount: decimal string, currency }` | `restaurantId` |
| `restaurant_orders` | aggregate `RestaurantOrder` | `marketId` (Market ресторана, получившего заказ), Preparation Items, `timeline`, `schedule` запланированного заказа (`windowStart`, `windowEnd`, `plannedReadyAt`, целиком или нет), причины отказа/сбоя/отмены | `restaurantId`, `orderId` (Order контекста `orders`), `cookId` — Cook, назначенный при Start Preparation; обязателен в `PREPARING` и `ASSEMBLED`, остаётся после завершения |
| `cooks` | aggregate `Cook` — повар Cook Pool ресторана (change `add-cook-and-courier-pools`) | `name`, `onLine`, `lastAssignedAt`, `retiredAt` | `restaurantId` |
| `schedule_slots` | aggregate `ScheduleSlot` — одно окно доставки ресторана (change `add-scheduled-orders`), `_id` = `<restaurantId>\|<windowStart ISO>` | `windowStart`, `windowEnd`, `reservations[]` (`orderId`, `status` `held` с `expiresAt` или `confirmed`; ограничены ёмкостью окна), `lapsed[]` — заказы с вычищенным просроченным hold, чтобы Confirm заново проверил ёмкость своего окна | `restaurantId`, `reservations.orderId` (Order контекста `orders`) |
| `outbox` | одно Integration Event или Change Notification ([ADR 31](../../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md), контракт CDC — [ADR 37](../../../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)) | `payload` — байты контракта (BinData), `traceparent` (`""` без trace context) | `aggregateType`, `aggregateId`, `aggregateVersion` (Long) |
| `inbox` | факт обработки Integration Event consumer'ом ([ADR 11](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md)), `_id` — `{consumer}:{eventId}` | — | `eventId`, `aggregateId`, `topic` |
| `simulator` | единственный документ состояния Demo Simulator (FR-604) | — | — |
| `migrations_changelog_<id>` | применённая миграция набора migrate-mongo (`fileName`, `appliedAt`, `migrationBlock`); `id` — storage id набора: `messaging`, `restaurants`, `menus`, `restaurant_orders`, `cooks`, `schedule_slots`, `simulator` | — | — |
| `migrations_lock` | lock migrate-mongo: не больше одного документа, пока процесс мигрирует набор | — | `createdAt` |

Занятость Cook не хранится в `cooks`: Cook занят, пока в `restaurant_orders` есть его заказ в `PREPARING` или `ASSEMBLED`. Иначе каждый переход заказа в `READY`, `FAILED` и `CANCELLED` менял бы второй документ, а состояние Cook могло бы разойтись с заказами. Cook Pool не вкладывается в `restaurants`: линия меняется на каждой смене, а Start Preparation пишет только назначенного Cook, не весь ресторан.

Restaurant вместе с меню и заказами не объединяется в один документ: заказы растут неограниченно и меняются с другой частотой. Read models kitchen board и каталога пока читаются запросами к документам aggregate по индексам; отдельные проекции — следующий шаг.

### Технические поля, валидация и индексы

Каждый документ aggregate содержит `_id` (id aggregate, строка), `version` (optimistic concurrency, начинается с 1) и `schemaVersion` (формат документа). Сохранение с `version = 0` — `insertOne`, иначе `replaceOne({ _id, version: expected })`; `matchedCount = 0` или duplicate key по `_id` — `ConcurrencyConflictError`.

На каждой коллекции — `$jsonSchema` validator с `validationLevel: strict`, `validationAction: error`: обязательные поля, типы, непустые строки, enum состояний `RestaurantOrder`, формат денег (`^\d+(\.\d+)?$`, `^[A-Z]{3}$`). Необязательные доменные поля не хранятся как `null` (`ignoreUndefined`).

| Коллекция | Индекс | Назначение |
| --- | --- | --- |
| `restaurants` | `{ marketId: 1, isOpen: 1, _id: 1 }` | каталог открытых ресторанов одного Market, пагинация по id |
| `restaurants` | `{ marketId: 1, _id: 1 }` | каталог Market с закрытыми (`include_closed`), пагинация по id |
| `restaurants` | `{ ownerId: 1, marketId: 1, _id: 1 }` | рестораны владельца в Market, пагинация по id |
| `menus` | `{ restaurantId: 1 }` unique | одно меню на ресторан → `AlreadyExistsError` |
| `restaurant_orders` | `{ orderId: 1 }` unique | идемпотентный Receive → `DuplicateOrderIdError` |
| `restaurant_orders` | `{ restaurantId: 1, state: 1, "timeline.preparationStartedAt": 1 }` | очередь и активные приготовления |
| `restaurant_orders` | `{ restaurantId: 1, state: 1, "timeline.readyAt": 1 }` | готовые к выдаче |
| `restaurant_orders` | `{ cookId: 1 }` unique, `partialFilterExpression: { state: { $in: ["PREPARING", "ASSEMBLED"] } }` | один активный заказ на Cook; конфликт — `ConcurrencyConflictError` |
| `cooks` | `{ restaurantId: 1, retiredAt: 1, onLine: 1, lastAssignedAt: 1 }` | Cook Pool ресторана и выбор свободного Cook |
| `restaurant_orders` | `{ restaurantId: 1, state: 1, "schedule.plannedReadyAt": 1 }`, `partialFilterExpression: { schedule: { $exists: true } }` | принятые запланированные заказы по `plannedReadyAt` (List Scheduled Orders) |
| `schedule_slots` | `{ restaurantId: 1, windowStart: 1 }` | окна ресторана в диапазоне (List Scheduled Slots, альтернативы) |
| `schedule_slots` | `{ "reservations.orderId": 1 }` unique, `partialFilterExpression: { "reservations.orderId": { $exists: true } }` | окно заказа (Confirm, Release, повтор Reserve); заказ держит не больше одного окна, конфликт — `ConcurrencyConflictError` |
| `schedule_slots` | `{ lapsed: 1 }` | окно заказа с вычищенным hold |
| `schedule_slots` | `{ windowEnd: 1 }` TTL 0 | прошедшие окна удаляет MongoDB; фоновых заданий нет |
| `outbox` | `{ eventId: 1 }` unique | UUIDv5 от `aggregateType:aggregateId:aggregateVersion:eventType`; конфликт откатывает транзакцию |
| `outbox` | `{ createdAt: 1 }` TTL 1 час | retention Outbox: Debezium читает запись из change stream сразу после commit (ADR 37) |
| `inbox` | `{ consumer: 1, eventId: 1 }` unique | дедупликация; конфликт откатывает транзакцию и считается duplicate |
| `inbox` | `{ processedAt: 1 }` TTL 30 дней | retention Inbox |

### Эволюция схемы

Миграции встроены в приложение и выполняются библиотекой [migrate-mongo](https://github.com/seppevs/migrate-mongo) ([ADR 39](../../../../docs/adr/0039-data-access-without-orm.md)): каждый процесс (`restaurants`, `restaurants-consumer`) при старте, до обслуживания, своим `MongoClient` (с телеметрией команд и пула) вызывает `migrateRestaurantsDatabase` (`infrastructure/repository/migrate.ts` поверх `pkg/infrastructure/repository/migrate.ts`).

Каждый вертикальный слайс владеет миграциями своих коллекций. Набор миграций — каталог файлов `<NNNN>-<изменение>.ts` с нумерацией с 0001 в каждом наборе и собственным changelog `migrations_changelog_<id>`.

Имя changelog строится только из `id` набора — явного строкового литерала в `RESTAURANTS_MIGRATION_SETS`, названного по хранимым данным. `id` — часть контракта схемы БД: он никогда не выводится из имён каталогов, пакетов и слайсов и не меняется при рефакторинге кода (переименование слайса или каталога не трогает `id`); смена `id` — изменение схемы и требует миграции, переносящей changelog. Применённые миграции возвращаются и логируются как `<id>/<файл>`.

| Набор (`id`) | Каталог | Коллекции |
| --- | --- | --- |
| `messaging` | `pkg/infrastructure/repository/migrations/` | `outbox`, `inbox`; чистки всего сервиса (остатки polling relay ADR 31, коллекции прежних миграторов) |
| `restaurants` | `slices/restaurant/infrastructure/repository/migrations/` | `restaurants` |
| `menus` | `slices/menu/infrastructure/repository/migrations/` | `menus` |
| `restaurant_orders` | `slices/restaurantorder/infrastructure/repository/migrations/` | `restaurant_orders` |
| `cooks` | `slices/cook/infrastructure/repository/migrations/` | `cooks` |
| `schedule_slots` | `slices/scheduleslot/infrastructure/repository/migrations/` | `schedule_slots` |
| `simulator` | `slices/simulator/infrastructure/repository/migrations/` | `simulator` |

Порядок наборов задан одним явным списком `RESTAURANTS_MIGRATION_SETS` в `infrastructure/repository/migrate.ts`, каталоги не ищутся: `messaging` (общие коллекции `pkg`) первым, затем слайсы. Наборы независимы — слайс не меняет чужие коллекции, миграция не ссылается на другой набор; общее идёт первым, чтобы чистки сервиса предшествовали коллекциям слайсов. Внутри набора библиотека применяет по порядку имён ещё не записанные в его changelog файлы и записывает каждый после успешного `up`; ошибка останавливает старт, следующие наборы не применяются. Новый слайс с коллекциями добавляет свой каталог и элемент списка.

Коллекция с validator и индексами — шаг `ensureCollection`: создаёт коллекцию или заменяет validator существующей (`collMod`) и создаёт индексы (`createIndexes` идемпотентен; индекс с тем же именем и другими опциями — ошибка `IndexOptionsConflict`). Поэтому миграции безопасно применяются и к базе, созданной прежним собственным мигратором или migrate-mongo с единым changelog: changelog наборов на такой базе пусты, и все миграции повторяются идемпотентно; миграция `messaging/0004-drop-previous-migrators` удаляет `schema_migrations`, `schema_migrations_lock` и прежний единый `migrations_changelog`. На актуальной базе изменений нет — процесс пишет `nothing to migrate` и продолжает. Отдельной команды и init container нет. В `dist` те же миграции скомпилированы в `.js` (каталоги `pkg` и `slices` входят в `tsconfig.json`), расширение файлов миграций следует за процессом.

API и consumer стартуют одновременно. Lock migrate-mongo — документ в `migrations_lock` с TTL-индексом по `createdAt` (30 с), но `up` при занятом lock отказывает, а не ждёт, и проверяет и вставляет lock двумя шагами. Поэтому обёртка перед `up` создаёт unique index по полю, которого у документов lock нет (unique index допускает один документ без поля), — вставка lock становится атомарной, а занятый lock повторяется каждые 500 мс, пока держатель его не снимет или TTL не удалит lock упавшего процесса (TTL monitor MongoDB работает раз в 60 с, то есть до ~90 с — дольше бюджета `startupProbe` в 60 с, pod перезапустится и продолжит ждать). Коллекция lock одна на все наборы, но lock держится на время одного набора: `up` сам захватывает и снимает его, а lock на все наборы сразу потребовал бы второго, собственного lock. Процессы мигрируют по одному набору за раз, каждый — в порядке списка, так что в процессе `messaging` всегда предшествует слайсам; наборы независимы, поэтому чередование наборов разных процессов безопасно. `up` читает changelog до захвата lock: процесс, прочитавший его перед тем, как держатель закончил, повторит его миграции, а lock не продлевается — поэтому каждая миграция обязана быть идемпотентной. Конфигурация migrate-mongo — одна переменная модуля, которую `up` читает между await, поэтому запуски внутри одного процесса выполняются по очереди.

Изменение формата документа: новый `schemaVersion`, читатель принимает предыдущую версию (как `StoredRestaurantSnapshot` принимает документы без `address` и `staff`), validator расширяется до перехода новой миграцией (`ensureCollection` с новым validator), миграция документов — тоже новым файлом в конце набора слайса-владельца; применённый файл никогда не редактируется: validator и индексы в нём — снимок на момент миграции, а не ссылка на код слайса.

Market (ADR 41) введён так: миграции `restaurants/0003-market`, `menus/0002-market` и `restaurant_orders/0003-market` сначала явно проставляют `marketId: "berlin"` документам без поля (данные до Market принадлежат стартовому Market), затем validator требует `marketId` (slug `^[a-z][a-z0-9-]{1,31}$`); `schemaVersion` — 2, 2 и 3. Читатель документа без `marketId` не допускает: после миграции таких нет, а подстановка Market в коде была бы скрытым выбором. `restaurants/0003-market` заменяет индексы `restaurants_open_by_id` и `restaurants_owner` индексами с ведущим `marketId`. Индексы `menus` и `restaurant_orders` не меняются: они читаются по ресторану или состоянию, а у ресторана один Market.

Pickup введён так: `restaurant_orders/0004-picked-up` (`schemaVersion` 4) только расширяет validator — состояние `PICKED_UP` и `timeline.pickedUpAt`; сохранённые документы остаются валидными, индексы не меняются.

Расписание введено так: `restaurants/0005-opening-hours` (`schemaVersion` 4) проставляет ресторанам стартового Market `timeZone: "Europe/Berlin"` и пустое `openingHours` (ресторан другого Market без пояса останавливает миграцию: пояс не угадывается), затем validator требует оба поля. Запланированные заказы введены так: `restaurants/0006-scheduled-orders` (`schemaVersion` 5) добавляет необязательные `scheduledOrders` и переименовывает `waitTime.open[].acceptedAt` в `queuedAt` (все открытые до этого заказы ставились в очередь в UoW Acceptance, момент тот же); `restaurant_orders/0006-scheduled-orders` (`schemaVersion` 6) добавляет необязательный `schedule` и индекс `restaurant_orders_board_scheduled`; набор `schedule_slots/0001-schedule-slots` создаёт коллекцию окон.

### Атомарность

- Изменение одного aggregate без событий (Register Restaurant, Assign/Revoke Staff, запись ZedToken reconciler'ом) опирается на single-document atomicity и `version`.
- Изменение aggregate вместе с записями Outbox и изменения нескольких aggregate выполняются в `UnitOfWork` = multi-document транзакция MongoDB (`readConcern: snapshot`, `writeConcern: majority`, чтение с primary). Сессия активной транзакции передаётся через `AsyncLocalStorage`, поэтому порты не зависят от драйвера. Driver повторяет `work` при `TransientTransactionError`, что допускает контракт `UnitOfWork`. Outbox вне активной транзакции отклоняет запись. Порядок доставки задаёт change stream (порядок commit), отдельного счётчика нет. Domain Event превращается в запись внедрённым mapper'ом (`eventType` — полное имя Protobuf-сообщения, `topic`, `payload`); `undefined` — запись не создаётся. Пока контрактов нет, используется mapper по умолчанию, не публикующий ничего.
- Start Preparation — UoW двух aggregate: заказ переходит в `PREPARING` с `cookId`, у Cook обновляется `lastAssignedAt` (растёт `version`). Два старта на одного Cook пишут один документ `cooks`, и snapshot-транзакция проигравшего завершается write conflict; Take Cook Off Line в своей UoW проверяет отсутствие активного заказа и сохраняет Cook по `version`, поэтому не проходит одновременно с назначением.
- Reserve, Confirm и Release Scheduled Slot — UoW над документом окна (Reserve ещё снимает просроченный hold заказа с другого окна): конкурентная запись того же окна проигрывает write conflict (driver повторяет `work`) или `ConcurrencyConflictError` (`version`, duplicate `_id` нового окна, unique `reservations.orderId`), и use case повторяет попытку до 5 раз, поэтому ёмкость окна не превышается. Просроченные `held` переносятся в `lapsed` при следующей записи окна.
- Транзакции требуют replica set (в kind — single-node replica set). Межконтекстных транзакций нет.

### Подключение

Официальный драйвер `mongodb` 7.x (проверено с MongoDB 8.3). `MONGODB__URI` обязателен: при пустом значении `restaurants` и `restaurants-consumer` не стартуют; недоступная MongoDB останавливает старт после `MONGODB__TIMEOUT`. Connection string с учётными данными приходит только из Secret.

## Consequences

Данные ресторанов, меню и заказов кухни переживают рестарт процесса, и reconciler больше не удаляет связи живых ресторанов. Optimistic concurrency и уникальность, ранее эмулированные в памяти, обеспечиваются индексами и условными записями MongoDB.

Окружения обязаны предоставлять replica set; standalone MongoDB не поддерживается. Каждое изменение формата документа требует обновления validator, `schemaVersion` и, при необходимости, миграции документов. In-memory адаптеры остаются только для unit-тестов; поведение MongoDB-адаптеров проверяют интеграционные тесты (`test/database-integration.test.ts`, `MONGODB_URI`).

Доставка Outbox в Kafka (Debezium, ADR 37), Protobuf-mappers и событийные проекции этим решением не реализуются; Inbox и consumer `OrderPlaced` добавлены change `add-restaurants-order-placed-consumer`; consumer `OrderPlaced` удалён change `start-fulfillment-and-pay-before-kitchen` (Restaurant Order создаёт только RPC из workflow после оплаты), Inbox остался у consumer `DeliveryPickedUp`.
