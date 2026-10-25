# Restaurants

## Назначение

Контекст `restaurants` отвечает за работу ресторанов, предложение блюд и выполнение рестораном принятого заказа.

## Язык

TypeScript.

Стратегия unit, BDD acceptance, integration и system E2E проверок описана в [общей документации тестирования](../../docs/testing.md).

## Данные и состояния

Контекст владеет данными ресторанов, меню, доступностью блюд, состоянием принятия заказа рестораном и состояниями приготовления. Кухонные сценарии `Restaurant Order → Kitchen Queue → Preparation → Assembly → Ready for Pickup`, доменная модель приготовления, состояния и бизнес-сценарии детализированы в [Kitchen](../../docs/kitchen.md).

## Границы ответственности

`restaurants` принимает или отклоняет заказ со стороны ресторана и управляет приготовлением. Он не управляет общим жизненным циклом заказа, назначением курьеров, доставкой или платёжными операциями; результаты передаются в `orders` для отражения в состоянии заказа.

## Use Cases

Статус реализации: application handlers всех сценариев, кроме **Start / Complete Preparation Item** и **Reject After Acceptance**, реализованы change [`implement-restaurants-application-layer`](../../openspec/changes/implement-restaurants-application-layer/) поверх портов и in-memory адаптеров; transport, хранилище и брокер не подключены (см. [Статус реализации](#статус-реализации)). Новые handlers, контракты и adapters по-прежнему создаются только вместе с SDD change; пустые программные scaffolds не используются.

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Register Restaurant** | Оператор платформы или владелец ресторана | Создать ресторан как управляемый ресурс | Создан `Restaurant`; onboarding, verification и billing не входят в сценарий | Authoritative authorization в `restaurants` |
| **Update Restaurant** | Авторизованный управляющий ресторана | Изменить основные данные ресторана | Разрешённые данные `Restaurant` обновлены | Authoritative authorization в `restaurants` |
| **Open Restaurant** | Авторизованный управляющий ресторана | Разрешить приём новых заказов | `Restaurant` открыт для новых `Restaurant Order` | Может опубликовать бизнес-факт через Outbox |
| **Close Restaurant** | Авторизованный управляющий ресторана | Прекратить приём новых заказов | `Restaurant` закрыт для новых `Restaurant Order`; уже принятые заказы не отменяются автоматически | Может опубликовать бизнес-факт через Outbox |
| **Create Menu** | Авторизованный управляющий ресторана | Создать меню ресторана | Создано принадлежащее ресторану меню | Authoritative authorization в `restaurants` |
| **Add Menu Item** | Авторизованный управляющий ресторана | Добавить блюдо в меню | Создан новый `Menu Item` с исходными данными, ценой и доступностью | Authoritative authorization в `restaurants` |
| **Update Menu Item** | Авторизованный управляющий ресторана | Изменить название, описание, цену, доступность, options/modifiers или иные основные данные блюда | `Menu Item` обновлён в пределах инвариантов меню | Не изменяет snapshot уже оформленного `Order` |
| **Set Menu Item Availability** | Авторизованный управляющий ресторана | Быстро изменить возможность заказать блюдо | Доступность `Menu Item` изменена без редактирования остальных данных меню | Может опубликовать бизнес-факт через Outbox |
| **Receive Order Request** | Fulfillment process `orders` | Передать ресторану состав заказа для решения и приготовления | Создан идемпотентный локальный `Restaurant Order` в состоянии ожидания решения | Вызывается Temporal Activity через internal gateway; повтор запроса не создаёт дубликат |
| **Accept Order** | Авторизованный сотрудник ресторана | Подтвердить возможность приготовить весь `Restaurant Order` | `Restaurant Order` принят и поставлен в Kitchen Queue, результат доступен `orders` | Проверяет открытость и доступность позиций; публикует `RestaurantOrderAccepted` через Outbox |
| **Reject Order** | Авторизованный сотрудник ресторана | Отказаться от приготовления `Restaurant Order` | `Restaurant Order` отклонён, причина сохранена при наличии и доступна `orders` | Публикует `RestaurantOrderRejected` через Outbox |
| **Queue Restaurant Order** | Система `restaurants` | Поставить принятый заказ в Kitchen Queue | Заказ занял место в очереди по согласованной политике; приготовление не начато | В MVP — в одной UoW с Accept Order; локальный факт, не публикуется |
| **Start Preparation** | Авторизованный сотрудник кухни | Зафиксировать начало Preparation заказа из очереди | `Restaurant Order` готовится; повтор — по политике `AlreadyInState` | Публикует `PreparationStarted` через Outbox; slice `start-preparing-order` |
| **Start / Complete Preparation Item** | Авторизованный сотрудник станции | Отметить начало и завершение приготовления позиции | Позиция `PREPARING` / `READY` | Только при item-level model; локальные факты |
| **Mark Assembled** | Авторизованный сотрудник кухни | Подтвердить, что все required items готовы и собраны | `Restaurant Order` собран | Guard готовности позиций; локальный факт |
| **Mark Ready for Pickup** | Авторизованный сотрудник кухни | Сообщить о готовности заказа к Pickup | `Restaurant Order` готов к выдаче; допустимо только после Assembly | Публикует `RestaurantOrderReady` через Outbox; slice `mark-order-ready` |
| **Report Preparation Delay** | Авторизованный сотрудник кухни | Сообщить, что заказ будет готов позже | `expected_ready_at` изменён, State не меняется | Публикует `PreparationDelayed` через Outbox |
| **Report Preparation Failure** | Авторизованный сотрудник кухни | Сообщить, что начатое приготовление невозможно завершить | `Restaurant Order` в `FAILED` с причиной | Публикует `PreparationFailed` через Outbox |
| **Cancel Restaurant Order** | Fulfillment process `orders` | Запросить прекращение работы над `Restaurant Order` | Синхронный ответ по стадии: отменено либо отказ с причиной | Temporal Activity → internal gateway → `restaurants`; `RestaurantOrderCancelled` через Outbox при изменении State; не завершает общий `Order` |
| **Reject After Acceptance** | Авторизованный сотрудник ресторана | Отказаться от принятого заказа до начала Preparation | `Restaurant Order` отклонён | Отдельное продуктовое решение; в MVP не разрешено |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Restaurant** | Клиент или внутренний actor | Получить публичное представление ресторана | Возвращены доступные публичные данные ресторана без изменения состояния | Query model `restaurants` |
| **List Restaurants** | Клиент | Найти доступные для выбора рестораны | Возвращён список доступных ресторанов без изменения состояния | Query model `restaurants`; критерии доступности уточняются в SDD |
| **Get Menu** | Клиент или внутренний actor | Получить актуальное меню ресторана | Возвращены актуальные позиции, цены, options/modifiers и доступность | Query model `restaurants`; это не snapshot оформленного заказа |
| **Get Kitchen Queue** | Авторизованный сотрудник ресторана | Увидеть заказы, ожидающие Preparation | Заказы в очереди в рекомендованном порядке | Projection Kitchen Queue, не aggregate |
| **Get Restaurant Order** | Авторизованный сотрудник ресторана или внутренний actor | Получить состав, State и timeline заказа | Представление одного `Restaurant Order` | Read model `restaurants` |
| **List Active Preparations** | Авторизованный сотрудник ресторана | Увидеть заказы в работе кухни | Готовящиеся и собранные заказы с задержками | Read model kitchen board |
| **List Ready Orders** | Авторизованный сотрудник ресторана | Увидеть заказы, ожидающие передачи курьеру | Готовые заказы и информационные данные курьера | Read model kitchen board; данные курьера — projection событий `delivery` |

Команды кухни требуют permission над `restaurant`, выводимую из relation `staff`; роли cook/manager не моделируются. Сценарии, политики повтора и отмены по стадиям — в [Kitchen](../../docs/kitchen.md#use-cases).

## Структура слайсов

Слайсы `restaurants` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста): `slices/restaurant` владеет рестораном и правилами приёма заказов, `slices/menu` — меню и доступностью позиций, а `slices/kitchenorder` владеет жизненным циклом `Restaurant Order`. Имена `kitchenorder`, `start-preparing-order` и `mark-order-ready` — legacy; переименование в язык `RestaurantOrder` выполняется отдельным изменением вместе с кодом. Use cases размещаются в `slices/<slice>/applications/<usecase>/`, shared packages — в `pkg/`. Доменные модели не зависят от транспорта, хранилища или Awilix.

## gRPC-адаптер

`restaurants` владеет API `nomnom.restaurants.v1` и публикует его как `buf.build/nomnom/restaurants`. Исходные messages находятся в `infrastructure/grpc/nomnom/restaurants/v1/restaurants.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер использует сгенерированные TypeScript messages и service descriptors с Connect runtime и преобразует request messages в прикладные команды или запросы, а результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделями `Restaurant`, `Menu Item`, `Restaurant Order` или их handlers. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API:

| Ошибка `restaurants`                                               | gRPC code             | `ErrorInfo.reason`                  |
| ------------------------------------------------------------------ | --------------------- | ----------------------------------- |
| `Restaurant`, `Menu Item` или `Kitchen Order` не найден            | `NOT_FOUND`           | `RESOURCE_NOT_FOUND`                |
| Некорректный состав `Kitchen Order`                                | `INVALID_ARGUMENT`    | `INVALID_KITCHEN_ORDER`             |
| Ресторан закрыт, позиция недоступна или переход состояния запрещён | `FAILED_PRECONDITION` | `KITCHEN_ORDER_PRECONDITION_FAILED` |
| `Kitchen Order` уже существует                                     | `ALREADY_EXISTS`      | `KITCHEN_ORDER_ALREADY_EXISTS`      |
| Неожиданная внутренняя ошибка                                      | `INTERNAL`            | `INTERNAL_ERROR`                    |
| Внешняя зависимость недоступна                                     | `UNAVAILABLE`         | `DEPENDENCY_UNAVAILABLE`            |

Термин и reasons `KITCHEN_ORDER_*` в таблице — legacy-имена; до создания `.proto` они приводятся к `Restaurant Order` (`RESTAURANT_ORDER_*`). Отказ в Cancel Restaurant Order по стадии — бизнес-результат ответа, а не ошибка transport ([Kitchen](../../docs/kitchen.md#cancel-restaurant-order)).

Transport adapter маппит application errors в gRPC status codes по стабильному `code` ошибки: валидация — `INVALID_ARGUMENT`, не найдено — `NOT_FOUND`, конфликтующий повтор — `ALREADY_EXISTS`, запрещённый переход — `FAILED_PRECONDITION`, нет прав — `PERMISSION_DENIED`, authorization недоступна — `UNAVAILABLE`. Добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.restaurants.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md).

RPC, подключённые в `cmd/restaurants` (native gRPC/HTTP2 на бизнес-порту, общий lifecycle):

| RPC | Handler | Результат |
| --- | --- | --- |
| `GetRestaurant` | `get-restaurant` | Публичные данные и pickup `address`; без identity |
| `ReceiveOrderRequest` | `receive-order-request` | `restaurant_order_id`, `created = false` при повторе по `order_id` |
| `CancelRestaurantOrder` | `cancel-restaurant-order` | `cancelled = true` (включая повтор) или `cancelled = false` + `rejection_reason` |

Командные RPC ([FR-428](../../openspec/changes/add-fulfillment-command-rpcs/)) получают `Subject` через `SubjectResolver` (`infrastructure/grpc/subject.ts`). По умолчанию используется `RejectingSubjectResolver`, который отвечает `UNAUTHENTICATED`, пока verified internal identity не спроектирована; тесты подставляют resolver с `service` identity `orders-worker`. Отказ отмены по стадии — бизнес-ответ, а не gRPC-ошибка.

TypeScript-код генерируется в `infrastructure/grpc/gen` командой `npm run generate:proto` (`buf generate` по `infrastructure/grpc/buf.gen.yaml`, плагин `buf.build/bufbuild/es`).

## Минимальная доменная модель

Для учебной реализации достаточно трёх небольших агрегатов.

| Тип                                               | Минимальные поля                             | Назначение                                             |
| ------------------------------------------------- | -------------------------------------------- | ------------------------------------------------------ |
| `Restaurant` — aggregate root (slice `restaurant`) | `id`, `name`, `ownerId`, `isOpen`, `address` | Управляет данными, pickup-адресом и открытостью ресторана |
| `Menu` — aggregate root (slice `menu`)            | `id`, `restaurantId`, `items`                | Одно меню на ресторан; управляет позициями меню        |
| `MenuItem` — entity внутри `Menu`                 | `id`, `name`, `description`, `price`, `isAvailable` | Описывает блюдо, его текущую цену и доступность  |
| `Money` — value object `Menu`                     | `amount` (decimal-строка), `currency`        | Неотрицательная цена `MenuItem` без потери точности    |
| `RestaurantOrder` — aggregate root                      | `id`, `orderId`, `restaurantId`, `items`, `state`, timeline | Управляет принятием и приготовлением заказа рестораном |
| `PreparationItem` — entity внутри `RestaurantOrder`     | `menuItemId`, `quantity`, `options`          | Фиксирует состав принятого на приготовление заказа; `options` — непрозрачные id выбранных опций, модель modifiers не согласована |

`Kitchen Order` — роль `RestaurantOrder` в процессе кухни, а не отдельный aggregate; прежнее имя `KitchenOrder` — legacy. Timeline: `accepted_at`, `queued_at`, `preparation_started_at`, `expected_ready_at`, `ready_at`.

Концептуальные состояния `RestaurantOrder`: `RECEIVED`, `ACCEPTED`, `QUEUED`, `PREPARING`, `ASSEMBLED`, `READY`; terminal negative — `REJECTED`, `CANCELLED`, `FAILED`. Transition table — в [FSM: Kitchen / Preparation](../../docs/fsm.md#kitchen--preparation).

Инварианты:
- меню содержит уникальные `MenuItem.id`;
- `orderId` уникален: один `RestaurantOrder` на один `Order`;
- `menuItemId` с одинаковыми options не повторяется внутри одного `RestaurantOrder`;
- цена блюда неотрицательна;
- принять можно только полученный заказ, когда ресторан открыт и все позиции доступны;
- отклонить можно только полученный заказ; отказ после Acceptance — отдельное продуктовое решение;
- приготовление начинается только для заказа в Kitchen Queue;
- заказ не становится `READY`, пока не готовы все required items: готовность допустима только после явного Mark Assembled и не наступает автоматически от последней позиции;
- после terminal state (`READY`, `REJECTED`, `CANCELLED`, `FAILED`) состояние больше не изменяется.

Проверка открытости ресторана и доступности меню выполняется доменной моделью при принятии `RestaurantOrder`; application handler только загружает необходимые агрегаты и координирует вызов.

### Value objects

| VO         | Значение и инварианты                                                                                                                                                  |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Money`    | Точная десятичная цена на основе [`decimal.js`](https://github.com/MikeMcl/decimal.js) и код валюты; цена неотрицательна, арифметика разрешена только для одной валюты |
| `Quantity` | Положительное целое число                                                                                                                                              |

`Money` создаётся из строки или другого точного представления, но не из JavaScript `number`. Требуемые масштаб и режим округления явно задаются доменной операцией.

Идентификаторы и статусы пока остаются типизированными значениями и перечислениями, а не отдельными value objects.

### Domain policies и Specification Pattern

Переиспользуемые условия вне state остаются в domain model `restaurants`. Реализованы:

- `RestaurantAcceptingOrders` — часы работы, расписание исключений и текущая capacity;
- `MenuItemAvailable` — окно доступности, остаток/capacity и временные ограничения предложения.

Допустимость переходов по State (`RECEIVED → ACCEPTED/REJECTED`, `QUEUED → PREPARING`, `ASSEMBLED → READY` и другие) определяет только FSM `RestaurantOrder`. Кандидат на новую spec — `RestaurantOrderCancellable` для отмены во время Preparation. Application загружает `Restaurant` и `RestaurantOrder`, получает `now` и capacity snapshot, затем вызывает domain behavior/domain service; authorization не входит в spec. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## Хранилище: MongoDB

MongoDB предлагается как основное логическое хранилище `restaurants`. Документная модель соответствует данным Restaurant, меню и категорий, Menu Item, вариантов блюд, модификаторов и дополнительных опций. Это самостоятельное хранилище контекста, а не read store или проекция `orders`.

Граница документа определяется доменной моделью и согласованными сценариями чтения и изменения. Данные, которые обычно используются и изменяются совместно, можно хранить вложенными. Самостоятельные или растущие наборы данных можно хранить отдельно со ссылками. Один Restaurant со всеми меню и всей историей не становится автоматически одним документом: размер, частота изменений и паттерны доступа учитываются явно.

Для каждого вида документа определяются структура, validation rules и необходимые indexes. Изменение формата документов требует управляемой эволюции схемы. Если операция затрагивает несколько документов, требования к атомарности формулируются в сценарии до выбора механизма реализации; document model не должна скрывать междокументную согласованность.

Направление для кухни: aggregate `RestaurantOrder` — отдельный document; Kitchen Queue и read models kitchen board — projections; один документ на ресторан вместе с меню и заказами не используется ([Kitchen](../../docs/kitchen.md#temporal-и-persistence)). Конкретные collections, поля, document boundaries и indexes проектируются по согласованным SDD-сценариям. Реализации репозиториев, BSON-модели и преобразования находятся в `infrastructure`; доменная модель и прикладные контракты не зависят от BSON, MongoDB driver или ODM.

Решение имеет статус Proposed в [ADR 8](../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md). Версия MongoDB, driver, ODM, инструменты schema evolution и реализация репозиториев относятся к следующим шагам.

## Событийная интеграция

Consumers интеграционных событий `restaurants` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение документов контекста, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной MongoDB-транзакции. Уникальность `{ consumer, event_id }` обеспечивается unique index; конфликт индекса откатывает транзакцию и считается duplicate. Kafka offset подтверждается только после commit.

Inbox-адаптер находится в `infrastructure/`, а транзакция обработки сообщения получает message scope Awilix. По умолчанию изменения опираются на single-document atomicity; Unit of Work с MongoDB transaction используется только для multi-document изменений или state + Inbox/Outbox — см. [документацию Unit of Work](../../docs/unit-of-work.md#restaurants--mongodb) и [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md). Выбор Kafka-клиента, коллекция Inbox, индексы и топология MongoDB с поддержкой транзакций относятся к следующим шагам. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

## Dependency Injection

В `restaurants` используется Awilix. Регистрация зависимостей находится в composition root каждого исполняемого процесса. Handlers и прикладные компоненты получают зависимости явно через параметры конструкторов или фабрик; прикладной код не вызывает `container.resolve()`, а доменная модель не зависит от Awilix.

Время жизни определяется назначением зависимости: общие клиенты получают application scope, а ресурсы конкретной обработки — request или message scope. В тестах handlers и прикладные компоненты создаются с явно переданными подменами.

Решение зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Observability

Инструментирование TypeScript/Node.js транспорта, MongoDB, Kafka и внешних клиентов размещается в `infrastructure/`; providers, logger и profiler собираются в composition root. Поддержка profiler должна быть проверена для выбранной версии Node.js и целевой платформы. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализованы доменная модель и application-слой по change [`implement-restaurants-application-layer`](../../openspec/changes/implement-restaurants-application-layer/) (требования `FR-200`…`FR-243`, acceptance criteria `AC-200`…`AC-283`; решения и допущения — в [design.md](../../openspec/changes/implement-restaurants-application-layer/design.md)).

- **Domain:** aggregate `Restaurant`; aggregate `Menu` с entity `MenuItem` и VO `Money` на `decimal.js` (меню — отдельный aggregate slice `menu`, одно на ресторан); aggregate `RestaurantOrder` с чистой FSM в виде transition table, VO `Quantity`, domain service `evaluateOrderAcceptance` поверх `RestaurantAcceptingOrders` и `MenuItemAvailable`, Domain Events с `pullEvents()`; контракты repositories в `domains/`.
- **Application:** по одному handler на use case в `slices/<slice>/applications/<usecase>/`: Register/Update/Open/Close Restaurant, Get/List Restaurants; Create Menu, Add/Update Menu Item, Set Menu Item Availability, Get Menu; Receive Order Request, Accept Order (с Queue в одной UoW), Reject Order, Queue Restaurant Order, Start Preparation, Mark Assembled, Mark Ready for Pickup, Report Preparation Delay/Failure, Cancel Restaurant Order, Get Restaurant Order, Get Kitchen Queue, List Active Preparations, List Ready Orders.
- **Порты:** `UnitOfWork`, `Outbox`, `Clock`, `IdGenerator`, `Subject` и application errors в `pkg/application`; узкие authorization ports в `applications/access-control.ts` каждого слайса и `OwnerRelationshipWriter`; query/read-model ports рядом с queries; ports открытых решений — `AcceptanceConditionsProvider`, `ReadyTimeEstimator`, `KitchenQueueOrdering`. Политика повтора Start Preparation/Mark Ready и политика отмены во время Preparation — обязательные параметры handlers без значения по умолчанию.
- **Адаптеры:** только in-memory (`pkg/infrastructure/memory`, `slices/*/infrastructure/memory`) с откатом UoW, optimistic concurrency по `version` и уникальностью `orderId`; MongoDB, SpiceDB, Kafka/Outbox publisher и Integration Event mappers не реализованы. `GetRestaurant`, `ReceiveOrderRequest` и `CancelRestaurantOrder` подключены к `cmd/restaurants` через Connect/gRPC (командные RPC с `RejectingSubjectResolver` по умолчанию), остальные handlers к transport пока не подключены.
- **Не реализованы:** Start / Complete Preparation Item (item-level model не входит в MVP) и Reject After Acceptance (не разрешён до продуктового решения); courier projection в List Ready Orders; BDD `.feature`.

## Запуск

Требуется Node.js 22.6 или новее с поддержкой запуска TypeScript через type stripping.

```bash
npm start
```

## Проверка

```bash
npm test
npm run check
```

Автоформатирование:

```bash
npm run format
```

## Контейнер

Образ собирается из каталога `boundaries/restaurants`. Доступные targets: `deps`, `build`,
`production-deps` и `runtime`; первые две стадии содержат инструменты разработки,
`production-deps` — только production-зависимости, а итоговая стадия `runtime` —
скомпилированный JavaScript и Node.js runtime:

```bash
docker build --target runtime -t nomnom/restaurants:local boundaries/restaurants
docker run --rm -p 8082:8082 -p 9082:9082 \
  -e SERVER__PORT=8082 \
  nomnom/restaurants:local
```

Контейнер запускается пользователем `node`; `tini` пересылает сигналы приложению, а
конфигурация передаётся переменными окружения. `pkg/lifecycle` реализует
[runtime contract](../../docs/cloud-native-runtime.md): `/health/startup`, `/health/live` и
`/health/ready` на техническом порту `MANAGEMENT__PORT` (по умолчанию `9082`), а по
`SIGTERM`/`SIGINT` — not ready, `SHUTDOWN__READINESS_DELAY` (по умолчанию `5s`), остановку
приёма запросов и drain в пределах `SHUTDOWN__DRAIN_TIMEOUT` (по умолчанию `20s`).
Файл `.env` предназначен для локальной разработки и в образ не копируется. Текущий
HTTP-каркас не требует MongoDB или Kafka.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.
