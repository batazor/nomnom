# Delivery

## Назначение

Контекст `delivery` отвечает за назначение курьера и выполнение доставки заказа.

## Язык

Rust.

Стратегия unit, BDD acceptance, integration и system E2E проверок описана в [общей документации тестирования](../../docs/testing.md).

## Данные и состояния

Контекст владеет доступностью курьеров, предложениями доставки, назначениями курьеров и состояниями выполнения доставки.

## Границы ответственности

`delivery` управляет поиском и назначением курьера, а также ходом доставки. Он не владеет приготовлением, общим жизненным циклом заказа или платёжными операциями; результаты назначения и доставки передаются в `orders` для отражения в состоянии заказа.

## Use Cases

Статус реализации: все перечисленные Commands и Queries реализованы как application handlers (`Input` / `Output` / `Handler::execute`) по SDD change [`implement-delivery-application-layer`](../../openspec/changes/implement-delivery-application-layer/) ; хранилище — PostgreSQL (см. «Хранилище (PostgreSQL)»), Integration Events публикует `delivery-relay` (см. «Событийная интеграция»); Kafka consumers ещё не подключены. Требования — `FR-300`…`FR-318`, acceptance criteria — `AC-300`…`AC-343`. Командные use cases процесса исполнения заказа (Cancel Delivery, Replan Delivery) и их gRPC-методы добавлены по change [`add-fulfillment-command-rpcs`](../../openspec/changes/add-fulfillment-command-rpcs/) (`FR-429`, `AC-455`, `AC-456`).

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Register Courier** | Оператор платформы | Создать курьера в системе доставки | Создан `Courier` с исходным состоянием доступности | Authoritative authorization в `delivery` |
| **Set Courier Availability** | Курьер или авторизованный диспетчер | Изменить доступность курьера для новых назначений | `Courier` отмечен available или unavailable; позиция курьера не изменена | Не совмещается с location update |
| **Update Courier Location** | Курьерское приложение | Обновить последнюю известную позицию курьера | Сохранена актуальная доступная `delivery` позиция курьера | Частота, точность и retention определяются отдельно |
| **Create Delivery** | Fulfillment process `orders` | Запросить доставку оформленного заказа | Создана идемпотентная `Delivery` с необходимыми pickup/dropoff references и данными контекста | Вызывается Temporal Activity через API `delivery` |
| **Find Courier** | Процесс назначения `delivery` | Подобрать подходящего доступного курьера | Выбран кандидат или зафиксировано отсутствие кандидата; алгоритм остаётся внутри `delivery` | Может запускаться запросом orchestration, но выбор выполняет `delivery` |
| **Offer Delivery** | Процесс назначения `delivery` | Предложить конкретному курьеру принять доставку | Создано актуальное ограниченное по времени `Delivery Offer`; `Courier Assignment` ещё не создан | Учитывает результат Find Courier |
| **Accept Delivery Offer** | Курьер | Принять актуальное предложение | Ровно один актуальный offer успешно превращён в `Courier Assignment`; курьер и доставка зарезервированы | Конкурентное принятие защищается доменным инвариантом и атомарной записью; публикует бизнес-факт через Outbox |
| **Reject Delivery Offer** | Курьер | Отклонить актуальное предложение | Offer закрыт как отклонённый и не создаёт `Courier Assignment` | Может инициировать дальнейший поиск внутри `delivery` |
| **Reassign Courier** | Процесс назначения `delivery` или авторизованный диспетчер | Прекратить неактуальное назначение и подобрать замену | Текущее назначение отменено или истекло, доставка снова участвует в поиске | Может вызываться Temporal Activity; алгоритм повторного выбора принадлежит `delivery` |
| **Replan Delivery** | Fulfillment process `orders` | Перепланировать доставку заказа по `order_id` | Если курьер назначен — Reassign Courier; затем Find Courier и Offer Delivery кандидату; без кандидата — успех без offer | Вызывается Temporal Activity через `ReplanDelivery`; композиция существующих handlers, алгоритм остаётся в `delivery` |
| **Cancel Delivery** | Fulfillment process `orders` | Отменить доставку заказа до Pickup по `order_id` | `waiting`/`assigned` → `cancelled`, курьер освобождён, `DeliveryCancelled` в Outbox; повтор — успех; после Pickup — бизнес-отказ `already_picked_up`, после `delivered`/`failed` — `already_closed` | Вызывается Temporal Activity через `CancelDelivery` как компенсация; бизнес-отказ не является ошибкой |
| **Confirm Pickup** | Назначенный курьер | Подтвердить получение заказа у ресторана | Для `Delivery` зафиксирован Pickup | Публикует бизнес-факт через Outbox |
| **Confirm Delivery** | Назначенный курьер | Подтвердить передачу заказа клиенту | Для `Delivery` зафиксирована Delivery Completion | Публикует бизнес-факт через Outbox |
| **Report Delivery Failure** | Назначенный курьер или авторизованный диспетчер | Зафиксировать невозможность завершить доставку | Неудача и её причина сохранены, результат доступен `orders` | Публикует бизнес-факт через Outbox; retryability причины остаётся открытым вопросом |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Courier Current Delivery** | Курьер | Получить свою активную доставку | Возвращено текущее назначение и разрешённое представление `Delivery` либо отсутствие активной доставки | Query model `delivery`; проверка принадлежности курьеру |
| **List Available Delivery Offers** | Курьер | Получить актуальные предложения, доступные этому курьеру | Возвращён список непротухших `Delivery Offer` без изменения состояния | Query model `delivery`; проверка адресата offer |

## Структура слайсов

Слайсы `delivery` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста): `slices/courier` владеет агрегатом и eligibility курьера, `slices/delivery` — доставкой и предложениями доставки. Use cases размещаются в `slices/<slice>/applications/<usecase>/`, shared packages — в `pkg/`. Traits репозиториев агрегатов определяются рядом с моделью в `domains/<domain>/`, а порты внешних интеграций и чтения — рядом с потребляющими сценариями в `applications/`.

## gRPC-адаптер

`delivery` владеет API `nomnom.delivery.v1` и публикует его как `buf.build/nomnom/delivery`. Исходные messages находятся в `infrastructure/grpc/nomnom/delivery/v1/delivery.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер генерирует серверные Rust/tonic stubs из локальной схемы и преобразует request messages в прикладные команды или запросы, а результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделями `Courier`, `Delivery` или их handlers. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API:

| Ошибка `delivery` | gRPC code | `ErrorInfo.reason` |
| --- | --- | --- |
| `Courier` или `Delivery` не найдены | `NOT_FOUND` | `RESOURCE_NOT_FOUND` |
| Некорректный адрес или запрос доставки | `INVALID_ARGUMENT` | `INVALID_DELIVERY` |
| Курьер занят или переход состояния запрещён | `FAILED_PRECONDITION` | `DELIVERY_PRECONDITION_FAILED` |
| `Delivery` уже существует | `ALREADY_EXISTS` | `DELIVERY_ALREADY_EXISTS` |
| Нет прав на операцию | `PERMISSION_DENIED` | — |
| Неожиданная внутренняя ошибка | `INTERNAL` | `INTERNAL_ERROR` |
| Внешняя зависимость недоступна или исход commit неизвестен | `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` |

RPC сервиса `DeliveryService`:

| RPC | Use case | Ответ |
| --- | --- | --- |
| `GetCourierCurrentDelivery` | Get Courier Current Delivery | `Delivery` или пусто |
| `CreateDelivery` | Create Delivery (`order_id`, `pickup_address`, `dropoff_address`) | `delivery_id`, `created = false` для идентичного повтора; другие адреса — `ALREADY_EXISTS` |
| `ReplanDelivery` | Replan Delivery (`order_id`) | `delivery_id`, `offered_courier_id` (пусто без кандидата); после Pickup — `FAILED_PRECONDITION` |
| `CancelDelivery` | Cancel Delivery (`order_id`, `reason`) | `cancelled = true` (включая повтор) или `cancelled = false` + `rejection_reason` (`already_picked_up`, `already_closed`) — бизнес-результат, не gRPC-ошибка |

Неизвестный `order_id` во всех командах — `NOT_FOUND`. Командные RPC авторизуются над ресурсом `Order(order_id)` (`CancelDelivery`, `ReplanDelivery`); Replan дополнительно проходит проверки вложенных use cases над `Delivery(id)`.

Генерация Rust/tonic кода (`neoeinstein-prost`/`neoeinstein-tonic`, см. `infrastructure/grpc/buf.gen.yaml`) — в `infrastructure/grpc/gen`:

```bash
make generate   # из корня: make buf-generate; только delivery: cd infrastructure/grpc && buf generate --template buf.gen.yaml
```

Transport adapter уже маппит application errors в gRPC status codes; добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.delivery.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). `GetCourierCurrentDelivery`, `CreateDelivery`, `ReplanDelivery` и `CancelDelivery` подключены к application use cases через tonic adapter; `cmd/delivery` запускает gRPC-сервер и завершает его через общий lifecycle. Subject вызывающего определяет `SubjectResolver` — см. «Service identity».

## Service identity

По [ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md) workloads других контекстов (Activities `orders-worker` и т. п.) предъявляют projected ServiceAccount token в gRPC metadata `authorization: Bearer <jwt>`. `ServiceTokenSubjectResolver` (`infrastructure/grpc/identity.rs`, `jsonwebtoken`) проверяет:

- `alg` ровно `RS256` или `ES256` (`none`, HS* и прочие отклоняются), `kid` обязателен и есть в JWKS, алгоритм совпадает с типом ключа (RSA → RS256, EC P-256 → ES256);
- подпись, `iss` равен `IDENTITY__ISSUER`, `aud` (строка или массив) содержит `IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s;
- `sub` = `system:serviceaccount:<namespace>:<serviceaccount>` есть в allowlist → `Subject::Service(<service-name>)`.

Любой отказ — `UNAUTHENTICATED` с общим сообщением; причина пишется только в stderr. Что разрешено service subject, решает authorization use case, не resolver.

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `IDENTITY__ISSUER` | `identity.issuer` | Issuer кластера (`/.well-known/openid-configuration`) |
| `IDENTITY__AUDIENCE` | `identity.audience` | Audience контекста, в кластере `nomnom.delivery` |
| `IDENTITY__JWKS_FILE` | `identity.jwks_file` | Файл JWKS кластера (`/openid/v1/jwks`); Helm монтирует `/etc/nomnom/identity/jwks.json` |
| `IDENTITY__SERVICE_ACCOUNTS` | `identity.service_accounts` | Allowlist `namespace/serviceaccount=service-name` через запятую, например `nomnom/orders-worker=orders-worker` |

Fail closed: если пусты issuer, audience или путь к JWKS, процесс использует `RejectingSubjectResolver` и пишет предупреждение при старте — все вызовы получают `UNAUTHENTICATED`. Нечитаемый или невалидный JWKS (нет usable RS256/ES256 ключей, ключ без `kid`, дубликат `kid`), пустой allowlist при заданной проверке и неверный формат allowlist завершают startup ошибкой. JWKS читается один раз при старте: после ротации signing key кластера нужен restart. Тесты проходят по общим векторам `tests/identity/fixtures` (AC-461).

## Авторизация (SpiceDB)

Use cases проверяют права через порты `CourierAuthorization` и `DeliveryAuthorization` (`slices/*/applications/authorization.rs`). При заданном `SPICEDB__ENDPOINT` оба порта закрывает `SpiceDbAuthorization` (`infrastructure/spicedb`, [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md), FR-435): каждый check — `PermissionsService.CheckPermission` (consistency `minimize_latency`, для ресурса с сохранённой revision — `at_least_as_fresh`, см. «Authorization revisions») по [схеме](../../authorization/schema.zed) и нормативной таблице [design.md](../../openspec/changes/add-spicedb-authorization/design.md):

| Порт / permission | SpiceDB check |
| --- | --- |
| `RegisterCourier` на `Platform` | `platform:nomnom#register_courier` |
| `CreateDelivery`, `FindCourier`, `OfferDelivery`, `ReassignCourier`, `CancelDelivery`, `ReplanDelivery` (любой resource) | `platform:nomnom#coordinate_delivery` |
| прочие permissions на `Courier(id)` | `courier:<id>#act` |
| прочие permissions на `Delivery(id)` | `delivery:<id>#act_as_courier` |
| остальные сочетания (`Order`/`Platform` для прочих permissions, `RegisterCourier` на `Courier`) | отказ без обращения к SpiceDB |

Subject: `Subject::User(id)` → `user:<id>`, `Subject::Service(id)` → `service:<id>`. Fulfillment-команды разрешены `service:orders-worker` grant'ом `platform:nomnom#fulfillment` из [`authorization/bootstrap.yaml`](../../authorization/bootstrap.yaml).

Fail closed: разрешает только `PERMISSIONSHIP_HAS_PERMISSION`; `NO_PERMISSION`, conditional и неизвестные значения — `AuthorizationError::Denied`. Ошибка gRPC, недоступность или превышение `SPICEDB__TIMEOUT` — `AuthorizationError::Unavailable` («authorization unavailable», gRPC `UNAVAILABLE`), а не разрешение. Соединение ленивое: недоступный SpiceDB не мешает startup.

Официального Rust SDK у SpiceDB нет: stubs `authzed.api.v1` (`CheckPermission`, `WriteRelationships`, `ReadRelationships` и их типы) генерируются buf remote plugins `neoeinstein-prost`/`neoeinstein-tonic` из `buf.build/authzed/api:v1.57.0` (`infrastructure/spicedb/buf.gen.yaml`, `make generate-spicedb`) и коммитятся в `infrastructure/spicedb/gen`. Preshared key передаётся interceptor'ом как `authorization: Bearer <key>`. Порты синхронные, а handlers tonic — async, поэтому клиент работает в собственном потоке с current-thread runtime, а ожидание ответа на multi-thread runtime выполняется через `block_in_place`.

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `SPICEDB__ENDPOINT` | `spicedb.endpoint` | gRPC endpoint `host:port` (или URL со схемой, совпадающей с `insecure`). Пусто — локальная авторизация процесса с предупреждением при старте |
| `SPICEDB__PRESHARED_KEY` | `spicedb.preshared_key` | Preshared key SpiceDB; в Kubernetes только из Secret. Обязателен при заданном endpoint, иначе ошибка startup |
| `SPICEDB__INSECURE` | `spicedb.insecure` | `true` — plaintext без TLS (только kind/локально); по умолчанию `false`, TLS с корнями webpki |
| `SPICEDB__TIMEOUT` | `spicedb.timeout` | Deadline одного check, по умолчанию `2s`; должен быть положительным |

`SPICEDB__ENDPOINT` обязателен: при пустом значении процесс завершается ошибкой startup. `StaticAuthorization` из `infrastructure/memory` используется только тестовым графом `di::build()`.

Integration-тесты (`tests/spicedb_authorization.rs`) выполняются против SpiceDB с `authorization/bootstrap.yaml`, только если задан `SPICEDB_ENDPOINT` (ключ `devkey` или `SPICEDB_PRESHARED_KEY`, plaintext); без переменной они пропускаются, кроме проверки недоступного endpoint:

```bash
make test-spicedb                          # SPICEDB_ENDPOINT=localhost:50051
SPICEDB_ENDPOINT=localhost:50051 cargo test --test spicedb_authorization
```

## Relationships и reconciliation (SpiceDB)

По change [`add-restaurant-delivery-relationships`](../../openspec/changes/add-restaurant-delivery-relationships/) (FR-444, FR-445, AC-477) `delivery` сам пишет relationships своих definitions. Порты записи — `CourierRelationshipWriter` и `DeliveryRelationshipWriter` (`slices/*/applications/relationships.rs`), SpiceDB-адаптер — `SpiceDbRelationships` (`infrastructure/spicedb/relationships.rs`, `WriteRelationships`), локальный/тестовый — `RecordingRelationships` (`infrastructure/memory`).

| Relationship | Source of truth | Запись |
| --- | --- | --- |
| `courier:<id>#self@user:<id>` | зарегистрированный `Courier` | `TOUCH` в Register Courier до сохранения |
| `delivery:<id>#courier@user:<courier_id>` | `Delivery.courier_id` | `TOUCH` в Accept Delivery Offer (Assignment); `DELETE` в Reassign Courier (и Replan Delivery через него) и в Cancel Delivery, если курьер освобождён — внутри Unit of Work до commit |

Допущение: курьер — пользователь с тем же id (`courier_id` = `user_id`, см. `GLOSSARY.md`). Ошибка записи отменяет use case (fail closed, `UNAVAILABLE`). Повторы, не меняющие состояние (повторная регистрация, повторное принятие offer, повторная отмена, отмена доставки без курьера), ничего не пишут. Если запись прошла, а commit нет, лишнюю связь удалит reconciliation; `DELETE` до commit при неуспешном commit временно отнимает доступ, его восстановит reconciliation.

Reconciliation (`infrastructure/reconcile.rs`, порт алгоритма `orders`): для `courier#self` (зарегистрированные курьеры) и `delivery#courier` (доставки с `courier_id`) граф читается страницами `ReadRelationships` с `fully_consistent`, хранилище — постранично по id и только committed state. Orphan/stale связь удаляется, только если была orphan на двух проходах с интервалом не меньше grace (кандидаты в памяти процесса); missing связь восстанавливается `TOUCH` сразу; id вне грамматики SpiceDB `^[a-zA-Z0-9/_|\-=+]+$` пропускаются и считаются `unrepairable`. Каждый проход пишет в stdout `checked`, `orphans_pending`, `orphans_deleted`, `missing_repaired`, `unrepairable`. Reconciler читает PostgreSQL через порт `ReconciliationStore` и работает фоновой задачей основного процесса (`ReconcilingWorker`): первый проход через `min(10s, interval)` после старта, остановка по сигналу shutdown (проход прерывается на границе страницы).

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `RECONCILER__INTERVAL` | `reconciler.interval` | Интервал между проходами, по умолчанию `5m`; положительный |
| `RECONCILER__GRACE` | `reconciler.grace` | Минимальный интервал между двумя orphan-проходами до удаления, по умолчанию `10m` |
| `RECONCILER__PAGE_SIZE` | `reconciler.page_size` | Размер страницы, по умолчанию `500`; `1..1000` |

### Authorization revisions (ZedToken)

По change [`add-authorization-revisions`](../../openspec/changes/add-authorization-revisions/) (FR-447, FR-448, AC-483, AC-484) writers возвращают ZedToken записи (`written_at`), а use cases сохраняют его в техническом поле `authorization_revision` ресурса в той же операции save: `Courier` — Register Courier (`TOUCH`), `Delivery` — Accept (`TOUCH`), Reassign/Replan и Cancel (`DELETE`). Repeat без записи revision не меняет; старые snapshots загружаются без неё. `SpiceDbAuthorization` читает revision через порт `RevisionReader` (`RepositoryRevisions` поверх репозиториев) и проверяет `courier:<id>`/`delivery:<id>` с `at_least_as_fresh`, поэтому собственная запись видна сразу (назначенный курьер сразу `act_as_courier`, снятый — сразу отказ); без revision и для `platform:nomnom` — `minimize_latency`. Ошибка чтения revision — authorization unavailable (fail closed). Reconciler, восстанавливая missing связь `TOUCH`, сохраняет её revision в курьера/доставку; удаление orphan revision не меняет.

Stubs `authzed.api.v1` теперь включают `CheckPermission`, `WriteRelationships` и `ReadRelationships` (`make generate-spicedb`). Integration-тесты `tests/spicedb_relationships.rs` (только при `SPICEDB_ENDPOINT`): регистрация → `courier#act`, принятие offer → `delivery#act_as_courier`, Reassign и Cancel → доступ пропадает, reconciler восстанавливает потерянную связь и удаляет сироту только после grace. Проверки идут через адаптер с revisions из хранилища и решаются сразу, без повторов; ждут окна квантования только сироты без revision (`minimize_latency`):

```bash
make test-spicedb   # SPICEDB_ENDPOINT=localhost:50051, оба integration-набора
```

## Хранилище (PostgreSQL)

[ADR 0001 контекста](docs/adr/0001-postgresql-хранилище-delivery.md), [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md). `PostgresStore` (`infrastructure/postgres`, `sqlx`) закрывает репозитории `Courier`, `Delivery`, `DeliveryOffer`, Outbox, `CourierLocationStore`, оба read model, `UnitOfWork` и `ReconciliationStore`. Схема — `infrastructure/postgres/migrations` (`sqlx::migrate!`), применяется процессом `delivery-migrate` (init container; advisory lock сериализует реплики).

- **Optimistic concurrency.** `Courier`, `Delivery` и `DeliveryOffer` несут технический `version` (`with_version`/`version()`, как `authorization_revision`). `save` выполняет `UPDATE … SET version = version + 1 WHERE id = $1 AND version = $2`; 0 строк при существующем id — `RepositoryError::ConcurrentModification` → `ConcurrentModification` application error → gRPC `ABORTED` (клиент повторяет вызов целиком); отсутствующий id — `Conflict`. Запись revision reconciler'ом тоже увеличивает `version`. In-memory store проверяет версии так же.
- **Unit of Work.** Порты не принимают handle транзакции: соединение текущей транзакции хранится в thread-local слоте store, поэтому записи агрегатов и Outbox внутри `run` фиксируются или откатываются вместе. Вложенные Unit of Work не поддерживаются.
- **Outbox.** Таблица `delivery_outbox` по ADR 31; `event_id` — UUIDv5 от `{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}`, `aggregate_version` — версия, записанная той же транзакцией. Domain Event → Integration Event переводит `OutboxMapper` (`PostgresStore::with_outbox_mapper`). Процесс `delivery` подключает `DeliveryIntegrationEvents` (`slices/delivery/infrastructure`) в `di::postgres`; `NoIntegrationEvents` (ничего не пишется) остаётся значением по умолчанию `PostgresStore::connect` для тестов.
- **Sync порты над async sqlx.** Store владеет tokio runtime пула; вызовы идут через `block_in_place` на multi-thread runtime процесса и через scoped thread на current-thread runtime.

| Переменная | Ключ | Назначение |
| --- | --- | --- |
| `DATABASE__URL` | `database.url` | URL `postgres://user:password@host:5432/db`; только из Secret. Пусто или база недоступна — ошибка startup процесса и `delivery-migrate` |
| `DATABASE__MAX_CONNS` | `database.max_conns` | Размер пула, по умолчанию `10` |

Integration-тесты (`tests/postgres_store.rs`: CRUD, конфликт двух конкурентных `save`, атомарность агрегата и Outbox, read models, reconciliation, оба вида runtime) выполняются только при заданном `DATABASE_URL`:

```bash
docker run -d --rm --name delivery-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 postgres:18
DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres cargo test --test postgres_store
docker stop delivery-pg
```

## Минимальная доменная модель

Для учебной реализации достаточно агрегатов `Courier` и `Delivery`.

| Тип | Минимальные поля | Назначение |
| --- | --- | --- |
| `Courier` — aggregate root | `id`, `status` | Хранит доступность курьера для назначения |
| `Delivery` — aggregate root | `id`, `orderId`, `courierId`, `pickupAddress`, `dropoffAddress`, `status`, `failureReason` | Управляет назначением курьера и выполнением доставки |
| `DeliveryOffer` — aggregate root | `id`, `deliveryId`, `courierId`, `offeredAt`, `expiresAt`, `state` | Ограниченное по времени предложение одному курьеру |

Состояния `Courier`: `available`, `busy`, `unavailable` (вне линии, не получает новых назначений). Состояния `Delivery`: `waiting`, `assigned`, `pickedUp`, `delivered`, `failed`, `cancelled`. В `DeliveryState` контракта `nomnom.delivery.v1` значения для `cancelled` пока нет: отменённая доставка не бывает текущей доставкой курьера. Состояния `DeliveryOffer`: `pending`, `accepted`, `rejected`, `withdrawn`; истечение вычисляется по `expiresAt` и не хранится.

Инварианты:

- назначить можно только доступного курьера на доставку в состоянии `waiting`;
- у доставки одновременно не бывает более одного курьера;
- забрать заказ можно только после назначения курьера;
- завершить можно только забранную доставку;
- доставленная, неудавшаяся или отменённая доставка больше не изменяется;
- отменить можно только доставку в `waiting` или `assigned`; отмена снимает курьера и освобождает его, повторная отмена — no-op;
- назначение делает курьера занятым, завершение, неудача доставки, отмена или Reassignment снова делают его доступным;
- Pickup, Delivery Completion и неудачу от имени курьера подтверждает только назначенный курьер;
- снять назначение (Reassignment) можно только до Pickup; неудача фиксируется только для `assigned` или `pickedUp`;
- занятый курьер не может стать `unavailable`, `unavailable` курьер не получает offer и назначение;
- offer принимает или отклоняет только его адресат и только до `expiresAt` включительно; принятие одного offer закрывает остальные pending offers доставки как `withdrawn`.

Application handler загружает `Courier`, `Delivery` и `DeliveryOffer` и сохраняет их вместе с записью Outbox в одной границе транзакции. Проверки допустимости назначения и переходов состояния выполняются их доменным поведением.

### Value objects

| VO | Значение и инварианты |
| --- | --- |
| `Address` | Непустая нормализованная строка; на первом этапе без геокодирования и отдельных полей адреса |
| `DeliveryFailureReason` | Непустая нормализованная строка; retryability не классифицируется |
| `Location` | Широта в [-90, 90] и долгота в [-180, 180], конечные значения; последняя позиция курьера хранится вне aggregate |

Идентификаторы и статусы пока остаются типизированными значениями и перечислениями, а не отдельными value objects. Маршрут, зоны и расчёт расстояния не входят в модель: расстояние до pickup, принадлежность зоне и число активных назначений поставляет порт `CourierLocator`.

### Domain policies и Specification Pattern

Переиспользуемые eligibility rules вне state реализованы как:

- `CourierEligibleForDelivery` — зона, расстояние, capacity и наличие активных назначений;
- `DeliveryOfferAcceptable` — срок действия offer, расстояние до pickup и лимиты активных назначений.

`Courier.status` и `Delivery.status` являются business state: допустимость назначения, pickup и completion остаётся только в FSM/domain behavior и не дублируется в specs. Application загружает `Courier` и `Delivery`, получает координаты, `now` и assignment snapshot и передаёт их domain service значениями. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## Dependency Injection

В `delivery` граф зависимостей описывается модулями [`shaku`](https://github.com/AzureMarker/shaku), которые проверяются при компиляции. Handlers по-прежнему получают зависимости явно (`&dyn Trait`) и создаются в тестах без контейнера.

- `slices/<slice>/di` — shaku-модуль слайса: интерфейс внешних портов (`CourierPorts`, `DeliveryPorts`), интерфейс use cases (`CourierUseCases`, `DeliveryUseCases`) и их реализация, которая собирает handler на каждый вызов. Порты слайс получает через submodule (`CourierDependencies`, `DeliveryDependencies`).
- `di` — корневой composition root: выбирает реализации портов (авторизация — `SpiceDbAuthorization` при заданном `SPICEDB__ENDPOINT`, иначе локальная; хранилище — один `PostgresStore` на оба слайса за интерфейсом `ProcessStore`; тестовый граф `di::build()` и locator — in-memory адаптеры из `infrastructure::memory`), задаёт `AssignmentPolicy` и собирает итоговый `ServiceModule`.
- `cmd/delivery` — загружает конфигурацию (невалидная завершает процесс до открытия портов), собирает модуль, открывает бизнес-порт через `di::HttpWorker` и передаёт его `pkg::lifecycle::Lifecycle`.

Shaku импортируют только модули `di`; `domains/` и `applications/` от него не зависят. Shaku требует `Send + Sync` от интерфейсов и создаёт отдельный экземпляр на каждый компонент, поэтому порты, которые обслуживает одно хранилище, собраны в интерфейс-агрегатор слайса. Generic `UnitOfWork` передаётся через object-safe `TransactionScope` и адаптер `ScopedUnitOfWork` из `pkg/application`.

Решение зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Runtime contract

`pkg/lifecycle` реализует [runtime contract](../../docs/cloud-native-runtime.md) ([ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md)):

- технический порт `management.port` (по умолчанию `9083`, переменная `MANAGEMENT__PORT`, отличается от `server.port`) отдаёт `GET /health/startup`, `/health/live`, `/health/ready` с JSON `{"status":"ok"}` или `{"status":"unavailable","reason":"initializing|shutting_down"}`;
- ready наступает после открытия бизнес-порта и запуска worker; ошибка открытия порта или завершение worker без сигнала — non-zero exit;
- `SIGTERM`/`SIGINT`: not ready (liveness остаётся 200) → `shutdown.readiness_delay` (по умолчанию `5s`, `SHUTDOWN__READINESS_DELAY`, ноль допустим) → worker закрывает listener и ждёт начатые соединения в пределах `shutdown.drain_timeout` (`20s`, `SHUTDOWN__DRAIN_TIMEOUT`) → технический порт закрывается последним → exit `0`; истёкший deadline прерывает соединения и даёт non-zero exit;
- повторный сигнал во время shutdown прерывает worker и завершает процесс с non-zero exit: tokio не позволяет вернуть обработчик сигнала по умолчанию.

## Событийная интеграция

Consumers интеграционных событий `delivery` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение состояния доставки, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной локальной транзакции. Kafka offset подтверждается только после commit.

Хранилище `delivery` — PostgreSQL ([ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md), [ADR 0001 контекста](docs/adr/0001-postgresql-хранилище-delivery.md)): Unit of Work, optimistic concurrency и таблица Outbox реализованы, Inbox — следующий шаг; см. [документацию Unit of Work](../../docs/unit-of-work.md#delivery) и [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md). Схема Inbox и Kafka consumers относятся к следующим шагам.

### Публикация Integration Events

Topic `delivery.v1.delivery-events`, контракт `infrastructure/grpc/nomnom/delivery/v1/events.proto` ([ADR 25](../../docs/adr/0025-integration-event-publication.md), [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md)).

| Domain Event | Integration Event (`event_type`) |
| --- | --- |
| `CourierAssignedToDelivery` | `nomnom.delivery.v1.CourierAssignedToDelivery` |
| `DeliveryPickedUp` | `nomnom.delivery.v1.DeliveryPickedUp` |
| `DeliveryCompleted` | `nomnom.delivery.v1.DeliveryCompleted` |
| `DeliveryFailed` | `nomnom.delivery.v1.DeliveryFailed` (с `reason`) |
| `CourierUnassignedFromDelivery`, `DeliveryCancelled` | не публикуются: контракта нет, mapper — явный allowlist |

`delivery-relay` (`cmd/delivery-relay`, тот же образ, runtime contract `pkg/lifecycle`) — polling relay ADR 31:

- **single active instance** — session-level `pg_try_advisory_lock` на выделенном соединении; реплики без lock не читают Outbox, потеря соединения снимает lock;
- **выборка** — пачка `published_at IS NULL` по `seq`; полные пачки без сбоев читаются подряд, затем пауза `OUTBOX__POLL_INTERVAL`;
- **публикация** — `rdkafka` idempotent producer, `acks=all`, key — `aggregate_id`; `published_at` выставляется после подтверждения брокера, повтор после сбоя штатен (consumers дедуплицируют по `event_id`);
- **head-of-line** — первая неудачная запись останавливает пачку, у неё растут `attempts` и `last_error`; следующий цикл начинает с неё;
- **retention** — опубликованные записи старше `OUTBOX__RETENTION` удаляются раз в `OUTBOX__CLEANUP_INTERVAL`; неопубликованные не удаляются.

Kafka headers совпадают с `orders/pkg/messaging/envelope.go`: `event_id`, `event_type`, `occurred_at` (RFC 3339 с наносекундами, UTC, как Go `time.RFC3339Nano`), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent` (если записан в `metadata`), плюс `_watermill_message_uuid` = `event_id`.

| Переменная | Ключ | Назначение |
| --- | --- | --- |
| `KAFKA__BROKERS` | `kafka.brokers` | `bootstrap.servers`; обязателен для `delivery-relay` (пусто — ошибка startup), основной процесс Kafka не использует |
| `KAFKA__CLIENT_ID` | `kafka.client_id` | `client.id`, по умолчанию `delivery-relay` |
| `OUTBOX__POLL_INTERVAL` | `outbox.poll_interval` | Пауза между циклами, по умолчанию `1s` |
| `OUTBOX__BATCH_SIZE` | `outbox.batch_size` | Размер пачки, по умолчанию `100` |
| `OUTBOX__PUBLISH_TIMEOUT` | `outbox.publish_timeout` | `message.timeout.ms` одной записи, по умолчанию `10s` |
| `OUTBOX__RETENTION` | `outbox.retention` | Хранение опубликованных записей, по умолчанию `7d` |
| `OUTBOX__CLEANUP_INTERVAL` | `outbox.cleanup_interval` | Интервал очистки, по умолчанию `1h` |

Relay также требует `DATABASE__URL`. Метрики (ADR 13, [инвентарь](../../docs/observability/metric-inventory.md#outbox)): `nomnom_outbox_pending_messages` и `nomnom_outbox_oldest_pending_age_seconds` (label `stream`), `nomnom_outbox_publish_retries_total` (`stream`, `event_type`, `error_class`), histograms `nomnom_outbox_publish_duration_seconds` (`outcome`) и `nomnom_outbox_publish_lag_seconds` (`occurred_at` → ack). Экспорт OTLP gRPC (`pkg/telemetry`) включается только при заданном `OTEL_EXPORTER_OTLP_ENDPOINT` (или `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`); `service.name` — `delivery-relay`, `service.namespace` — `nomnom`.

Integration-тест `tests/outbox_relay.rs` (запись основного процесса → relay → настоящий Kafka, проверка key, headers и payload; advisory lock; retention) выполняется при заданных `DATABASE_URL` и `KAFKA_BROKERS`:

```bash
docker run -d --rm --name delivery-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 postgres:18
docker run -d --rm --name delivery-kafka -p 59092:9092 \
  -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
  -e KAFKA_LISTENERS=PLAINTEXT://:9092,CONTROLLER://:9093 \
  -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:59092 \
  -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
  -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
  -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
  -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 \
  -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 apache/kafka:4.1.0
DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres KAFKA_BROKERS=localhost:59092 \
  cargo test --test outbox_relay
docker stop delivery-pg delivery-kafka
``` Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

## Observability

Инструментирование Rust транспорта, хранилища, Kafka и внешних клиентов размещается в `infrastructure/`, а lifecycle SDK и profiler — в bootstrap процесса. Rust profiling зависит от выбранного client/backend и целевой платформы и проверяется до добавления зависимости. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализованы:

- доменная модель: `Courier` (включая `unavailable`), `Location`, табличные FSM `Delivery` (6 states × 6 triggers, включая `Cancelled`/`Cancel`) и `DeliveryOffer` (4 × 3) с тестами полноты, domain events `CourierAssignedToDelivery`, `CourierUnassignedFromDelivery`, `DeliveryPickedUp`, `DeliveryCompleted`, `DeliveryFailed`, `DeliveryCancelled`; policies `CourierEligibleForDelivery` и `DeliveryOfferAcceptable`; контракты репозиториев рядом с моделями;
- application handlers всех 16 use cases в `slices/<slice>/applications/<usecase>/mod.rs` с unit-тестами;
- порты: `UnitOfWork`, `Clock`, `IdGenerator`, `Subject`/`AuthorizationError` (`pkg/application`), `RepositoryError` (`pkg/persistence`), `CourierAuthorization` и `DeliveryAuthorization`, `DeliveryOutbox`, `CourierLocator`, `CourierLocationStore`, read models `CourierCurrentDeliveryReadModel` и `PendingDeliveryOffersReadModel`, параметры `AssignmentPolicy`;
- `ServiceTokenSubjectResolver`: service identity по projected ServiceAccount token (ADR 27);
- `SpiceDbAuthorization`: SpiceDB-адаптер `CourierAuthorization` и `DeliveryAuthorization` (ADR 18, FR-435), выбирается при заданном `SPICEDB__ENDPOINT`;
- `PostgresStore` (`infrastructure/postgres`): все порты хранилища на `sqlx`, optimistic concurrency по `version`, Unit of Work, Outbox ADR 31, миграции `delivery-migrate`;
- публикация Integration Events: mapper `DeliveryIntegrationEvents` (4 события `events.proto`) и процесс `delivery-relay` (`infrastructure/outbox`: advisory lock, head-of-line, retention, `rdkafka`, OTLP-метрики ADR 13);
- in-memory адаптеры всех портов в `infrastructure/memory` (транзакционный store с rollback и инъекцией сбоя commit, статическая authorization, управляемые clock и id generator, статический locator) — только для тестов и локальной сборки.

Не реализованы: Kafka consumers и Inbox, распространение trace context в Outbox (`traceparent` не пишется: OpenTelemetry tracing в процессе не подключён), метрики основного процесса, user identity через BFF (ADR 27 оставляет её открытой), расчёт геопозиции/зон, BDD `.feature`. По gRPC опубликованы `GetCourierCurrentDelivery`, `CreateDelivery`, `ReplanDelivery`, `CancelDelivery`; остальные application handlers к transport пока не подключены.

Допущения по открытым продуктовым вопросам (момент начала поиска курьера, retryability причин неудачи, параметры позиции и политики назначения) зафиксированы в [design.md](../../openspec/changes/implement-delivery-application-layer/design.md#допущения-по-открытым-вопросам) change.

## Запуск

```bash
make run
```

## Проверка

```bash
make check
```

Автоформатирование:

```bash
make format
```

## Контейнер

Образ собирается из каталога `boundaries/delivery`. Доступные targets: `build` и
`runtime`; итоговая стадия — `runtime`:

```bash
docker build --target runtime -t nomnom/delivery:local boundaries/delivery
docker run --rm -p 8083:8083 -p 9083:9083 \
  -e SERVER__PORT=8083 \
  nomnom/delivery:local
```

Release-бинарник собирается с отладочной информацией и без stripping, чтобы сохранить
данные для символизации Pyroscope-профилей. Runtime основан на Debian с `ca-certificates`
и запускается непривилегированным пользователем `app`; `tini` пересылает сигналы
приложению. TOML-конфигурацию можно подключить
read-only через `CONFIG_FILE`:

```bash
docker run --rm -p 8083:8083 \
  -v "$PWD/boundaries/delivery/config.example.toml:/config/delivery.toml:ro" \
  -e CONFIG_FILE=/config/delivery.toml \
  nomnom/delivery:local
```

Текущий HTTP-каркас не требует внешних сервисов.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.
