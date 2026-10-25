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
| `outbox` | одно Integration Event ([ADR 31](../../../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md)) | `payload` — Protobuf-байты контракта (BinData), `metadata` (`traceparent`, `causationId`) | `aggregateType`, `aggregateId`, `aggregateVersion` |
| `outbox_counters` | счётчик `seq` Outbox | — | — |
| `outbox_leases` | lease единственного активного relay | — | — |
| `inbox` | факт обработки Integration Event consumer'ом ([ADR 11](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md)), `_id` — `{consumer}:{eventId}` | — | `eventId`, `aggregateId`, `topic` |
| `schema_migrations` | применённая миграция документов | — | — |

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
| `outbox` | `{ seq: 1 }` unique | монотонный порядок вставки |
| `outbox` | `{ publishedAt: 1, seq: 1 }` | relay: `{ publishedAt: null }` по `seq` |
| `outbox_leases` | `{ expiresAt: 1 }` | поиск истёкшего lease |
| `inbox` | `{ consumer: 1, eventId: 1 }` unique | дедупликация; конфликт откатывает транзакцию и считается duplicate |
| `inbox` | `{ processedAt: 1 }` TTL 30 дней | retention Inbox |

### Эволюция схемы

Коллекции, validators и индексы создаёт идемпотентная команда `restaurants-migrate` (`cmd/restaurants-migrate`): недостающие коллекции создаются, validators существующих заменяются `collMod`, недостающие индексы создаются, затем по порядку применяются миграции документов из `infrastructure/mongodb/collections.ts` с записью id в `schema_migrations`. В Kubernetes она выполняется init container'ом `migrate` перед процессом, как `orders-migrate`. Основной процесс схему не меняет.

Изменение формата документа: новый `schemaVersion`, читатель принимает предыдущую версию (как `StoredRestaurantSnapshot` принимает документы без `address` и `staff`), validator расширяется до перехода, миграция документов добавляется в конец списка и никогда не редактируется после применения.

### Атомарность

- Изменение одного aggregate без событий (Register Restaurant, Assign/Revoke Staff, запись ZedToken reconciler'ом) опирается на single-document atomicity и `version`.
- Изменение aggregate вместе с записями Outbox и изменения нескольких aggregate выполняются в `UnitOfWork` = multi-document транзакция MongoDB (`readConcern: snapshot`, `writeConcern: majority`, чтение с primary). Сессия активной транзакции передаётся через `AsyncLocalStorage`, поэтому порты не зависят от драйвера. Driver повторяет `work` при `TransientTransactionError`, что допускает контракт `UnitOfWork`. Outbox вне активной транзакции отклоняет запись. `seq` выделяется `$inc` документа `outbox_counters` в той же транзакции. Domain Event превращается в запись внедрённым mapper'ом (`eventType` — полное имя Protobuf-сообщения, `topic`, `payload`); `undefined` — запись не создаётся. Пока контрактов нет, используется mapper по умолчанию, не публикующий ничего.
- Транзакции требуют replica set (в kind — single-node replica set). Межконтекстных транзакций нет.

### Подключение

Официальный драйвер `mongodb` 7.x (проверено с MongoDB 8.3). `MONGODB__URI` обязателен: при пустом значении `restaurants` и `restaurants-migrate` не стартуют; недоступная MongoDB останавливает старт после `MONGODB__TIMEOUT`. Connection string с учётными данными приходит только из Secret.

## Consequences

Данные ресторанов, меню и заказов кухни переживают рестарт процесса, и reconciler больше не удаляет связи живых ресторанов. Optimistic concurrency и уникальность, ранее эмулированные в памяти, обеспечиваются индексами и условными записями MongoDB.

Окружения обязаны предоставлять replica set; standalone MongoDB не поддерживается. Каждое изменение формата документа требует обновления validator, `schemaVersion` и, при необходимости, миграции документов. In-memory адаптеры остаются только для unit-тестов; поведение MongoDB-адаптеров проверяют интеграционные тесты (`test/mongodb-integration.test.ts`, `MONGODB_URI`).

Relay `restaurants-relay` (polling с lease, ADR 31), Protobuf-mappers, Kafka и событийные проекции этим решением не реализуются; Inbox и consumer `OrderPlaced` добавлены change `add-restaurants-order-placed-consumer`.
