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

Статус реализации: все перечисленные Commands и Queries реализованы как application handlers (`Input` / `Output` / `Handler::execute`) по SDD change [`implement-delivery-application-layer`](../../openspec/changes/implement-delivery-application-layer/) ; хранилище — PostgreSQL (см. «Хранилище (PostgreSQL)»), Integration Events публикует `delivery-relay` (см. «Событийная интеграция»); Kafka consumers ещё не подключены. Требования — `FR-300`…`FR-318`, acceptance criteria — `AC-300`…`AC-343`. Командные use cases процесса исполнения заказа (Cancel Delivery, Replan Delivery) и их gRPC-методы добавлены по change [`add-fulfillment-command-rpcs`](../../openspec/changes/add-fulfillment-command-rpcs/) (`FR-429`, `AC-455`, `AC-456`). gRPC API курьера и Register Courier, а также Dispatch Deliveries добавлены по change [`close-order-lifecycle-in-kind`](../../openspec/changes/close-order-lifecycle-in-kind/) (`FR-580`…`FR-582`, `AC-730`…`AC-736`).

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Register Courier** | Оператор платформы | Создать курьера в системе доставки | Создан `Courier` с исходным состоянием доступности | Authoritative authorization в `delivery` |
| **Set Courier Availability** | Курьер или авторизованный диспетчер | Изменить доступность курьера для новых назначений | `Courier` отмечен available или unavailable; позиция курьера не изменена | Не совмещается с location update |
| **Update Courier Location** | Курьерское приложение | Обновить последнюю известную позицию курьера | Сохранена актуальная доступная `delivery` позиция курьера | Частота, точность и retention определяются отдельно |
| **Create Delivery** | Fulfillment process `orders` | Запросить доставку оформленного заказа | Создана идемпотентная `Delivery` с необходимыми pickup/dropoff references и данными контекста | Вызывается Temporal Activity через API `delivery`; поиск курьера не запускает — его начинает Dispatch Deliveries |
| **Find Courier** | Процесс назначения `delivery` | Подобрать подходящего доступного курьера | Выбран кандидат или зафиксировано отсутствие кандидата; алгоритм остаётся внутри `delivery` | Может запускаться запросом orchestration, но выбор выполняет `delivery` |
| **Offer Delivery** | Процесс назначения `delivery` | Предложить конкретному курьеру принять доставку | Создано актуальное ограниченное по времени `Delivery Offer` и `DeliveryOffered` в Outbox; `Courier Assignment` ещё не создан | Учитывает результат Find Courier |
| **Accept Delivery Offer** | Курьер | Принять актуальное предложение | Ровно один актуальный offer успешно превращён в `Courier Assignment`; курьер и доставка зарезервированы | Конкурентное принятие защищается доменным инвариантом и атомарной записью; публикует бизнес-факт через Outbox |
| **Reject Delivery Offer** | Курьер | Отклонить актуальное предложение | Offer закрыт как отклонённый и не создаёт `Courier Assignment` | Следующий проход Dispatch Deliveries предлагает доставку другому курьеру |
| **Reassign Courier** | Процесс назначения `delivery` или авторизованный диспетчер | Прекратить неактуальное назначение и подобрать замену | Текущее назначение отменено или истекло, доставка снова участвует в поиске | Может вызываться Temporal Activity; алгоритм повторного выбора принадлежит `delivery` |
| **Replan Delivery** | Fulfillment process `orders` | Перепланировать доставку заказа по `order_id` | Если курьер назначен — Reassign Courier; затем Find Courier и Offer Delivery кандидату; без кандидата — успех без offer | Вызывается Temporal Activity через `ReplanDelivery`; композиция существующих handlers, алгоритм остаётся в `delivery` |
| **Cancel Delivery** | Fulfillment process `orders` | Отменить доставку заказа до Pickup по `order_id` | `waiting`/`assigned` → `cancelled`, курьер освобождён, `DeliveryCancelled` в Outbox; повтор — успех; после Pickup — бизнес-отказ `already_picked_up`, после `delivered`/`failed` — `already_closed` | Вызывается Temporal Activity через `CancelDelivery` как компенсация; бизнес-отказ не является ошибкой |
| **Confirm Pickup** | Назначенный курьер | Подтвердить получение заказа у ресторана | Для `Delivery` зафиксирован Pickup | Публикует бизнес-факт через Outbox |
| **Confirm Delivery** | Назначенный курьер | Подтвердить передачу заказа клиенту | Для `Delivery` зафиксирована Delivery Completion | Публикует бизнес-факт через Outbox |
| **Report Delivery Failure** | Назначенный курьер или авторизованный диспетчер | Зафиксировать невозможность завершить доставку | Неудача сохранена с кодом причины из каталога Delivery Failure Reason и необязательным пояснением, результат доступен `orders` | Публикует бизнес-факт через Outbox; неизвестный код — ошибка валидации; retryability кодов остаётся открытым вопросом |
| **Expire Delivery Offers** | Фоновая задача процесса `delivery` | Закрыть pending offers, на которые не ответили до `expiresAt` | Offers с `expiresAt < now` переведены в `expired` пачками, каждый в своей Unit of Work с `DeliveryOfferExpired` в Outbox; конкурентно закрытые offers пропускаются | Без subject и проверки прав; повторное предложение делает Dispatch Deliveries (см. «Истечение Delivery Offers») |
| **Dispatch Deliveries** | Фоновая задача процесса `delivery` | Начать поиск курьера для ожидающих доставок | Ожидающей `Delivery` без pending offer предложена доставка курьеру, которому её ещё не предлагали; без кандидата доставка ждёт следующего прохода; конкурентно изменённые доставки пропускаются | Без subject и проверки прав; Find Courier и Offer Delivery в одной Unit of Work на доставку (см. «Dispatch Deliveries») |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Courier Current Delivery** | Курьер | Получить свою активную доставку | Возвращено текущее назначение и разрешённое представление `Delivery` либо отсутствие активной доставки | Query model `delivery`; проверка принадлежности курьеру |
| **List Available Delivery Offers** | Курьер | Получить актуальные предложения, доступные этому курьеру | Возвращён список непротухших `Delivery Offer` без изменения состояния | Query model `delivery`; проверка адресата offer |

## Структура слайсов

Слайсы `delivery` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста): `slices/courier` владеет агрегатом и eligibility курьера, `slices/delivery` — доставкой и предложениями доставки. Use cases размещаются в `slices/<slice>/applications/<usecase>/`, shared packages — в `pkg/`. Traits репозиториев агрегатов определяются рядом с моделью в `domains/<domain>/`, а порты внешних интеграций и чтения — рядом с потребляющими сценариями в `applications/`.

Зависимость между слайсами идёт только в направлении `delivery → courier` (FR-486): `slices/courier` не импортирует `slices::delivery`, это проверяет `tests/slice_boundaries.rs`. Поэтому query Get Courier Current Delivery, который читает модель `Delivery`, принадлежит слайсу `delivery` (`slices/delivery/applications/get_courier_current_delivery`); право курьера по-прежнему проверяет `CourierAuthorization` слайса `courier`.

Занятость курьера use cases слайса `delivery` проверяют и меняют только через прикладной порт `CourierAssignment` слайса `courier` (`slices/courier/applications/assignment.rs`: `ensure_registered`, `ensure_available`, `occupy`, `release`), а не через агрегат `Courier` и `CourierRepository` (FR-485). Реализация порта загружает `Courier` через `CourierRepository`, вызывает доменный метод и сохраняет агрегат в текущей Unit of Work.

Accept Delivery Offer, Reassign Courier, Confirm Delivery, Report Delivery Failure и Cancel Delivery изменяют offer, `Delivery` и `Courier` одной локальной транзакцией. Это записанное исключение из правила «один aggregate на транзакцию» ([Unit of Work](../../docs/unit-of-work.md#исключение-offer-delivery-и-courier-в-одной-транзакции)): инвариант «у курьера не больше одной активной доставки» и FR-310 требуют атомарности, а конкуренция за `Courier` ограничена optimistic concurrency по `version`.

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
| `GetCourierCurrentDelivery` | Get Courier Current Delivery | `courier_status` и `Delivery` (`ASSIGNED`/`PICKED_UP` с `pickup_address` и `dropoff_address`) или пусто; незарегистрированный курьер — `NOT_FOUND` |
| `CreateDelivery` | Create Delivery (`order_id`, `pickup_address`, `dropoff_address`) | `delivery_id`, `created = false` для идентичного повтора; другие адреса — `ALREADY_EXISTS` |
| `ReplanDelivery` | Replan Delivery (`order_id`) | `delivery_id`, `offered_courier_id` (пусто без кандидата); после Pickup — `FAILED_PRECONDITION` |
| `CancelDelivery` | Cancel Delivery (`order_id`, `reason`) | `cancelled = true` (включая повтор) или `cancelled = false` + `rejection_reason` (`already_picked_up`, `already_closed`) — бизнес-результат, не gRPC-ошибка |
| `RegisterCourier` | Register Courier (`courier_id` = user id курьера) | `courier_id`, `status`; повтор — `ALREADY_EXISTS` |
| `UpdateCourierLocation` | Update Courier Location (`courier_id`, `latitude`, `longitude`) | позиция и `recorded_at_epoch_seconds`; координаты вне диапазона — `INVALID_ARGUMENT` |
| `SetCourierAvailability` | Set Courier Availability (`courier_id`, `availability`) | `status`, `changed = false`, если доступность уже такая; `COURIER_AVAILABILITY_UNSPECIFIED` — `INVALID_ARGUMENT` |
| `ListAvailableDeliveryOffers` | List Available Delivery Offers (`courier_id`) | pending и непротухшие `DeliveryOffer` курьера |
| `AcceptDeliveryOffer` | Accept Delivery Offer (`offer_id`, `courier_id`) | `offer_id`, `delivery_id`, `courier_id`, `already_accepted` |
| `RejectDeliveryOffer` | Reject Delivery Offer (`offer_id`, `courier_id`) | `offer_id`, `delivery_id`, `changed` |
| `ConfirmPickup` | Confirm Pickup (`delivery_id`, `courier_id`) | `delivery_id`, `state`, `changed` |
| `ConfirmDelivery` | Confirm Delivery (`delivery_id`, `courier_id`) | `delivery_id`, `state = COMPLETED`, `changed` |
| `ReportDeliveryFailure` | Report Delivery Failure (`delivery_id`, `courier_id` или пусто для диспетчера, `reason` — код каталога, `detail`) | `delivery_id`, `reason`, `detail`, `released_courier_id`; неизвестный код — `INVALID_ARGUMENT` |

Неизвестный `order_id` во всех командах — `NOT_FOUND`. Командные RPC авторизуются над ресурсом `Order(order_id)` (`CancelDelivery`, `ReplanDelivery`); Replan дополнительно проходит проверки вложенных use cases над `Delivery(id)`.

RPC курьера вызываются от имени пользователя: subject — user из assertion делегата (см. «User identity от BFF»). `RegisterCourier` требует `platform:nomnom#register_courier` (оператор) и до сохранения записывает `courier:<id>#self@user:<id>`, поэтому пользователь получает `courier#act` на нового курьера. Остальные RPC курьера требуют `courier#act` на `courier_id` запроса; `ReportDeliveryFailure` без `courier_id` — действие диспетчера с `platform:nomnom#dispatch_delivery`. Принадлежность доставки или offer курьеру проверяет домен. Ошибки `courier` и `delivery` переводятся одной таблицей (`infrastructure/grpc/service/status.rs`).

Генерация Rust/tonic кода (`neoeinstein-prost`/`neoeinstein-tonic`, см. `infrastructure/grpc/buf.gen.yaml`) — в `infrastructure/grpc/gen`:

```bash
make generate   # из корня: make buf-generate; только delivery: cd infrastructure/grpc && buf generate --template buf.gen.yaml
```

Transport adapter уже маппит application errors в gRPC status codes; добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.delivery.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). Все RPC `DeliveryService` подключены к application use cases через tonic adapter; `cmd/delivery` запускает gRPC-сервер и завершает его через общий lifecycle. Subject вызывающего определяет `SubjectResolver` — см. «Service identity».

## Service identity

По [ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md) workloads других контекстов (Activities `orders-worker` и т. п.) предъявляют projected ServiceAccount token в gRPC metadata `authorization: Bearer <jwt>`. `ServiceTokenSubjectResolver` (`infrastructure/grpc/identity.rs`, `jsonwebtoken`) только соединяет verifier'ы модулей `identity/service_token.rs` и `identity/user_assertion.rs`; ключи разбирает `identity/jwks.rs`, файлы и allowlist читает `identity/config.rs`. Проверяется:

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

### User identity от BFF

По [ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md) BFF передаёт пользователя в metadata `x-nomnom-user-assertion: <jwt>` вместе со своим service token. Порядок проверки в `ServiceTokenSubjectResolver`:

1. service token (выше); без него assertion не рассматривается;
2. assertion нет → `Subject::Service(<service-name>)`, как раньше;
3. assertion повторён или пуст, user assertions не настроены или service не входит в `IDENTITY__USER_DELEGATES` → `UNAUTHENTICATED`;
4. `UserAssertionVerifier`: `alg` ровно `ES256`, `kid` есть в JWKS BFF (ключи EC P-256), `iss` равен `IDENTITY__USER_ISSUER`, `aud` содержит `IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s, `iat` обязателен, `exp - iat ≤ 60 s`, `sub` непустой и без пробелов по краям, `jti` непустой → `Subject::User(<sub>)`.

Service identity делегата остаётся в `Caller::delegate` (`ServiceTokenSubjectResolver::authenticate`); принятие и отказ пишутся в stderr с `jti` и делегатом, без token.

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `IDENTITY__USER_ISSUER` | `identity.user_issuer` | Ожидаемый `iss` assertion, `nomnom-bff` |
| `IDENTITY__USER_JWKS_FILE` | `identity.user_jwks_file` | JWKS BFF; Helm монтирует `/etc/nomnom/user-identity/jwks.json` из `identity.userJwksConfigMap` |
| `IDENTITY__USER_DELEGATES` | `identity.user_delegates` | Services через запятую, которым разрешено передавать user subject, в развёртываниях `bff` |

User assertions принимаются, только если заданы все три переменные; иначе любой вызов с assertion получает `UNAUTHENTICATED`, вызовы без assertion работают как раньше. Нечитаемый JWKS BFF, JWKS без ES256-ключей и неверный формат списка делегатов завершают startup ошибкой. Ротация ключа BFF: новый `kid` добавляется в ConfigMap, затем rollout restart, затем BFF переключается на новый ключ. Тесты проходят по `tests/identity/fixtures/user-vectors.json` с часами, зафиксированными на `now` (FR-471–FR-473).

## Авторизация (SpiceDB)

Use cases проверяют права через порты `CourierAuthorization` и `DeliveryAuthorization` (`slices/*/applications/authorization.rs`). При заданном `SPICEDB__ENDPOINT` оба порта закрывает `SpiceDbAuthorization` (`infrastructure/spicedb`, [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md), FR-435): каждый check — `PermissionsService.CheckPermission` (consistency `minimize_latency`, для ресурса с сохранённой revision — `at_least_as_fresh`, см. «Authorization revisions») по [схеме](../../authorization/schema.zed) и нормативной таблице [design.md](../../openspec/changes/add-spicedb-authorization/design.md):

| Порт / permission | SpiceDB check |
| --- | --- |
| `RegisterCourier` на `Platform` | `platform:nomnom#register_courier` |
| `CreateDelivery`, `FindCourier`, `OfferDelivery`, `ReassignCourier`, `CancelDelivery`, `ReplanDelivery` (любой resource) | `platform:nomnom#coordinate_delivery` |
| прочие permissions на `Courier(id)` | `courier:<id>#act` |
| `ReportDeliveryFailure` на `Delivery(id)` (диспетчер) | `platform:nomnom#dispatch_delivery` (`operator`, AC-694) |
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

Integration-тесты (`tests/spicedb_authorization.rs`) выполняются против SpiceDB, который тест сам поднимает через testcontainers (`authzed/spicedb` той же версии, что в `ops/kind/bootstrap/values.yaml`, memory datastore с `authorization/bootstrap.yaml`, ключ `devkey`, `tests/support/mod.rs`). Переменных окружения и внешних инстансов нет, нужен только Docker:

```bash
make test-spicedb   # cargo test --test spicedb_authorization --test spicedb_relationships
```

## Relationships и reconciliation (SpiceDB)

По change [`add-restaurant-delivery-relationships`](../../openspec/changes/add-restaurant-delivery-relationships/) (FR-444, FR-445, AC-477) `delivery` сам пишет relationships своих definitions. Порты записи — `CourierRelationshipWriter` и `DeliveryRelationshipWriter` (`slices/*/applications/relationships.rs`), SpiceDB-адаптер — `SpiceDbRelationships` (`infrastructure/spicedb/relationships.rs`, `WriteRelationships`), локальный/тестовый — `RecordingRelationships` (`infrastructure/memory`).

| Relationship | Source of truth | Запись |
| --- | --- | --- |
| `courier:<id>#self@user:<id>` | зарегистрированный `Courier` | `TOUCH` в Register Courier до сохранения |
| `delivery:<id>#courier@user:<courier_id>` | активное назначение: `Delivery::active_courier()` — `courier_id` у доставки assigned или picked up | `TOUCH` в Accept Delivery Offer (Assignment); `DELETE` при каждом окончании назначения — Reassign Courier (и Replan Delivery через сервис `end_assignment`), Cancel Delivery, если курьер освобождён, Confirm Delivery и Report Delivery Failure — внутри Unit of Work до commit |

Снятие назначения реализовано одной функцией `release_assignment` (`slices/delivery/applications/assignment.rs`): она освобождает курьера, удаляет `delivery#courier` до commit и сохраняет revision `DELETE` с доставкой (FR-444, FR-447, AC-483). Её вызывают Reassign Courier, Replan Delivery, Cancel Delivery, Confirm Delivery и Report Delivery Failure (change [`scope-courier-access-to-assignment`](../../openspec/changes/scope-courier-access-to-assignment/), AC-690, AC-691). `Delivery.courier_id` доставленной или неуспешной доставки остаётся фактом «кто вёз», но доступа не даёт: право курьера действовать и видеть заказ (`order#view` через `delivery->act_as_courier`) живёт, пока назначение активно. Неудачный `DELETE` откатывает Unit of Work (AC-692).

Допущение: курьер — пользователь с тем же id (`courier_id` = `user_id`, см. `GLOSSARY.md`). Ошибка записи отменяет use case (fail closed, `UNAVAILABLE`). Повторы, не меняющие состояние (повторная регистрация, повторное принятие offer, повторная отмена, отмена доставки без курьера), ничего не пишут. Если запись прошла, а commit нет, лишнюю связь удалит reconciliation; `DELETE` до commit при неуспешном commit временно отнимает доступ, его восстановит reconciliation.

Reconciliation (`infrastructure/reconcile.rs`, порт алгоритма `orders`): для `courier#self` (зарегистрированные курьеры) и `delivery#courier` (активные назначения по `Delivery::active_courier()`; связь доставленной или неуспешной доставки удаляется как stale и не восстанавливается, AC-693) граф читается страницами `ReadRelationships` с `fully_consistent`, хранилище — постранично по id и только committed state. Orphan/stale связь удаляется, только если была orphan на двух проходах с интервалом не меньше grace (кандидаты в памяти процесса); missing связь восстанавливается `TOUCH` так же только после двух проходов с интервалом не меньше grace, на которых она отсутствовала при том же subject и той же версии строки `Courier`/`Delivery` в хранилище: Reassign Courier и Cancel Delivery делают `DELETE` до `UPDATE` строки и без row lock, и проход, прочитавший граф после `DELETE`, а хранилище до commit, иначе вернул бы снятому курьеру доступ (FR-563). Кандидат missing ключуется версией строки: страницы `ReconciliationStore::courier_page`/`assignment_page` возвращают `version`, и если между проходами версия изменилась (writer закоммитил, например курьера сняли, снова назначили и снова сняли, а второй `DELETE` ещё не закоммичен), grace начинается заново с новой версией, и такой subject не восстанавливается (AC-760). Пока кандидат ждёт grace (первое наблюдение, внутри grace или после смены версии), он считается в `missing_pending`; кандидат, ставший present или потерявший subject, забывается; id вне грамматики SpiceDB `^[a-zA-Z0-9/_|\-=+]+$` пропускаются и считаются `unrepairable`. Каждый проход пишет в stdout `checked`, `orphans_pending`, `orphans_deleted`, `missing_pending`, `missing_repaired`, `unrepairable`. Reconciler читает PostgreSQL через порт `ReconciliationStore` и работает фоновой задачей основного процесса (`ReconcilingWorker`): первый проход через `min(10s, interval)` после старта, остановка по сигналу shutdown (проход прерывается на границе страницы).

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `RECONCILER__INTERVAL` | `reconciler.interval` | Интервал между проходами, по умолчанию `5m`; положительный |
| `RECONCILER__GRACE` | `reconciler.grace` | Минимальный интервал между двумя проходами до удаления orphan или восстановления missing, по умолчанию `10m` |
| `RECONCILER__PAGE_SIZE` | `reconciler.page_size` | Размер страницы, по умолчанию `500`; `1..1000` |

### Authorization revisions (ZedToken)

По change [`add-authorization-revisions`](../../openspec/changes/add-authorization-revisions/) (FR-447, FR-448, AC-483, AC-484) writers возвращают ZedToken записи (`written_at`), а use cases сохраняют его в техническом поле `authorization_revision` ресурса в той же операции save: `Courier` — Register Courier (`TOUCH`), `Delivery` — Accept (`TOUCH`), Reassign/Replan, Cancel, Confirm Delivery и Report Delivery Failure (`DELETE`). Repeat без записи revision не меняет; старые snapshots загружаются без неё. `SpiceDbAuthorization` читает revision через порт `RevisionReader` (`RepositoryRevisions` поверх репозиториев) и проверяет `courier:<id>`/`delivery:<id>` с `at_least_as_fresh`, поэтому собственная запись видна сразу (назначенный курьер сразу `act_as_courier`, снятый — сразу отказ); без revision и для `platform:nomnom` — `minimize_latency`. Ошибка чтения revision — authorization unavailable (fail closed). Reconciler, восстанавливая missing связь `TOUCH`, сохраняет её revision в курьера/доставку; удаление orphan revision не меняет.

Stubs `authzed.api.v1` теперь включают `CheckPermission`, `WriteRelationships` и `ReadRelationships` (`make generate-spicedb`). Integration-тесты `tests/spicedb_relationships.rs` (SpiceDB и PostgreSQL из testcontainers; reconciler-тест получает собственный PostgreSQL, потому что сканирует все доставки): регистрация → `courier#act`, принятие offer → `delivery#act_as_courier`, Reassign, Cancel, Confirm Delivery и Report Delivery Failure → доступ пропадает, operator платформы → `dispatch_delivery`, reconciler восстанавливает потерянную связь и удаляет сироту только после grace. Проверки идут через адаптер с revisions из хранилища и решаются сразу, без повторов; ждут окна квантования только сироты без revision (`minimize_latency`):

```bash
make test-spicedb   # оба integration-набора, контейнеры поднимаются сами
```

## Истечение Delivery Offers

Фоновая задача основного процесса (`OfferExpiryWorker` в `di/offer_expiry_worker.rs`, рядом с `ReconcilingWorker`) раз в `OFFERS__EXPIRY_INTERVAL` вызывает Expire Delivery Offers (`slices/delivery/applications/expire_delivery_offers`): pending offers с `expires_at < now` читаются пачками по `OFFERS__EXPIRY_BATCH` (`ORDER BY expires_at`, partial index `delivery_offers_pending_expiry`), полные пачки — подряд. Каждый offer истекает в своей Unit of Work вместе с `DeliveryOfferExpired` в Outbox; optimistic-concurrency конфликт (offer конкурентно принят, отклонён или отозван) — пропуск, не ошибка. Цикл останавливается по сигналу shutdown основного worker; начатая пачка доводится до конца. Ошибка пачки пишется в stderr и прерывает только текущий проход. `DeliveryOfferExpired` — внутренний факт: не публикуется, считается в `nomnom.delivery.offers{nomnom.outcome="expired"}`. Queries (`ListAvailableDeliveryOffers`, `is_open_at`) считают `expired` закрытым, как и pending offer после `expiresAt`.

Повторное предложение после истечения в этот use case не входит: истёкший offer больше не pending, поэтому доставку берёт следующий проход Dispatch Deliveries (см. ниже), а Find Courier пропускает курьеров, которым эта доставка уже предлагалась в любом состоянии, и следующий offer получает другой курьер. Эскалацией остаётся `orders`: fulfillment workflow без `CourierAssignedToDelivery` за `FULFILLMENT__ASSIGNMENT_TIMEOUT` (15m) вызывает `ReplanDelivery` (до `FULFILLMENT__MAX_REPLANS`, затем эскалация оператору).

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `OFFERS__EXPIRY_INTERVAL` | `offers.expiry_interval` | Интервал между проходами, по умолчанию `15s`; положительный |
| `OFFERS__EXPIRY_BATCH` | `offers.expiry_batch` | Максимум offers пачки, по умолчанию `100`; `1..1000` |

Невалидные значения завершают startup.

## Dispatch Deliveries

Поиск курьера начинает сам `delivery`, а не Create Delivery и не оркестратор заказа (FR-582). Фоновая задача основного процесса (`DispatchWorker` в `di/dispatch_worker.rs`, рядом с `OfferExpiryWorker`) раз в `OFFERS__DISPATCH_INTERVAL` вызывает Dispatch Deliveries (`slices/delivery/applications/dispatch_deliveries`): ожидающие доставки без pending offer читаются по возрастанию `(created_at, id)` пачками по `OFFERS__DISPATCH_BATCH` через порт `WaitingDeliveries` (только `PostgresStore`: фильтр и keyset-пагинация в SQL, partial index `deliveries_waiting`; тестовые графы над in-memory хранилищем выборки не имеют); каждая следующая пачка прохода начинается после последней доставки предыдущей, поэтому доставки без кандидата не загораживают следующие. Для каждой доставки в своей Unit of Work Find Courier выбирает доступного курьера, которому её ещё не предлагали, `Delivery` сохраняется с версией из пачки, и Offer Delivery создаёт offer с `DeliveryOffered` в Outbox. Без кандидата доставка остаётся ожидающей до следующего прохода; конкурентное изменение доставки (другая реплика, принятый offer, Replan, Cancel) или курьер, переставший быть доступным, — пропуск, не ошибка. Subject и проверки прав нет: это работа процесса. Остановка и ошибки пачки — как у истечения offers.

Поэтому offer появляется в течение `OFFERS__DISPATCH_INTERVAL` после Create Delivery, а после отказа или истечения — следующему курьеру. Когда доставку уже предлагали всем доступным курьерам, она ждёт Replan Delivery из `orders`. `DeliveryOffered` — внутренний факт: не публикуется и метрикой не считается.

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `OFFERS__DISPATCH_INTERVAL` | `offers.dispatch_interval` | Интервал между проходами, по умолчанию `2s`; положительный |
| `OFFERS__DISPATCH_BATCH` | `offers.dispatch_batch` | Максимум доставок пачки, по умолчанию `100`; `1..1000` |

Невалидные значения завершают startup.

## Хранилище (PostgreSQL)

[ADR 0001 контекста](docs/adr/0001-postgresql-хранилище-delivery.md), [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md). `PostgresStore` (`infrastructure/postgres`, `sqlx`) закрывает репозитории `Courier`, `Delivery`, `DeliveryOffer`, Outbox, `CourierLocationStore`, оба read model, `UnitOfWork` и `ReconciliationStore`. Схема — `infrastructure/postgres/migrations` (`sqlx::migrate!`), применяется процессом `delivery-migrate` (init container; advisory lock сериализует реплики).

- **Optimistic concurrency.** `Courier`, `Delivery` и `DeliveryOffer` несут технический `version` (`with_version`/`version()`, как `authorization_revision`). `save` выполняет `UPDATE … SET version = version + 1 WHERE id = $1 AND version = $2`; 0 строк при существующем id — `RepositoryError::ConcurrentModification` → `ConcurrentModification` application error → gRPC `ABORTED` (клиент повторяет вызов целиком); отсутствующий id — `Conflict`. Запись revision reconciler'ом тоже увеличивает `version`. In-memory store проверяет версии так же.
- **Unit of Work.** Порты не принимают handle транзакции: соединение текущей транзакции хранится в thread-local слоте store, поэтому записи агрегатов и Outbox внутри `run` фиксируются или откатываются вместе. Вложенные Unit of Work не поддерживаются.
- **Outbox.** Таблица `delivery_outbox` по ADR 31; `event_id` — UUIDv5 от `{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}`, `aggregate_version` — версия, записанная той же транзакцией. Domain Event → Integration Event переводит `OutboxMapper` (`PostgresStore::with_outbox_mapper`). Процесс `delivery` подключает `DeliveryIntegrationEvents` (`slices/delivery/infrastructure`) в `di::postgres`; `NoIntegrationEvents` (ничего не пишется) остаётся значением по умолчанию `PostgresStore::connect` для тестов.
- **Sync порты над async sqlx.** Store владеет tokio runtime пула; вызовы идут через `block_in_place` на multi-thread runtime процесса и через scoped thread на current-thread runtime.

| Переменная | Ключ | Назначение |
| --- | --- | --- |
| `DATABASE__URL` | `database.url` | URL `postgres://user:password@host:5432/db`; только из Secret. Пусто или база недоступна — ошибка startup процесса и `delivery-migrate` |
| `DATABASE__MAX_CONNS` | `database.max_conns` | Размер пула процесса, по умолчанию `10`; `1..100`. `delivery-migrate` всегда открывает одно соединение (`MIGRATE_MAX_CONNS`) |
| `DATABASE__MIN_CONNS` | `database.min_conns` | Idle-минимум, по умолчанию `0`; `0..1` и не больше `max_conns` |
| `DATABASE__MAX_LIFETIME` | `database.max_lifetime` | Соединение старше закрывается при возврате в пул, по умолчанию `30m`; положительный |
| `DATABASE__IDLE_TIMEOUT` | `database.idle_timeout` | Простаивающее дольше соединение закрывается (выше `min_conns`), по умолчанию `5m`; положительный и не больше `max_lifetime` |
| `DATABASE__ACQUIRE_TIMEOUT` | `database.acquire_timeout` | Ожидание соединения пула, затем `UNAVAILABLE` и `db.client.connection.timeouts`; у relay также предел установки lock-соединения и захвата lock. По умолчанию `5s`; положительный |

Невалидные значения завершают startup. Helm передаёт их из `database.pool.*` (`ops/helm/values.yaml`, `values-relay.yaml`), URL — из Secret `database.urlSecret`.

### Пулер PostgreSQL

Перед базой стоит пулер соединений, и он обязан работать в **session mode**: `delivery` опирается на состояние server-сессии. `delivery-relay` держит session-level `pg_try_advisory_lock` на выделенном соединении вне пула, проверяет владение по `pg_locks` своего `pg_backend_pid()` и задаёт на этой сессии `statement_timeout` и TCP keepalives (startup options); `delivery-migrate` (`sqlx::migrate!`) берёт session-level advisory lock на единственном соединении; sqlx кэширует prepared statements на соединении. Transaction mode ломает всё это: lock, настройки сессии и prepared statements окажутся на чужом server-соединении. Unit of Work и запросы Outbox живут внутри транзакции и от режима не зависят; `LISTEN/NOTIFY` и временные таблицы не используются. Lock-соединение relay живёт всё время активности реплики, поэтому `client_idle_timeout` пулера не должен быть короче `OUTBOX__POLL_INTERVAL`.

Каждый процесс держит собственный пул sqlx (`DATABASE__*`), в session mode каждое его соединение занимает server-соединение пулера. Сумма Σ(replicas × maxConns) всех release плюс по одному lock-соединению на реплику relay должна помещаться в `pool_size` пулера для базы `delivery`:

| Процесс | values | maxConns | minConns |
| --- | --- | --- | --- |
| `delivery` (gRPC, reconciler, истечение offers) | `values.yaml` | 10 | 0 |
| `delivery-relay` | `values-relay.yaml` | 2 (+1 lock-соединение вне пула) | 0 |
| init container `delivery-migrate` | `values.yaml` (`migrateMaxConns`) | 1 (одно соединение под advisory lock) | 0 |

При одной реплике каждого release (`replicaCount: 1`) это 13 server-соединений плюс одно на время миграций в каждом стартующем pod `delivery`. Пул экспортирует OTel semconv `db.client.connection.*` (`count` по `state`, `max`, `idle.min`, `pending_requests`, `timeouts`, `wait_time`, `use_time`, `create_time`; `db.client.connection.pool.name=delivery`), инвентарь — [`docs/observability/metric-inventory.md`](../../docs/observability/metric-inventory.md#db).

Тесты хранилища идут в настоящий PostgreSQL: каждый тестовый бинарь поднимает свой контейнер через testcontainers (`postgres:18.6-alpine3.24`, как в ops) и удаляет его при выходе; нужен только запущенный Docker. Unit-тесты use cases работают через `TestStore` (`infrastructure/postgres/test_support.rs`) — `PostgresStore` над отдельной базой на тест (копия мигрированной template-базы) с тестовыми хуками: отключение хранилища, сбой commit, счётчики Unit of Work и закоммиченные Outbox-сообщения. In-memory варианта хранилища нет. Integration-тесты (`tests/postgres_store.rs`: CRUD, конфликт двух конкурентных `save`, атомарность агрегата и Outbox, read models, reconciliation, оба вида runtime) используют общую мигрированную базу контейнера (`tests/support`), идентификаторы уникальны:

```bash
DOCKER_CONFIG=$(mktemp -d) cargo test --test postgres_store
```

## Минимальная доменная модель

Для учебной реализации достаточно агрегатов `Courier` и `Delivery`.

| Тип | Минимальные поля | Назначение |
| --- | --- | --- |
| `Courier` — aggregate root | `id`, `status` | Хранит доступность курьера для назначения |
| `Delivery` — aggregate root | `id`, `orderId`, `courierId`, `pickupAddress`, `dropoffAddress`, `status`, `failureReason` | Управляет назначением курьера и выполнением доставки |
| `DeliveryOffer` — aggregate root | `id`, `deliveryId`, `courierId`, `offeredAt`, `expiresAt`, `state` | Ограниченное по времени предложение одному курьеру |

Состояния `Courier`: `available`, `busy`, `unavailable` (вне линии, не получает новых назначений). Состояния `Delivery`: `waiting`, `assigned`, `pickedUp`, `delivered`, `failed`, `cancelled`. В `DeliveryState` контракта `nomnom.delivery.v1` значения для `cancelled` пока нет: отменённая доставка не бывает текущей доставкой курьера. Состояния `DeliveryOffer`: `pending`, `accepted`, `rejected`, `withdrawn`, `expired`. Pending offer после `expiresAt` уже закрыт для принятия и отклонения; переход `Expire` (только из `pending` и только при `now > expiresAt`) сохраняет это как `expired`. Повторный `Expire` — `AlreadyApplied`, `Expire` из других состояний и `Accept`/`Reject`/`Withdraw` из `expired` — `Invalid`.

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

Application handler загружает `Delivery` и `DeliveryOffer`, меняет занятость `Courier` через порт `CourierAssignment` и сохраняет всё вместе с записью Outbox в одной границе транзакции. Проверки допустимости назначения и переходов состояния выполняются их доменным поведением.

### Value objects

| VO | Значение и инварианты |
| --- | --- |
| `Address` | Непустая нормализованная строка; на первом этапе без геокодирования и отдельных полей адреса |
| `DeliveryFailureReason` | Код закрытого каталога `DeliveryFailureCode` — `customer_unreachable`, `address_not_found`, `no_access`, `customer_refused`, `order_damaged`, `courier_incident`, `other` — и необязательное пояснение (`detail`): пробелы нормализуются, не длиннее 500 символов. Неизвестный или пустой код — ошибка валидации, а не `other`. Строки, записанные до каталога свободным текстом, читаются как `other` с прежним текстом в `detail`. Retryability кодов не классифицируется |
| `Location` | Широта в [-90, 90] и долгота в [-180, 180], конечные значения; последняя позиция курьера хранится вне aggregate |

Идентификаторы и статусы пока остаются типизированными значениями и перечислениями, а не отдельными value objects. Маршрут, зоны и расчёт расстояния не входят в модель: расстояние до pickup, принадлежность зоне и число активных назначений поставляет порт `CourierLocator`.

### Domain policies и Specification Pattern

Переиспользуемые eligibility rules вне state реализованы как:

- `CourierEligibleForDelivery` — зона, расстояние, capacity и наличие активных назначений;
- `DeliveryOfferAcceptable` — срок действия offer, расстояние до pickup и лимиты активных назначений.

`Courier.status` и `Delivery.status` являются business state: допустимость назначения, pickup и completion остаётся только в FSM/domain behavior и не дублируется в specs. Application загружает `Courier` и `Delivery`, получает координаты, `now` и assignment snapshot и передаёт их domain service значениями. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## Dependency Injection

В `delivery` граф зависимостей описывается модулями [`shaku`](https://github.com/AzureMarker/shaku), которые проверяются при компиляции. Handlers по-прежнему получают зависимости явно (`&dyn Trait`) и создаются в тестах без контейнера.

- `slices/<slice>/di` — shaku-модуль слайса: интерфейс внешних портов (`CourierPorts`, `DeliveryPorts`; хранилище `DeliveryPorts` видно только ролями `TransactionalStore` — Unit of Work, репозитории, занятость курьера, Outbox — и `DeliveryReadModels`), интерфейс use cases (`CourierUseCases`, `DeliveryUseCases`) и их реализация, которая собирает handler на каждый вызов. Порты слайс получает через submodule (`CourierDependencies`, `DeliveryDependencies`).
- `di` — корневой composition root, по модулю на процесс:
  - `di::infrastructure` — `InfrastructureModule`: реализации портов обоих слайсов (роли одного хранилища `ProcessStores`: `CourierStore`, `TransactionalStore`, `DeliveryReadModels`; генератор id, locator курьеров, одна авторизация, один writer relationships, `SystemClock`, `AssignmentPolicy`); реализации приходят параметрами компонентов, memory-адаптеры — только в тестовом графе;
  - `di::service` — модули слайсов над `InfrastructureModule` и итоговый `ServiceModule` (use cases); `di::build_over` и тестовые графы — над PostgreSQL (в unit-тестах `TestStore`) с тестовыми адаптерами `infrastructure::memory`. Транспорт получает use cases через `ServiceModule::couriers()` / `deliveries()` и shaku не импортирует;
  - `di::server` — `ServerModule` процесса `delivery`: `compose` выбирает `SpiceDbAuthorization` с revisions из хранилища (роль `RevisionReader`), `SpiceDbRelationships`, `PostgresStore` с mapper Integration Events, `UuidIdGenerator` (UUIDv7, FR-541), `StoredCourierLocator` (последние позиции доступных курьеров, FR-542) и resolver service identity; каждая роль хранилища разрешается отдельно над одним `Arc<PostgresStore>`; компоненты `TonicTransport` (use cases, identity, бизнес-порт), `SpiceDbReconciliation` (граф SpiceDB и то же хранилище в роли `ReconciliationStore`), `UseCaseOfferExpiry` и `UseCaseDeliveryDispatch` (фоновые задачи над тем же графом use cases), provider `DeliveryWorkerProvider` — gRPC worker с reconciliation, истечением offers и Dispatch Deliveries;
  - `di::relay` — `RelayModule` процесса `delivery-relay`: компоненты `PostgresRelayOutbox` (`PgOutbox`), `KafkaRelayPublisher`, `OtlpTelemetry`, `OutboxSettings`, provider `RelayWorkerProvider`; `RelayModule::shutdown` после drain дожидается Kafka и выгружает метрики;
  - `di::migrate` — `MigrateModule` процесса `delivery-migrate` с компонентом `PostgresMigration`.
- `cmd/*` — загружают конфигурацию (невалидная завершает процесс до открытия портов), вызывают `compose` своего модуля, открывают технический порт `pkg::lifecycle::Lifecycle`, затем получают worker из модуля и передают его lifecycle.

Ресурсы: клиенты SpiceDB и Kafka, пулы PostgreSQL, resolver identity и MeterProvider создаёт `compose` один раз на процесс, ими владеет модуль; транзакция живёт один use case (Unit of Work хранилища); worker создаётся provider'ом один раз после открытия технического порта, поэтому бизнес-порт открывается последним, а instruments relay берутся у уже запущенной telemetry. Shaku-компоненты собираются синхронно и без ошибок, поэтому подключение к инфраструктуре (fail fast, `async` для relay) выполняет `compose`, а не компонент. `Lifecycle` (технический порт, сигналы, drain) остаётся в `cmd`: это runtime contract процесса, а не зависимость use cases.

Shaku импортируют только модули `di`; `domains/`, `applications/`, `infrastructure/` и `cmd/` от него не зависят. Shaku требует `Send + Sync` от интерфейсов и создаёт отдельный экземпляр на каждый компонент, поэтому порты, которые обслуживает одно хранилище, собраны в интерфейс-агрегатор слайса. Generic `UnitOfWork` передаётся через object-safe `TransactionScope` и адаптер `ScopedUnitOfWork` из `pkg/application`.

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
| `DeliveryFailed` | `nomnom.delivery.v1.DeliveryFailed`: `reason` — код каталога (до каталога — свободный текст), `detail` (поле 4) — пояснение, пустое, если не указано |
| `CourierUnassignedFromDelivery` | `nomnom.delivery.v1.CourierUnassignedFromDelivery` (курьер снят без замены) |
| `DeliveryCancelled` | `nomnom.delivery.v1.DeliveryCancelled` (с `reason`, пустой — причина не указана) |

`orders` удаляет `order#delivery` по `DeliveryCancelled` — use case Record Courier Release; доступ курьера к заказу отзывает сам `delivery` через `delivery#courier`.

Mapper — явный allowlist: новый Domain Event не публикуется, пока для него нет контракта (match без wildcard не скомпилируется). Все события одной транзакции получают один `aggregate_version`; `event_id` различается через `event_type` (ADR 31).

`delivery-relay` (`cmd/delivery-relay`, тот же образ, runtime contract `pkg/lifecycle`) — polling relay ADR 31:

- **single active instance** — session-level `pg_try_advisory_lock` на выделенном соединении; реплики без lock не читают Outbox, потеря соединения снимает lock. Перед публикацией каждой записи relay проверяет владение lock, а не только живость сессии (FR-537): `pg_locks` должен показывать advisory lock ключа `RELAY_LOCK_KEY` (`classid`/`objid` из bigint-ключа), выданный именно этому backend (`pg_backend_pid()`). Lock-соединение выделено вне пула и ограничено по времени: на сессии `statement_timeout=2000` мс, клиент ждёт проверку не дольше `LOCK_CHECK_TIMEOUT` (2 s), server-side TCP keepalives (`tcp_keepalives_idle=10`, `interval=5`, `count=3`, `tcp_user_timeout=25000` мс) дают PostgreSQL закрыть сессию исчезнувшего клиента и освободить lock для standby; установка соединения и захват ограничены `DATABASE__ACQUIRE_TIMEOUT`. Ошибка или таймаут проверки (в том числе half-open соединение) означают потерю lock: relay останавливает пачку до следующей записи, пропускает retention, сбрасывает lock-соединение и становится standby до повторного захвата в следующем цикле;
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

Relay также требует `DATABASE__URL`. Метрики (ADR 13, [инвентарь](../../docs/observability/metric-inventory.md#outbox)): `nomnom_outbox_pending_messages` и `nomnom_outbox_oldest_pending_age_seconds` (label `stream`; после каждого цикла запрос backlog возвращает только streams с неопубликованными записями, поэтому stream, о котором gauge уже сообщал, и topic Outbox с самого старта при пустом backlog получают `0` pending и `0` age, а не последнее значение), `nomnom_outbox_publish_retries_total` (`stream`, `event_type`, `error_class`), histograms `nomnom_outbox_publish_duration_seconds` (`outcome`) и `nomnom_outbox_publish_lag_seconds` (`occurred_at` → ack). Экспорт OTLP gRPC (`pkg/telemetry`) включается только при заданном `OTEL_EXPORTER_OTLP_ENDPOINT` (или `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`); `service.name` — `delivery-relay`, `service.namespace` — `nomnom`.

Бизнес-метрики процесса `delivery` (раздел domain [инвентаря](../../docs/observability/metric-inventory.md), имена — `ops/semconv/model/domain.yaml`): `slices/delivery/infrastructure/observability` — декоратор портов Unit of Work и Outbox (`ObservedOutbox`; в `di::infrastructure` роль `TransactionalStore` слайса закрывает `ObservedStore`, так что Unit of Work и Outbox handlers идут через декоратор). Domain Events, переданные в Outbox внутри Unit of Work, записываются только после commit внешней Unit of Work: rollback и идемпотентный повтор без новых событий ничего не меняют. `DeliveryCreated` → `nomnom.delivery.created`; `CourierAssignedToDelivery` → `assignments` и `assignment.latency` (от `created_at`); `CourierUnassignedFromDelivery` → `reassignments` (`reason=other`: доменной причины снятия нет); `DeliveryOfferAccepted`/`DeliveryOfferRejected`/`DeliveryOfferExpired` → `offers` (`outcome` accepted/rejected/expired); `DeliveryPickedUp` → `pickups`; `DeliveryCompleted` → `completed` и `duration` (от `created_at`); `DeliveryFailed` → `failed` (`reason` — код каталога Delivery Failure Reason; `detail` никогда не становится атрибутом метрики). Каждый закоммиченный `DeliveryFailed` также пишется одной JSON-записью в stdout (`slices/delivery/infrastructure/failure_log.rs`: общая схема логов, `severity=INFO`, `operation=delivery.failure_reported`, `delivery_id`, `order_id`, `courier_id`, `reason`, `detail`, `trace_id`/`span_id` span use case `report_delivery_failure`) и ставит на активный span атрибуты `nomnom.delivery.failure.reason` и `nomnom.delivery.failure.detail` (до 256 символов). `DeliveryCreated` и события offer не имеют контракта и не публикуются. `service.name` — `delivery`.

### Observability

Контракт — [docs/observability.md](../../docs/observability.md); pipeline — `pkg/telemetry`, по образцу `orders/pkg/telemetry`. Все процессы (`delivery`, `delivery-relay`, `delivery-migrate`) используют один resource: `service.namespace=nomnom`, `service.name` (имя процесса или `OTEL_SERVICE_NAME`), `service.version`, `service.instance.id`, `deployment.environment.name` из `OTEL_RESOURCE_ATTRIBUTES` (Helm: `telemetry.*`, pod name и image tag).

| Сигнал | Что есть |
| --- | --- |
| Traces | W3C `traceparent`/`tracestate` (baggage не передаётся). TracerProvider с batch OTLP gRPC exporter — только при `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`. Server span `package.Service/Method` на каждый gRPC-вызов (`infrastructure/grpc/tracing.rs`, продолжает входящий trace); span `usecase <name>` на каждый use case (`slices/<slice>/infrastructure/observed_use_cases.rs`); client span и `traceparent` в metadata на каждый вызов SpiceDB; client span PostgreSQL на statement внутри trace (`db.system.name`, `db.operation.name`, `db.collection.name`, без текста и параметров); Outbox хранит `traceparent` span'а, записавшего событие: gRPC server span продолжает входящий `traceparent` и остаётся текущим контекстом, пока handler синхронно выполняет Unit of Work, а запись Outbox (`infrastructure/postgres/outbox.rs`) добавляет его `traceparent` в `metadata` строки (уже заданный в `metadata` не перезаписывается; без span — не добавляется; `tests/outbox_tracing.rs`); relay публикует в producer span `send <topic>` (продолжение того trace) и передаёт в Kafka header `traceparent` producer span'а. |
| Metrics | При `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`: `rpc.server.call.duration` и `rpc.client.call.duration` (`rpc.system.name`, `rpc.method`, `rpc.response.status_code` — имя кода; client — `server.address`, `server.port`, `error.type`), `nomnom.usecase.duration` (`nomnom.usecase.name`, `nomnom.usecase.kind`, `nomnom.outcome`, `error.type`), `db.client.operation.duration` и `db.client.connection.*` пула, `process.cpu.time`, `process.memory.usage`, `process.thread.count` (procfs), метрики Outbox и бизнес-метрики. Длительности — явные buckets в секундах. |
| Logs | Одна JSON-запись на строку в stdout (`pkg/telemetry/log.rs`, `tracing`): `timestamp` (UTC), `severity`, `message`, поля resource, `trace_id`/`span_id` только внутри span, `operation`, `outcome`, `error.type`, `error.message`. Библиотеки — от `WARN` (`sqlx` — `ERROR`). Токены, ключи, payload и bind parameters не логируются. |

Классификация use cases: `rejected` — `permission_denied`, `invalid_argument`, `not_found`, `already_exists`, `failed_precondition`; `error` — `concurrent_modification`, `unavailable`, `transaction`, `internal`. `Telemetry::shutdown` выгружает spans и точки после drain.


Integration-тест `tests/outbox_relay.rs` (запись основного процесса → relay → настоящий Kafka, проверка key, headers и payload; advisory lock; retention) поднимает PostgreSQL и Kafka (`apache/kafka:4.4.0`, как в ops) через testcontainers; `tests/outbox_relay_lock.rs` (проверка видит только lock своей сессии; `pg_terminate_backend` и half-open lock-соединение замечаются за `LOCK_CHECK_TIMEOUT` до следующей записи, standby захватывает lock) и `tests/outbox_tracing.rs` (`traceparent` gRPC-команды в `metadata` строки Outbox) — PostgreSQL:

```bash
DOCKER_CONFIG=$(mktemp -d) cargo test --test outbox_relay --test outbox_relay_lock
``` Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

## Observability

Инструментирование Rust транспорта, хранилища, Kafka и внешних клиентов размещается в `infrastructure/`, а lifecycle SDK и profiler — в bootstrap процесса. Rust profiling зависит от выбранного client/backend и целевой платформы и проверяется до добавления зависимости. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализованы:

- доменная модель: `Courier` (включая `unavailable`), `Location`, табличные FSM `Delivery` (6 states × 6 triggers, включая `Cancelled`/`Cancel`) и `DeliveryOffer` (4 × 3) с тестами полноты, domain events `DeliveryOffered`, `CourierAssignedToDelivery`, `CourierUnassignedFromDelivery`, `DeliveryPickedUp`, `DeliveryCompleted`, `DeliveryFailed`, `DeliveryCancelled`; policies `CourierEligibleForDelivery` и `DeliveryOfferAcceptable`; контракты репозиториев рядом с моделями;
- application handlers всех 17 use cases в `slices/<slice>/applications/<usecase>/mod.rs` с unit-тестами;
- порты: `UnitOfWork`, `Clock`, `IdGenerator`, `Subject`/`AuthorizationError` (`pkg/application`), `RepositoryError` (`pkg/persistence`), `CourierAuthorization` и `DeliveryAuthorization`, порт занятости курьера `CourierAssignment`, `DeliveryOutbox`, `CourierLocator`, `CourierLocationStore`, read models `CourierCurrentDeliveryReadModel` и `PendingDeliveryOffersReadModel`, параметры `AssignmentPolicy`;
- `ServiceTokenSubjectResolver`: service identity по projected ServiceAccount token (ADR 27) и user identity по assertion BFF (ADR 32);
- `SpiceDbAuthorization`: SpiceDB-адаптер `CourierAuthorization` и `DeliveryAuthorization` (ADR 18, FR-435), выбирается при заданном `SPICEDB__ENDPOINT`;
- `PostgresStore` (`infrastructure/postgres`): все порты хранилища на `sqlx`, optimistic concurrency по `version`, Unit of Work, Outbox ADR 31, миграции `delivery-migrate`;
- публикация Integration Events: mapper `DeliveryIntegrationEvents` (4 события `events.proto`) и процесс `delivery-relay` (`infrastructure/outbox`: advisory lock, head-of-line, retention, `rdkafka`, OTLP-метрики ADR 13);
- `UuidIdGenerator` (`infrastructure/ids.rs`): UUIDv7-идентификаторы `Delivery` и offer, уникальные между перезапусками и репликами (FR-541);
- `StoredCourierLocator` (`infrastructure/locator.rs`): кандидаты — доступные курьеры с последней позицией из `CourierLocationStore` (роль `CourierPositions`, FR-542). Pickup не геокодируется, поэтому курьер с известной позицией считается в зоне на расстоянии 0, и среди равных выбор решает id курьера;
- тестовые адаптеры не-storage портов в `infrastructure/memory` (статическая authorization, запись relationships, управляемые clock и id generator, статический locator) — только для тестов; хранилище в тестах — PostgreSQL (testcontainers, `TestStore`).

Не реализованы: Kafka consumers и Inbox, расчёт геопозиции/зон, BDD `.feature`. По gRPC опубликованы все use cases, кроме Find Courier, Offer Delivery и Reassign Courier (шаги процесса назначения) и фоновых Expire Delivery Offers и Dispatch Deliveries.

Момент начала поиска курьера решён Dispatch Deliveries (см. выше). Допущения по остальным открытым продуктовым вопросам (retryability причин неудачи, параметры позиции и политики назначения) зафиксированы в [design.md](../../openspec/changes/implement-delivery-application-layer/design.md#допущения-по-открытым-вопросам) change.

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
