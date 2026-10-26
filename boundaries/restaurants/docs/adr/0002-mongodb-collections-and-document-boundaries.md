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
| `restaurants` | aggregate `Restaurant` | профиль, `staff` (множество user id, ограничено командой ресторана), `authorizationRevision` (ZedToken) | — |
| `menus` | aggregate `Menu` | `items` — Menu Item с ценой `{ amount: decimal string, currency }` | `restaurantId` |
| `restaurant_orders` | aggregate `RestaurantOrder` | Preparation Items, `timeline`, причины отказа/сбоя/отмены | `restaurantId`, `orderId` (Order контекста `orders`) |
| `outbox` | одно Integration Event или Change Notification ([ADR 31](../../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md), контракт CDC — [ADR 37](../../../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)) | `payload` — байты контракта (BinData), `traceparent` (`""` без trace context) | `aggregateType`, `aggregateId`, `aggregateVersion` (Long) |
| `inbox` | факт обработки Integration Event consumer'ом ([ADR 11](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md)), `_id` — `{consumer}:{eventId}` | — | `eventId`, `aggregateId`, `topic` |
| `simulator` | единственный документ состояния Demo Simulator (FR-604) | — | — |
| `migrations_changelog_<набор>` | применённая миграция набора migrate-mongo (`fileName`, `appliedAt`, `migrationBlock`); набор — `pkg` или слайс | — | — |
| `migrations_lock` | lock migrate-mongo: не больше одного документа, пока процесс мигрирует набор | — | `createdAt` |

Restaurant вместе с меню и заказами не объединяется в один документ: заказы растут неограниченно и меняются с другой частотой. Read models kitchen board и каталога пока читаются запросами к документам aggregate по индексам; отдельные проекции — следующий шаг.

### Технические поля, валидация и индексы

Каждый документ aggregate содержит `_id` (id aggregate, строка), `version` (optimistic concurrency, начинается с 1) и `schemaVersion` (формат документа). Сохранение с `version = 0` — `insertOne`, иначе `replaceOne({ _id, version: expected })`; `matchedCount = 0` или duplicate key по `_id` — `ConcurrencyConflictError`.

На каждой коллекции — `$jsonSchema` validator с `validationLevel: strict`, `validationAction: error`: обязательные поля, типы, непустые строки, enum состояний `RestaurantOrder`, формат денег (`^\d+(\.\d+)?$`, `^[A-Z]{3}$`). Необязательные доменные поля не хранятся как `null` (`ignoreUndefined`).

| Коллекция | Индекс | Назначение |
| --- | --- | --- |
| `restaurants` | `{ isOpen: 1, _id: 1 }` | каталог открытых ресторанов, пагинация по id |
| `restaurants` | `{ ownerId: 1 }` | выборка ресторанов владельца |
| `menus` | `{ restaurantId: 1 }` unique | одно меню на ресторан → `AlreadyExistsError` |
| `restaurant_orders` | `{ orderId: 1 }` unique | идемпотентный Receive → `DuplicateOrderIdError` |
| `restaurant_orders` | `{ restaurantId: 1, state: 1, "timeline.preparationStartedAt": 1 }` | очередь и активные приготовления |
| `restaurant_orders` | `{ restaurantId: 1, state: 1, "timeline.readyAt": 1 }` | готовые к выдаче |
| `outbox` | `{ eventId: 1 }` unique | UUIDv5 от `aggregateType:aggregateId:aggregateVersion:eventType`; конфликт откатывает транзакцию |
| `outbox` | `{ createdAt: 1 }` TTL 1 час | retention Outbox: Debezium читает запись из change stream сразу после commit (ADR 37) |
| `inbox` | `{ consumer: 1, eventId: 1 }` unique | дедупликация; конфликт откатывает транзакцию и считается duplicate |
| `inbox` | `{ processedAt: 1 }` TTL 30 дней | retention Inbox |

### Эволюция схемы

Миграции встроены в приложение и выполняются библиотекой [migrate-mongo](https://github.com/seppevs/migrate-mongo) ([ADR 39](../../../../docs/adr/0039-data-access-without-orm.md)): каждый процесс (`restaurants`, `restaurants-consumer`) при старте, до обслуживания, своим `MongoClient` (с телеметрией команд и пула) вызывает `migrateRestaurantsDatabase` (`infrastructure/repository/migrate.ts` поверх `pkg/infrastructure/repository/migrate.ts`).

Каждый вертикальный слайс владеет миграциями своих коллекций. Набор миграций — каталог файлов `<NNNN>-<изменение>.ts` с нумерацией с 0001 в каждом наборе и собственным changelog `migrations_changelog_<набор>`:

| Набор | Каталог | Коллекции |
| --- | --- | --- |
| `pkg` | `pkg/infrastructure/repository/migrations/` | `outbox`, `inbox`; чистки всего сервиса (остатки polling relay ADR 31, коллекции прежних миграторов) |
| `restaurant` | `slices/restaurant/infrastructure/repository/migrations/` | `restaurants` |
| `menu` | `slices/menu/infrastructure/repository/migrations/` | `menus` |
| `restaurantorder` | `slices/restaurantorder/infrastructure/repository/migrations/` | `restaurant_orders` |
| `simulator` | `slices/simulator/infrastructure/repository/migrations/` | `simulator` |

Порядок наборов задан одним явным списком `RESTAURANTS_MIGRATION_SETS` в `infrastructure/repository/migrate.ts`, каталоги не ищутся: `pkg` первым, затем слайсы. Наборы независимы — слайс не меняет чужие коллекции, миграция не ссылается на другой набор; общее идёт первым, чтобы чистки сервиса предшествовали коллекциям слайсов. Внутри набора библиотека применяет по порядку имён ещё не записанные в его changelog файлы и записывает каждый после успешного `up`; ошибка останавливает старт, следующие наборы не применяются. Новый слайс с коллекциями добавляет свой каталог и элемент списка.

Коллекция с validator и индексами — шаг `ensureCollection`: создаёт коллекцию или заменяет validator существующей (`collMod`) и создаёт индексы (`createIndexes` идемпотентен; индекс с тем же именем и другими опциями — ошибка `IndexOptionsConflict`). Поэтому миграции безопасно применяются и к базе, созданной прежним собственным мигратором или migrate-mongo с единым changelog: changelog наборов на такой базе пусты, и все миграции повторяются идемпотентно; миграция `pkg/0004-drop-previous-migrators` удаляет `schema_migrations`, `schema_migrations_lock` и прежний единый `migrations_changelog`. На актуальной базе изменений нет — процесс пишет `nothing to migrate` и продолжает. Отдельной команды и init container нет. В `dist` те же миграции скомпилированы в `.js` (каталоги `pkg` и `slices` входят в `tsconfig.json`), расширение файлов миграций следует за процессом.

API и consumer стартуют одновременно. Lock migrate-mongo — документ в `migrations_lock` с TTL-индексом по `createdAt` (30 с), но `up` при занятом lock отказывает, а не ждёт, и проверяет и вставляет lock двумя шагами. Поэтому обёртка перед `up` создаёт unique index по полю, которого у документов lock нет (unique index допускает один документ без поля), — вставка lock становится атомарной, а занятый lock повторяется каждые 500 мс, пока держатель его не снимет или TTL не удалит lock упавшего процесса (TTL monitor MongoDB работает раз в 60 с, то есть до ~90 с — дольше бюджета `startupProbe` в 60 с, pod перезапустится и продолжит ждать). Коллекция lock одна на все наборы, но lock держится на время одного набора: `up` сам захватывает и снимает его, а lock на все наборы сразу потребовал бы второго, собственного lock. Процессы мигрируют по одному набору за раз, каждый — в порядке списка, так что в процессе `pkg` всегда предшествует слайсам; наборы независимы, поэтому чередование наборов разных процессов безопасно. `up` читает changelog до захвата lock: процесс, прочитавший его перед тем, как держатель закончил, повторит его миграции, а lock не продлевается — поэтому каждая миграция обязана быть идемпотентной. Конфигурация migrate-mongo — одна переменная модуля, которую `up` читает между await, поэтому запуски внутри одного процесса выполняются по очереди.

Изменение формата документа: новый `schemaVersion`, читатель принимает предыдущую версию (как `StoredRestaurantSnapshot` принимает документы без `address` и `staff`), validator расширяется до перехода новой миграцией (`ensureCollection` с новым validator), миграция документов — тоже новым файлом в конце набора слайса-владельца; применённый файл никогда не редактируется: validator и индексы в нём — снимок на момент миграции, а не ссылка на код слайса.

### Атомарность

- Изменение одного aggregate без событий (Register Restaurant, Assign/Revoke Staff, запись ZedToken reconciler'ом) опирается на single-document atomicity и `version`.
- Изменение aggregate вместе с записями Outbox и изменения нескольких aggregate выполняются в `UnitOfWork` = multi-document транзакция MongoDB (`readConcern: snapshot`, `writeConcern: majority`, чтение с primary). Сессия активной транзакции передаётся через `AsyncLocalStorage`, поэтому порты не зависят от драйвера. Driver повторяет `work` при `TransientTransactionError`, что допускает контракт `UnitOfWork`. Outbox вне активной транзакции отклоняет запись. Порядок доставки задаёт change stream (порядок commit), отдельного счётчика нет. Domain Event превращается в запись внедрённым mapper'ом (`eventType` — полное имя Protobuf-сообщения, `topic`, `payload`); `undefined` — запись не создаётся. Пока контрактов нет, используется mapper по умолчанию, не публикующий ничего.
- Транзакции требуют replica set (в kind — single-node replica set). Межконтекстных транзакций нет.

### Подключение

Официальный драйвер `mongodb` 7.x (проверено с MongoDB 8.3). `MONGODB__URI` обязателен: при пустом значении `restaurants` и `restaurants-consumer` не стартуют; недоступная MongoDB останавливает старт после `MONGODB__TIMEOUT`. Connection string с учётными данными приходит только из Secret.

## Consequences

Данные ресторанов, меню и заказов кухни переживают рестарт процесса, и reconciler больше не удаляет связи живых ресторанов. Optimistic concurrency и уникальность, ранее эмулированные в памяти, обеспечиваются индексами и условными записями MongoDB.

Окружения обязаны предоставлять replica set; standalone MongoDB не поддерживается. Каждое изменение формата документа требует обновления validator, `schemaVersion` и, при необходимости, миграции документов. In-memory адаптеры остаются только для unit-тестов; поведение MongoDB-адаптеров проверяют интеграционные тесты (`test/mongodb-integration.test.ts`, `MONGODB_URI`).

Доставка Outbox в Kafka (Debezium, ADR 37), Protobuf-mappers и событийные проекции этим решением не реализуются; Inbox и consumer `OrderPlaced` добавлены change `add-restaurants-order-placed-consumer`.
