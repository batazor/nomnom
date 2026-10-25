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

Статус реализации: application handlers всех сценариев, кроме **Start / Complete Preparation Item** и **Reject After Acceptance**, реализованы change [`implement-restaurants-application-layer`](../../openspec/changes/implement-restaurants-application-layer/) поверх портов; хранилище — MongoDB, брокер не подключён, к transport подключена часть handlers (см. [Статус реализации](#статус-реализации)). Новые handlers, контракты и adapters по-прежнему создаются только вместе с SDD change; пустые программные scaffolds не используются.

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Register Restaurant** | Оператор платформы или владелец ресторана | Создать ресторан как управляемый ресурс | Создан `Restaurant`; onboarding, verification и billing не входят в сценарий | Authoritative authorization в `restaurants` |
| **Update Restaurant** | Авторизованный управляющий ресторана | Изменить основные данные ресторана | Разрешённые данные `Restaurant` обновлены | Authoritative authorization в `restaurants` |
| **Open Restaurant** | Авторизованный управляющий ресторана | Разрешить приём новых заказов | `Restaurant` открыт для новых `Restaurant Order` | Может опубликовать бизнес-факт через Outbox |
| **Close Restaurant** | Авторизованный управляющий ресторана | Прекратить приём новых заказов | `Restaurant` закрыт для новых `Restaurant Order`; уже принятые заказы не отменяются автоматически | Может опубликовать бизнес-факт через Outbox |
| **Assign Staff** | Пользователь с `restaurant#manage` (владелец) | Дать пользователю доступ персонала | User id добавлен в `Restaurant.staff`; `TOUCH restaurant#staff` до сохранения | Повтор — no-op без записи; см. [Relationships](#relationships-и-reconciliation) |
| **Revoke Staff** | Пользователь с `restaurant#manage` (владелец) | Отозвать доступ персонала | User id удалён из `Restaurant.staff`; `DELETE restaurant#staff` до сохранения | Повтор — no-op без записи; см. [Relationships](#relationships-и-reconciliation) |
| **Create Menu** | Авторизованный управляющий ресторана | Создать меню ресторана | Создано принадлежащее ресторану меню | Authoritative authorization в `restaurants` |
| **Add Menu Item** | Авторизованный управляющий ресторана | Добавить блюдо в меню | Создан новый `Menu Item` с исходными данными, ценой и доступностью | Authoritative authorization в `restaurants` |
| **Update Menu Item** | Авторизованный управляющий ресторана | Изменить название, описание, цену, доступность, options/modifiers или иные основные данные блюда | `Menu Item` обновлён в пределах инвариантов меню | Не изменяет snapshot уже оформленного `Order` |
| **Set Menu Item Availability** | Авторизованный управляющий ресторана | Быстро изменить возможность заказать блюдо | Доступность `Menu Item` изменена без редактирования остальных данных меню | Может опубликовать бизнес-факт через Outbox |
| **Receive Order Request** | Fulfillment process `orders` | Передать ресторану состав заказа для решения и приготовления | Создан идемпотентный локальный `Restaurant Order` в состоянии ожидания решения | Вызывается Temporal Activity через internal gateway; повтор запроса не создаёт дубликат |
| **Accept Order** | Авторизованный сотрудник ресторана | Подтвердить возможность приготовить весь `Restaurant Order` | `Restaurant Order` принят и поставлен в Kitchen Queue, результат доступен `orders` | Проверяет открытость и доступность позиций; публикует `RestaurantOrderAccepted` через Outbox |
| **Reject Order** | Авторизованный сотрудник ресторана | Отказаться от приготовления `Restaurant Order` | `Restaurant Order` отклонён, причина сохранена при наличии и доступна `orders` | Публикует `RestaurantOrderRejected` через Outbox |
| **Queue Restaurant Order** | Система `restaurants` | Поставить принятый заказ в Kitchen Queue | Заказ занял место в очереди по согласованной политике; приготовление не начато | В MVP — в одной UoW с Accept Order; локальный факт, не публикуется |
| **Start Preparation** | Авторизованный сотрудник кухни | Зафиксировать начало Preparation заказа из очереди | `Restaurant Order` готовится; повтор — по политике `AlreadyInState` | Публикует `PreparationStarted` через Outbox; slice `start-preparation` |
| **Start / Complete Preparation Item** | Авторизованный сотрудник станции | Отметить начало и завершение приготовления позиции | Позиция `PREPARING` / `READY` | Только при item-level model; локальные факты |
| **Mark Assembled** | Авторизованный сотрудник кухни | Подтвердить, что все required items готовы и собраны | `Restaurant Order` собран | Guard готовности позиций; локальный факт |
| **Mark Ready for Pickup** | Авторизованный сотрудник кухни | Сообщить о готовности заказа к Pickup | `Restaurant Order` готов к выдаче; допустимо только после Assembly | Публикует `RestaurantOrderReady` через Outbox; slice `mark-ready-for-pickup` |
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

Слайсы `restaurants` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста): `slices/restaurant` владеет рестораном и правилами приёма заказов, `slices/menu` — меню и доступностью позиций, а `slices/restaurantorder` владеет жизненным циклом `Restaurant Order`, включая его роль `Kitchen Order` на кухне. Use cases размещаются в `slices/<slice>/applications/<usecase>/`, shared packages — в `pkg/`. Доменные модели не зависят от транспорта, хранилища или Awilix.

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
| `GetMenu` | `get-menu` | Текущее меню: название, описание, цена, доступность; без identity; нет меню — `NOT_FOUND` |
| `ReceiveOrderRequest` | `receive-order-request` | `restaurant_order_id`, `created = false` при повторе по `order_id` |
| `CancelRestaurantOrder` | `cancel-restaurant-order` | `cancelled = true` (включая повтор) или `cancelled = false` + `rejection_reason` |
| `AcceptRestaurantOrder` | `accept-order` | `RestaurantOrder` в `QUEUED` с `expected_ready_at`; публикует `RestaurantOrderAccepted` |
| `RejectRestaurantOrder` | `reject-order` | `RestaurantOrder` в `REJECTED`; публикует `RestaurantOrderRejected` |
| `StartPreparation` | `start-preparation` | `RestaurantOrder` в `PREPARING`; публикует `PreparationStarted`, повтор — без факта |
| `MarkRestaurantOrderAssembled` | `mark-assembled` | `RestaurantOrder` в `ASSEMBLED`; локальный факт, без Outbox |
| `MarkRestaurantOrderReady` | `mark-ready-for-pickup` | `RestaurantOrder` в `READY`; публикует `RestaurantOrderReady`, повтор — без факта |
| `ReportPreparationDelay` | `report-preparation-delay` | `RestaurantOrder` с новым `expected_ready_at`; публикует `PreparationDelayed` |
| `ReportPreparationFailure` | `report-preparation-failure` | `RestaurantOrder` в `FAILED`; публикует `PreparationFailed` |
| `RegisterRestaurant` | `register-restaurant` | `Restaurant` (с `address`) и `version`; требует `platform#register_restaurant`, пишет `restaurant#owner`; не идемпотентен |
| `AssignStaff` | `assign-staff` | `restaurant_id`, отсортированный `staff`, `changed = false` при повторе; требует `restaurant#manage`, пишет `restaurant#staff` |
| `RevokeStaff` | `revoke-staff` | `restaurant_id`, отсортированный `staff`, `changed = false` при повторе; требует `restaurant#manage`, удаляет `restaurant#staff` |

Командные RPC ([FR-428](../../openspec/changes/add-fulfillment-command-rpcs/), [FR-446](../../openspec/changes/add-restaurant-admin-rpcs/)) получают `Subject` через `SubjectResolver` (`infrastructure/grpc/subject.ts`). Без конфигурации identity используется `RejectingSubjectResolver`, который отвечает `UNAUTHENTICATED`; с конфигурацией — `ServiceTokenSubjectResolver` (см. [Service identity](#service-identity) и [User identity](#user-identity)). Отказ отмены по стадии — бизнес-ответ, а не gRPC-ошибка.

Команды кухни ([FR-480…FR-482](../../openspec/changes/add-kitchen-command-rpcs/)) адресуют заказ парой `restaurant_id` + `restaurant_order_id` и требуют `restaurant#operate_kitchen`; заказ другого ресторана — `NOT_FOUND`, запрещённый переход и повтор (кроме `StartPreparation`/`MarkRestaurantOrderReady`, которые по умолчанию отвечают текущим заказом без факта) — `FAILED_PRECONDITION`. Пока модель расписания и capacity не согласована, Accept Order собран с `UnconstrainedAcceptanceConditions` (решают `Restaurant.isOpen` и `MenuItem.isAvailable`) и `FixedPreparationTimeEstimator`.

Открытые продуктовые решения команд кухни handlers получают обязательными параметрами без значения по умолчанию; значения задаёт конфигурация основного процесса (`pkg/config`, секция `kitchen`):

| Переменная | Назначение | По умолчанию |
| --- | --- | --- |
| `KITCHEN__PREPARATION_TIME` | Начальный `expected_ready_at` после Acceptance (`FixedPreparationTimeEstimator`); позже сдвигается Report Preparation Delay | `20m` |
| `KITCHEN__REPEAT_POLICY` | Повтор `StartPreparation`/`MarkRestaurantOrderReady`: `succeed-without-event` — текущий заказ без факта, `reject` — `FAILED_PRECONDITION` | `succeed-without-event` |
| `KITCHEN__CANCELLATION_DURING_PREPARATION` | `CancelRestaurantOrder` во время Preparation: `reject` — отказ по стадии, Preparation продолжается; `allow` — отмена | `reject` |

### Service identity

Вызывающий сервис доказывает `service` identity projected ServiceAccount token ([ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md), [FR-431, FR-432](../../openspec/changes/add-service-identity/specs/service-identity/spec.md)). `ServiceTokenSubjectResolver` (`jose`) проверяет:

- metadata `authorization: Bearer <jwt>`;
- header: `alg` только `RS256` или `ES256`, `kid` обязателен и есть в JWKS (`alg: none`, HMAC и неизвестный `kid` отклоняются);
- подпись по локальному JWKS из `IDENTITY__JWKS_FILE` — Kubernetes API при проверке не используется;
- `iss` равен `IDENTITY__ISSUER`, `aud` (строка или массив) содержит `IDENTITY__AUDIENCE`;
- `exp`/`nbf` с допуском 30 s;
- `sub` `system:serviceaccount:<namespace>:<name>` есть в allowlist `IDENTITY__SERVICE_ACCOUNTS` → `Subject{type: "service", id: <service-name>}`.

Любой отказ — `UNAUTHENTICATED` с общим сообщением; причина пишется только в log. Что разрешено identity, решают authorization ports use case, а не resolver.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `IDENTITY__ISSUER` | Issuer токенов кластера, например `https://kubernetes.default.svc.cluster.local` | пусто |
| `IDENTITY__AUDIENCE` | Audience этого контекста, в кластере `nomnom.restaurants` | пусто |
| `IDENTITY__JWKS_FILE` | Путь к JWKS кластера (`/openid/v1/jwks`); в chart — `/etc/nomnom/identity/jwks.json` | пусто |
| `IDENTITY__SERVICE_ACCOUNTS` | Allowlist через запятую: `namespace/serviceaccount=service-name`, например `nomnom/orders-worker=orders-worker` | пусто |

Если `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` или `IDENTITY__JWKS_FILE` пусты, сервер стартует с `RejectingSubjectResolver` и пишет предупреждение (fail closed). Нечитаемый или невалидный JWKS, пустой allowlist при заданном issuer и неверный формат allowlist — ошибка startup. Тесты (`test/identity.test.ts`) проходят по общим векторам `tests/identity/fixtures` с audience векторов `nomnom.payments`.

### User identity

Пользователя передаёт только BFF: user assertion в metadata `x-nomnom-user-assertion` ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md), [FR-471–FR-473](../../openspec/changes/add-user-identity-assertions/specs/user-identity/spec.md)). `ServiceTokenSubjectResolver` проверяет по порядку:

1. service token, как выше; без него assertion не рассматривается;
2. assertion нет → `Subject{type: "service"}`, как раньше;
3. assertion повторён или пуст, user-настройки не заданы или service не входит в `IDENTITY__USER_DELEGATES` → `UNAUTHENTICATED`;
4. assertion: `alg` только `ES256`, `kid` обязателен и есть в JWKS BFF, `iss` равен `IDENTITY__USER_ISSUER`, `aud` содержит `IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s, `iat` обязателен и `exp - iat ≤ 60 s`, `sub` непуст и без пробелов по краям, `jti` непуст → `Subject{type: "user", id: <sub>, delegate: <service>}`.

`delegate` нужен только для log и audit. Принятие и отказ пишутся в log с delegate и `jti`, сами токены не пишутся; `jti` не дедуплицируется.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `IDENTITY__USER_ISSUER` | Ожидаемый `iss` assertion, `nomnom-bff` | пусто |
| `IDENTITY__USER_JWKS_FILE` | JWKS BFF (EC P-256); в chart — `/etc/nomnom/user-identity/jwks.json` из `identity.userJwksConfigMap` | пусто |
| `IDENTITY__USER_DELEGATES` | Services через запятую, которым разрешено передавать пользователя, например `bff` | пусто |

Assertions принимаются, только если заданы issuer и JWKS и список делегатов не пуст; иначе любой вызов с assertion — `UNAUTHENTICATED`, вызовы без него работают как раньше. Нечитаемый JWKS BFF или ключ не EC P-256 — ошибка startup. Тесты (`test/user-identity.test.ts`) проходят по `tests/identity/fixtures/user-vectors.json` с часами, зафиксированными на `now` векторов.

### Authorization: SpiceDB

Authorization ports слайсов (`applications/access-control.ts`) реализует `SpiceDbAccessControl` (`infrastructure/spicedb/access-control.ts`, `@authzed/authzed-node`) по нормативной таблице [design.md](../../openspec/changes/add-spicedb-authorization/design.md) и schema [`authorization/schema.zed`](../../authorization/schema.zed) ([FR-435](../../openspec/changes/add-spicedb-authorization/specs/spicedb-authorization/spec.md)):

| Порт | SpiceDB check |
| --- | --- |
| `canRegisterRestaurant` | `platform:nomnom#register_restaurant` |
| `canManageRestaurant(id)` | `restaurant:<id>#manage` |
| `canManageMenu(id)` | `restaurant:<id>#manage_menu` |
| `canOperateKitchen(id)` | `restaurant:<id>#operate_kitchen` |
| `canCoordinateFulfillment(id)` | `platform:nomnom#coordinate_fulfillment` (platform capability, `id` не участвует) |
| `canViewRestaurantOrders(id)` | `restaurant:<id>#view_orders` |

Subject `user` → `user:<id>`, `service` → `service:<id>`. Consistency — см. [Authorization revisions](#authorization-revisions-zedtokens). Разрешает только `PERMISSIONSHIP_HAS_PERMISSION`; `NO_PERMISSION` и conditional — отказ (`PERMISSION_DENIED`). Ошибка, недоступность или превышение `SPICEDB__TIMEOUT` — `AuthorizationUnavailableError` (fail closed, gRPC `UNAVAILABLE`, AC-466). Grants `service` identities (`platform:nomnom#fulfillment@service:orders-worker`) принадлежат развёртыванию (`authorization/bootstrap.yaml`), а не коду; связи `restaurant#owner`/`staff` пишут use cases ресторана (см. [Relationships и reconciliation](#relationships-и-reconciliation)).

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `SPICEDB__ENDPOINT` | gRPC endpoint `host:port`, например `spicedb.nomnom-authz.svc.cluster.local:50051` | пусто |
| `SPICEDB__PRESHARED_KEY` | Preshared key (bearer); в кластере — из Secret | пусто |
| `SPICEDB__INSECURE` | `true` — plaintext gRPC без TLS, только kind и локально | `false` |
| `SPICEDB__TIMEOUT` | Deadline одной проверки (`500ms`, `2s`, `1m`) | `2s` |

Пустой `SPICEDB__ENDPOINT` — ошибка startup: процесс без SpiceDB не стартует. Endpoint без preshared key, неверный `SPICEDB__INSECURE` или `SPICEDB__TIMEOUT` — ошибка startup.

Unit-тесты маппинга и fail closed — `test/spicedb-access-control.test.ts` (fake клиент). Integration-тесты `test/spicedb-integration.test.ts` пропускаются без `SPICEDB_ENDPOINT`; против SpiceDB с `authorization/bootstrap.yaml` (ключ `devkey`, plaintext):

```bash
SPICEDB_ENDPOINT=localhost:50051 node --test test/spicedb-integration.test.ts
```

### Authorization revisions (ZedTokens)

Change [`add-authorization-revisions`](../../openspec/changes/add-authorization-revisions/) (FR-447, FR-448). `touchOwner`, `touchStaff` и `deleteStaff` возвращают ZedToken записи (`writtenAt.token`); Register Restaurant, Assign Staff и Revoke Staff сохраняют его в техническом поле snapshot `authorizationRevision` тем же save (повтор без записи revision не меняет; старые snapshots загружаются без него). Reconciler при `TOUCH` missing связи тоже сохраняет revision в ресторан; удаление orphan revision не меняет. `SpiceDbAccessControl` читает revision через порт `RevisionReader` (в `cmd/restaurants` — тот же `InMemoryRestaurantRepository`): проверки `restaurant:<id>` идут с `at_least_as_fresh` по сохранённому токену, без revision — `minimize_latency`; проверки `platform:nomnom` всегда `minimize_latency`. Ошибка чтения revision — `AuthorizationUnavailableError` (fail closed). Integration-тесты проверяют адаптер без принудительной full consistency: персонал получает `operate_kitchen` сразу после Assign Staff и теряет сразу после Revoke Staff (AC-482), владелец получает `manage` сразу после регистрации.

### Relationships и reconciliation

Change [`add-restaurant-delivery-relationships`](../../openspec/changes/add-restaurant-delivery-relationships/) (FR-443, FR-445). Источник истины — `Restaurant` в хранилище `restaurants`; SpiceDB — производный граф.

| Relation | Источник истины | Запись |
| --- | --- | --- |
| `restaurant:<id>#owner@user:<ownerId>` | `Restaurant.ownerId` | Register Restaurant: `TOUCH` до сохранения |
| `restaurant:<id>#staff@user:<userId>` | `Restaurant.staff` (множество user id) | Assign Staff: `TOUCH`, Revoke Staff: `DELETE` — до сохранения |

Writer — `SpiceDbRelationshipWriter` (`infrastructure/spicedb/relationships.ts`), реализует порты `OwnerRelationshipWriter` и `StaffRelationshipWriter` слайса `restaurant`. Запись синхронная, с deadline `SPICEDB__TIMEOUT`; ошибка или timeout — `AuthorizationUnavailableError`, команда отменяется без сохранения (fail closed). Assign/Revoke Staff требуют `restaurant#manage`; повтор — no-op без записи. `cmd/restaurants` собирает Register Restaurant, Assign Staff и Revoke Staff с `SpiceDbAccessControl` и этим writer над одним SpiceDB-клиентом и той же MongoDB-коллекцией `restaurants`, что читает reconciler.

`RelationshipReconciler` (`slices/restaurant/infrastructure/reconcile`) повторяет алгоритм `orders`: graph → business — постраничный `ReadRelationships` (`fully_consistent`) по `restaurant#owner` и `restaurant#staff` с проверкой по хранилищу; business → graph — по всем ресторанам. Orphan или stale связь удаляется только если она была сиротой на двух проходах с интервалом не меньше grace (кандидаты в памяти процесса); missing связь восстанавливается `TOUCH` сразу; id, не подходящие под SpiceDB object id `^[a-zA-Z0-9/_|\-=+]+$`, пропускаются и считаются `unrepairable`. Каждый проход пишет в log `checked`, `orphans_pending`, `orphans_deleted`, `missing_repaired`, `unrepairable`.

Reconciler — фоновый цикл (`PeriodicLoop`, `pkg/lifecycle/periodic.ts`) внутри основного процесса: первый проход через `min(10s, RECONCILER__INTERVAL)` после старта, следующие — через интервал после окончания предыдущего; ошибка прохода пишется в log, цикл продолжается. При shutdown цикл останавливается (текущий проход прерывается между вызовами SpiceDB) до drain gRPC.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `RECONCILER__INTERVAL` | Пауза между проходами (`30s`, `5m`) | `5m` |
| `RECONCILER__GRACE` | Минимальное время, которое связь остаётся сиротой до удаления | `10m` |
| `RECONCILER__PAGE_SIZE` | Размер страницы `ReadRelationships` и хранилища, 1…1000 | `500` |

Unit-тесты: `test/staff-applications.test.ts`, `test/spicedb-relationships.test.ts`, `test/reconcile.test.ts` (fake клиент), `test/periodic.test.ts`. Integration-тесты `test/spicedb-relationships-integration.test.ts` (AC-476, AC-478) используют уникальный префикс id и ограничивают reconciler им, чтобы не трогать чужие связи общего SpiceDB:

```bash
SPICEDB_ENDPOINT=localhost:50051 node --test test/spicedb-integration.test.ts test/spicedb-relationships-integration.test.ts
```

TypeScript-код генерируется в `infrastructure/grpc/gen` командой `npm run generate:proto` (`buf generate` по `infrastructure/grpc/buf.gen.yaml`, плагин `buf.build/bufbuild/es`).

## Минимальная доменная модель

Для учебной реализации достаточно трёх небольших агрегатов.

| Тип                                               | Минимальные поля                             | Назначение                                             |
| ------------------------------------------------- | -------------------------------------------- | ------------------------------------------------------ |
| `Restaurant` — aggregate root (slice `restaurant`) | `id`, `name`, `ownerId`, `isOpen`, `address`, `staff` | Управляет данными, pickup-адресом, открытостью ресторана и множеством персонала |
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

Решение имеет статус Proposed в [ADR 8](../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md); коллекции, границы документов, validators, индексы, эволюция схемы и атомарность конкретизированы в локальном [ADR 2](docs/adr/0002-mongodb-collections-and-document-boundaries.md).

Реализация: официальный драйвер `mongodb` без ODM. Общие части — `pkg/infrastructure/mongodb` (`client.ts`, `unit-of-work.ts` — UoW как multi-document транзакция, `outbox.ts` — коллекции `outbox`, `outbox_counters`, `outbox_leases` по [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) с внедряемым mapper Domain Event → Protobuf (по умолчанию ничего не пишет; `cmd/restaurants` подключает mapper Kitchen Order), `outbox-relay-store.ts` — lease и выборка relay, `versioned-collection.ts` — optimistic concurrency по `version`, `migrate.ts`), адаптеры слайсов — `slices/<slice>/infrastructure/mongodb`, реестр коллекций и миграций — `infrastructure/mongodb/collections.ts`. Коллекции: `restaurants`, `menus`, `restaurant_orders`, `outbox`, `outbox_counters`, `outbox_leases`, `schema_migrations`. Транзакции требуют replica set.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `MONGODB__URI` | connection string replica set; обязателен — без него `restaurants` и `restaurants-migrate` не стартуют | — |
| `MONGODB__DATABASE` | имя базы | `restaurants` |
| `MONGODB__TIMEOUT` | server selection timeout; недоступная MongoDB останавливает старт | `5s` |

Коллекции, validators и индексы создаёт `npm run migrate` (`cmd/restaurants-migrate`, идемпотентно); основной процесс схему не меняет.

## Событийная интеграция

Consumers интеграционных событий `restaurants` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение документов контекста, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной MongoDB-транзакции. Уникальность `{ consumer, event_id }` обеспечивается unique index; конфликт индекса откатывает транзакцию и считается duplicate. Kafka offset подтверждается только после commit.

### Публикация: Outbox и restaurants-relay

Публикация Integration Events реализована по [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) и [ADR 13](../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md):

- **Mapper** `slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts` — явный allowlist Domain Event → Protobuf `nomnom/restaurants/v1/events.proto`, topic `restaurants.v1.restaurant-order-events`: `RestaurantOrderAccepted`, `RestaurantOrderRejected` (`reason` пустой, если не указан), `PreparationStarted`, `PreparationDelayed`, `RestaurantOrderFailed` → `PreparationFailed`, `RestaurantOrderReady`, `RestaurantOrderCancelled` (`reason`, `preparation_started`). Остальные события `RestaurantOrder` (`Received`, `Queued`, `Assembled`) и события `Restaurant`/`Menu` контракта не имеют и в Outbox не пишутся. Mapper подключён к `MongoOutbox` в `cmd/restaurants`; `traceparent` берётся из активного span, если он есть.
- **`event_id`** — UUIDv5 (namespace `8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13`) от `"{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}"`, `event_type` — полное имя Protobuf-сообщения.
- **`cmd/restaurants-relay`** (`npm run relay`, в образе — `node dist/cmd/restaurants-relay/main.js`) — polling relay с runtime contract `pkg/lifecycle` (только технический порт). Единственный активный экземпляр — lease-документ `restaurants-relay` в `outbox_leases` (владелец `hostname/pid`, срок `OUTBOX__LEASE_TTL`, продлевается каждый цикл, освобождается при shutdown). Цикл читает до `OUTBOX__BATCH_SIZE` записей `{ publishedAt: null }` по `seq`, публикует их idempotent producer'ом (`acks=all`, key — `aggregate_id`) и выставляет `publishedAt` только после ack. Первая ошибка останавливает пачку (head-of-line): у записи растут `attempts` и `lastError`, следующий цикл начинает с неё. Опубликованные записи старше `OUTBOX__RETENTION` удаляются (не чаще раза в минуту).
- **Kafka headers** совпадают с `orders` (`pkg/messaging/envelope.go`): `event_id`, `event_type`, `occurred_at` (RFC 3339 Nano, UTC), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent` (если есть), а также `_watermill_message_uuid` = `event_id`.
- **Метрики** (OpenTelemetry, OTLP gRPC только при заданном `OTEL_EXPORTER_OTLP_ENDPOINT`): `nomnom_outbox_pending_messages`, `nomnom_outbox_oldest_pending_age_seconds` (labels `stream`, `boundary`; только у держателя lease), `nomnom_outbox_publish_retries_total` (`stream`, `event_type`, `boundary`, `error_class` ∈ `timeout|broker|unknown`), `nomnom_outbox_publish_duration_seconds` и `nomnom_outbox_publish_lag_seconds` (`occurred_at` → ack; `stream`, `event_type`, `boundary`).

| Переменная | Назначение | По умолчанию |
| --- | --- | --- |
| `KAFKA__BROKERS` | `host:port` через запятую; обязателен для `restaurants-relay`, основной процесс к Kafka не подключается | — |
| `KAFKA__CLIENT_ID` | client id producer'а | `restaurants-relay` |
| `OUTBOX__POLL_INTERVAL` | пауза между циклами | `1s` |
| `OUTBOX__BATCH_SIZE` | записей за цикл | `100` |
| `OUTBOX__LEASE_TTL` | срок lease; должен быть больше интервала опроса | `15s` |
| `OUTBOX__RETENTION` | срок хранения опубликованных записей (`ms`, `s`, `m`, `h`, `d`) | `7d` |

### Потребление: Inbox

Inbox-адаптер находится в `infrastructure/`, а транзакция обработки сообщения получает message scope Awilix. По умолчанию изменения опираются на single-document atomicity; Unit of Work с MongoDB transaction используется только для multi-document изменений или state + Inbox/Outbox — см. [документацию Unit of Work](../../docs/unit-of-work.md#restaurants--mongodb) и [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md). Kafka-клиент — `@confluentinc/kafka-javascript` (ADR 31); consumer, коллекция Inbox и её индексы относятся к следующим шагам. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

## Dependency Injection

В `restaurants` используется Awilix. Регистрация зависимостей находится в composition root каждого исполняемого процесса. Handlers и прикладные компоненты получают зависимости явно через параметры конструкторов или фабрик; прикладной код не вызывает `container.resolve()`, а доменная модель не зависит от Awilix.

Время жизни определяется назначением зависимости: общие клиенты получают application scope, а ресурсы конкретной обработки — request или message scope. В тестах handlers и прикладные компоненты создаются с явно переданными подменами.

Структура:

- `slices/<slice>/di/registrations.ts` — регистрации слайса: MongoDB adapters его ports, MVP adapters и handlers. Модуль объявляет `<Slice>Dependencies` (что берёт у процесса) и `<Slice>Cradle` (что предоставляет); тип `Registrations<Cradle>` из `pkg/di/container.ts` проверяет при компиляции, что зарегистрирован каждый ключ с нужным типом, а сигнатура `register<Slice>` — что процесс даёт все зависимости.
- `di/restaurants.ts`, `di/relay.ts`, `di/migrate.ts` — контейнеры процессов: общие клиенты (MongoDB Unit of Work, Outbox, SpiceDB client, access control, relationship writer), модули слайсов, gRPC server и фоновые циклы. Фабрики регистраций — единственное место, где разбирается cradle; handlers получают обычный объект зависимостей.
- `cmd/*/main.ts` — точки входа: читают конфигурацию, проверяют обязательную инфраструктуру, асинхронно открывают ресурсы (MongoDB, Kafka producer, subject resolver, telemetry), передают их контейнеру как values, запускают `Lifecycle` с `cradle.worker` и освобождают ресурсы в фиксированном порядке: `container.dispose()` (disposer закрывает SpiceDB client), затем Kafka producer, MongoDB и telemetry.

Контейнер создаётся в PROXY injection mode со `strict: true`: регистрация с большим временем жизни не может зависеть от более короткоживущей. Сейчас все регистрации — application scope (singleton): handlers без состояния поверх общих клиентов, а ресурс одной операции — MongoDB session транзакции — принадлежит `MongoUnitOfWork.run` и живёт ровно один use case. Request/message scope (`container.createScope()`) появится вместе с первым ресурсом, принадлежащим одной обработке, — транзакцией Inbox-сообщения. `test/composition.test.ts` проверяет, что каждый контейнер разрешает все регистрации.

Решение зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Observability

Инструментирование TypeScript/Node.js транспорта, MongoDB, Kafka и внешних клиентов размещается в `infrastructure/`; providers, logger и profiler собираются в composition root. Поддержка profiler должна быть проверена для выбранной версии Node.js и целевой платформы. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализованы доменная модель и application-слой по change [`implement-restaurants-application-layer`](../../openspec/changes/implement-restaurants-application-layer/) (требования `FR-200`…`FR-243`, acceptance criteria `AC-200`…`AC-283`; решения и допущения — в [design.md](../../openspec/changes/implement-restaurants-application-layer/design.md)).

- **Domain:** aggregate `Restaurant`; aggregate `Menu` с entity `MenuItem` и VO `Money` на `decimal.js` (меню — отдельный aggregate slice `menu`, одно на ресторан); aggregate `RestaurantOrder` с чистой FSM в виде transition table, VO `Quantity`, domain service `evaluateOrderAcceptance` поверх `RestaurantAcceptingOrders` и `MenuItemAvailable`, Domain Events с `pullEvents()`; контракты repositories в `domains/`.
- **Application:** по одному handler на use case в `slices/<slice>/applications/<usecase>/`: Register/Update/Open/Close Restaurant, Assign/Revoke Staff, Get/List Restaurants; Create Menu, Add/Update Menu Item, Set Menu Item Availability, Get Menu; Receive Order Request, Accept Order (с Queue в одной UoW), Reject Order, Queue Restaurant Order, Start Preparation, Mark Assembled, Mark Ready for Pickup, Report Preparation Delay/Failure, Cancel Restaurant Order, Get Restaurant Order, Get Kitchen Queue, List Active Preparations, List Ready Orders.
- **Порты:** `UnitOfWork`, `Outbox`, `Clock`, `IdGenerator`, `Subject` и application errors в `pkg/application`; узкие authorization ports в `applications/access-control.ts` каждого слайса и `OwnerRelationshipWriter`; query/read-model ports рядом с queries; ports открытых решений — `AcceptanceConditionsProvider`, `ReadyTimeEstimator`, `KitchenQueueOrdering`. Политика повтора Start Preparation/Mark Ready и политика отмены во время Preparation — обязательные параметры handlers без значения по умолчанию; значения задаёт конфигурация (`KITCHEN__*`, см. [gRPC-адаптер](#grpc-адаптер)).
- **Адаптеры:** MongoDB (`pkg/infrastructure/mongodb`, `slices/*/infrastructure/mongodb`, см. [Хранилище: MongoDB](#хранилище-mongodb)) — UoW как транзакция, Outbox-коллекция, optimistic concurrency по `version`, уникальность `orderId` и меню на ресторан индексами; in-memory адаптеры (`pkg/infrastructure/memory`, `slices/*/infrastructure/memory`) — только для unit-тестов; SpiceDB adapter authorization ports подключается при заданном `SPICEDB__ENDPOINT` (см. [Authorization: SpiceDB](#authorization-spicedb)); writer relationships `restaurant#owner`/`staff` и фоновый reconciler (см. [Relationships и reconciliation](#relationships-и-reconciliation)); mapper Integration Events Kitchen Order и polling relay `cmd/restaurants-relay` в Kafka (см. [Публикация](#публикация-outbox-и-restaurants-relay)); Kafka consumers и Inbox не реализованы. Через transport в Outbox пишут `CancelRestaurantOrder` и команды кухни. `GetRestaurant`, `GetMenu`, `ReceiveOrderRequest`, `CancelRestaurantOrder`, команды кухни (`AcceptRestaurantOrder`, `RejectRestaurantOrder`, `StartPreparation`, `MarkRestaurantOrderAssembled`, `MarkRestaurantOrderReady`, `ReportPreparationDelay`, `ReportPreparationFailure`, change [`add-kitchen-command-rpcs`](../../openspec/changes/add-kitchen-command-rpcs/)), `RegisterRestaurant`, `AssignStaff` и `RevokeStaff` подключены к `cmd/restaurants` через Connect/gRPC (командные RPC с `ServiceTokenSubjectResolver` при заданной конфигурации identity, иначе с `RejectingSubjectResolver`), к transport пока не подключены команды меню, Update/Open/Close Restaurant, List Restaurants и queries kitchen board.
- **Не реализованы:** Start / Complete Preparation Item (item-level model не входит в MVP) и Reject After Acceptance (не разрешён до продуктового решения); courier projection в List Ready Orders; BDD `.feature`.

## Запуск

Требуется Node.js 22.6 или новее с поддержкой запуска TypeScript через type stripping.

Нужны MongoDB replica set и SpiceDB. Локальный single-node replica set:

```bash
docker run -d --name restaurants-mongo -p 27017:27017 mongo:8 --replSet rs0
docker exec restaurants-mongo mongosh --quiet --eval 'rs.initiate()'
export MONGODB__URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true'
npm run migrate
npm start
# Публикация Outbox (отдельный процесс, нужен Kafka):
KAFKA__BROKERS=localhost:9092 npm run relay
```

## Проверка

```bash
npm test
npm run check
```

Интеграционные тесты MongoDB (`test/mongodb-integration.test.ts`, команды кухни через gRPC и Outbox — `test/grpc-kitchen-integration.test.ts`, контейнеры migrate и основного процесса — `test/composition.test.ts`) пропускаются без `MONGODB_URI`, тест Outbox → relay → Kafka (`test/outbox-relay-integration.test.ts`) — без `MONGODB_URI` и `KAFKA_BROKERS`; каждый запуск создаёт и удаляет временную базу:

```bash
docker run -d --name restaurants-kafka -p 9092:9092 apache/kafka:4.1.0
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  KAFKA_BROKERS=localhost:9092 npm test
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
Файл `.env` предназначен для локальной разработки и в образ не копируется. Основной процесс
требует MongoDB и SpiceDB, `restaurants-relay` — MongoDB и Kafka.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.

Вызывающий может передать ZedToken своей записи в metadata `authorization-revision`: проверки `platform:nomnom` и ресторанов без сохранённой revision в этом запросе идут `at_least_as_fresh` по нему (например, оператор сразу после выдачи `platform#operator`). Права token не расширяет.
