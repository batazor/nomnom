# Application boundaries

Каталог `boundaries` содержит границы самостоятельно развиваемых приложений nomnom. Backend-приложения в нём организованы как bounded contexts — границы доменных моделей, данных и ответственности. Customer web UI находится в `boundaries/ui/` как отдельная клиентская application boundary и не владеет backend-доменной моделью.

Эта структура сама по себе не определяет количество процессов, сервисов или единиц развёртывания. Физическая архитектура может быть выбрана отдельно от доменных границ.

[Control plane](controlplane/README.md) находится в `boundaries/controlplane/` как глобальная application boundary cell-based архитектуры ([ADR 41](../docs/adr/0041-cell-based-architecture-with-control-plane.md)): реестр Cell, Market и Market Placement, вне ячеек и вне горячего пути запросов.

Сервис [BFF](bff/README.md) находится в `boundaries/bff/` как самостоятельная application boundary и единая прикладная точка входа для клиентского API, но не является bounded context. Он не владеет доменной моделью или хранилищем доменных данных. BFF проектирует GraphQL API на WunderGraph Cosmo вокруг клиентских сценариев, получает доменные данные только через gRPC API сервисов-владельцев и преобразует их в клиентские представления. Бизнес-инварианты, проверка прав на конкретные действия, принадлежность ресурсов и tenant-изоляция остаются у владельцев соответствующих операций и данных.

Допустимое направление синхронного взаимодействия клиента — `client → edge → Cosmo Router → BFF plugin → internal gateway → bounded context`. Клиенты не обращаются к внутренним gRPC/Connect API напрямую. BFF не читает базы данных контекстов, не публикует и не потребляет их Kafka-сообщения и не запускает Temporal workflow. Необходимые межсервисные взаимодействия между bounded contexts сохраняют собственные правила аутентификации и делегирования контекста. Платёжные webhooks обслуживаются интеграционными адаптерами `payments`, а health checks, metrics и другие служебные интерфейсы имеют отдельные технические маршруты и правила доступности. Решение зафиксировано в [локальном ADR 3 сервиса BFF](bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md).

Архитектурные решения, относящиеся к отдельному контексту, хранятся внутри этого контекста в `docs/adr`. Общесистемные решения находятся в корневом [`docs/adr`](../docs/adr/).

Все bounded contexts используют обязательный [Ubiquitous Language](../docs/ubiquitous-language.md): один термин имеет одно значение внутри контекста, а перевод моделей между контекстами выполняется явно на application/infrastructure boundary. Локальные glossary служат контекстными индексами и не создают shared domain model.

Общая [стратегия тестирования](../docs/testing.md) определяет уровни unit, BDD slice acceptance, BDD boundary journey, integration и kind E2E, а также размещение acceptance scenarios внутри slices и boundaries.

## Команды разработки

Контекст предоставляет короткие команды запуска и проверки через принятый в его экосистеме task runner. `restaurants` использует scripts из `package.json`; `orders`, `delivery`, `payments` и `controlplane`, где эквивалентного script-интерфейса нет, предоставляют `Makefile`.

Основные возможности единообразны: запуск, тесты, lint, форматирование и объединяющая команда `check`. Точное имя следует соглашениям task runner: например, `make run` и `npm start`. Makefile дополнительно предоставляет `format-check`; специфичные проверки, например `typecheck` в `payments`, включаются в `check`.

## Организация кода внутри контекста

Внутри bounded context основной каркас строится по принципам Vertical Slice Architecture: код группируется по бизнес-модулям. Слайс является cohesive модулем, внутри которого use cases разделены по отдельным packages; use case сам по себе не создаёт новый верхнеуровневый слайс.

Горизонтальные технические слои не определяют структуру контекста верхнего уровня. Сначала код разделяется по bounded contexts и функциональным слайсам. От корня bounded context используется следующая DDD-структура:

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

Каталоги создаются и заполняются по необходимости конкретного слайса. Эта структура не требует добавлять фиктивные агрегаты, сервисы, value objects или репозитории.

### `domains/<domain>/`

Содержит конкретную доменную модель и принадлежащие ей бизнес-правила. Основной файл называется как домен: `domains/<domain>/<domain>.<ext>`.

- агрегаты и сущности;
- доменные сервисы для правил, которые естественным образом не принадлежат одной сущности или value object;
- доменные события;
- доменные ошибки и инварианты;
- контракты репозиториев агрегатов.

Бизнес-инварианты реализуются в доменной модели. Application handlers вызывают её поведение, но не реализуют вместо неё правила допустимого изменения доменного состояния.

### `domains/<domain>/vo/`

Содержит value objects: значения без собственной идентичности, проверяющие свои инварианты при создании, сравниваемые по значению и предоставляющие только сохраняющие корректность значения операции. Предпочтительны неизменяемые value objects.

Value object выделяется для понятия с доменным смыслом и собственными правилами, а не автоматически для каждого примитивного поля.

#### Денежные значения

`Money` остаётся доменным value object из точной десятичной суммы и кода валюты, но контексты не реализуют десятичную арифметику самостоятельно. Для суммы используются проверенные типы соответствующего языка:

| Контекст | Язык | Десятичный тип |
| --- | --- | --- |
| `orders` | Go | [`decimal.Decimal` из `shopspring/decimal`](https://github.com/shopspring/decimal) |
| `restaurants` | TypeScript | [`Decimal` из `decimal.js`](https://github.com/MikeMcl/decimal.js) |
| `delivery` | Rust | [`rust_decimal::Decimal` стабильной ветки 1.x](https://github.com/paupino/rust-decimal/tree/v1), если в контексте появятся денежные значения |
| `payments` | Python | [`decimal.Decimal`](https://docs.python.org/3/library/decimal.html) из стандартной библиотеки |

Денежные значения создаются из строкового или точного десятичного представления, но не из `float`, JavaScript `number` и других бинарных floating-point типов. Сложение, вычитание и сравнение допустимы только для одной валюты; масштаб и режим округления явно задаются доменной операцией, которой требуется округление. Хранилище использует своё точное decimal-представление: например, PostgreSQL `numeric`/`decimal` или MongoDB `Decimal128`. В gRPC-контрактах используется `google.type.Money`; инфраструктурный адаптер явно преобразует `units` и `nanos` в доменный decimal и обратно, не используя floating-point арифметику.

Каждый bounded context владеет собственным `Money`: выбранные библиотеки дают реализацию десятичного числа, но не создают общую межконтекстную доменную модель. Зависимость добавляется в конкретный контекст только тогда, когда начинается реализация использующего её сценария.

### `applications/`

Содержит прикладные сценарии слайса, по одному package/module на use case:

- обязательный `README.md` с actor, намерением, результатом, владельцем данных и существенными взаимодействиями;
- use cases;
- commands, queries и их handlers там, где они предусмотрены архитектурой контекста;
- прикладные входные и выходные модели;
- координацию загрузки агрегатов, вызова доменного поведения и сохранения результата;
- определение границы транзакции;
- порты внешних интеграций и чтения данных, необходимые конкретным сценариям.

Handler управляет выполнением сценария. Правила допустимого изменения доменного состояния остаются в доменной модели.

Общее для нескольких use cases слайса получает собственный каталог по ответственности (`applications/permission/`, `applications/errorkind/`): файлов прямо в `applications/` нет.

Sequence diagram в README используется для нетривиальной оркестрации, асинхронного обмена и взаимодействия нескольких владельцев. Для локального линейного сценария достаточно текста. Handler и другие исполняемые артефакты появляются вместе с реализацией SDD change; пустой scaffold рядом с README не создаётся.

### `pkg/`

Содержит общие библиотеки bounded context, у которых нет одного business owner: контракт use case и его ошибки (`usecase`), identity и authorization (`authz`), локальную транзакцию (`unitofwork`, `unit_of_work`, `unit-of-work`), конфигурацию, lifecycle и telemetry. Имя пакета называет ответственность, а не слой: `pkg/application`, `pkg/infrastructure` и `pkg/di` не создаются. Use cases, агрегаты, бизнес-правила и адаптеры в `pkg/` не размещаются.

### `infrastructure/`

Содержит технические реализации и адаптеры:

- реализации репозиториев;
- работу с БД и моделями хранения;
- адаптеры внешних API;
- транспортные обработчики и адаптеры обмена сообщениями;
- преобразование транспортных DTO и моделей хранения в прикладные и доменные типы.

Инфраструктура реализует контракты внутренних частей слайса. Адаптеры всего контекста (gRPC API, messaging, клиент internal gateway, миграции) находятся в `<boundary>/infrastructure/`; адаптер, реализующий порты одного слайса, — в `infrastructure/` этого слайса, а его сборка — в `di/` слайса. Прикладной слой и домены существуют только внутри слайсов.

### Направление зависимостей

На уровне импортов действуют следующие правила:

- `applications` зависит от `domains`;
- `infrastructure` зависит от необходимых контрактов и типов в `applications` и `domains`;
- `domains` не зависит от `applications`, `infrastructure`, ORM, транспорта и DI-библиотек;
- `domains/<domain>/vo` не зависит от агрегатов, сущностей и внешних адаптеров.

Контракт репозитория агрегата размещается в `domains`, а его реализация — в `infrastructure`. Порты прикладных интеграций и query-сценариев размещаются рядом с потребляющим кодом в `applications`. Выбор и сборка конкретных реализаций выполняются в существующем composition root.

### Общая доменная модель внутри слайса

Один агрегат и его инварианты имеют одну каноническую реализацию внутри bounded context. Если несколько слайсов работают с одним агрегатом, они используют общую доменную модель этого контекста: например, создание и отмена заказа не получают собственные расходящиеся реализации `Order`.

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
          README.md
        requestordercancellation/
          README.md
        completeorder/
          README.md
```

Например, use cases `placeorder`, `requestordercancellation` и `completeorder` используют один `Order` из `slices/order/domains/order/order.go`. Они остаются отдельными packages внутри `slices/order/applications/`.

Каждый bounded context самостоятельно владеет своей моделью. Совпадение названий сущностей или value objects в разных контекстах само по себе не является основанием для общей библиотеки.

### DTO, Commands, Queries и entrypoints

```text
Transport DTO → Command / Query → Domain Model
Domain Model / Read Model → Application DTO → Transport DTO
```

| Модель | Размещение | Правило |
| --- | --- | --- |
| Transport DTO — Protobuf, Kafka event payload, HTTP/webhook payload, Temporal Activity input/output | `infrastructure/` adapter'а-владельца | Сгенерированный Buf-код владельца живёт в `infrastructure/grpc/gen`, контракты соседей подключаются как BSR Generated SDK; импортируются только из `infrastructure` |
| Command — намерение изменить состояние (`PlaceOrderCommand`) | `<slice>/applications/<usecase>/` | Без бизнес-логики и transport-типов |
| Query — чтение без изменения состояния (`GetOrderQuery`) | `<slice>/applications/<usecase>/` | Без transport-типов |
| Application DTO — вход/выход use case (`OrderDetailsDTO`) | `<slice>/applications/<usecase>/` | Без поведения; read model query **является** Application DTO; не создаётся на каждый domain type |

Именование следует идиомам языка: в Go допустимо `PlaceOrder` вместо `PlaceOrderCommand`. Доменная модель не зависит от Protobuf, GraphQL, Kafka, ORM, Temporal SDK и DTO; доменные ошибки типизированы (sentinel, класс, `enum`), чтобы `infrastructure` сопоставляла их с gRPC-кодами.

Все entrypoints сводятся к Command или Query и прямому вызову handler'а через DI, без in-process command bus или mediator:

- gRPC/HTTP — adapter выполняет transport validation, собирает Command/Query и вызывает handler;
- Kafka integration event — consumer преобразует payload в Command (`MarkRestaurantAcceptedCommand`), Inbox оборачивает вызов handler'а; событие не является отдельным типом модели;
- Temporal Activity — преобразует payload в Command/Query и вызывает handler; Workflow вместе с типами Activity payloads находится в Temporal-адаптере в `infrastructure/` и не знает о Commands и домене.

Mapping transport ↔ application выполняется в `infrastructure/`, application ↔ domain — в `applications/` там, где типы различаются. Для исходящих integration events application решает, какое событие создать, а `infrastructure/` преобразует его в Protobuf и записывает в Outbox в той же транзакции. Глобальный mapper framework не используется.

Решение зафиксировано в [ADR 6](../docs/adr/0006-ddd-structure-inside-vertical-slices.md).

## Dependency Injection

Dependency Injection (DI) описывает зависимости vertical slices и способ их сборки, не меняя организацию кода вокруг сценариев. Каждый исполняемый процесс, например API или worker, имеет собственный composition root. Интерфейсы зависимостей располагаются рядом с потребляющим кодом, если структура конкретного контекста не обосновывает другое место; общие регистрации выделяются только после появления реального повторного использования.

| Контекст | Подход |
| --- | --- |
| [`orders`](orders/README.md#dependency-injection) | Compile-time DI через Google Wire |
| [`restaurants`](restaurants/README.md#dependency-injection) | Awilix |
| [`delivery`](delivery/README.md#dependency-injection) | Compile-time DI через shaku |
| [`payments`](payments/README.md#dependency-injection) | Dependency Injector как кандидат |

Контексты взаимодействуют через согласованные контракты и не импортируют внутренние контейнеры или реализации друг друга. Транзакции и другие ресурсы отдельной операции получают соответствующее время жизни. Доменные объекты и обработчики use cases должны создаваться в тестах с явно переданными зависимостями.

Стратегия и статус отдельных инструментов описаны в [ADR 5](../docs/adr/0005-dependency-injection-strategy.md).

## Хранилища и владение данными

Для контекстов предлагается polyglot persistence, исходя из их моделей и сценариев:

| Bounded context | Основное хранилище |
| --- | --- |
| `orders` | PostgreSQL |
| `restaurants` | MongoDB |
| `payments` | Не выбрано; требуется отдельное решение |
| `delivery` | Не выбрано; требуется отдельное решение |

Каждый сервис владеет своим логическим хранилищем и доступом к нему. BFF и соседние сервисы получают данные через API или интеграционные события, а не читают чужие таблицы или коллекции. Ссылки на понятия другого bounded context передаются и хранятся как идентификаторы; межконтекстные foreign keys не создаются.

PostgreSQL `orders` и MongoDB `restaurants` не участвуют в общей транзакции. Согласованность между контекстами обеспечивается явно спроектированными взаимодействиями, а не распределённой транзакцией хранилищ. MongoDB является основным хранилищем `restaurants`, а не read store для `orders`.

Контракты репозиториев остаются во внутренних слоях согласно [DDD-структуре](#организация-кода-внутри-контекста). Реализации репозиториев, SQL/BSON-модели хранения и преобразования располагаются в `infrastructure` соответствующего сервиса или слайса. `domains` и `applications` не зависят от SQL, BSON, драйверов, ORM или ODM.

Распределение, основания и открытые эксплуатационные вопросы описаны в предложенном [ADR 8](../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md). Версии СУБД, библиотеки доступа, миграции и физические модели будут выбраны на следующих шагах.

## Unit of Work

Use case, которому нужна атомарность нескольких persistence operations — aggregate, Inbox, Outbox, idempotency key, — использует Unit of Work: локальную транзакцию хранилища своего boundary. Это не distributed transaction: Kafka, Temporal, SpiceDB, gRPC к другим boundaries и платёжные провайдеры находятся вне неё и вызываются до или после commit.

Port — один на boundary в shared application-уровне (`Do(ctx, func(ctx) error) error` в Go, идиоматичный transaction scope в других языках), реализация — в shared infrastructure boundary; `domains/` о транзакциях не знает. UoW не раздаёт repositories: они, Outbox и UoW внедряются в handler через DI и участвуют в активной транзакции через единый механизм propagation. Границу определяет use case, а не transport: транзакция на каждый gRPC request запрещена. Isolation по умолчанию, optimistic concurrency по `version`, bounded retry, Inbox insert-first и Outbox в одной транзакции, порядок с SpiceDB и Temporal описаны в [документации Unit of Work](../docs/unit-of-work.md), решение — в [ADR 20](../docs/adr/0020-unit-of-work-for-local-transactions.md).

## Authorization

SpiceDB используется как централизованный authorization graph, но каждый bounded context остаётся authoritative enforcement point для своих операций и ресурсов. BFF может выполнять дополнительную coarse-grained проверку, однако state-changing use case всегда проверяет permission повторно у владельца операции. Затем `domains/` независимо проверяет доменные инварианты.

Subject context (`user` или конкретная `service` identity) должен быть проверяемым: propagated access token либо signed internal identity. Доверять произвольному header, факту прохождения через internal gateway или одной NetworkPolicy недостаточно. Temporal Activities, Kafka consumers и фоновые задачи используют собственную `service` identity; исходный user subject сохраняется для audit, но не impersonate при позднем выполнении.

Authorization port располагается рядом с use case в `applications/`. SpiceDB client и adapter находятся в `infrastructure/`; `domains/` и `domains/<domain>/vo/` не знают о SpiceDB SDK, gRPC, schema или ZedToken. Обычные checks используют `minimize_latency`, read-after-write — `at_least_as_fresh` с ZedToken. State-changing операции fail closed при denial, error и timeout.

Business storage контекста остаётся source of truth, а SpiceDB хранит производный graph. Creation/owner relationships записываются синхронно до local commit; derived relationships — через Outbox и dedicated consumer с Inbox, если use case допускает eventual consistency. `orders` синхронизирует связи `order` по состоянию через Outbox без dual write и reconciliation ([ADR 18](../docs/adr/0018-spicedb-for-authorization.md#relationship-lifecycle-и-dual-write)). Идемпотентные writes используют `TOUCH`, drift исправляет reconciliation. Полный contract — в [документации security](../docs/security.md), решение — в [ADR 18](../docs/adr/0018-spicedb-for-authorization.md).

## gRPC и Protocol Buffers

Синхронные service-to-service вызовы используют gRPC и проходят через internal gateway: вызывающее приложение, включая BFF и Temporal Activity workers, подключается к адресу из `INTERNAL_GATEWAY_ADDR`, а не к Kubernetes Service владельца API. Соответствие сервиса и метода backend'у хранится в реестре `GRPCRoute`, прямой доступ запрещён NetworkPolicy ([ADR 15](../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md), [ADR 16](../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md)). Каждый новый RPC публикуется маршрутом с явным решением о retries — порядок описан в [`ops/kind/README.md`](../ops/kind/README.md#internal-gateway).

Исходной `.proto`-схемой владеет предоставляющий API контекст; контракт и адаптер располагаются в его `infrastructure/grpc`. Корень Buf-модуля совпадает с этим каталогом, а package повторяет путь внутри модуля:

```text
boundaries/<service>/infrastructure/grpc/
  nomnom/<domain-service>/v1/
    <domain-service>.proto
    service.proto
```

| API | Корень модуля | Package | BSR-модуль |
| --- | --- | --- | --- |
| Orders | `boundaries/orders/infrastructure/grpc` | `nomnom.orders.v1` | `buf.build/nomnom/orders` |
| Restaurants | `boundaries/restaurants/infrastructure/grpc` | `nomnom.restaurants.v1` | `buf.build/nomnom/restaurants` |
| Delivery | `boundaries/delivery/infrastructure/grpc` | `nomnom.delivery.v1` | `buf.build/nomnom/delivery` |
| Payments | `boundaries/payments/infrastructure/grpc` | `nomnom.payments.v1` | `buf.build/nomnom/payments` |

Префикс `nomnom.` и повтор имени сервиса в пути обязательны. Они предотвращают коллизии пакетов и обеспечивают `PACKAGE_DIRECTORY_MATCH`. Отдельного общего `contracts/` нет, и модули `nomnom` не импортируют друг друга. Пока не появится подтверждённая потребность в `buf.build/nomnom/common`, API самостоятельно описывают Address, пагинацию и другие transport messages. Стандартные типы берутся из `buf.build/googleapis/googleapis` и `buf.build/protocolbuffers/wellknowntypes`.

Владелец генерирует серверные stubs из локального модуля. Его generated code коммитится в `infrastructure/grpc/gen`, а CI проверяет повторяемость через `buf generate` и `git diff --exit-code`. Потребитель подключает BSR Generated SDK как зависимость с закреплённой версией в `go.mod` или `package.json`; labels для пиннинга не применяются. Так же получают клиенты адаптеры слайсов BFF. Generated gRPC-контракт Cosmo Router/plugin в `boundaries/bff/plugins/bff/generated` является отдельным техническим контрактом runtime.

Адаптер переводит transport messages в типы `applications` и `domains` и обратно. Protobuf-типы не импортируются внутренними слоями. Он также фиксированно отображает ошибки и добавляет `google.rpc.ErrorInfo` со стабильными `domain` и `reason`:

| Категория | gRPC code |
| --- | --- |
| Ресурс не найден | `NOT_FOUND` |
| Некорректный запрос | `INVALID_ARGUMENT` |
| Операция запрещена текущим состоянием | `FAILED_PRECONDITION` |
| Ресурс уже существует | `ALREADY_EXISTS` |
| Нет аутентификации | `UNAUTHENTICATED` |
| Недостаточно прав | `PERMISSION_DENIED` |
| Неожиданная ошибка | `INTERNAL` |
| Зависимость временно недоступна | `UNAVAILABLE` |

Совместимость проверяется Buf policy `FILE`: поля в `v1` не удаляются и не переименовываются, а помечаются `[deprecated = true]`; несовместимый API получает package `v2`. Полные правила, скелеты `buf.yaml` и `buf.gen.yaml`, версии generators и workflow публикации зафиксированы в [ADR 7](../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md).

## Интеграционные события

Асинхронное взаимодействие между bounded contexts строится через интеграционные события. Apache Kafka используется как транспорт этих событий.

В Go-контекстах публикация и обработка сообщений выполняются через Watermill и адаптер `watermill-kafka`. Выбор Kafka-клиентов для TypeScript, Rust, Python и других языков остаётся открытым.

Доставка имеет семантику at-least-once (Guaranteed Delivery), обработка — idempotent / effectively-once в пределах локального хранилища consumer. Exactly-once между сервисами не обещается, порядок гарантируется только внутри Kafka partition по выбранному key:

- publisher фиксирует изменение состояния и запись Transactional Outbox в одной локальной транзакции, а Outbox publisher отдельно доставляет событие в Kafka; синхронная публикация внутри бизнес-транзакции и dual write без Outbox запрещены;
- каждый контекст владеет своим Outbox в своём основном хранилище и не использует Outbox другого контекста;
- каждое интеграционное событие имеет стабильный `event_id`, передаваемый в Kafka header `event_id`; повтор операции после commit не создаёт второго события, потому что операции, пишущие Outbox, идемпотентны по idempotency key или `aggregate_version`;
- publisher публикует записи одного key в порядке вставки и не пропускает упавшую публикацию (head-of-line blocking с alert'ом);
- в `orders` Outbox реализуется через `watermill-sql` и Watermill Forwarder, Kafka key передаётся через metadata и partitioning marshaler, Forwarder работает как single active instance; для `restaurants`, `delivery` и `payments` библиотеки и выбор polling или CDC остаются открытыми;
- consumer интеграционных событий в `orders`, `restaurants`, `delivery` и `payments`, изменяющий своё состояние, использует Transactional Inbox: проверка `event_id`, изменение business state и запись Inbox выполняются в одной локальной транзакции, а Kafka acknowledgement — только после commit;
- если обработчик публикует новое событие, Inbox, business state и Outbox фиксируются в одной транзакции;
- уникальность `(consumer, event_id)` обеспечивается хранилищем: уникальным ограничением PostgreSQL, unique index MongoDB или эквивалентной атомарной защитой;
- внутренние доменные события внутри одной транзакционной границы Inbox не используют.

Inbox, Outbox, Kafka consumer и acknowledgement находятся в `infrastructure/`; `applications/` обрабатывает событие как use case, не проверяя Inbox вручную, а `domains/` не знает о механизме доставки. Ordering решается partition key и при необходимости `aggregate_version` отдельно от дедупликации.

Pipeline, ошибки, конкурентная обработка, observability, fault scenarios и открытые вопросы описаны в [документации событийного взаимодействия](../docs/event-driven-architecture.md), решения — в [ADR 13](../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md) (Outbox) и [ADR 11](../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md) (Inbox).

## Runtime lifecycle

Каждый запускаемый процесс контекста — API, Kafka consumer, Outbox relay или Temporal worker — реализует общий cloud-native runtime contract: startup, liveness и readiness с разной семантикой, health на техническом порту `metrics`, graceful shutdown по `SIGTERM`, конфигурация извне с fail fast и учёт resource limits. Consumers подтверждают только обработанные сообщения, а повторная доставка после shutdown обрабатывается Inbox. Lifecycle, health и обработка сигналов находятся в composition root процесса; `domains/` и `domains/<domain>/vo/` не знают о контейнерах, Kubernetes, сигналах и probes, а `applications/` получает только языковые абстракции cancellation и deadline. Подробности — в [документации cloud-native runtime](../docs/cloud-native-runtime.md) и [ADR 17](../docs/adr/0017-cloud-native-application-runtime.md).

## Observability

Все запускаемые приложения bounded contexts используют общую идентификацию и правила инструментирования, но observability SDK остаются за пределами `domains/` и `domains/<domain>/vo/`. Транспорт, БД, Kafka и внешние клиенты инструментируются в `infrastructure/`, а lifecycle SDK принадлежит bootstrap и composition root. Полная схема находится в [общесистемной документации observability](../docs/observability.md).

| Контекст | Язык | Ответственность |
| --- | --- | --- |
| [`orders`](orders/README.md) | Go | Оформленный заказ, его состав и стоимость, общий жизненный цикл, запросы отмены и оркестрация процесса через Temporal |
| [`restaurants`](restaurants/README.md) | TypeScript | Рестораны, меню, доступность блюд, принятие заказа рестораном и приготовление |
| [`delivery`](delivery/README.md) | Rust | Доступность курьеров, предложения доставки, назначение курьера и выполнение доставки |
| [`payments`](payments/README.md) | Python | Авторизация, списание, возвраты и результаты платёжных операций |

`restaurants` управляет приготовлением, `delivery` — назначениями и доставкой, `payments` — платёжными операциями. `orders` координирует общий процесс и отражает результаты других контекстов в состоянии заказа.
