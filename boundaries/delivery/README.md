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

Статус реализации: все перечисленные Commands и Queries реализованы как application handlers (`Input` / `Output` / `Handler::execute`) по SDD change [`implement-delivery-application-layer`](../../openspec/changes/implement-delivery-application-layer/) с in-memory адаптерами портов; транспорт, хранилище и интеграционные адаптеры ещё не подключены. Требования — `FR-300`…`FR-318`, acceptance criteria — `AC-300`…`AC-343`. Командные use cases процесса исполнения заказа (Cancel Delivery, Replan Delivery) и их gRPC-методы добавлены по change [`add-fulfillment-command-rpcs`](../../openspec/changes/add-fulfillment-command-rpcs/) (`FR-429`, `AC-455`, `AC-456`).

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

Transport adapter уже маппит application errors в gRPC status codes; добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.delivery.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). `GetCourierCurrentDelivery`, `CreateDelivery`, `ReplanDelivery` и `CancelDelivery` подключены к application use cases через tonic adapter; `cmd/delivery` запускает gRPC-сервер и завершает его через общий lifecycle. До появления проверяющего JWT/mTLS adapter'а production `SubjectResolver` отклоняет вызовы без доверенной identity.

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
- `di` — корневой composition root: выбирает реализации портов (сейчас in-memory адаптеры из `infrastructure::memory` и одно общее хранилище на оба слайса), задаёт `AssignmentPolicy` и собирает итоговый `ServiceModule`.
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

Хранилище `delivery` ещё не выбрано. Его выбор должен подтвердить атомарную уникальность `(consumer, event_id)` вместе с изменением business state, а также transaction capabilities для Unit of Work и защиту конкурентных courier assignments — см. [документацию Unit of Work](../../docs/unit-of-work.md#delivery) и [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md). Kafka-клиент, схема Inbox и адаптеры относятся к следующим шагам. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

## Observability

Инструментирование Rust транспорта, хранилища, Kafka и внешних клиентов размещается в `infrastructure/`, а lifecycle SDK и profiler — в bootstrap процесса. Rust profiling зависит от выбранного client/backend и целевой платформы и проверяется до добавления зависимости. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализованы:

- доменная модель: `Courier` (включая `unavailable`), `Location`, табличные FSM `Delivery` (6 states × 6 triggers, включая `Cancelled`/`Cancel`) и `DeliveryOffer` (4 × 3) с тестами полноты, domain events `CourierAssignedToDelivery`, `CourierUnassignedFromDelivery`, `DeliveryPickedUp`, `DeliveryCompleted`, `DeliveryFailed`, `DeliveryCancelled`; policies `CourierEligibleForDelivery` и `DeliveryOfferAcceptable`; контракты репозиториев рядом с моделями;
- application handlers всех 16 use cases в `slices/<slice>/applications/<usecase>/mod.rs` с unit-тестами;
- порты: `UnitOfWork`, `Clock`, `IdGenerator`, `Subject`/`AuthorizationError` (`pkg/application`), `RepositoryError` (`pkg/persistence`), `CourierAuthorization` и `DeliveryAuthorization`, `DeliveryOutbox`, `CourierLocator`, `CourierLocationStore`, read models `CourierCurrentDeliveryReadModel` и `PendingDeliveryOffersReadModel`, параметры `AssignmentPolicy`;
- in-memory адаптеры всех портов в `infrastructure/memory` (транзакционный store с rollback и инъекцией сбоя commit, статическая authorization, управляемые clock и id generator, статический locator) — только для тестов и локальной сборки.

Не реализованы: постоянное хранилище и optimistic concurrency, Outbox relay и Kafka, SpiceDB adapter, production JWT/mTLS `SubjectResolver`, расчёт геопозиции/зон, BDD `.feature`. По gRPC опубликованы `GetCourierCurrentDelivery`, `CreateDelivery`, `ReplanDelivery`, `CancelDelivery`; остальные application handlers к transport пока не подключены.

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
