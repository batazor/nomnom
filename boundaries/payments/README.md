# Payments

## Назначение

Контекст `payments` отвечает за выполнение платёжных операций, необходимых для заказа.

## Язык

Python.

Стратегия unit, BDD acceptance, integration и system E2E проверок описана в [общей документации тестирования](../../docs/testing.md).

## Данные и состояния

Контекст владеет запросами и результатами авторизации, списания и возврата, а также состояниями выполнения платёжных операций.

## Границы ответственности

`payments` выполняет и отслеживает платёжные операции. Он не определяет состав, стоимость и общий жизненный цикл заказа, не управляет приготовлением или доставкой; результаты операций передаются в `orders` для отражения в состоянии заказа.

## Use Cases

Статус реализации: application handlers всех перечисленных сценариев реализованы change [`implement-payments-application-layer`](../../openspec/changes/implement-payments-application-layer/) поверх портов и проверены тестами поверх реального PostgreSQL (testcontainers); хранилище — PostgreSQL (`infrastructure/repository`, [локальный ADR 1](docs/adr/0001-use-postgresql-for-payments-storage.md)), provider adapters ещё не реализованы (см. [Статус реализации](#статус-реализации)).

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Create Payment** | Fulfillment process `orders` | Создать локальное платёжное намерение для заказа | Создан идемпотентный `Payment`, связанный с заказом, суммой и валютой | Вызывается через API `payments`; не определяет правила жизненного цикла `Order` |
| **Authorize Payment** | Fulfillment process `orders` | Зарезервировать сумму для последующего Capture | Authorization успешно зафиксирована либо сохранён её отказ | Payment provider; обязательный idempotency key; результат публикуется через Outbox |
| **Capture Payment** | Fulfillment process `orders` | Списать ранее авторизованную сумму | Capture успешно зафиксирован либо сохранён его отказ | Payment provider; идемпотентность операции; результат публикуется через Outbox |
| **Cancel Authorization** | Fulfillment process `orders` | Освободить авторизованные средства, когда Capture больше не требуется | Authorization отменена либо сохранён отказ провайдера | Payment provider; идемпотентность операции; не отменяет `Order` |
| **Refund Payment** | Fulfillment process `orders` или авторизованный оператор | Вернуть поддерживаемую сумму ранее выполненного Capture | Refund успешно зафиксирован либо сохранён его отказ | Payment provider; идемпотентность операции; текущая минимальная модель поддерживает полный Refund, partial refund остаётся открытым вопросом |
| **Apply Provider Callback** | Payment provider | Идемпотентно применить webhook/callback к известной операции | Состояние `Payment` или операции обновлено не более одного раза, бизнес-результат опубликован при изменении | Проверка подлинности provider callback и отдельная дедупликация; callback не является Kafka event |
| **Apply Provider Operation Result** | Асинхронный адаптер payment provider | Применить полученный вне исходного запроса результат операции | Результат сопоставлен с локальной операцией и применён не более одного раза | Корреляция и идемпотентность provider operation; результат публикуется через Outbox |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Payment Status** | Fulfillment process `orders`, клиент или авторизованный оператор | Получить текущее разрешённое представление оплаты | Возвращено состояние `Payment` без изменения данных | Query model `payments`; чувствительные provider data не раскрываются |
| **List Payment Operations** | Авторизованный оператор или разрешённый пользовательский сценарий | Получить историю операций по `Payment` | Возвращён разрешённый список попыток и результатов без изменения состояния | Query model `payments`; назначение диагностики не ослабляет authorization |

`payments` отвечает только за допустимость и результат финансовой операции. Решение, следует ли отменять или продолжать `Order`, остаётся в fulfillment process `orders`.

## Структура слайсов

Слайсы `payments` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста): модули находятся в `slices/<slice>/`, use cases — в `slices/<slice>/applications/<usecase>/`, shared packages — в `pkg/`. Доменные модели платёжных операций не зависят от web-фреймворка, моделей хранения или Dependency Injector. Клиенты платёжных провайдеров и транспортные обработчики реализуются как инфраструктурные адаптеры, а их необходимые конкретному сценарию порты располагаются в `applications/`.

## gRPC-адаптер

`payments` владеет API `nomnom.payments.v1` и публикует его как `buf.build/nomnom/payments`. Исходные messages находятся в `infrastructure/grpc/nomnom/payments/v1/payments.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер генерирует серверные Python stubs из локальной схемы и преобразует request messages в прикладные команды или запросы, а результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделью `Payment` или handlers. Модули `infrastructure/grpc` разделены по ответственности (FR-550): `servicer.py` — servicer и маппинг Protobuf, `errors.py` — таблица application errors → gRPC status codes, `worker.py` — lifecycle сервера, `generated.py` — загрузка сгенерированных stubs; `service.py` только реэкспортирует имена, которые импортируют тесты. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API:

| Ошибка `payments` | gRPC code | `ErrorInfo.reason` |
| --- | --- | --- |
| `Payment` не найден | `NOT_FOUND` | `PAYMENT_NOT_FOUND` |
| Некорректная сумма или запрос операции | `INVALID_ARGUMENT` | `INVALID_PAYMENT` |
| Текущее состояние запрещает авторизацию, списание или возврат | `FAILED_PRECONDITION` | `PAYMENT_STATE_CONFLICT` |
| `Payment` заказа уже существует с другими условиями | `ALREADY_EXISTS` | `PAYMENT_ALREADY_EXISTS` |
| Неожиданная внутренняя ошибка | `INTERNAL` | `INTERNAL_ERROR` |
| Платёжный провайдер недоступен | `UNAVAILABLE` | `PAYMENT_PROVIDER_UNAVAILABLE` |

Остальные application errors: `PaymentOperationNotFoundError` — `NOT_FOUND`, `IdempotencyKeyConflictError` — `FAILED_PRECONDITION`, `OperationInProgressError` — `ABORTED` (повтор с тем же ключом после завершения начатой попытки), `PermissionDeniedError` — `PERMISSION_DENIED`, `AuthorizationUnavailableError` — `UNAVAILABLE` (fail closed), `ProviderResultConflictError` и `ConcurrencyConflictError` — `ABORTED`.

Командные RPC для Order Fulfillment Workflow `orders` (FR-427):

| RPC | Идемпотентность | Ответ |
| --- | --- | --- |
| `CreatePayment` | по `order_id`: повтор с теми же условиями возвращает существующий `payment_id` и `created=false` | `payment_id`, `state`, `created` |
| `AuthorizePayment`, `CapturePayment`, `CancelAuthorization`, `RefundPayment` | по `idempotency_key`: повтор возвращает ту же операцию с `replayed=true` без повторного вызова провайдера (AC-453) | `PaymentOperation`: `operation_id`, `status`, `payment_state`, `decline_reason`, `replayed` |

`PaymentOperation.status`: `SUCCEEDED`, `DECLINED` (бизнес-отказ провайдера — обычный ответ, не gRPC-ошибка) или `PENDING` (провайдер ответит асинхронно; повторять с тем же `idempotency_key`). Provider references не раскрываются.

Transport adapter уже маппит application errors в gRPC status codes; добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.payments.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). `GetPaymentStatus` и командные RPC подключены к application handlers через grpcio adapter; `entrypoints.payments.main` запускает gRPC-сервер и завершает его через общий lifecycle. Subject вызывающего определяется проверкой service identity (см. [Service identity](#service-identity)).

## Service identity

Синхронные вызовы других контекстов (Activities Order Fulfillment Workflow `orders-worker`) аутентифицируются Kubernetes projected ServiceAccount token по [ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md) (FR-431, FR-432). `ServiceTokenSubjectResolver` (`infrastructure/grpc/identity.py`) реализует порт `SubjectResolver`: читает metadata вызова и соединяет не зависящие от транспорта `ServiceTokenVerifier` (`service_token.py`) и `UserAssertionVerifier` (`user_assertion.py`). Каждый вызов проверяется по общему контракту [design](../../openspec/changes/add-service-identity/design.md):

| Проверка | Отказ |
| --- | --- |
| metadata `authorization: Bearer <jwt>` (ровно одно значение) | нет metadata или другой scheme |
| header `alg` ∈ {`RS256`, `ES256`}, `kid` есть в JWKS и совпадает по алгоритму | `none`, `HS*`, прочие алгоритмы, нет или неизвестный `kid` |
| подпись ключом JWKS (PyJWT с явным списком алгоритмов) | неверная подпись |
| `iss` == `IDENTITY__ISSUER` | иначе |
| `aud` (строка или массив) содержит `IDENTITY__AUDIENCE` | token другого контекста (AC-460) |
| `exp` > now − 30 s, `nbf` ≤ now + 30 s | просрочен / ещё не действителен |
| `sub` = `system:serviceaccount:<ns>:<sa>` есть в `IDENTITY__SERVICE_ACCOUNTS` | неизвестный ServiceAccount |

Результат — `Subject(service, <service-name>)` из allowlist; что разрешено этой identity, решает authorization в use case. Любой отказ — `UNAUTHENTICATED` с сообщением `invalid caller identity`; конкретная причина пишется в log (`WARNING`), сам token не логируется.

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `IDENTITY__ISSUER` | пусто | Issuer токенов кластера (`/.well-known/openid-configuration`). |
| `IDENTITY__AUDIENCE` | пусто | Audience контекста; в кластере `nomnom.payments`. |
| `IDENTITY__JWKS_FILE` | пусто | Путь к JWKS кластера (`/openid/v1/jwks`); в Helm chart — `/etc/nomnom/identity/jwks.json`. |
| `IDENTITY__SERVICE_ACCOUNTS` | пусто | Allowlist через запятую: `namespace/serviceaccount=service-name`, например `nomnom/orders-worker=orders-worker`. |

`IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` и `IDENTITY__JWKS_FILE` обязательны (AC-462). Ошибки startup (процесс завершается до открытия портов): пустые значения identity, нечитаемый или невалидный JWKS либо JWKS без ключей `RS256`/`ES256` с `kid`, пустой allowlist при заданном issuer, неверный формат или дубликаты allowlist. JWKS читается один раз при старте: после ротации signing key кластера нужен restart pod.

Совместимость с реализациями других контекстов закреплена общими векторами `tests/identity/fixtures`: `tests/test_service_identity.py` принимает только `valid` и `valid_string_aud` (AC-461) и проверяет вызов по gRPC до handler (AC-459). Время проверки `exp`/`nbf` инжектируется (`clock`) для тестов допуска. Helm values `identity.*` описаны в [ops/README.md](ops/README.md#service-identity).

### User identity

Пользователя передаёт только BFF: вместе с собственным service token он присылает metadata `x-nomnom-user-assertion` — ES256 JWT, подписанный ключом BFF ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md), FR-471–FR-473). `ServiceTokenSubjectResolver` проверяет вызов по порядку [design](../../openspec/changes/add-user-identity-assertions/design.md):

1. service token, как выше; отказ — `UNAUTHENTICATED`, assertion не рассматривается;
2. нет `x-nomnom-user-assertion` — `Subject(service, <service-name>)`, как раньше;
3. assertion повторяется или пуст, user assertions не настроены или service не входит в `IDENTITY__USER_DELEGATES` — `UNAUTHENTICATED` (AC-526);
4. `UserAssertionVerifier`: `alg` только `ES256`, `kid` есть в JWKS BFF (EC P-256), `iss` == `IDENTITY__USER_ISSUER`, `aud` содержит `IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s, `iat` обязателен и `exp − iat ≤ 60 s`, `sub` непуст и без пробелов по краям, `jti` непуст.

Результат — `Subject(user, <sub>)` с `delegate` = имя service BFF (для log и audit, в сравнении и authorization не участвует). Принятие (`INFO`) и отказ (`WARNING`) пишутся в log с `delegate` и `jti`; токены не логируются.

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `IDENTITY__USER_ISSUER` | пусто | Ожидаемый `iss` assertion; в кластере `nomnom-bff`. |
| `IDENTITY__USER_JWKS_FILE` | пусто | Путь к JWKS BFF; в Helm chart — `/etc/nomnom/user-identity/jwks.json`. |
| `IDENTITY__USER_DELEGATES` | пусто | Имена services через запятую, которым разрешено передавать user; в кластере `bff`. |

User assertions принимаются, только если заданы все три переменные; иначе любой вызов с assertion — `UNAUTHENTICATED`, а вызовы без него обрабатываются как раньше (AC-529). Ошибки startup: заданный, но нечитаемый JWKS BFF либо JWKS без ключей `ES256` P-256 с `kid`, неверный формат или дубликаты `IDENTITY__USER_DELEGATES`. Тесты — `tests/test_user_identity.py` на `user-vectors.json` с часами, зафиксированными на `now`.

## Authorization

Порт `PaymentPermissionChecker` (`slices/payment/applications/ports.py`) реализует `SpiceDBPermissionChecker` (`infrastructure/authorization/spicedb.py`) на официальном клиенте `authzed` по [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md) и нормативной таблице [design](../../openspec/changes/add-spicedb-authorization/design.md) (FR-435). Schema и grants развёртывания — `authorization/schema.zed` и `authorization/bootstrap.yaml`.

| `PaymentPermission` | SpiceDB `CheckPermission` |
| --- | --- |
| `create`, `authorize`, `capture`, `cancel_authorization`, `refund` | `platform:nomnom#manage_payments` |
| `view`, `view_operations` | `platform:nomnom#view_payments` |

Subject: `user` → `user:<id>`, `service` → `service:<id>`. Consistency `minimize_latency`. Разрешает только `PERMISSIONSHIP_HAS_PERMISSION`; `NO_PERMISSION` и conditional permission — отказ (`PermissionDeniedError` → `PERMISSION_DENIED`). Любая ошибка gRPC, неверный ключ, недоступность или превышение `SPICEDB__TIMEOUT` — `AuthorizationUnavailableError` → `UNAVAILABLE` (fail closed, AC-466); причина пишется в log (`WARNING`).

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `SPICEDB__ENDPOINT` | пусто | gRPC endpoint `host:port`. Обязателен: пусто — ошибка startup. |
| `SPICEDB__PRESHARED_KEY` | пусто | Bearer preshared key; обязателен при заданном endpoint (иначе ошибка startup). В Helm — из Secret. |
| `SPICEDB__INSECURE` | `false` | Plaintext gRPC без TLS — только kind и локальный запуск. |
| `SPICEDB__TIMEOUT` | `2s` | Deadline одного `CheckPermission`, `<int>(ms|s|m)`, `> 0`. |

Unit-тесты маппинга и fail-closed поведения с fake клиентом — `tests/test_spicedb_authorization.py`. Integration-тесты `tests/test_spicedb_integration.py` против SpiceDB `authzed/spicedb:v1.57.0` в testcontainers (memory datastore, `authorization/bootstrap.yaml` plaintext, ключ `devkey`) проверяют, что `service:orders-worker` имеет все payment permissions (AC-465), `service:bff` — ни одного, а недоступный endpoint даёт `AuthorizationUnavailableError`.

Helm values `spicedb.*` описаны в [ops/README.md](ops/README.md#authorization-spicedb).

## Минимальная доменная модель

Для учебной реализации достаточно одного агрегата `Payment`, который последовательно отражает авторизацию, списание и полный возврат.

| Тип | Минимальные поля | Назначение |
| --- | --- | --- |
| `Payment` — aggregate root | `id`, `orderId`, `amount`, `status`, `providerReference` | Управляет состоянием платёжной операции для заказа |

Минимальные состояния `Payment`: `pending`, `authorized`, `captured`, `refunded`, `failed`. `providerReference` отсутствует до успешного ответа платёжного провайдера.

Инварианты:

- сумма платежа положительна;
- авторизовать можно только платёж в состоянии `pending`;
- списать можно только авторизованный платёж;
- вернуть можно только полностью списанный платёж;
- `refunded` и `failed` являются конечными состояниями (compound state `closed`); `fail` допустим только до Capture (compound state `uncaptured`: `pending`, `authorized`), см. [FSM](../../docs/fsm.md#payment);
- повторная обработка одного результата провайдера не меняет состояние повторно.

На первом этапе частичные списания, частичные возвраты и несколько попыток платежа для одного заказа не моделируются.

### Value objects

| VO | Значение и инварианты |
| --- | --- |
| `Money` | Точная десятичная сумма на основе стандартного [`decimal.Decimal`](https://docs.python.org/3/library/decimal.html) и код валюты; значение неотрицательно, а операции разрешены только для одной валюты |

`Money` создаётся из строки или другого точного представления, но не из `float`. Требуемые масштаб и режим округления явно задаются доменной операцией. Положительность суммы является инвариантом `Payment`: общий `Money` допускает ноль, но платёж — нет. Идентификаторы, `providerReference` и статус пока остаются типизированными значениями и перечислением, а не отдельными value objects.

### Domain policies и Specification Pattern

Переиспользуемые финансовые policies вне state реализованы как:

- `PaymentRefundable` — requested amount, доступный остаток к возврату и срок после Capture;
- `PaymentCapturable` — requested amount, доступный остаток Authorization и срок её действия.

Допустимость Capture только из `authorized` и Refund только из `captured` остаётся в FSM. Ограничения `captured ≤ authorized` и `refunded ≤ captured` являются aggregate invariants, а не specs. Application получает `now` и provider data и передаёт их значениями в `Payment`; authorization через SpiceDB остаётся отдельной application-проверкой. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## Dependency Injection

`payments` использует [Dependency Injector](https://python-dependency-injector.ets-labs.org/) (`dependency-injector>=4.49.1`): каждый исполняемый процесс имеет собственный `DeclarativeContainer` в пакете `di/` ([ADR 5](../../docs/adr/0005-dependency-injection-strategy.md), решение для `payments` — Accepted). Контейнер — единственное место, где выбираются реализации портов, собирается граф объектов и определяется владение ресурсами; `entrypoints/*/main.py` загружают settings, инициализируют и освобождают ресурсы контейнера и переводят ошибку в код выхода.

| Процесс | Контейнер | Ресурсы (`providers.Resource`) |
| --- | --- | --- |
| `payments` (gRPC API и provider webhooks в одном `WorkerGroup`) | `di/payments.py` — `PaymentsContainer` | telemetry providers, psycopg pool (перед ним — миграции), SpiceDB client |

- **Configuration.** `providers.Configuration(strict=True)` заполняется провалидированными pydantic settings через `di.resources.sections`: секции остаются типизированными объектами `pkg.config` (`config.identity`, `config.database`, …); секция `provider` хранится как `config.payment_provider`, потому что `Configuration.provider` — собственный атрибут провайдера.
- **Ресурсы и порядок.** `TelemetryResource` и `ProcessResource` (`di/resources.py`) — подтипы `providers.Resource`. Entrypoint вызывает `init_resources(TelemetryResource)`, затем `init_resources(ProcessResource)`, а в `finally` — `shutdown_resources(ProcessResource)` и последним `shutdown_resources(TelemetryResource)`: telemetry сбрасывается последней (runtime contract, шаг 8). Ресурс базы сначала применяет миграции, затем открывает и проверяет pool — всё до открытия портов. Authzed client не хранит ссылку на канал, поэтому его release пуст.
- **Use cases.** `PaymentsHandlers`, `PaymentOperationExecutor` и `ApplyProviderCallbackHandler` — `Singleton`/`Factory` провайдеры на процесс; зависимости передаются через конструкторы. Выбор реализаций — `create_subject_resolver` (identity), `create_permission_checker` (SpiceDB; ошибка старта без `SPICEDB__ENDPOINT`), `create_provider` (`PROVIDER__KIND`), `PaymentEventsMapper` — описан только в `PaymentsContainer`.
- **Границы.** `@inject` и wiring не используются; `slices/`, `pkg/` и `infrastructure/` не импортируют `di` и `dependency_injector`. Тесты собирают handlers напрямую с fakes (`tests/payment_fakes.py`) или через контейнер с `override()` (`tests/test_di_containers.py`: разрешение графа, подмена fakes, порядок ресурсов в entrypoints).

Runtime — Python 3.14 (`python:3.14.9-slim-bookworm`): Dependency Injector 4.49.1 официально поддерживает его. Обоснование и fallback (composition roots вручную, как в `bb45bc6`) — в ADR 5.

## Событийная интеграция

Consumers интеграционных событий `payments` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение состояния платёжной операции, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной локальной транзакции. Kafka offset подтверждается только после commit.

Inbox не защищает вызовы платёжного провайдера: авторизация, списание и возврат вне локальной транзакции требуют собственного idempotency key операции. Платёжные webhooks не являются Kafka integration events и получают отдельную идемпотентную boundary.

Unit of Work охватывает только локальные изменения: payment state, operation record и Outbox. Вызов провайдера выполняется вне транзакции, обычно между записью операции и фиксацией её результата; порядок определяется use case и idempotency policy — см. [документацию Unit of Work](../../docs/unit-of-work.md#payments) и [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md).

Хранилище `payments` — PostgreSQL ([локальный ADR 1](docs/adr/0001-use-postgresql-for-payments-storage.md)): таблица `inbox` с первичным ключом `(consumer, event_id)` и таблица `payments_outbox` записываются в той же локальной транзакции, что и `payments`/`payment_operations`. Provider callbacks дедуплицируются через `inbox` (consumer `payment-provider`). Kafka consumers ещё нет. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

### Публикация Integration Events

По [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) и [ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md) `payments` публикует в topic `payments.v1.payment-events` контракт [`nomnom/payments/v1/events.proto`](infrastructure/grpc/nomnom/payments/v1/events.proto): `PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded` (с `Money`), `PaymentFailed` (с `reason`). `PaymentOperationDeclined` — внутренний факт и не публикуется.

- `PaymentEventsMapper` (`infrastructure/messaging/events.py`) — явный allowlist Domain Event → Protobuf; контейнер `di/payments.py` передаёт его gRPC handlers и webhook worker, поэтому событие пишется в Outbox в той же транзакции, что и Payment.
- Публикует Debezium (ADR 37): коннектор `payments-outbox` в общем Kafka Connect читает вставки `payments_outbox` из WAL (publication и слот `payments_outbox`, только `INSERT`) и через Outbox Event Router пишет сообщение в topic из поля `topic`, key — `aggregate_id`, value — `payload` байт в байт. Кода публикации, relay-процесса и подключения к Kafka в `payments` нет. Конфигурацию коннектора держит Helm chart (`cdc.*`, [ops/README.md](ops/README.md#cdc)).
- Kafka headers прежние (`orders/pkg/messaging/envelope.go`), Debezium берёт их из полей записи: `event_id` и `_watermill_message_uuid` = `event_id`, `event_type`, `occurred_at` (RFC 3339, UTC), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent` (`''` без span). `tracestate` не хранится.
- Хранение: запись нужна только до чтения WAL. `DatabasePaymentOutbox` в транзакции каждой записи удаляет строки старше часа (`created_at`); publication удаления не публикует, фонового процесса нет.
- Доставка at-least-once, порядок по агрегату — порядок commit в WAL. Состояние коннектора — `GET /connectors/payments-outbox/status` Kafka Connect; запись, нарушающая контракт, останавливает коннектор (head-of-line).

## Observability

Инструментирование Python транспорта, PostgreSQL и платёжного провайдера размещается в `infrastructure/`; токены, платёжные реквизиты и payload провайдера исключаются из telemetry. OpenTelemetry и Pyroscope зависимости должны отдельно подтвердить поддержку целевого Python 3.14. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

Pipeline процесса — `pkg/telemetry` (контракт [docs/observability.md](../../docs/observability.md)); его поднимает `TelemetryResource` первым и сбрасывает последним.

| Сигнал | Что пишется | Где |
| --- | --- | --- |
| Resource | `service.namespace=nomnom`, `service.name` процесса (`payments`; `OTEL_SERVICE_NAME` переопределяет), `OTEL_RESOURCE_ATTRIBUTES` (`service.version`, `service.instance.id`, `deployment.environment.name`). Один resource для traces и metrics, те же поля в логах. | `pkg/telemetry/resource.py` |
| Propagation | W3C `traceparent`/`tracestate`; baggage — только ключи `BAGGAGE_ALLOWLIST` (сейчас пусто). Некорректный `traceparent` отбрасывается, начинается новый trace. | `pkg/telemetry/propagation.py` |
| Traces | `TracerProvider` + OTLP gRPC, `BatchSpanProcessor` (очередь 2048, batch 512, export timeout 3s) — только при `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`. Sampler `ParentBased(SkipOrphanClientSpans)`: client span без родителя (проверка пула) не пишется. | `pkg/telemetry/__init__.py` |
| gRPC | Server и client interceptors (`pkg/telemetry/rpc.py`): span `Service/Method`, `rpc.system.name`, `rpc.method`, `rpc.response.status_code` (имя кода); `rpc.server.call.duration` / `rpc.client.call.duration` (s). Client interceptor стоит на канале SpiceDB (`server.address`/`server.port`). `opentelemetry-instrumentation-grpc` не используется: у него нет RPC-метрик и старые attributes. | `infrastructure/grpc/worker.py`, `infrastructure/authorization/spicedb.py` |
| PostgreSQL | Cursor пула (`cursor_factory`): client span `{db.operation.name} {db.collection.name}` на statement только внутри trace и `db.client.operation.duration` (s, buckets semconv) на каждый statement — `db.system.name=postgresql`, `db.operation.name`, `db.collection.name`, при ошибке `error.type` (SQLSTATE или класс) и в span `db.response.status_code`. Текст statement и bind parameters не пишутся. `opentelemetry-instrumentation-psycopg` не используется: метрики только при opt-in, без `db.collection.name`, старые attributes spans. | `infrastructure/repository/statements.py` |
| Unit of Work | `nomnom.uow.transactions` и `nomnom.uow.duration` (s) на каждую попытку внешнего Unit of Work (`nomnom.outcome`: committed \| rolled_back \| commit_failed), `nomnom.uow.retries` (`nomnom.reason`: serialization \| deadlock \| write_conflict \| other); retry также — span event `nomnom.uow.retry` и WARN-лог. | `infrastructure/repository/transactions.py`, `infrastructure/repository/database.py` |
| FSM | `nomnom.fsm.transitions` перехода `Payment` (`nomnom.fsm.aggregate_type=payment`, `from_state`/`to_state` — `PaymentState`, `trigger` — `PaymentTrigger`, `result`: applied \| rejected, `terminal`); applied считается после commit внешнего Unit of Work, rejected (триггер запрещён таблицей, состояние не меняется) — сразу. Домен только копит `TransitionResult`; порт `PaymentTransitionObserver` реализует `FsmMetrics`. | `infrastructure/observability/fsm.py`, `infrastructure/observability/committed.py` |
| Authorization | `nomnom.authz.checks` и `nomnom.authz.check.duration` (s) на каждый `CheckPermission`: `nomnom.authz.permission` и `nomnom.authz.resource_type` SpiceDB (`manage_payments` \| `view_payments`, `platform`), `nomnom.authz.result`: allowed \| denied \| error \| timeout (`DEADLINE_EXCEEDED`). Subject и resource ID не пишутся. | `infrastructure/authorization/spicedb.py` |
| Webhook | Server span `POST /webhooks/provider` (`http.route`, `http.response.status_code`); trace context провайдера не продолжается. | `infrastructure/provider/http.py` |
| Kafka | Outbox сохраняет `traceparent` контекста записи события (`''` без span); Debezium передаёт его в header `traceparent` без изменений, consumers продолжают trace записи. Span отправки payments не пишет и Kafka не потребляет. | `infrastructure/repository/outbox.py` |
| Use cases | Декоратор `Handler` в DI: span `usecase <name>` и `nomnom.usecase.duration` (`nomnom.usecase.name`, `nomnom.usecase.kind`, `nomnom.outcome`, `error.type`); классификация ошибок — `infrastructure/observability/usecases.py`. | `pkg/telemetry/usecase.py`, `di/payments.py` |
| Provider | `nomnom.payments.provider.request.duration` (`nomnom.payment.operation`: authorize \| capture \| refund \| cancel; `nomnom.outcome`: success \| declined \| error \| timeout) и client span `payments.provider <operation>`. | `infrastructure/observability/provider.py` |
| Runtime | `process.cpu.time` (`cpu.mode`), `process.memory.usage`, `process.thread.count`, `cpython.gc.collections` (`cpython.gc.generation`) — только при `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`. Свои callbacks вместо `opentelemetry-instrumentation-system-metrics`: тот пишет устаревшие `type`/`generation`. | `pkg/telemetry/runtime.py` |
| Logs | Одна JSON-строка на запись в stdout для всех loggers (gRPC, lifecycle, SDK): `timestamp` (UTC), `severity` (`DEBUG`/`INFO`/`WARN`/`ERROR`/`FATAL`), `message`, `service.*`, `deployment.environment.name`, `logger`, `trace_id`/`span_id` только внутри span, поля операции из allowlist `extra`, `exception` (`type`, `message`, `stacktrace`). Bearer/JWT, пароли в DSN, `password=`/`secret=`/`token=` и номера карт (Luhn) заменяются на `[REDACTED]`. | `pkg/telemetry/logs.py` |

Histograms в секундах получают явные границы `SECONDS_BUCKETS` (как `orders`). Сбой или недоступность collector не влияет на запросы: export асинхронный, очередь ограничена, export timeout 3s укладывается в `SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT`.

## Статус реализации

Реализован in-memory агрегат `Payment`: точный `Money`, чистая табличная FSM, типизированные ошибки переходов, восстановление из persisted state и забор domain events (`PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded`, `PaymentFailed`). Также реализованы policies `PaymentRefundable` и `PaymentCapturable` с типизированными reasons и unit tests.

Реализован application-слой всех use cases по change [`implement-payments-application-layer`](../../openspec/changes/implement-payments-application-layer/) (`FR-400`…`FR-414`, `AC-400`…`AC-432`):

- `Payment Operation` (`domains/payment/operation.py`) — запись одной логической операции с idempotency key и FSM `pending → succeeded | declined`. Что делает каждый `OperationKind`, задаёт одна таблица `OPERATION_KINDS` (`domains/payment/operation_kinds.py`): право (`PaymentPermission`), trigger `Payment`, действие при успехе и поведение при отказе (отказ Authorization переводит `Payment` в `failed`, остальные отказы его не меняют). Доменный сервис `complete_operation` (`domains/payment/completion.py`) применяет результат provider по этой таблице; вид без правила — `UnknownOperationKindError`, `Payment` и операция не меняются (FR-549, AC-639). Бизнес-метрики (`infrastructure/observability/domain.py`) относят отказ к счётчику по той же таблице. Контракты repositories — в `domains/payment/repository.py`.
- Финансовые операции следуют схеме `UoW { pending operation } → provider call → UoW { result + Payment + Outbox }`. Повтор с тем же idempotency key возвращает сохранённый результат без provider call либо завершает незавершённую операцию повтором provider call с тем же ключом. У `Payment` не более одной незавершённой операции.
- Provider callbacks дедуплицируются по `provider_message_id` в той же Unit of Work, что и изменение состояния; асинхронный результат применяется не более одного раза.
- Порты: `UnitOfWork`, `Clock`, `Subject`, `AggregateRef` и общие application errors — в `pkg/application`; authorization (`PaymentPermissionChecker`), provider gateway, Outbox, provider message inbox и источники идентификаторов (`PaymentIdSource`, `PaymentOperationIdSource`; реализация `UuidIdentifiers` — `infrastructure/ids.py`) — в `slices/payment/applications/ports.py`. `PaymentOperationExecutor` получает `ProviderResultApplier` извне; контейнер `di/payments.py` создаёт один applier для синхронных результатов и provider callbacks. Handlers и порты синхронные.
- Тестовые fakes (провайдер, permissions, часы, идентификаторы) находятся в `tests/payment_fakes.py`; хранилище в тестах — реальные PostgreSQL-адаптеры.

Реализован runtime contract процесса по [Cloud Native Runtime](../../docs/cloud-native-runtime.md) и [ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md) в `pkg/lifecycle`:

- `Lifecycle(LifecycleConfig(...)).run(worker)` — технический порт → `worker.start()` → `worker.run()` в собственном потоке → ready → `SIGTERM`/`SIGINT` → not ready → `shutdown.readiness_delay` → `worker.shutdown(deadline)` в пределах `shutdown.drain_timeout` → закрытие технического порта последним. Exit `0` при штатном shutdown, `1` при ошибке (невалидная конфигурация, ошибка start, неожиданная остановка worker, превышение drain timeout). Повторный сигнал во время shutdown завершает процесс сразу.
- Технический порт `management.port` (`MANAGEMENT__PORT`, по умолчанию `9084`, отличается от бизнес-порта): `GET /health/startup`, `/health/live`, `/health/ready` с JSON `{"status":"ok"}` или `{"status":"unavailable","reason":"initializing|shutting_down"}`; liveness остаётся `200` во время shutdown, probes не логируются. Внешние зависимости в readiness не входят.
- `HttpWorker` — бизнес-порт на `http.server`: при shutdown прекращает приём, закрывает idle keep-alive соединения, дожидается начатых запросов до deadline и сообщает об ошибке, если они не завершились.

Длительности задаются как `<int>(ms|s|m)`: `SHUTDOWN__READINESS_DELAY` (по умолчанию `5s`, `>= 0`), `SHUTDOWN__DRAIN_TIMEOUT` (по умолчанию `20s`, `> 0`) и `SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT` (по умолчанию `5s`, `> 0`). Traces экспортируются через OTLP gRPC, только если задан `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`, metrics — `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`; после drain providers сбрасываются не дольше `SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT`.

Реализовано хранилище PostgreSQL в `infrastructure/repository` (psycopg 3, пул `psycopg_pool`):

- Схема — forward-only SQL-миграции, разделённые на наборы по хранимым данным: `infrastructure/repository/migrations/*.sql` (storage id `messaging`) — общие таблицы всех slices (`payments_outbox` с publication, `inbox`), рядом с их кодом `infrastructure/repository/outbox.py` и `repositories.py`; `slices/payment/infrastructure/repository/migrations/*.sql` (storage id `payments`) — таблицы slice `payment` (`payments`, `payment_operations`). Каталоги, пакеты и идентификаторы кода не называются по технологии хранения. Миграции slice меняют только его таблицы, внешних ключей между slices нет. Каждый набор нумерует файлы с `00001` и ведёт свои таблицы yoyo: `_yoyo_migration_<storage_id>` (применённые миграции), `_yoyo_log_<storage_id>` (журнал), `_yoyo_version_<storage_id>` (версия внутренней схемы yoyo) — сейчас `_yoyo_{migration,log,version}_messaging` и `_yoyo_{migration,log,version}_payments`. Storage id — часть контракта схемы БД: явный строковый литерал в `MigrationSet`, не выводится из имён пакетов, каталогов и slices и не меняется при рефакторинге кода; его смена переименовывает таблицы yoyo и требует миграции. Упорядоченный список наборов задан явно в одном месте — `MIGRATIONS` в `di/resources.py`: сначала `messaging`, затем slices; новый slice добавляет туда свой набор, автопоиска нет. Набор без файлов миграций — ошибка старта. Схема вшита в образ (каталоги `infrastructure` и `slices` копируются целиком) и применяется самим процессом `payments` при старте, до открытия портов, библиотекой [yoyo-migrations](https://ollycope.com/software/yoyo/latest/) (backend `postgresql+psycopg`, `infrastructure/repository/migrate.py`): все наборы — под одним session advisory lock (`pg_advisory_lock`) на отдельном соединении, затем открывается пул. Каждый файл применяется в своей транзакции, порядок внутри набора — по имени файла. Нечего применять — лог `none pending` и обычный старт, иначе лог перечисляет `<storage_id>/<migration>` применённых миграций; ошибка миграции завершает процесс с кодом 1. Отдельного шага миграций нет.
- Реплики, стартующие одновременно, ждут advisory lock, пока первая не применит миграции; остальные находят, что применять нечего. Lock держит server-сессия, поэтому процесс, убитый посреди миграции, освобождает его вместе с соединением. Собственный lock yoyo (строка `yoyo_lock`) не используется: строка убитого процесса осталась бы и блокировала остальных до ручного `yoyo break-lock`. Соединения yoyo открываются с `lock_timeout = 5s` (advisory lock — без него): `ALTER TABLE`, вставший в очередь за транзакцией живого трафика во время rolling update, не замораживает таблицу, а падает, транзакция файла откатывается, и pod повторяет миграцию после рестарта. Таблицы `payments`, `payment_operations`, `payments_outbox`, `inbox`.
- `DatabaseUnitOfWork` — одна локальная транзакция; repositories, Outbox и Inbox внутри `run` используют её соединение (context variable), вложенный `run` присоединяется. Вне Unit of Work каждый вызов берёт своё соединение из пула. Внешний `run` повторяет работу в новой транзакции при serialization failure (`40001`), deadlock (`40P01`), другом откате класса `40` и `ConcurrencyConflictError` ([ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md)): не больше `MAX_ATTEMPTS` (3) попыток, backoff 10 ms с удвоением, новая попытка не начинается позже 1 s от первой. Ошибка COMMIT не повторяется: результат неизвестен и разрешается идемпотентностью операции.
- Outbox `payments_outbox` — контракт записи [ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md): `event_id` (PK, UUIDv5 от `{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}`), `event_type` (полное имя Protobuf-сообщения), `aggregate_type`, `aggregate_id`, `aggregate_version`, `occurred_at`, `topic`, `payload` (Protobuf, `BYTEA`), `traceparent` (`NOT NULL DEFAULT ''`) и `created_at` для retention (индекс). Таблицу, индекс и publication создаёт миграция `infrastructure/repository/migrations/00001_outbox.sql`. Какие Domain Events публикуются, решает внедряемый `OutboxEventMapper` (`infrastructure/repository/outbox.py`); событие без контракта (mapper вернул `None`) в Outbox не пишется. `NoPublishedContracts` — mapper без опубликованных контрактов для тестовых сборок; контейнер `di/payments.py` передаёт `PaymentEventsMapper` — см. [Публикация Integration Events](#публикация-integration-events).
- Нарушения уникальности отображаются в доменные ошибки: `payments_order_id_key` → `DuplicatePaymentError`, `payment_operations_idempotency_key_key` → `DuplicateIdempotencyKeyError`, частичный уникальный индекс `payment_operations_one_pending` (`WHERE state = 'pending'`) → `OperationAlreadyPendingError`. Insert выполняется в savepoint, поэтому обработанная ошибка не ломает транзакцию. `save` обновляет строку только при совпадении `version`, иначе `ConcurrencyConflictError`.
- `DATABASE__URI` обязателен и принимается только как connection URI PostgreSQL (`postgresql://` или `postgres://`): пустое значение, форма libpq `key=value` или другая схема завершают процесс на старте; недоступная база — тоже (проверка соединения до открытия портов). Лимиты пула — `DATABASE__MAX_CONNS` (`10`), `DATABASE__MIN_CONNS` (`0`, допустимо 0–1), `DATABASE__MAX_CONN_LIFETIME` (`30m`, psycopg_pool закрывает соединение на 0–5% раньше — jitter), `DATABASE__MAX_CONN_IDLE_TIME` (`5m`), `DATABASE__CONNECT_TIMEOUT` (`5s`, `connect_timeout` libpq в целых секундах), `DATABASE__ACQUIRE_TIMEOUT` (`5s`, ожидание соединения пула, затем `PoolTimeout`); недопустимое значение — ошибка startup. См. [Пулер PostgreSQL](#пулер-postgresql).
- Репозитории содержат только `get`, поиск по уникальному натуральному ключу (`get_by_order`, `get_by_idempotency_key`), `add` и `save`. Проверка незавершённой операции в командах — application query-порт `PendingOperationQuery`, история операций — read-model порт `PaymentOperationHistory`; оба реализованы в `infrastructure/repository/queries.py`. Инвариант «не более одной pending-операции» остаётся на уровне БД.
- Тесты (`make test`, pytest) используют только реальную инфраструктуру: `tests/conftest.py` поднимает через testcontainers по одному контейнеру на сессию — `postgres:18.6-alpine3.24` (с миграциями) и `authzed/spicedb:v1.57.0`; классы подключают их через `@pytest.mark.usefixtures("database" | "spicedb")`. Переменных окружения тесты не требуют и не пропускаются; данные изолируются `TRUNCATE` перед каждым тестом. In-memory адаптеров хранилища нет; `tests/payment_fakes.py` содержит только fakes провайдера, permissions, часов и идентификаторов. Нужен Docker.

### Пулер PostgreSQL

Перед базой стоит пулер соединений, и он обязан работать в **session mode**: payments опирается на состояние server-сессии. psycopg автоматически готовит prepared statements после `prepare_threshold` выполнений; transaction mode ломает это: prepared statements окажутся на чужом server-соединении. Миграции берут session-level advisory lock на отдельном соединении — transaction mode отдал бы его чужому server-соединению. Unit of Work, savepoints и запросы Outbox живут внутри транзакции и от режима не зависят; `LISTEN/NOTIFY` и временные таблицы не используются.

Каждый процесс держит собственный пул (`DATABASE__*`), в session mode каждое его соединение занимает server-соединение пулера. Сумма replicas × maxConns должна помещаться в `pool_size` пулера для базы payments:

| Процесс | values | maxConns | minConns |
| --- | --- | --- | --- |
| payments (gRPC + webhooks) | `values.yaml` | 10 | 1 |
| миграции при старте `payments` | — | 1 (до открытия пула) | 0 |

При одной реплике это 10 server-соединений; соединение миграций закрывается до открытия пула. Debezium держит своё соединение репликации к базе (не через пулер). Пул экспортирует OTel semconv `db.client.connection.*` (`count` по `state`, `max`, `idle.min`, `pending_requests`, `timeouts`, `wait_time`, `use_time`; `db.client.connection.pool.name=payments`), инвентарь — [`docs/observability/metric-inventory.md`](../../docs/observability/metric-inventory.md#db).

Не реализованы: provider client, проверка подписи webhooks, Kafka consumers (Inbox для integration events) и остальные transport handlers. `GetPaymentStatus` и командные RPC работают через gRPC на бизнес-порту; контейнер `di/payments.py` требует identity, `SPICEDB__ENDPOINT`, `PROVIDER__KIND=fake` и `PROVIDER__WEBHOOK_SECRET` (см. [Authorization](#authorization)). Момент Authorization/Capture определяет оркестратор `orders`; partial capture/refund и сроки `PaymentCapturable`/`PaymentRefundable` не поддерживаются до продуктового решения.

## Запуск

```bash
make run  # payments: gRPC + provider webhooks
```

## Проверка

```bash
make check
```

Автоформатирование и безопасные исправления lint-ошибок:

```bash
make format
make lint-fix
```

## Контейнер

Образ собирается из каталога `boundaries/payments`. Доступные targets: `uv`, `build` и
`runtime`; `build` создаёт окружение из `uv.lock` без dev-зависимостей, а итоговая стадия
`runtime` содержит только virtualenv, исходники приложения и Python runtime:

```bash
docker build --target runtime -t nomnom/payments:local boundaries/payments
docker run --rm -p 8084:8084 -p 9084:9084 \
  -e SERVER__PORT=8084 \
  -e MANAGEMENT__PORT=9084 \
  nomnom/payments:local
```

Контейнер запускается непривилегированным пользователем `app`; `tini` пересылает сигналы
приложению. Конфигурация передаётся
переменными окружения; `.env` в образ не копируется. Основной процесс требует `DATABASE__URI` и при старте сам применяет миграции.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.
