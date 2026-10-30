# 6. DDD Structure Inside Vertical Slices

Date: 2026-10-24

## Status

Accepted

Связан с [7. gRPC Contracts and Buf Schema Registry](0007-grpc-contracts-and-buf-schema-registry.md), [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md), [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md) и [25. Integration Event Publication](0025-integration-event-publication.md)

## Context

Vertical Slice Architecture уже определяет верхнеуровневую организацию кода внутри каждого bounded context: код сначала группируется по функциональным модулям. Слайс является модулем бизнес-возможности, внутри которого несколько use cases могут работать с одной моделью. По мере появления доменной логики каждому слайсу нужна единая внутренняя структура, которая отделяет бизнес-инварианты и прикладную координацию от технических адаптеров, не превращая bounded context обратно в набор глобальных горизонтальных слоёв.

Некоторые слайсы работают с одним агрегатом. Копирование такого агрегата и его инвариантов по слайсам создаёт несколько расходящихся реализаций одной модели. В то же время совпадение терминов между bounded contexts не означает, что контексты разделяют одну модель.

Use case внутри слайса вызывается из разных entrypoints: gRPC, Kafka consumer, Temporal Activity, платёжный webhook. Каждый use case является отдельным package/module внутри `applications/`; сам use case не считается отдельным слайсом. Без явных правил transport-типы (Protobuf, event payloads, Activity payloads) проникают в handlers и доменную модель, каждый entrypoint получает собственный вариант use case, а количество промежуточных DTO и mapper'ов растёт без пользы.

## Decision

Vertical Slice Architecture остаётся верхнеуровневой организацией: сначала bounded context и функциональный слайс. От корня bounded context используется структура:

```text
<boundary>/
  slices/
    <slice>/
      domains/
        <domain>/
          <domain>.<ext>
          vo/
      applications/
        <usecase>/
          README.md
      infrastructure/
      di/
  infrastructure/
    <adapter>/
  di/
  cmd/
  pkg/
    <shared-package>/
```

Каталоги создаются и заполняются только по необходимости конкретного слайса.

### Ответственность каталогов

`domains/<domain>/` содержит конкретную доменную модель и принадлежащие ей бизнес-правила. Основной файл модели называется как домен: `domains/<domain>/<domain>.<ext>`. Здесь находятся агрегаты, сущности, доменные сервисы, доменные события, доменные ошибки, инварианты и контракты репозиториев агрегатов. Бизнес-инварианты реализуются доменной моделью; application handlers вызывают её поведение.

`domains/<domain>/vo/` содержит value objects конкретной доменной модели: значения без собственной идентичности, проверяющие собственные инварианты при создании, сравниваемые по значению и предоставляющие операции, сохраняющие корректность. Предпочтительны неизменяемые value objects. Они выделяются для понятий с доменным смыслом и правилами, а не для каждого примитивного поля.

`applications/` содержит отдельный package/module на каждый use case. В каждом каталоге обязателен `README.md`: он фиксирует actor, намерение, результат, владельца данных и существенные взаимодействия сценария. Mermaid sequence diagram добавляется только для нетривиальной оркестрации, асинхронного обмена или взаимодействия нескольких владельцев; локальный линейный сценарий описывается текстом. Package содержит command/query, handler, прикладные входные и выходные модели, координацию загрузки агрегатов, вызова доменного поведения и сохранения результата, а также необходимые конкретному use case порты. Исполняемые артефакты создаются вместе с реализацией SDD change; каталог с README не требует пустого handler scaffold. Handler управляет выполнением сценария, но не присваивает себе правила допустимого изменения доменного состояния.

`pkg/` содержит только shared libraries bounded context, которые переиспользуются несколькими слайсами или entrypoints: контракт use case (`usecase`), identity и authorization (`authz`), локальная транзакция (`unitofwork`, `unit_of_work`, `unit-of-work`), конфигурация, lifecycle, telemetry. Имя пакета называет его ответственность, а не архитектурный слой: `pkg/application`, `pkg/infrastructure` и `pkg/di` не создаются. Use cases, доменная модель, адаптеры и код единственного entrypoint в `pkg/` не размещаются.

`infrastructure/` содержит реализации репозиториев, работу с БД и моделями хранения, адаптеры внешних API, транспортные обработчики и адаптеры обмена сообщениями. Здесь транспортные DTO и модели хранения преобразуются в прикладные и доменные типы. Инфраструктура реализует контракты внутренних частей слайса.

`<boundary>/infrastructure/` содержит адаптеры всего контекста, которые используют несколько слайсов: gRPC API, Kafka и messaging, клиент internal gateway, миграции и общие части хранилища. `pkg/` не импортирует их. Прикладной слой и домены существуют только внутри слайсов; каталогов `application/` и `domains/` в корне bounded context нет.

### Направление зависимостей

На уровне импортов:

- `applications` зависит от `domains`;
- `infrastructure` зависит от необходимых контрактов и типов в `applications` и `domains`;
- `domains` не зависит от `applications`, `infrastructure`, ORM, транспорта и DI-библиотек;
- `domains/<domain>/vo` не зависит от агрегатов, сущностей и внешних адаптеров.

Контракт репозитория агрегата размещается в `domains`, его реализация — в `infrastructure`. Порты прикладных интеграций и query-сценариев размещаются рядом с потребляющим кодом в `applications`. Конкретные реализации выбираются и собираются в существующем composition root.

### Единая модель между слайсами

Один агрегат и его инварианты имеют одну каноническую реализацию внутри bounded context. Если несколько слайсов работают с одним агрегатом, они используют общую доменную модель этого bounded context. Например, создание и отмена заказа не получают отдельные реализации `Order`.

Общая для use cases одного слайса модель размещается в `domains/` этого слайса:

```text
<boundary>/
  slices/
    order/
      domains/
        order/
          order.go
      applications/
        placeorder/
        requestordercancellation/
        completeorder/
      infrastructure/
```

Use cases `placeorder`, `requestordercancellation` и `completeorder` используют одну модель `Order` из слайса `order`; агрегат не копируется по package use cases. Разделение на новый слайс выполняется только при появлении самостоятельной бизнес-возможности и собственного cohesive модуля, а не для каждой команды.

Каждый bounded context самостоятельно владеет своей моделью. Совпадение названий сущностей или value objects между контекстами само по себе не является основанием для общей библиотеки.

### Типы моделей

Use case работает с четырьмя видами моделей:

```text
Transport DTO → Command / Query → Domain Model
Domain Model / Read Model → Application DTO → Transport DTO
```

| Модель | Назначение | Размещение | Не зависит от |
| --- | --- | --- | --- |
| Transport DTO | Форма данных конкретного adapter'а: Protobuf messages, Kafka event payloads, HTTP request/response, webhook payloads, Temporal Activity input/output | `infrastructure/` adapter'а-владельца | — |
| Command | Намерение изменить состояние: `PlaceOrderCommand`, `CancelOrderCommand` | `<slice>/applications/<usecase>/` | transport-типов, ORM; не содержит бизнес-логики |
| Query | Чтение без изменения состояния: `GetOrderQuery`, `ListCustomerOrdersQuery` | `<slice>/applications/<usecase>/` | transport-типов, ORM |
| Application DTO | Вход/выход use case, когда domain object не подходит наружу: `OrderDetailsDTO`, `PaymentStatusDTO` | `<slice>/applications/<usecase>/` | transport-типов; не имеет поведения, не является aggregate, entity или value object |

Правила:

- Command и Query содержат примитивы, идентификаторы и value objects из `domains`; transport-типы, generated code и verified token в сыром виде в них не попадают.
- Verified subject передаётся handler'у как прикладной тип (поле Command/Query или явный параметр), а не как header или gRPC metadata ([ADR 18](0018-spicedb-for-authorization.md)).
- Для queries read model **является** Application DTO: read-порт в `applications/` возвращает его напрямую, реализация в `infrastructure/` заполняет его из хранилища без промежуточного копирования.
- Command handler возвращает минимальный результат — идентификатор, версию или Application DTO, если сценарию нужен ответ; aggregate наружу не возвращается.
- Application DTO не создаётся на каждый domain type: он появляется только там, где domain object действительно не подходит наружу.
- Именование следует идиомам языка: в Go допустимо `PlaceOrder` вместо `PlaceOrderCommand`. Суть правила — в размещении и направлении зависимостей, а не в суффиксе.

Сгенерированный Buf-код находится в отдельном пакете/модуле внутри `infrastructure` (`infrastructure/grpc/gen` владельца или потребителя, [ADR 7](0007-grpc-contracts-and-buf-schema-registry.md)) и импортируется только из `infrastructure`.

### Доменная модель

Доменная модель не зависит от Protobuf, GraphQL, Kafka, ORM/ODM, Temporal SDK, Command/Query и Application/Transport DTO.

Доменные ошибки типизированы, чтобы infrastructure сопоставляла их с transport-кодами без разбора текста: sentinel errors в Go, классы ошибок или discriminated unions в TypeScript, `enum` в Rust, классы исключений в Python. Таблица gRPC-кодов и `google.rpc.ErrorInfo` определены в ADR 7.

### Входы в application

Каждый entrypoint сводится к Command или Query и прямому вызову handler'а:

| Вход | Adapter в `infrastructure/` | Что вызывается |
| --- | --- | --- |
| gRPC / HTTP | Transport validation (обязательные поля, форматы, разбор `google.type.Money`) → Command/Query | handler слайса |
| Kafka integration event | Consumer: event payload → Command, например `MarkRestaurantAcceptedCommand`. Inbox — инфраструктурная обёртка вокруг вызова handler'а ([ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md)) | command handler слайса |
| Payment webhook `payments` | Проверка подписи и payload → Command | command handler слайса |
| Temporal Activity | Activity: payload → Command/Query | handler слайса |

Integration event не является пятым типом модели: на входе это Transport DTO, который consumer преобразует в Command.

Temporal Workflow оркестрирует процесс через Activities и не знает о Commands, Queries, handlers и доменной модели. Workflow и типы Activity payloads образуют Temporal-адаптер в `infrastructure/`; workflow, охватывающий несколько слайсов, размещается в `<boundary>/infrastructure/temporal/`, а Activity, вызывающая use case, — в `infrastructure/` соответствующего слайса и регистрируется в composition root worker'а. Activity, которая только вызывает API другого bounded context через internal gateway, является client adapter'ом и Command не создаёт. Когда use case должен запустить workflow или передать ему Signal/Update, он делает это через порт в `applications/`; реализация на Temporal client — в `infrastructure/`.

GraphQL используется только в BFF, который не является bounded context и не имеет доменной модели: GraphQL input/output — Transport DTO BFF, преобразуемые в gRPC-запросы внутри его adapter'ов. Правила Command/Query относятся к bounded contexts.

По умолчанию in-process command bus или mediator не используется: adapter получает handler через DI ([ADR 5](0005-dependency-injection-strategy.md)) и вызывает его напрямую. Bus вводится только при конкретной причине, например общем cross-cutting pipeline, и фиксируется отдельным решением.

### Mapping

- Transport ↔ application: в `infrastructure/`, внутри adapter'а, которому принадлежит Transport DTO.
- Application ↔ domain: в `applications/`, только там, где типы действительно различаются.
- Persistence ↔ domain: в реализации репозитория в `infrastructure/`.
- Исходящие integration events: application решает, *публиковать ли* факт и какой, и передаёт в порт доменное событие; Application DTO — только если payload требует данных сверх доменного события. `infrastructure/` строит из него integration event и envelope, сериализует и записывает в Outbox в той же транзакции ([ADR 13](0013-transactional-outbox-for-integration-event-publishing.md), [ADR 25](0025-integration-event-publication.md)).

Mapping пишется явным кодом рядом с adapter'ом или use case. Глобальный mapper framework и reflection-based automapping не используются.

### Проверка направления импортов

Направление зависимостей проверяется автоматически — architecture fitness functions в стандартной проверке каждого bounded context. Нарушение роняет тесты или lint, а не ждёт code review:

| Контекст | Проверка | Запуск |
| --- | --- | --- |
| `orders` (Go) | depguard в `.golangci.yml`: `domains` — только stdlib, `decimal` и домены; `vo` — только stdlib и `decimal`; `applications` — только домены, use cases, `pkg/usecase`, `pkg/authz`, `pkg/unitofwork`, `pkg/telemetry`; `pkg` не знает слайсов и процессов; `infrastructure` не импортирует `di` и `cmd`; generated code — только в adapters, composition root и пакете-владельце | `make lint`, `make check` |
| `restaurants` (TypeScript) | dependency-cruiser в `.dependency-cruiser.cjs` (парсер swc: у TypeScript 7 нет compiler API): те же правила; `domains` — только `decimal.js`, Node built-ins и домены; `applications` — домены, use cases и `pkg/usecase`, `pkg/authz`, `pkg/unit-of-work`, `pkg/outbox`; неразрешённый импорт — ошибка, чтобы зависимость не выпала из проверки | `npm run architecture`, `npm run check` |
| `payments` (Python) | import-linter в `pyproject.toml`: слои процесса и слайса, запрет фреймворков и драйверов в `slices`, `pkg.usecase`, `pkg.authz`, `pkg.unit_of_work` и `pkg.outbox` | `make imports`, `make check` |
| `delivery` (Rust) | `tests/slice_boundaries.rs`: направление `delivery → courier` между слайсами | `cargo test`, `make check` |
| `ui` | `scripts/check-boundaries.mjs`: браузерный код вызывает только публичный GraphQL API | `npm run lint` |

Проверяются прямые импорты production-кода; тесты и test-support пакеты исключены. Allowlist слоя расширяется явно в конфиге, когда у домена или use case появляется новая допустимая библиотека. depguard сравнивает пакеты по префиксу пути, поэтому новый слайс `orders` дописывает свои `domains`, `applications` и `di` в правила.

## Consequences

Внутренняя организация слайсов и направление импортов становятся единообразными для всех языков проекта. Доменные правила остаются независимыми от фреймворков, handlers концентрируются на координации, а технические детали заменяются через явно расположенные контракты и адаптеры.

Каноническая реализация агрегата в `<boundary>/slices/<slice>/domains/<domain>/<domain>.<ext>` предотвращает расхождение инвариантов между сценариями. Такой модуль нельзя превращать в неограниченный общий каталог или межконтекстную библиотеку.

Один use case обслуживает все entrypoints через одну пару Command/handler, поэтому gRPC, Kafka и Temporal не получают расходящихся реализаций сценария. Цена — явный mapping-код в каждом adapter'е; она ограничивается запретом на DTO для каждого domain type и использованием read model как Application DTO. Отказ от mediator оставляет вызовы прозрачными, но cross-cutting поведение (telemetry, authorization, транзакции) остаётся в adapter'ах и composition root.

Структура допускает пустые на раннем этапе части, но не требует создавать каталоги и типы заранее. Направление импортов проверяют архитектурные тесты и правила линтера каждого bounded context; code review остаётся для того, что граф импортов не выражает: размещения моделей, mapping и ответственности handlers.
