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

Статус реализации: application handlers всех перечисленных сценариев реализованы change [`implement-payments-application-layer`](../../openspec/changes/implement-payments-application-layer/) поверх портов и проверены unit-тестами с in-memory adapters; хранилище — PostgreSQL (`infrastructure/postgres`, [локальный ADR 1](docs/adr/0001-use-postgresql-for-payments-storage.md)), provider adapters ещё не реализованы (см. [Статус реализации](#статус-реализации)).

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

Адаптер генерирует серверные Python stubs из локальной схемы и преобразует request messages в прикладные команды или запросы, а результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделью `Payment` или handlers. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API:

| Ошибка `payments` | gRPC code | `ErrorInfo.reason` |
| --- | --- | --- |
| `Payment` не найден | `NOT_FOUND` | `PAYMENT_NOT_FOUND` |
| Некорректная сумма или запрос операции | `INVALID_ARGUMENT` | `INVALID_PAYMENT` |
| Текущее состояние запрещает авторизацию, списание или возврат | `FAILED_PRECONDITION` | `PAYMENT_STATE_CONFLICT` |
| `Payment` заказа уже существует с другими условиями | `ALREADY_EXISTS` | `PAYMENT_ALREADY_EXISTS` |
| Неожиданная внутренняя ошибка | `INTERNAL` | `INTERNAL_ERROR` |
| Платёжный провайдер недоступен | `UNAVAILABLE` | `PAYMENT_PROVIDER_UNAVAILABLE` |

Остальные application errors: `PaymentOperationNotFound` — `NOT_FOUND`, `IdempotencyKeyConflict` — `FAILED_PRECONDITION`, `OperationInProgress` — `ABORTED` (повтор с тем же ключом после завершения начатой попытки), `PermissionDenied` — `PERMISSION_DENIED`, `AuthorizationUnavailable` — `UNAVAILABLE` (fail closed), `ProviderResultConflict` и `ConcurrencyConflict` — `ABORTED`.

Командные RPC для Order Fulfillment Workflow `orders` (FR-427):

| RPC | Идемпотентность | Ответ |
| --- | --- | --- |
| `CreatePayment` | по `order_id`: повтор с теми же условиями возвращает существующий `payment_id` и `created=false` | `payment_id`, `state`, `created` |
| `AuthorizePayment`, `CapturePayment`, `CancelAuthorization`, `RefundPayment` | по `idempotency_key`: повтор возвращает ту же операцию с `replayed=true` без повторного вызова провайдера (AC-453) | `PaymentOperation`: `operation_id`, `status`, `payment_state`, `decline_reason`, `replayed` |

`PaymentOperation.status`: `SUCCEEDED`, `DECLINED` (бизнес-отказ провайдера — обычный ответ, не gRPC-ошибка) или `PENDING` (провайдер ответит асинхронно; повторять с тем же `idempotency_key`). Provider references не раскрываются.

Transport adapter уже маппит application errors в gRPC status codes; добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.payments.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). `GetPaymentStatus` и командные RPC подключены к application handlers через grpcio adapter; `entrypoints.payments.main` запускает gRPC-сервер и завершает его через общий lifecycle. Subject вызывающего определяется проверкой service identity (см. [Service identity](#service-identity)).

## Service identity

Синхронные вызовы других контекстов (Activities Order Fulfillment Workflow `orders-worker`) аутентифицируются Kubernetes projected ServiceAccount token по [ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md) (FR-431, FR-432). `ServiceTokenSubjectResolver` (`infrastructure/grpc/identity.py`) реализует порт `SubjectResolver` и проверяет каждый вызов по общему контракту [design](../../openspec/changes/add-service-identity/design.md):

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

Fail closed: если `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` или `IDENTITY__JWKS_FILE` пусты, composition root (`create_default_worker`) оставляет `RejectingSubjectResolver` и пишет предупреждение при старте — все вызовы отклоняются `UNAUTHENTICATED` (AC-462). Ошибки startup (процесс завершается до открытия портов): нечитаемый или невалидный JWKS либо JWKS без ключей `RS256`/`ES256` с `kid`, пустой allowlist при заданном issuer, неверный формат или дубликаты allowlist. JWKS читается один раз при старте: после ротации signing key кластера нужен restart pod.

Совместимость с реализациями других контекстов закреплена общими векторами `tests/identity/fixtures`: `tests/test_service_identity.py` принимает только `valid` и `valid_string_aud` (AC-461) и проверяет вызов по gRPC до handler (AC-459). Время проверки `exp`/`nbf` инжектируется (`clock`) для тестов допуска. Helm values `identity.*` описаны в [ops/README.md](ops/README.md#service-identity).

## Authorization

Порт `PaymentPermissionChecker` (`slices/payment/applications/ports.py`) реализует `SpiceDBPermissionChecker` (`infrastructure/authorization/spicedb.py`) на официальном клиенте `authzed` по [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md) и нормативной таблице [design](../../openspec/changes/add-spicedb-authorization/design.md) (FR-435). Schema и grants развёртывания — `authorization/schema.zed` и `authorization/bootstrap.yaml`.

| `PaymentPermission` | SpiceDB `CheckPermission` |
| --- | --- |
| `create`, `authorize`, `capture`, `cancel_authorization`, `refund` | `platform:nomnom#manage_payments` |
| `view`, `view_operations` | `platform:nomnom#view_payments` |

Subject: `user` → `user:<id>`, `service` → `service:<id>`. Consistency `minimize_latency`. Разрешает только `PERMISSIONSHIP_HAS_PERMISSION`; `NO_PERMISSION` и conditional permission — отказ (`PermissionDenied` → `PERMISSION_DENIED`). Любая ошибка gRPC, неверный ключ, недоступность или превышение `SPICEDB__TIMEOUT` — `AuthorizationUnavailable` → `UNAVAILABLE` (fail closed, AC-466); причина пишется в log (`WARNING`).

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `SPICEDB__ENDPOINT` | пусто | gRPC endpoint `host:port`. Обязателен: пусто — ошибка startup. |
| `SPICEDB__PRESHARED_KEY` | пусто | Bearer preshared key; обязателен при заданном endpoint (иначе ошибка startup). В Helm — из Secret. |
| `SPICEDB__INSECURE` | `false` | Plaintext gRPC без TLS — только kind и локальный запуск. |
| `SPICEDB__TIMEOUT` | `2s` | Deadline одного `CheckPermission`, `<int>(ms|s|m)`, `> 0`. |

Unit-тесты маппинга и fail-closed поведения с fake клиентом — `tests/test_spicedb_authorization.py`. Integration-тесты `tests/test_spicedb_integration.py` пропускаются без `SPICEDB_ENDPOINT`; против SpiceDB с `authorization/bootstrap.yaml` (plaintext, ключ `devkey`) они проверяют, что `service:orders-worker` имеет все payment permissions (AC-465), `service:bff` — ни одного, а недоступный endpoint даёт `AuthorizationUnavailable`:

```bash
SPICEDB_ENDPOINT=localhost:50051 uv run python -m unittest tests.test_spicedb_integration
```

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
- `refunded` и `failed` являются конечными состояниями;
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

[Dependency Injector](https://python-dependency-injector.ets-labs.org/) рассматривается как кандидат для `payments`. Контейнер собирается в composition root каждого исполняемого процесса, а зависимости явно передаются в прикладные компоненты. Доменная модель не зависит от контейнера и механизма wiring.

Контейнер должен управлять инициализацией и освобождением ресурсов и позволять подменять зависимости в тестах. Целевой версией runtime выбран Python 3.15. Текущий `pyproject.toml` всё ещё задаёт только нижнюю границу `>=3.9` и будет приведён в соответствие на этапе реализации.

Совместимость Dependency Injector с Python 3.15 пока не подтверждена: changelog заявляет поддержку до Python 3.14, а официальная тестовая матрица и классификаторы PyPI не содержат 3.15. `Requires-Python >=3.8` и наличие `abi3` wheels сами по себе не считаются подтверждением. Поэтому Dependency Injector остаётся кандидатом со статусом Proposed до появления upstream-поддержки и проверки установки и выполнения в целевой среде.

Статус кандидата и проверка совместимости описаны в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Событийная интеграция

Consumers интеграционных событий `payments` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение состояния платёжной операции, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной локальной транзакции. Kafka offset подтверждается только после commit.

Inbox не защищает вызовы платёжного провайдера: авторизация, списание и возврат вне локальной транзакции требуют собственного idempotency key операции. Платёжные webhooks не являются Kafka integration events и получают отдельную идемпотентную boundary.

Unit of Work охватывает только локальные изменения: payment state, operation record и Outbox. Вызов провайдера выполняется вне транзакции, обычно между записью операции и фиксацией её результата; порядок определяется use case и idempotency policy — см. [документацию Unit of Work](../../docs/unit-of-work.md#payments) и [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md).

Хранилище `payments` — PostgreSQL ([локальный ADR 1](docs/adr/0001-use-postgresql-for-payments-storage.md)): таблица `inbox` с первичным ключом `(consumer, event_id)` и таблица `outbox` записываются в той же локальной транзакции, что и `payments`/`payment_operations`. Provider callbacks дедуплицируются через `inbox` (consumer `payment-provider`). Kafka consumers ещё нет. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

### Публикация Integration Events

По [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) `payments` публикует в topic `payments.v1.payment-events` контракт [`nomnom/payments/v1/events.proto`](infrastructure/grpc/nomnom/payments/v1/events.proto): `PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded` (с `Money`), `PaymentFailed` (с `reason`). `PaymentOperationDeclined` — внутренний факт и не публикуется.

- `PaymentEventsMapper` (`infrastructure/messaging/events.py`) — явный allowlist Domain Event → Protobuf; composition root `entrypoints/payments/main.py` передаёт его gRPC handlers и webhook worker, поэтому событие пишется в Outbox в той же транзакции, что и Payment.
- `payments-relay` (`python -m entrypoints.payments_relay.main`, тот же образ) — polling relay с runtime contract `pkg/lifecycle` и техническим портом `MANAGEMENT__PORT`. Активен один экземпляр: session-level `pg_try_advisory_lock`, остальные реплики — failover. Цикл читает до `OUTBOX__BATCH_SIZE` записей с `published_at IS NULL` в порядке `seq`, публикует по одной idempotent producer'ом `confluent-kafka` (`acks=all`, key — `aggregate_id`) и выставляет `published_at` только после delivery report. Первая неудача останавливает пачку (head-of-line): `attempts`/`last_error` обновляются, следующий цикл начинает с неё. Раз в минуту удаляются опубликованные записи старше `OUTBOX__RETENTION`.
- Kafka headers совпадают с `orders/pkg/messaging/envelope.go`: `event_id`, `event_type`, `occurred_at` (RFC 3339 nano, UTC), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent` (если был span), плюс `_watermill_message_uuid` = `event_id` для Watermill consumers.
- Конфигурация relay: `DATABASE__URL` и `KAFKA__BROKERS` (через запятую) обязательны — пустое значение завершает процесс на старте; `KAFKA__CLIENT_ID` (`payments-relay`), `OUTBOX__POLL_INTERVAL` (`1s`), `OUTBOX__BATCH_SIZE` (`100`), `OUTBOX__RETENTION` (7 дней; общий формат длительности — `10080m`). Основной процесс к Kafka не подключается.
- Метрики (OTel, `pkg/telemetry`, label `stream` = topic): `nomnom_outbox_pending_messages`, `nomnom_outbox_oldest_pending_age_seconds` (только активный экземпляр), `nomnom_outbox_publish_retries_total` (`error_class`: transient | permanent), `nomnom_outbox_publish_duration_seconds` и `nomnom_outbox_publish_lag_seconds` (`occurred_at` → подтверждение брокера; плюс `event_type`).
- `tests/test_kafka_relay_integration.py` при заданных `DATABASE_URL` и `KAFKA_BROKERS` проводит Authorize через Outbox и relay в реальную Kafka и читает headers обратно:

```bash
docker run -d --rm --name payments-kafka -p 59092:9092 \
  -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
  -e KAFKA_LISTENERS=PLAINTEXT://:9092,CONTROLLER://:9093 \
  -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:59092 \
  -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
  -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
  -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
  -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 \
  -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 apache/kafka:4.1.0
DATABASE_URL=postgresql://payments:payments@localhost:55432/payments \
  KAFKA_BROKERS=localhost:59092 make test
docker stop payments-kafka
```

## Observability

Инструментирование Python транспорта, PostgreSQL, Kafka и платёжного провайдера размещается в `infrastructure/`; токены, платёжные реквизиты и payload провайдера исключаются из telemetry. OpenTelemetry и Pyroscope зависимости должны отдельно подтвердить поддержку целевого Python 3.15. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализован in-memory агрегат `Payment`: точный `Money`, чистая табличная FSM, типизированные ошибки переходов, восстановление из persisted state и забор domain events (`PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded`, `PaymentFailed`). Также реализованы policies `PaymentRefundable` и `PaymentCapturable` с типизированными reasons и unit tests.

Реализован application-слой всех use cases по change [`implement-payments-application-layer`](../../openspec/changes/implement-payments-application-layer/) (`FR-400`…`FR-414`, `AC-400`…`AC-432`):

- `Payment Operation` (`domains/payment/operation.py`) — запись одной логической операции с idempotency key и FSM `pending → succeeded | declined`; доменная функция `complete_operation` определяет переход `Payment` по результату provider. Контракты repositories — в `domains/payment/repository.py`.
- Финансовые операции следуют схеме `UoW { pending operation } → provider call → UoW { result + Payment + Outbox }`. Повтор с тем же idempotency key возвращает сохранённый результат без provider call либо завершает незавершённую операцию повтором provider call с тем же ключом. У `Payment` не более одной незавершённой операции.
- Provider callbacks дедуплицируются по `provider_message_id` в той же Unit of Work, что и изменение состояния; асинхронный результат применяется не более одного раза.
- Порты: `UnitOfWork`, `Clock`, `Subject`, `AggregateRef` и общие application errors — в `pkg/application`; authorization (`PaymentPermissionChecker`), provider gateway, Outbox, provider message inbox и генератор идентификаторов — в `slices/payment/applications/ports.py`. Handlers и порты синхронные.
- In-memory/fake adapters находятся только в `tests/payment_fakes.py` и повторяют контракт хранилища: rollback, optimistic concurrency, уникальность `order_id` и idempotency key.

Реализован runtime contract процесса по [Cloud Native Runtime](../../docs/cloud-native-runtime.md) и [ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md) в `pkg/lifecycle`:

- `Lifecycle(LifecycleConfig(...)).run(worker)` — технический порт → `worker.start()` → `worker.run()` в собственном потоке → ready → `SIGTERM`/`SIGINT` → not ready → `shutdown.readiness_delay` → `worker.shutdown(deadline)` в пределах `shutdown.drain_timeout` → закрытие технического порта последним. Exit `0` при штатном shutdown, `1` при ошибке (невалидная конфигурация, ошибка start, неожиданная остановка worker, превышение drain timeout). Повторный сигнал во время shutdown завершает процесс сразу.
- Технический порт `management.port` (`MANAGEMENT__PORT`, по умолчанию `9084`, отличается от бизнес-порта): `GET /health/startup`, `/health/live`, `/health/ready` с JSON `{"status":"ok"}` или `{"status":"unavailable","reason":"initializing|shutting_down"}`; liveness остаётся `200` во время shutdown, probes не логируются. Внешние зависимости в readiness не входят.
- `HttpWorker` — бизнес-порт на `http.server`: при shutdown прекращает приём, закрывает idle keep-alive соединения, дожидается начатых запросов до deadline и сообщает об ошибке, если они не завершились.

Длительности задаются как `<int>(ms|s|m)`: `SHUTDOWN__READINESS_DELAY` (по умолчанию `5s`, `>= 0`), `SHUTDOWN__DRAIN_TIMEOUT` (по умолчанию `20s`, `> 0`) и `SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT` (по умолчанию `5s`, `> 0`). Traces и metrics экспортируются через OTLP, только если задан `OTEL_EXPORTER_OTLP_ENDPOINT`; после drain providers сбрасываются не дольше `SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT`.

Реализовано хранилище PostgreSQL в `infrastructure/postgres` (psycopg 3, пул `psycopg_pool`):

- Схема — `infrastructure/postgres/migrations/*.sql` (forward-only), применяется `python -m entrypoints.payments_migrate.main` под advisory lock; версии — в `schema_migrations`. Таблицы `payments`, `payment_operations`, `outbox`, `inbox`.
- `PostgresUnitOfWork` — одна локальная транзакция; repositories, Outbox и Inbox внутри `run` используют её соединение (context variable), вложенный `run` присоединяется. Вне Unit of Work каждый вызов берёт своё соединение из пула.
- Outbox по [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md): `seq`, `event_id` (UUIDv5 от `{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}`), `event_type` (полное имя Protobuf-сообщения), `topic`, `payload` (Protobuf, `BYTEA`), `metadata` (`traceparent`), `published_at`, `attempts`, `last_error`. Какие Domain Events публикуются, решает внедряемый `OutboxEventMapper` (`infrastructure/postgres/outbox.py`); событие без контракта (mapper вернул `None`) в Outbox не пишется. `NoPublishedContracts` остаётся значением по умолчанию фабрик (`create_default_handlers`, `create_default_worker`, `create_webhook_worker`); entrypoint передаёт `PaymentEventsMapper` — см. [Публикация Integration Events](#публикация-integration-events).
- Нарушения уникальности отображаются в доменные ошибки: `payments_order_id_key` → `DuplicatePayment`, `payment_operations_idempotency_key_key` → `DuplicateIdempotencyKey`, частичный уникальный индекс `payment_operations_one_pending` (`WHERE state = 'pending'`) → `OperationAlreadyPending`. Insert выполняется в savepoint, поэтому обработанная ошибка не ломает транзакцию. `save` обновляет строку только при совпадении `version`, иначе `ConcurrencyConflict`.
- `DATABASE__URL` обязателен: пустое значение завершает процесс на старте; недоступная база — тоже (проверка соединения до открытия портов). `DATABASE__MAX_CONNS` — размер пула (по умолчанию `10`).
- Integration-тесты `tests/test_postgres_integration.py` запускаются при заданном `DATABASE_URL`:

```bash
docker run -d --rm --name payments-pg -p 55432:5432 \
  -e POSTGRES_USER=payments -e POSTGRES_PASSWORD=payments postgres:18.6-alpine3.24
DATABASE_URL=postgresql://payments:payments@localhost:55432/payments make test
docker stop payments-pg
```

Не реализованы: provider client, проверка подписи webhooks, user identity через BFF, Kafka consumers (Inbox для integration events), Helm-развёртывание `payments-relay`, telemetry flush и остальные transport handlers. `GetPaymentStatus` и командные RPC работают через gRPC на бизнес-порту; до подключения провайдера composition root (`create_default_worker`) собран fail closed: `RejectingSubjectResolver` без конфигурации identity, обязательный `SPICEDB__ENDPOINT` (см. [Authorization](#authorization)). Момент Authorization/Capture определяет оркестратор `orders`; partial capture/refund и сроки `PaymentCapturable`/`PaymentRefundable` не поддерживаются до продуктового решения.

## Запуск

```bash
make run        # payments: gRPC + provider webhooks
make run-relay  # payments-relay: Outbox → Kafka
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
переменными окружения; `.env` в образ не копируется. Основной процесс требует `DATABASE__URL`; тот же образ запускает
`python -m entrypoints.payments_relay.main` (дополнительно `KAFKA__BROKERS`) и
`python -m entrypoints.payments_migrate.main`.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.
