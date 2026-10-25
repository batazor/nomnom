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

Статус реализации: application handlers всех перечисленных сценариев реализованы change [`implement-payments-application-layer`](../../openspec/changes/implement-payments-application-layer/) поверх портов и проверены unit-тестами с in-memory adapters; transport, хранилище и provider adapters ещё не реализованы (см. [Статус реализации](#статус-реализации)).

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

Transport adapter уже маппит application errors в gRPC status codes; добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.payments.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). `GetPaymentStatus` и командные RPC подключены к application handlers через grpcio adapter; `entrypoints.payments.main` запускает gRPC-сервер и завершает его через общий lifecycle. До появления проверяющего JWT/mTLS adapter'а production `SubjectResolver` отклоняет вызовы без доверенной identity.

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

Хранилище `payments` ещё не выбрано. Его выбор должен подтвердить атомарную уникальность `(consumer, event_id)` вместе с изменением business state. Kafka-клиент, схема Inbox и адаптеры относятся к следующим шагам. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

## Observability

Инструментирование Python транспорта, PostgreSQL, Kafka и платёжного провайдера размещается в `infrastructure/`; токены, платёжные реквизиты и payload провайдера исключаются из telemetry. OpenTelemetry и Pyroscope зависимости должны отдельно подтвердить поддержку целевого Python 3.15. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализован in-memory агрегат `Payment`: точный `Money`, чистая табличная FSM, типизированные ошибки переходов, восстановление из persisted state и забор domain events (`PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded`, `PaymentFailed`). Также реализованы policies `PaymentRefundable` и `PaymentCapturable` с типизированными reasons и unit tests.

Реализован application-слой всех use cases по change [`implement-payments-application-layer`](../../openspec/changes/implement-payments-application-layer/) (`FR-400`…`FR-414`, `AC-400`…`AC-432`):

- `Payment Operation` (`domains/payment/operation.py`) — запись одной логической операции с idempotency key и FSM `pending → succeeded | declined`; доменная функция `complete_operation` определяет переход `Payment` по результату provider. Контракты repositories — в `domains/payment/repository.py`.
- Финансовые операции следуют схеме `UoW { pending operation } → provider call → UoW { result + Payment + Outbox }`. Повтор с тем же idempotency key возвращает сохранённый результат без provider call либо завершает незавершённую операцию повтором provider call с тем же ключом. У `Payment` не более одной незавершённой операции.
- Provider callbacks дедуплицируются по `provider_message_id` в той же Unit of Work, что и изменение состояния; асинхронный результат применяется не более одного раза.
- Порты: `UnitOfWork`, `Clock`, `Subject`, `AggregateRef` и общие application errors — в `pkg/application`; authorization (`PaymentPermissionChecker`), provider gateway, Outbox, provider message inbox и генератор идентификаторов — в `slices/payment/applications/ports.py`. Handlers и порты синхронные.
- In-memory/fake adapters находятся только в `tests/payment_fakes.py` и повторяют контракт будущего хранилища: rollback, optimistic concurrency, уникальность `order_id` и idempotency key.

Реализован runtime contract процесса по [Cloud Native Runtime](../../docs/cloud-native-runtime.md) и [ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md) в `pkg/lifecycle`:

- `Lifecycle(LifecycleConfig(...)).run(worker)` — технический порт → `worker.start()` → `worker.run()` в собственном потоке → ready → `SIGTERM`/`SIGINT` → not ready → `shutdown.readiness_delay` → `worker.shutdown(deadline)` в пределах `shutdown.drain_timeout` → закрытие технического порта последним. Exit `0` при штатном shutdown, `1` при ошибке (невалидная конфигурация, ошибка start, неожиданная остановка worker, превышение drain timeout). Повторный сигнал во время shutdown завершает процесс сразу.
- Технический порт `management.port` (`MANAGEMENT__PORT`, по умолчанию `9084`, отличается от бизнес-порта): `GET /health/startup`, `/health/live`, `/health/ready` с JSON `{"status":"ok"}` или `{"status":"unavailable","reason":"initializing|shutting_down"}`; liveness остаётся `200` во время shutdown, probes не логируются. Внешние зависимости в readiness не входят.
- `HttpWorker` — бизнес-порт на `http.server`: при shutdown прекращает приём, закрывает idle keep-alive соединения, дожидается начатых запросов до deadline и сообщает об ошибке, если они не завершились.

Длительности задаются как `<int>(ms|s|m)`: `SHUTDOWN__READINESS_DELAY` (по умолчанию `5s`, `>= 0`) и `SHUTDOWN__DRAIN_TIMEOUT` (по умолчанию `20s`, `> 0`).

Не реализованы: provider client, проверка подписи webhooks, постоянное хранилище, production JWT/mTLS `SubjectResolver`, Outbox publisher, Kafka, telemetry flush и остальные transport handlers. `GetPaymentStatus` и командные RPC работают через gRPC на бизнес-порту; до подключения хранилища, провайдера и SpiceDB composition root (`create_default_worker`) собран fail closed: `RejectingSubjectResolver`, запрет всех permissions, отказ записи в хранилище. Момент Authorization/Capture определяет оркестратор `orders`; partial capture/refund и сроки `PaymentCapturable`/`PaymentRefundable` не поддерживаются до продуктового решения.

## Запуск

```bash
make run
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
переменными окружения; `.env` в образ не копируется. Текущий HTTP-каркас не требует БД,
Kafka или подключения к платёжному провайдеру.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.
