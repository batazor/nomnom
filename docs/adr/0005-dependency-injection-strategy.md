# 5. Dependency Injection Strategy

Date: 2026-10-24

## Status

Proposed

## Context

Bounded contexts nomnom реализуются на разных языках и содержат API, workers и другие исполняемые процессы. Им нужен согласованный способ описывать и собирать зависимости, управлять временем жизни ресурсов и подменять реализации в тестах, не связывая доменную и прикладную логику с DI-фреймворком.

DI должен дополнять Vertical Slice Architecture: срез остаётся организован вокруг use case, а зависимости и их сборка не становятся новым горизонтальным каркасом. Контексты не должны связываться через внутренние контейнеры или реализации соседей.

Для `payments` целевой версией runtime изначально выбирался Python 3.15; 2026-10-25 runtime переведён на Python 3.14 (образ `python:3.14.9-slim-bookworm`), см. [Payments](#payments).

На момент принятия предложения совместимость Dependency Injector с Python 3.15 не подтверждена. [Changelog](https://python-dependency-injector.ets-labs.org/main/changelog.html) заявляет поддержку только до Python 3.14, [тестовая матрица](https://github.com/ets-labs/python-dependency-injector/blob/master/tox.ini) заканчивается на CPython 3.14, а [PyPI](https://pypi.org/project/dependency-injector/) не содержит классификатора Python 3.15. Нижняя граница `Requires-Python >=3.8` и наличие `abi3` wheels могут допускать установку, но сами по себе не подтверждают полноценную поддержку новой версии интерпретатора.

## Decision

Принять отдельную стратегию Dependency Injection для каждого bounded context:

- `orders` и `controlplane` (Go, [ADR 41](0041-cell-based-architecture-with-control-plane.md)): compile-time DI через [`github.com/google/wire`](https://github.com/google/wire);
- `restaurants` (TypeScript): Awilix;
- `delivery` (Rust): compile-time DI через [`shaku`](https://github.com/AzureMarker/shaku);
- `payments` (Python): Dependency Injector — Accepted для `payments` на Python 3.14 (см. [Payments](#payments)).

### Общие правила

Vertical slices организуются вокруг use cases; DI описывает зависимости срезов и их сборку. Интерфейсы зависимостей располагаются рядом с потребляющим кодом, если существующая структура не даёт обоснования для другого места. Общие регистрации выделяются внутри bounded context только по мере реального повторного использования.

Каждый исполняемый процесс, например API или worker, имеет собственный composition root. В нём выбираются реализации, собирается граф объектов и определяется владение ресурсами. Транзакции, request/message context и другие ресурсы отдельной операции имеют ограниченное этой операцией время жизни; общие клиенты могут жить в течение процесса.

Bounded contexts взаимодействуют через согласованные контракты и не импортируют внутренние контейнеры или реализации соседнего контекста. Доменную модель и обработчики use cases можно создавать в тестах с явно переданными зависимостями.

### Orders

Зависимости передаются через обычные Go-конструкторы. Wire генерирует код сборки зависимостей в composition root; provider sets группируются по функциональности и необходимым инфраструктурным компонентам. Импорты Wire и объявления сборки остаются за пределами доменной модели и обработчиков use cases. Запуск, остановка и освобождение ресурсов имеют явного владельца в composition root.

Зависимости внедряются в Temporal Activities и компоненты worker при запуске приложения. Детерминированный workflow-код не обращается к DI-контейнеру и не получает через DI клиенты для внешних операций. Все внешние операции выполняются через Activities.

Оригинальный репозиторий Google Wire [архивирован владельцем 25 августа 2025 года](https://github.com/google/wire) и доступен только для чтения. Выбранным вариантом остаётся `github.com/google/wire`, но отсутствие дальнейшей поддержки генератора означает риск несовместимости с будущими версиями Go, отсутствия исправлений и необходимости миграции или собственного сопровождения.

### Restaurants

Регистрация Awilix находится в composition root. Handlers и прикладные компоненты получают зависимости явно через параметры конструкторов или фабрик. Доменная модель не зависит от Awilix, а прикладной код не вызывает `container.resolve()`.

Время жизни выбирается по назначению: application scope используется для общих клиентов, request/message scope — для ресурсов конкретной обработки.

### Delivery

Зависимости по-прежнему передаются через конструкторы и параметры функций: handlers принимают `&dyn Trait` и создаются в тестах с явно переданными реализациями. Граф зависимостей описывается модулями [`shaku`](https://github.com/AzureMarker/shaku), которые проверяются при компиляции.

Каждый слайс имеет собственный shaku-модуль в `slices/<slice>/di`. Модуль объявляет интерфейс внешних портов слайса и интерфейс его use cases, а реализации портов получает через submodule. Корневой `di` выбирает реализации портов, собирает модули слайсов и итоговый модуль процесса. Shaku импортируют только модули `di`; `domains/` и `applications/` от него не зависят.

Shaku хранит компоненты как `Arc<dyn Interface>` и требует `Send + Sync`. Порты `delivery` этого не требуют, поэтому слайс получает их через собственный интерфейс-агрегатор, а не как отдельные компоненты. Generic `UnitOfWork` недоступен через `dyn`, поэтому `pkg/application` предоставляет object-safe `TransactionScope` и адаптер `ScopedUnitOfWork`, возвращающий его к `UnitOfWork` для handlers.

### Payments

**Решение для `payments`: Accepted (2026-10-25, решение пользователя).** Dependency Injector (`dependency-injector>=4.49.1`) принят для `payments`, а runtime `payments` переведён с Python 3.15 на 3.14, официально поддерживаемый upstream; общий статус ADR не меняется.

Каждый процесс имеет собственный `DeclarativeContainer` в `boundaries/payments/di`: `PaymentsContainer` (gRPC API и provider webhook worker одного `WorkerGroup`); миграции он применяет при старте ресурсом базы ([ADR 42](0042-schema-migrations-at-process-startup.md)), Outbox доставляет Debezium ([ADR 37](0037-outbox-delivery-via-debezium-cdc.md)). `providers.Configuration(strict=True)` заполняется уже провалидированными pydantic settings (`pkg.config`) по секциям; секции остаются типизированными объектами. Ресурсы процесса — `providers.Resource` с инициализацией и освобождением: telemetry providers, psycopg pool, SpiceDB client, Kafka producer. Подтипы `TelemetryResource` и `ProcessResource` фиксируют порядок runtime contract: entrypoint вызывает `init_resources(TelemetryResource)`, затем `init_resources(ProcessResource)`, а в `finally` — `shutdown_resources(ProcessResource)` и последним `shutdown_resources(TelemetryResource)`. Kafka producer зависит от pool, поэтому создаётся после проверки базы и освобождается раньше неё; flush producer остаётся в `RelayWorker.shutdown` в пределах drain deadline. Выбор реализаций (identity, SpiceDB или deny-all, payment provider, PostgreSQL, Outbox mapper) находится только в контейнерах; use cases и handlers — `Singleton`/`Factory` провайдеры на процесс. `@inject` и wiring не используются: `slices/`, `pkg/` и `infrastructure/` не импортируют контейнер и получают зависимости через конструкторы. Тесты проверяют разрешение графа каждого контейнера, подмену зависимостей через `override()` и порядок ресурсов в entrypoints. Сообщения fail-fast и коды выхода процессов не изменились.

Свидетельства на 2026-10-25:

- upstream: релиз 4.49.1 (2026-06-18) заявляет поддержку до Python 3.14 — в [changelog](https://python-dependency-injector.ets-labs.org/main/changelog.html) (4.49.0: «Python 3.14 support»), классификаторах [PyPI](https://pypi.org/project/dependency-injector/), [`tox.ini`](https://github.com/ets-labs/python-dependency-injector/blob/master/tox.ini) и CI; для 3.15 поддержки нет;
- Python 3.15 отклонён не из-за Dependency Injector (его `abi3` wheel на 3.15.0 устанавливается, upstream unit-тесты проходят: 1198 passed, 3 skipped), а из-за `confluent-kafka` 2.16.0 (тогдашний `payments-relay`): wheels для CPython 3.15 нет, и runtime-образ на 3.15 не собирался. С ADR 37 `payments` от `confluent-kafka` не зависит, и выбор версии Python можно пересмотреть;
- образ `python:3.14.9-slim-bookworm`: `docker build --target runtime` проходит, `dependency_injector` 4.49.1, `confluent_kafka` и `psycopg` импортируются, процесс стартует и падает fail-fast на отсутствующей конфигурации; полный набор тестов, интеграционные тесты с PostgreSQL и Kafka и smoke-запуск трёх процессов проходят на Python 3.14.

Fallback: вручную написанные composition roots того же устройства (модуль на процесс, ресурсы как context managers, фабричные функции), как в коммите `bb45bc6`; переход затрагивает только `di/` и entrypoints. Переход на Python 3.15 выполняется отдельно, когда `confluent-kafka` и Dependency Injector объявят его поддержку.

## Alternatives

- Ручная сборка зависимостей во всех контекстах уменьшила бы число инструментов, но не использовала бы подходящие для Go, TypeScript и Rust проверки и управление временем жизни. Для `delivery` ручная сборка без контейнера рассматривалась как исходный вариант и заменена на `shaku`.
- Runtime DI-контейнер для Go упростил бы динамическую регистрацию, но перенёс бы ошибки графа зависимостей с генерации и компиляции на запуск.
- Ручная передача зависимостей (как в коммите `bb45bc6`) или другой Python DI-фреймворк остаются fallback для `payments`, если Dependency Injector перестанет подходить.

## Consequences

Сборка зависимостей и владение ресурсами получают явное место, а доменная и прикладная логика остаётся тестируемой без запуска контейнера. Подход соответствует идиомам каждого языка и не создаёт общий межконтекстный контейнер.

В проекте появятся разные DI-механизмы, поэтому единообразие обеспечивается архитектурными границами, а не общим API. Composition roots и scopes потребуется поддерживать отдельно для каждого процесса. Использование архивированного Google Wire создаёт риск будущей миграции. Переход `payments` на Python 3.15 ждёт upstream-поддержки `confluent-kafka` и Dependency Injector.

Этот ADR не добавляет зависимости, generated code или runtime-конфигурацию; они относятся к следующему шагу реализации.
