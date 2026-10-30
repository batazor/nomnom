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

## Market

По [ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md) `delivery` работает в Cell и обслуживает размещённые в ней Market. `market_id` — slug `^[a-z][a-z0-9-]{1,31}$` (VO `MarketId` в `slices/courier/domains/courier/market.rs`), обязательный и неизменяемый:

- `Courier` получает Market из `RegisterCourier.market_id`;
- `Delivery` — из `CreateDelivery.market_id` (Market заказа); повтор Create Delivery по тому же `order_id` с другим Market — ошибка `ALREADY_EXISTS`, а не replay;
- курьеру предлагается и назначается только Delivery его Market: правило `Courier::ensure_serves` в агрегате курьера (порт `CourierAssignment::ensure_available`/`occupy` принимает Market доставки), причина `OtherMarket` спецификации `CourierEligibleForDelivery`, а поиск кандидатов (`CourierLocator::candidates(market_id)`) фильтрует курьеров по Market в SQL;
- `GetCourierPool` читает курьеров, Pool Load и backlog ожидающих доставок одного Market;
- каждое Integration Event несёт `market_id` агрегата.

Пустой или не slug `market_id` — `INVALID_ARGUMENT`. Ячейка не проверяет, что Market размещён в ней. Строки до введения Market получили стартовый Market `berlin` миграциями `couriers/0003_courier_market.sql` и `deliveries/0007_delivery_market.sql`. Demo Simulator Market не выбирает: он не регистрирует курьеров, а действует только от имени уже зарегистрированных курьеров над их offers и доставками, поэтому Market берётся из данных.

## Use Cases

Статус реализации: все перечисленные Commands и Queries реализованы как application handlers (`Input` / `Output` / `Handler::execute`) по SDD change [`implement-delivery-application-layer`](../../openspec/changes/archive/2026-10-25-implement-delivery-application-layer/) ; хранилище — PostgreSQL (см. «Хранилище (PostgreSQL)»), Integration Events из Outbox публикует коннектор Debezium `delivery-outbox` (CDC, см. «Событийная интеграция»); единственный Kafka consumer — `delivery.duration-statistics` собственных фактов Delivery (см. «Delivery Duration Statistics»). Требования — `FR-300`…`FR-318`, acceptance criteria — `AC-300`…`AC-343`. Командный use case процесса исполнения заказа Cancel Delivery и его gRPC-метод добавлены по change [`add-fulfillment-command-rpcs`](../../openspec/changes/archive/2026-10-25-add-fulfillment-command-rpcs/) (`FR-429`, `AC-455`, `AC-456`). gRPC API курьера и Register Courier добавлены по change [`close-order-lifecycle-in-kind`](../../openspec/changes/archive/2026-10-25-close-order-lifecycle-in-kind/) (`FR-580`, `FR-581`, `AC-730`…`AC-733`). Поиск курьера ведёт Assignment Workflow на Temporal (change [`move-courier-assignment-to-temporal`](../../openspec/changes/archive/2026-10-30-move-courier-assignment-to-temporal/), `FR-704`…`FR-708`, [ADR 2](docs/adr/0002-temporal-для-назначения-курьера.md)).

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Register Courier** | Оператор или диспетчер Courier Pool | Создать курьера в системе доставки | Создан `Courier` с именем (1–64 символа) и исходным состоянием доступности | Authoritative authorization в `delivery`, `platform#manage_couriers` ([FR-301](../../openspec/changes/add-cook-and-courier-pools/)) |
| **Retire Courier** | Оператор или диспетчер Courier Pool | Убрать курьера из Courier Pool | Курьер вне линии выбыл и больше не получает офферов; на линии — `OnLine` | `platform#manage_couriers` ([FR-619](../../openspec/changes/add-cook-and-courier-pools/)) |
| **Set Courier Availability** | Курьер или диспетчер Courier Pool | Изменить доступность курьера для новых назначений | `Courier` отмечен available или unavailable; позиция курьера не изменена; выбывший не возвращается | Не совмещается с location update; `courier#act`, иначе `platform#manage_couriers` ([FR-620](../../openspec/changes/add-cook-and-courier-pools/)) |
| **Update Courier Location** | Курьерское приложение | Обновить последнюю известную позицию курьера | Позиция записана, если сохранённая старше `TRACKING__PERSIST_INTERVAL`; у курьера с активной доставкой точка опубликована в её Tracking Channel | FR-303, FR-661; см. «Live tracking» |
| **Create Delivery** | Fulfillment process `orders` | Запросить доставку оформленного заказа | Создана идемпотентная `Delivery` для заказа | Вызывается Temporal Activity через API `delivery`; после commit, и на повторе, запускает Assignment Workflow доставки |
| **Find Courier** | Процесс назначения `delivery` | Подобрать подходящего доступного курьера | Выбран кандидат или зафиксировано отсутствие кандидата; алгоритм остаётся внутри `delivery` | Может запускаться запросом orchestration, но выбор выполняет `delivery` |
| **Offer Delivery** | Процесс назначения `delivery` | Предложить конкретному курьеру принять доставку | Создано актуальное ограниченное по времени `Delivery Offer` и `DeliveryOffered` в Outbox; `Courier Assignment` ещё не создан | Учитывает результат Find Courier |
| **Offer Delivery To Courier** | Диспетчер Courier Pool или оператор | Предложить доставку заказа выбранному курьеру | Создано `Delivery Offer` выбранному курьеру и `DeliveryOffered` в Outbox; назначение — только после Accept Delivery Offer | Шаг Offer Delivery (`make_offer`) под `platform:nomnom#offer_delivery`; offers процесса назначения не отзываются (см. [README](slices/delivery/applications/offer_delivery_to_courier/README.md)) |
| **Accept Delivery Offer** | Курьер | Принять актуальное предложение | Ровно один актуальный offer успешно превращён в `Courier Assignment`; курьер и доставка зарезервированы | Конкурентное принятие защищается доменным инвариантом и атомарной записью; публикует бизнес-факт через Outbox |
| **Reject Delivery Offer** | Курьер | Отклонить актуальное предложение | Offer закрыт как отклонённый и не создаёт `Courier Assignment` | После commit будит Assignment Workflow, который предлагает доставку другому курьеру |
| **Reassign Courier** | Процесс назначения `delivery` или авторизованный диспетчер | Прекратить неактуальное назначение и подобрать замену | Текущее назначение отменено, доставка снова участвует в поиске | После commit будит Assignment Workflow доставки и поиск Market освобождённым курьером |
| **Cancel Delivery** | Fulfillment process `orders` | Отменить доставку заказа до Pickup по `order_id` | `waiting`/`assigned` → `cancelled`, курьер освобождён, `DeliveryCancelled` в Outbox; повтор — успех; после Pickup — бизнес-отказ `already_picked_up`, после `delivered`/`failed` — `already_closed` | Вызывается Temporal Activity через `CancelDelivery` как компенсация; бизнес-отказ не является ошибкой |
| **Confirm Pickup** | Назначенный курьер | Подтвердить получение заказа у ресторана | Для `Delivery` зафиксирован Pickup | Публикует бизнес-факт через Outbox |
| **Confirm Delivery** | Назначенный курьер | Подтвердить передачу заказа клиенту | Для `Delivery` зафиксирована Delivery Completion | Публикует бизнес-факт через Outbox |
| **Report Delivery Failure** | Назначенный курьер или авторизованный диспетчер | Зафиксировать невозможность завершить доставку | Неудача сохранена с кодом причины из каталога Delivery Failure Reason и необязательным пояснением, результат доступен `orders` | Публикует бизнес-факт через Outbox; неизвестный код — ошибка валидации; retryability кодов остаётся открытым вопросом |
| **Expire Delivery Offer** | Таймер Assignment Workflow (`delivery-worker`) | Закрыть pending offer, на который не ответили до `expiresAt` | Offer переведён в `expired` с `DeliveryOfferExpired` в Outbox; принятый, отклонённый или отозванный offer не меняется | Без subject и проверки прав (см. «Assignment Workflow») |
| **Advance Assignment** | Шаг Assignment Workflow (`delivery-worker`) | Сказать workflow, чего ждать дальше | `finished`, `assigned`, открытый offer или — для ожидающей доставки — истечение просроченного pending offer, Find Courier и Offer Delivery в одной Unit of Work; без кандидата — `no_candidate` | Без subject и проверки прав; конкурентное изменение — повтор Activity (см. «Assignment Workflow») |
| **Record Delivery Duration** | Consumer `delivery.duration-statistics` собственных фактов Delivery | Обновить Delivery Duration Statistics Market | По Pickup записан `courier_lead` (Create Delivery → Pickup) и открыт transit, по вручению — `transit`; отмена или неудача закрывают transit без длительности; повтор `event_id` — no-op (Inbox) | Без subject и проверки прав; Inbox и статистика в одной Unit of Work ([FR-676](../../openspec/changes/add-scheduled-orders/specs/delivery-duration-estimate/spec.md)); см. «Delivery Duration Statistics» |
| **Set Simulator Enabled** | Оператор платформы | Включить или выключить демо Simulator курьеров | Переключатель сохранён в PostgreSQL и переживает рестарт | `platform#operate_simulator`, иначе `PERMISSION_DENIED` (FR-604); см. «Demo Simulator» |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Courier Pool** | Оператор или диспетчер Courier Pool | Увидеть линию и загрузку курьеров | Размер пула, на линии, занято, Delivery в `Waiting` и невыбывшие курьеры с именем, pending offer и активной доставкой | Query model `delivery`; `platform#manage_couriers` ([FR-621](../../openspec/changes/add-cook-and-courier-pools/)) |
| **Get Order Delivery** | Тот, кто видит заказ (покупатель) | Узнать, кто везёт заказ, и следить за ним | Имя курьера доставки в `Assigned`, `PickedUp`, `Delivered` и, пока доставка в `Assigned`/`PickedUp` с каналом, Tracking Channel, Virtual City, точки pickup и dropoff и сохранённая позиция курьера; без доставки — пусто | Одна проверка `order#view`, один запрос query model `delivery` ([FR-626](../../openspec/changes/add-cook-and-courier-pools/), [FR-660](../../openspec/changes/add-live-courier-tracking/)) |
| **Get Courier Current Delivery** | Курьер | Получить свою активную доставку | Возвращено текущее назначение и разрешённое представление `Delivery` либо отсутствие активной доставки | Query model `delivery`; проверка принадлежности курьеру |
| **List Available Delivery Offers** | Курьер | Получить актуальные предложения, доступные этому курьеру | Возвращён список непротухших `Delivery Offer` без изменения состояния | Query model `delivery`; проверка адресата offer |
| **Estimate Delivery Duration** | Fulfillment process `orders` | Оценить доставку в Market для плана запланированного заказа | Медианы `courier_lead` и `transit` последних `DELIVERY_ESTIMATE__SAMPLE_SIZE` длительностей Market; вид без набранной выборки — его базовое значение `DELIVERY_ESTIMATE__BASELINE_*` | `platform#coordinate_delivery` (как команды fulfillment); пустой или не slug `market_id` — `INVALID_ARGUMENT` ([FR-691](../../openspec/changes/add-scheduled-orders/specs/delivery-duration-estimate/spec.md)) |
| **Get Simulator** | Оператор платформы | Узнать, включён ли демо Simulator | `enabled`; без записи в хранилище — `false` | `platform#operate_simulator`, иначе `PERMISSION_DENIED` (FR-604) |

## Структура слайсов

Слайсы `delivery` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста): `slices/courier` владеет агрегатом и eligibility курьера, `slices/delivery` — доставкой и предложениями доставки. Use cases размещаются в `slices/<slice>/applications/<usecase>/`, shared packages — в `pkg/`. Traits репозиториев агрегатов определяются рядом с моделью в `domains/<domain>/`, а порты внешних интеграций и чтения — рядом с потребляющими сценариями в `applications/`.

Зависимость между слайсами идёт только в направлении `delivery → courier` (FR-486): `slices/courier` не импортирует `slices::delivery`, это проверяет `tests/slice_boundaries.rs`. Поэтому queries Get Courier Current Delivery и Get Courier Pool, которые читают модель `Delivery`, принадлежат слайсу `delivery` (`slices/delivery/applications/get_courier_current_delivery`, `get_courier_pool`); право по-прежнему проверяет `CourierAuthorization` слайса `courier`.

Занятость курьера use cases слайса `delivery` проверяют и меняют только через прикладной порт `CourierAssignment` слайса `courier` (`slices/courier/applications/assignment.rs`: `ensure_registered`, `ensure_available`, `occupy`, `release`), а не через агрегат `Courier` и `CourierRepository` (FR-485). Реализация порта загружает `Courier` через `CourierRepository`, вызывает доменный метод и сохраняет агрегат в текущей Unit of Work.

Accept Delivery Offer, Reassign Courier, Confirm Delivery, Report Delivery Failure и Cancel Delivery изменяют offer, `Delivery` и `Courier` одной локальной транзакцией. Это записанное исключение из правила «один aggregate на транзакцию» ([Unit of Work](../../docs/unit-of-work.md#исключение-offer-delivery-и-courier-в-одной-транзакции)): инвариант «у курьера не больше одной активной доставки» и FR-310 требуют атомарности, а конкуренция за `Courier` ограничена optimistic concurrency по `version`.

## Demo Simulator

Слайс `slices/simulator` (change [`add-demo-simulator`](../../openspec/changes/add-demo-simulator/), FR-604…FR-606) владеет переключателем Simulator (таблица `simulator`, одна строка; нет строки — выключен) и глобальной выборкой работы (pending offers всех курьеров, доставки в `assigned`/`picked_up`). RPC `GetSimulator` и `SetSimulatorEnabled` требуют `platform#operate_simulator`.

Фоновый `di::SimulatorWorker` каждые `SIMULATOR__INTERVAL` читает переключатель: выключенный Simulator сбрасывает расписание и ничего не меняет. Включённый назначает новой сущности момент `firstSeen + random[SIMULATOR__MIN_DELAY, SIMULATOR__MAX_DELAY]` и, когда он наступил, вызывает настоящий use case от имени курьера (`Subject::User(courier_id)`): pending offer — accept 85% / reject 15%; `Assigned` — Confirm Pickup 95% / Report Delivery Failure 5%; `PickedUp` — Confirm Delivery 95% / Report Delivery Failure 5% (причина — случайный код каталога, не требующий пояснения). Offer живёт 60s, поэтому верхняя граница его задержки обрезается до `expiresAt − SIMULATOR__INTERVAL`; если не успевает и `MIN_DELAY`, offer получает действие на последнем проходе до истечения. Сроки — запланированные моменты (FR-610): принимая offer, Simulator сразу выбирает задержку Pickup и передаёт `expected_pickup_at = now + delay`, а Pickup ставит в расписание на этот момент; подтверждая Pickup, так же выбирает задержку вручения, передаёт `expected_delivered_at` и вручает в этот момент. Планы живут только в расписании процесса: после выключения или рестарта доставка планируется заново от первого прохода. Отказ use case пишется в лог, запись расписания удаляется, Simulator продолжает. `SIMULATOR__INTERVAL`, `SIMULATOR__MIN_DELAY` и `SIMULATOR__MAX_DELAY` обязательны, значений по умолчанию нет; `MIN_DELAY > MAX_DELAY` или нулевое значение завершают startup (kind: 5s / 30s / 2m).

Движение (FR-662, FR-667, change [`add-live-courier-tracking`](../../openspec/changes/add-live-courier-tracking/)): доставку с точками Virtual City Simulator ведёт по случайному кратчайшему пути по улицам — в `Assigned` от перекрёстка у сохранённой позиции курьера (позиция вне рамки прижимается к краю) к pickup, в `PickedUp` от pickup к dropoff — с прибытием в запланированный момент шага (`expected_pickup_at`, `expected_delivered_at`). Каждые `SIMULATOR__TRACK_INTERVAL` (`di/simulator_worker/movement.rs`) он вызывает настоящий Update Courier Location от имени курьера с точкой маршрута по доле прошедшего времени; после прибытия курьер стоит в конечной точке до действия. Проход расписания идёт раз в `SIMULATOR__INTERVAL` внутри того же цикла. Маршруты живут в памяти, после рестарта строятся заново. `SIMULATOR__TRACK_INTERVAL` обязателен; ноль или значение больше `SIMULATOR__INTERVAL` завершают startup (kind: 1s). Доставки, созданные до точек, Simulator не двигает.

## Live tracking

Change [`add-live-courier-tracking`](../../openspec/changes/add-live-courier-tracking/) (FR-658…FR-669). Адресов в проекте нет, поэтому пространство доставки — **Virtual City** (`slices/delivery/domains/delivery/tracking.rs`): сетка `TRACKING__CITY_BLOCKS × TRACKING__CITY_BLOCKS` кварталов со стороной `TRACKING__BLOCK_METERS` от юго-западного угла `TRACKING__CITY_ORIGIN_LATITUDE`/`_LONGITUDE`. Координаты остаются широтой и долготой, контракт `UpdateCourierLocation` не меняется.

- **Точки и канал.** Create Delivery выбирает два разных случайных перекрёстка (pickup, dropoff) и Tracking Channel — 128 случайных бит в 32 hex-символах (`RandomSource`, в процессе — ОС через `getrandom`) — и хранит их в `deliveries` (`pickup_x`, `pickup_y`, `dropoff_x`, `dropoff_y`, `tracking_channel`, миграция `0008_delivery_tracking.sql`); повтор возвращает сохранённые. Доставки до миграции живут без точек и канала.
- **Поток позиций.** Update Courier Location пишет позицию в `courier_positions` условным upsert, только если сохранённая старше `TRACKING__PERSIST_INTERVAL`, затем читает канал активной доставки курьера (порт `ActiveTrackingChannel`: слайс `courier` не знает `Delivery`, читает адаптер `Store`) и публикует в Valkey канал `delivery.tracks.<channel>` JSON `{"__typename":"CourierPosition","channel",…,"recordedAt"}` (`infrastructure/tracks.rs`). Ни Integration Event, ни Outbox на точку нет. Ошибка публикации — `UNAVAILABLE`.
- **Чтение.** Get Order Delivery (`order#view`, permission `ViewOrderDelivery`, одна проверка и один SQL) отдаёт имя курьера и трекинг: канал, Virtual City, точки и сохранённую позицию; Router подписывает клиентов на канал сам (ADR 36).

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `TRACKING__CITY_ORIGIN_LATITUDE` | `tracking.city_origin_latitude` | Широта юго-западного угла; обязательна |
| `TRACKING__CITY_ORIGIN_LONGITUDE` | `tracking.city_origin_longitude` | Долгота юго-западного угла; обязательна |
| `TRACKING__CITY_BLOCKS` | `tracking.city_blocks` | Кварталов по стороне, не меньше 2 |
| `TRACKING__BLOCK_METERS` | `tracking.block_meters` | Сторона квартала в метрах, положительная |
| `TRACKING__PERSIST_INTERVAL` | `tracking.persist_interval` | Минимальный интервал записи позиции в PostgreSQL, положительный |
| `VALKEY__URL` | `valkey.url` | `redis://`/`rediss://` URL ACL-пользователя, который только публикует в `delivery.tracks.*`; только из Secret |

Значений по умолчанию нет: пустое или невалидное значение, рамка вне диапазонов широты и долготы и недоступный Valkey (`PING` при старте) завершают startup. В kind — 12 × 12 кварталов по 150 м от `(52.515, 13.385)`, интервал 15s, Valkey `valkey-delivery` в `nomnom-data`.

## gRPC-адаптер

`delivery` владеет API `nomnom.delivery.v1` и публикует его как `buf.build/nomnom/delivery`. Исходные messages находятся в `infrastructure/grpc/nomnom/delivery/v1/delivery.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер генерирует серверные Rust/tonic stubs из локальной схемы и преобразует request messages в прикладные команды или запросы, а результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделями `Courier`, `Delivery` или их handlers. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API:

| Ошибка `delivery` | gRPC code | `ErrorInfo.reason` |
| --- | --- | --- |
| `Courier` или `Delivery` не найдены | `NOT_FOUND` | `RESOURCE_NOT_FOUND` |
| Некорректный запрос доставки | `INVALID_ARGUMENT` | `INVALID_DELIVERY` |
| Курьер занят или переход состояния запрещён | `FAILED_PRECONDITION` | `DELIVERY_PRECONDITION_FAILED` |
| Нет прав на операцию | `PERMISSION_DENIED` | — |
| Неожиданная внутренняя ошибка | `INTERNAL` | `INTERNAL_ERROR` |
| Внешняя зависимость недоступна или исход commit неизвестен | `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` |

RPC сервиса `DeliveryService`:

| RPC | Use case | Ответ |
| --- | --- | --- |
| `GetCourierCurrentDelivery` | Get Courier Current Delivery | `courier_status` и `Delivery` (`ASSIGNED`/`PICKED_UP`) или пусто; незарегистрированный курьер — `NOT_FOUND` |
| `CreateDelivery` | Create Delivery (`order_id`, `market_id` — Market заказа) | `delivery_id`, `created = false` для повтора по тому же заказу и Market; повтор с другим `market_id` — `ALREADY_EXISTS`; пустой или не slug `market_id` — `INVALID_ARGUMENT` |
| `CancelDelivery` | Cancel Delivery (`order_id`, `reason`) | `cancelled = true` (включая повтор) или `cancelled = false` + `rejection_reason` (`already_picked_up`, `already_closed`) — бизнес-результат, не gRPC-ошибка |
| `EstimateDeliveryDuration` | Estimate Delivery Duration (`market_id`) | `courier_lead` и `transit` (`google.protobuf.Duration`); пустой или не slug `market_id` — `INVALID_ARGUMENT`; требует `platform:nomnom#coordinate_delivery` (`service` identity `orders-worker`) |
| `RegisterCourier` | Register Courier (`courier_id` = user id курьера, `name`, `market_id`) | `courier_id`, `status`, `name`; повтор — `ALREADY_EXISTS`; пустое или длиннее 64 символов имя, пустой или не slug `market_id` — `INVALID_ARGUMENT` |
| `RetireCourier` | Retire Courier (`courier_id`) | `changed = false` при повторе; курьер на линии — `FAILED_PRECONDITION` |
| `GetOrderDelivery` | Get Order Delivery (`order_id`) | `courier` (`courier_id`, `name`) и `tracking` (`tracking_channel`, `city`, `pickup`, `dropoff`, `courier_position`); незаданная часть — нет курьера или трекинга; требует `order:<id>#view` |
| `GetCourierPool` | Get Courier Pool (`market_id`) | Pool Load и `couriers[]` одного Market: `size`, `on_line`, `busy`, `waiting` (ожидающие доставки этого Market), `couriers[]` по имени (`pending_offer_id`, `active_delivery_id` пусты без них); пустой или не slug `market_id` — `INVALID_ARGUMENT` |
| `OfferDeliveryToCourier` | Offer Delivery To Courier (`order_id`, `courier_id`) | `offer` (`DeliveryOffer`), `courier_id`, `created = false` при открытом offer того же курьера; нет доставки заказа — `NOT_FOUND`; доставка не ожидает курьера, курьер не `available` или из другого Market — `FAILED_PRECONDITION` |
| `UpdateCourierLocation` | Update Courier Location (`courier_id`, `latitude`, `longitude`) | позиция и `recorded_at_epoch_seconds`; координаты вне диапазона — `INVALID_ARGUMENT` |
| `SetCourierAvailability` | Set Courier Availability (`courier_id`, `availability`) | `status`, `changed = false`, если доступность уже такая; `COURIER_AVAILABILITY_UNSPECIFIED` — `INVALID_ARGUMENT` |
| `ListAvailableDeliveryOffers` | List Available Delivery Offers (`courier_id`) | pending и непротухшие `DeliveryOffer` курьера |
| `AcceptDeliveryOffer` | Accept Delivery Offer (`offer_id`, `courier_id`, необязательный `expected_pickup_at`) | `offer_id`, `delivery_id`, `courier_id`, `already_accepted` |
| `RejectDeliveryOffer` | Reject Delivery Offer (`offer_id`, `courier_id`) | `offer_id`, `delivery_id`, `changed` |
| `ConfirmPickup` | Confirm Pickup (`delivery_id`, `courier_id`, необязательный `expected_delivered_at`) | `delivery_id`, `state`, `changed` |
| `ConfirmDelivery` | Confirm Delivery (`delivery_id`, `courier_id`) | `delivery_id`, `state = COMPLETED`, `changed` |
| `ReportDeliveryFailure` | Report Delivery Failure (`delivery_id`, `courier_id` или пусто для диспетчера, `reason` — код каталога, `detail`) | `delivery_id`, `reason`, `detail`, `released_courier_id`; неизвестный код — `INVALID_ARGUMENT` |

`Delivery` в ответах (`GetCourierCurrentDelivery`) несёт `market_id` своего Market.

Неизвестный `order_id` во всех командах — `NOT_FOUND`. Командный RPC `CancelDelivery` авторизуется над ресурсом `Order(order_id)`.

RPC курьера вызываются от имени пользователя: subject — user из assertion делегата (см. «User identity от BFF»). `RegisterCourier`, `RetireCourier` и `GetCourierPool` требуют `platform:nomnom#manage_couriers` (оператор или диспетчер); `RegisterCourier` и до сохранения записывает `courier:<id>#self@user:<id>`, поэтому пользователь получает `courier#act` на нового курьера. Остальные RPC курьера требуют `courier#act` на `courier_id` запроса, `SetCourierAvailability` — либо `platform:nomnom#manage_couriers`; `ReportDeliveryFailure` без `courier_id` — действие диспетчера с `platform:nomnom#dispatch_delivery`. `OfferDeliveryToCourier` — действие диспетчера Courier Pool с `platform:nomnom#offer_delivery` (оператор или диспетчер). Принадлежность доставки или offer курьеру проверяет домен. Ошибки `courier` и `delivery` переводятся одной таблицей (`infrastructure/grpc/service/status.rs`).

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

Issuer, audience и путь к JWKS обязательны: без них процесс не стартует. Нечитаемый или невалидный JWKS (нет usable RS256/ES256 ключей, ключ без `kid`, дубликат `kid`), пустой allowlist при заданной проверке и неверный формат allowlist завершают startup ошибкой. JWKS читается один раз при старте: после ротации signing key кластера нужен restart. Тесты проходят по общим векторам `tests/identity/fixtures` (AC-461).

### User identity от BFF

По [ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md) BFF передаёт пользователя в metadata `x-nomnom-user-assertion: <jwt>` вместе со своим service token. Порядок проверки в `ServiceTokenSubjectResolver`:

1. service token (выше); без него assertion не рассматривается;
2. assertion нет → `Subject::Service(<service-name>)`, как раньше;
3. assertion повторён или пуст или service не входит в `IDENTITY__USER_DELEGATES` → `UNAUTHENTICATED`;
4. `UserAssertionVerifier`: `alg` ровно `ES256`, `kid` есть в JWKS BFF (ключи EC P-256), `iss` равен `IDENTITY__USER_ISSUER`, `aud` содержит `IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s, `iat` обязателен, `exp - iat ≤ 60 s`, `sub` непустой и без пробелов по краям, `jti` непустой → `Subject::User(<sub>)`.

Service identity делегата остаётся в `Caller::delegate` (`ServiceTokenSubjectResolver::authenticate`); принятие и отказ пишутся в stderr с `jti` и делегатом, без token.

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `IDENTITY__USER_ISSUER` | `identity.user_issuer` | Ожидаемый `iss` assertion, `nomnom-bff` |
| `IDENTITY__USER_JWKS_FILE` | `identity.user_jwks_file` | JWKS BFF; Helm монтирует `/etc/nomnom/user-identity/jwks.json` из `identity.userJwksConfigMap` |
| `IDENTITY__USER_DELEGATES` | `identity.user_delegates` | Services через запятую, которым разрешено передавать user subject, в развёртываниях `bff` |

Все три переменные обязательны: пустая завершает startup ошибкой. Нечитаемый JWKS BFF, JWKS без ES256-ключей и неверный формат списка делегатов завершают startup ошибкой. Ротация ключа BFF: новый `kid` добавляется в ConfigMap, затем rollout restart, затем BFF переключается на новый ключ. Тесты проходят по `tests/identity/fixtures/user-vectors.json` с часами, зафиксированными на `now` (FR-471–FR-473).

## Авторизация (SpiceDB)

Use cases проверяют права через порты `CourierAuthorization` и `DeliveryAuthorization` (`slices/*/applications/authorization.rs`). При заданном `SPICEDB__ENDPOINT` оба порта закрывает `SpiceDbAuthorization` (`infrastructure/spicedb`, [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md), FR-435): каждый check — `PermissionsService.CheckPermission` (consistency `minimize_latency`, для ресурса с сохранённой revision — `at_least_as_fresh`, см. «Authorization revisions») по [схеме](../../authorization/schema.zed) и нормативной таблице [design.md](../../openspec/changes/archive/2026-10-25-add-spicedb-authorization/design.md):

| Порт / permission | SpiceDB check |
| --- | --- |
| `ManageCouriers` на `Platform` | `platform:nomnom#manage_couriers` |
| `ViewOrderCourier` на `Order(id)` | `order:<id>#view` (покупатель и все, кто видит заказ) |
| `CreateDelivery`, `FindCourier`, `OfferDelivery`, `ReassignCourier`, `CancelDelivery` (любой resource) | `platform:nomnom#coordinate_delivery` |
| прочие permissions на `Courier(id)` | `courier:<id>#act` |
| `ReportDeliveryFailure` на `Delivery(id)` (диспетчер) | `platform:nomnom#dispatch_delivery` (`operator`, AC-694) |
| прочие permissions на `Delivery(id)` | `delivery:<id>#act_as_courier` |
| остальные сочетания (`Order`/`Platform` для прочих permissions, `ManageCouriers` на `Courier`) | отказ без обращения к SpiceDB |

Subject: `Subject::User(id)` → `user:<id>`, `Subject::Service(id)` → `service:<id>`. Fulfillment-команды разрешены `service:orders-worker` grant'ом `platform:nomnom#fulfillment` из [`authorization/bootstrap.yaml`](../../authorization/bootstrap.yaml).

Fail closed: разрешает только `PERMISSIONSHIP_HAS_PERMISSION`; `NO_PERMISSION`, conditional и неизвестные значения — `AuthorizationError::Denied`. Ошибка gRPC, недоступность или превышение `SPICEDB__TIMEOUT` — `AuthorizationError::Unavailable` («authorization unavailable», gRPC `UNAVAILABLE`), а не разрешение. Соединение ленивое: недоступный SpiceDB не мешает startup.

Официального Rust SDK у SpiceDB нет: stubs `authzed.api.v1` (`CheckPermission`, `WriteRelationships`, `ReadRelationships` и их типы) генерируются buf remote plugins `neoeinstein-prost`/`neoeinstein-tonic` из `buf.build/authzed/api:v1.57.0` (`infrastructure/spicedb/buf.gen.yaml`, `make generate-spicedb`) и коммитятся в `infrastructure/spicedb/gen`. Preshared key передаётся interceptor'ом как `authorization: Bearer <key>`. Порты синхронные, а handlers tonic — async, поэтому клиент работает в собственном потоке с current-thread runtime, а ожидание ответа на multi-thread runtime выполняется через `block_in_place`.

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `SPICEDB__ENDPOINT` | `spicedb.endpoint` | gRPC endpoint `host:port` (или URL со схемой, совпадающей с `insecure`). Обязателен: пусто — ошибка startup |
| `SPICEDB__PRESHARED_KEY` | `spicedb.preshared_key` | Preshared key SpiceDB; в Kubernetes только из Secret. Обязателен при заданном endpoint, иначе ошибка startup |
| `SPICEDB__INSECURE` | `spicedb.insecure` | `true` — plaintext без TLS (только kind/локально); по умолчанию `false`, TLS с корнями webpki |
| `SPICEDB__TIMEOUT` | `spicedb.timeout` | Deadline одного check, по умолчанию `2s`; должен быть положительным |

`SPICEDB__ENDPOINT` обязателен: при пустом значении процесс завершается ошибкой startup. `StaticAuthorization` из `infrastructure/memory` используется только тестовым графом `di::build()`.

Integration-тесты (`tests/spicedb_authorization.rs`) выполняются против SpiceDB, который тест сам поднимает через testcontainers (`authzed/spicedb` той же версии, что в `ops/kind/bootstrap/values.yaml`, memory datastore с `authorization/bootstrap.yaml`, ключ `devkey`, `tests/support/mod.rs`). Переменных окружения и внешних инстансов нет, нужен только Docker:

```bash
make test-spicedb   # cargo test --test spicedb_authorization --test spicedb_relationships
```

## Relationships и reconciliation (SpiceDB)

По change [`add-restaurant-delivery-relationships`](../../openspec/changes/archive/2026-10-25-add-restaurant-delivery-relationships/) (FR-444, FR-445, AC-477) `delivery` сам пишет relationships своих definitions. Порты записи — `CourierRelationshipWriter` и `DeliveryRelationshipWriter` (`slices/*/applications/relationships.rs`), SpiceDB-адаптер — `SpiceDbRelationships` (`infrastructure/spicedb/relationships.rs`, `WriteRelationships`), локальный/тестовый — `RecordingRelationships` (`infrastructure/memory`).

| Relationship | Source of truth | Запись |
| --- | --- | --- |
| `courier:<id>#self@user:<id>` | зарегистрированный `Courier` | `TOUCH` в Register Courier до сохранения |
| `delivery:<id>#courier@user:<courier_id>` | активное назначение: `Delivery::active_courier()` — `courier_id` у доставки assigned или picked up | `TOUCH` в Accept Delivery Offer (Assignment); `DELETE` при каждом окончании назначения — Reassign Courier, Cancel Delivery, если курьер освобождён, Confirm Delivery и Report Delivery Failure — внутри Unit of Work до commit |

Снятие назначения реализовано одной функцией `release_assignment` (`slices/delivery/applications/assignment.rs`): она освобождает курьера, удаляет `delivery#courier` до commit и сохраняет revision `DELETE` с доставкой (FR-444, FR-447, AC-483). Её вызывают Reassign Courier, Cancel Delivery, Confirm Delivery и Report Delivery Failure (change [`scope-courier-access-to-assignment`](../../openspec/changes/archive/2026-10-25-scope-courier-access-to-assignment/), AC-690, AC-691). `Delivery.courier_id` доставленной или неуспешной доставки остаётся фактом «кто вёз», но доступа не даёт: право курьера действовать и видеть заказ (`order#view` через `delivery->act_as_courier`) живёт, пока назначение активно. Неудачный `DELETE` откатывает Unit of Work (AC-692).

Допущение: курьер — пользователь с тем же id (`courier_id` = `user_id`, см. `GLOSSARY.md`). Ошибка записи отменяет use case (fail closed, `UNAVAILABLE`). Повторы, не меняющие состояние (повторная регистрация, повторное принятие offer, повторная отмена, отмена доставки без курьера), ничего не пишут. Если запись прошла, а commit нет, лишнюю связь удалит reconciliation; `DELETE` до commit при неуспешном commit временно отнимает доступ, его восстановит reconciliation.

Reconciliation (`infrastructure/reconcile.rs`, порт алгоритма `orders`): для `courier#self` (зарегистрированные курьеры) и `delivery#courier` (активные назначения по `Delivery::active_courier()`; связь доставленной или неуспешной доставки удаляется как stale и не восстанавливается, AC-693) граф читается страницами `ReadRelationships` с `fully_consistent`, хранилище — постранично по id и только committed state. Orphan/stale связь удаляется, только если была orphan на двух проходах с интервалом не меньше grace (кандидаты в памяти процесса); missing связь восстанавливается `TOUCH` так же только после двух проходов с интервалом не меньше grace, на которых она отсутствовала при том же subject и той же версии строки `Courier`/`Delivery` в хранилище: Reassign Courier и Cancel Delivery делают `DELETE` до `UPDATE` строки и без row lock, и проход, прочитавший граф после `DELETE`, а хранилище до commit, иначе вернул бы снятому курьеру доступ (FR-563). Кандидат missing ключуется версией строки: страницы `ReconciliationStore::courier_page`/`assignment_page` возвращают `version`, и если между проходами версия изменилась (writer закоммитил, например курьера сняли, снова назначили и снова сняли, а второй `DELETE` ещё не закоммичен), grace начинается заново с новой версией, и такой subject не восстанавливается (AC-760). Пока кандидат ждёт grace (первое наблюдение, внутри grace или после смены версии), он считается в `missing_pending`; кандидат, ставший present или потерявший subject, забывается; id вне грамматики SpiceDB `^[a-zA-Z0-9/_|\-=+]+$` пропускаются и считаются `unrepairable`. Каждый проход пишет в stdout `checked`, `orphans_pending`, `orphans_deleted`, `missing_pending`, `missing_repaired`, `unrepairable`. Reconciler читает PostgreSQL через порт `ReconciliationStore` и работает фоновой задачей основного процесса (`ReconcilingWorker`): первый проход через `min(10s, interval)` после старта, остановка по сигналу shutdown (проход прерывается на границе страницы).

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `RECONCILER__INTERVAL` | `reconciler.interval` | Интервал между проходами, по умолчанию `5m`; положительный |
| `RECONCILER__GRACE` | `reconciler.grace` | Минимальный интервал между двумя проходами до удаления orphan или восстановления missing, по умолчанию `10m` |
| `RECONCILER__PAGE_SIZE` | `reconciler.page_size` | Размер страницы, по умолчанию `500`; `1..1000` |

### Authorization revisions (ZedToken)

По change [`add-authorization-revisions`](../../openspec/changes/archive/2026-10-25-add-authorization-revisions/) (FR-447, FR-448, AC-483, AC-484) writers возвращают ZedToken записи (`written_at`), а use cases сохраняют его в техническом поле `authorization_revision` ресурса в той же операции save: `Courier` — Register Courier (`TOUCH`), `Delivery` — Accept (`TOUCH`), Reassign, Cancel, Confirm Delivery и Report Delivery Failure (`DELETE`). Repeat без записи revision не меняет; старые snapshots загружаются без неё. `SpiceDbAuthorization` читает revision через порт `RevisionReader` (`RepositoryRevisions` поверх репозиториев) и проверяет `courier:<id>`/`delivery:<id>` с `at_least_as_fresh`, поэтому собственная запись видна сразу (назначенный курьер сразу `act_as_courier`, снятый — сразу отказ); без revision и для `platform:nomnom` — `minimize_latency`. Ошибка чтения revision — authorization unavailable (fail closed). Reconciler, восстанавливая missing связь `TOUCH`, сохраняет её revision в курьера/доставку; удаление orphan revision не меняет.

Stubs `authzed.api.v1` теперь включают `CheckPermission`, `WriteRelationships` и `ReadRelationships` (`make generate-spicedb`). Integration-тесты `tests/spicedb_relationships.rs` (SpiceDB и PostgreSQL из testcontainers; reconciler-тест получает собственный PostgreSQL, потому что сканирует все доставки): регистрация → `courier#act`, принятие offer → `delivery#act_as_courier`, Reassign, Cancel, Confirm Delivery и Report Delivery Failure → доступ пропадает, operator платформы → `dispatch_delivery`, reconciler восстанавливает потерянную связь и удаляет сироту только после grace. Проверки идут через адаптер с revisions из хранилища и решаются сразу, без повторов; ждут окна квантования только сироты без revision (`minimize_latency`):

```bash
make test-spicedb   # оба integration-набора, контейнеры поднимаются сами
```

## Assignment Workflow

Поиск курьера ведёт Temporal workflow `delivery:<delivery_id>` (`infrastructure/temporal`, [ADR 2](docs/adr/0002-temporal-для-назначения-курьера.md), FR-704…FR-708) на task queue `delivery`; исполняет его процесс `delivery-worker` (`cmd/delivery-worker`, `di::worker`). Workflow не хранит состояние доставки: каждый шаг — Activity над use case Advance Assignment (`slices/delivery/applications/advance_assignment`), который читает доставку и её offers в одной Unit of Work:

- забранная, доставленная, неуспешная или отменённая доставка — `finished`, workflow завершается;
- назначенная — `assigned`, workflow ждёт signal (Reassign, Pickup, Cancel, Report Delivery Failure);
- ожидающая с открытым offer — `offered`: workflow ждёт signal или Temporal timer на `expires_at + 1 s`, таймер вызывает Expire Delivery Offer для этого offer (`expire_delivery_offer`);
- ожидающая без открытого offer — pending offer с прошедшим сроком закрывается как `expired`, Find Courier выбирает доступного курьера, которому её ещё не предлагали, `Delivery` сохраняется с прочитанной версией, Offer Delivery создаёт offer с `DeliveryOffered` в Outbox — `offered`; без кандидата — `no_candidate`, workflow ждёт signal.

Конкурентное изменение доставки или курьер, переставший быть доступным, — ошибка шага, которую повторяет retry policy Activity (30 s start-to-close, backoff 1 s × 2 до 60 s); неизвестная доставка или offer — non-retryable. Перед шагом workflow запоминает счётчик signals, поэтому signal во время шага не теряется. Continue-as-new — по подсказке сервера.

Signal `wake` (причина — только для Event History) use cases передают после commit через порт `AssignmentProcess` (`applications/assignment_process.rs`, адаптер `TemporalAssignment`): Accept и Reject Delivery Offer, Offer Delivery To Courier, Reassign Courier, Cancel Delivery, Confirm Pickup, Report Delivery Failure; Create Delivery запускает workflow (`RejectDuplicate` + `UseExisting`: работающий или завершённый workflow остаётся). Signal завершённому workflow — не ошибка. Ошибка start или signal — `UNAVAILABLE` при зафиксированной транзакции; повтор вызова, не меняющий состояние, передаёт signal заново (FR-707).

Доставка без кандидата ждёт без таймеров перепроверки. Курьер, ставший Courier Candidate, будит одну доставку (FR-706, порт `CandidateWake` слайса `courier`): Set Courier Availability, после которого курьер available, сохранённая позиция available курьера (Update Courier Location), освобождение курьера Reassign Courier, Cancel Delivery, Confirm Delivery и Report Delivery Failure. Доставку выбирает query-порт `CandidateDeliveries` (SQL, partial index `deliveries_waiting` по Market): самая старая по `(created_at, id)` ожидающая доставка Market курьера без pending offer, которой его ещё не предлагали; нет такой — signal не передаётся.

`DeliveryOffered` и `DeliveryOfferExpired` — внутренние факты: не публикуются; истечение считается в `nomnom.delivery.offers{nomnom.outcome="expired"}`. Queries (`ListAvailableDeliveryOffers`, `is_open_at`) считают `expired` закрытым, как и pending offer после `expiresAt`. Spans и `nomnom.usecase.duration` шагов (`advance_assignment`, `expire_delivery_offer`) пишет `delivery-worker`; trace context через Temporal headers не переносится.

| Переменная | Ключ TOML | Значение |
| --- | --- | --- |
| `TEMPORAL__HOST_PORT` | `temporal.host_port` | `host:port` frontend Temporal; обязателен `delivery` и `delivery-worker` |
| `TEMPORAL__NAMESPACE` | `temporal.namespace` | Namespace; обязателен |
| `TEMPORAL__CONNECT_TIMEOUT` | `temporal.connect_timeout` | Таймаут подключения при старте; положительный |

Значений по умолчанию нет: пустое или невалидное значение, недоступный Temporal завершают startup. `delivery-worker` читает только `MANAGEMENT__*`, `SHUTDOWN__*`, `DATABASE__*` и `TEMPORAL__*` из того же `CONFIG_FILE`; схему мигрирует процесс `delivery`. SDK подключён без default features и с `vendored-protox`: сборке не нужен `protoc`.

Integration-тест `tests/assignment_workflow.rs` поднимает Temporal dev server (`temporalio/temporal:1.9.1`) и PostgreSQL из testcontainers и проверяет предложение после создания, истечение по таймеру, ожидание без кандидата, пробуждение курьером и завершение после Pickup.

## Хранилище (PostgreSQL)

[ADR 0001 контекста](docs/adr/0001-postgresql-хранилище-delivery.md), [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md). `Store` (`infrastructure/repository`, `sqlx`) закрывает репозитории `Courier`, `Delivery`, `DeliveryOffer`, Outbox, `CourierLocationStore`, оба read model, `UnitOfWork` и `ReconciliationStore`. Схема (`sqlx::migrate!`) разбита на наборы миграций по хранимым данным, у каждого своя нумерация с 1 и своя таблица истории sqlx `_sqlx_migrations_<id>`. `id` набора — явный стабильный storage id, часть контракта схемы БД: он задаётся литералом в `MIGRATION_SETS`, не выводится из имён модулей, каталогов и слайсов и не меняется при рефакторинге кода; смена id требует миграции таблицы истории. Наборы: `messaging` — `infrastructure/repository/migrations` рядом с кодом Outbox (общий Outbox `delivery_outbox` и publication `delivery_outbox`, общий Inbox `delivery_inbox`; `_sqlx_migrations_messaging`), `couriers` — `slices/courier/infrastructure/repository/migrations` (`couriers` с именем и `retired_at`, `courier_positions`; `_sqlx_migrations_couriers`), `deliveries` — `slices/delivery/infrastructure/repository/migrations` (`deliveries`, `delivery_offers`, Delivery Duration Statistics `delivery_duration_samples` и `delivery_open_transits`; `_sqlx_migrations_deliveries`), `simulator` — `slices/simulator/infrastructure/repository/migrations` (`simulator`; `_sqlx_migrations_simulator`). Набор меняет только свои таблицы, внешних ключей между наборами нет. Порядок наборов задан в одном месте — `MIGRATION_SETS` (`infrastructure/repository/migrate.rs`): сначала `messaging`, затем наборы слайсов; новый набор добавляется туда и в `SQLX_MIGRATION_SETS` Makefile с тем же id. Схема встроена в бинарь и применяется самим процессом при старте до открытия портов (`di::store`): актуальная схема — no-op, иначе ожидающие версии всех наборов применяются; advisory lock сериализует старт реплик, ошибка миграции завершает процесс. Lock берёт `migrate` один раз на все наборы, а не sqlx (мигратор каждого набора без `locking`): после него соединение получает `lock_timeout = 5s`, и `ALTER TABLE`, вставший в очередь за транзакцией живого трафика во время rolling update, не замораживает таблицу, а падает; транзакция миграции откатывается, pod повторяет её после рестарта. Ожидание другой реплики этим таймаутом не ограничено. Отдельного шага миграций нет ([ADR 42](../../docs/adr/0042-schema-migrations-at-process-startup.md)).

- **Optimistic concurrency.** `Courier`, `Delivery` и `DeliveryOffer` несут технический `version` (`with_version`/`version()`, как `authorization_revision`). `save` выполняет `UPDATE … SET version = version + 1 WHERE id = $1 AND version = $2`; 0 строк при существующем id — `RepositoryError::ConcurrentModification` → `ConcurrentModification` application error → gRPC `ABORTED` (клиент повторяет вызов целиком); отсутствующий id — `Conflict`. Запись revision reconciler'ом тоже увеличивает `version`. In-memory store проверяет версии так же.
- **Unit of Work.** Порты не принимают handle транзакции: соединение текущей транзакции хранится в thread-local слоте store, поэтому записи агрегатов и Outbox внутри `run` фиксируются или откатываются вместе. Вложенные Unit of Work не поддерживаются.
- **Outbox.** Таблица `delivery_outbox` — запись контракта [ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md) (поля сообщения Kafka, `traceparent` — `''` без span, `created_at` для retention); `event_id` — UUIDv5 от `{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}`, `aggregate_version` — версия, записанная той же транзакцией. Domain Event → Integration Event переводит `OutboxMapper` (`Store::with_outbox_mapper`). Процесс `delivery` подключает `DeliveryIntegrationEvents` (`slices/delivery/infrastructure`) в `di::store`; `NoIntegrationEvents` (ничего не пишется) остаётся значением по умолчанию `Store::connect` для тестов.
- **Sync порты над async sqlx.** Store владеет tokio runtime пула; вызовы идут через `block_in_place` на multi-thread runtime процесса и через scoped thread на current-thread runtime.
- **Compile-time checked запросы.** Каждый statement — литерал `sqlx::query!` / `query_as!` / `query_scalar!`: SQL, типы параметров и колонок и их nullability проверяются при компиляции по мигрированной схеме. Строки агрегатов читаются `query_as!` в `CourierRow`/`DeliveryRow`/`OfferRow` (`infrastructure/repository/rows.rs`), в доменные типы их переводит infrastructure; read models берут записи `query!`. Где Postgres выводит nullability неверно, колонка переопределяется в SQL (`courier_id AS "courier_id!"` после `courier_id IS NOT NULL`). Динамического SQL нет: макрос принимает только литерал, поэтому CTE retention Outbox повторяется в каждом statement записи Outbox. Обёртка `sql!` берёт операцию и таблицу span/метрики из литерала вызова (`Statement::of_source`, обычный или raw literal).
- **Offline query cache.** `.sqlx/` (закоммичен) — кеш описаний запросов: сборка без `DATABASE_URL` (переменная sqlx-cli и макросов sqlx, не конфиг процесса; и Docker-образ, `SQLX_OFFLINE=true`) проверяет запросы по нему и к базе не ходит. После изменения запроса или миграции кеш пересобирается `make sqlx-prepare`: одноразовый PostgreSQL через `docker run` (не kind), миграции всех наборов через `psql` в порядке `MIGRATION_SETS`, `cargo sqlx prepare -- --all-targets`, остановка контейнера; `make sqlx-prepare-check` сверяет кеш без перезаписи. Нужен sqlx-cli версии sqlx из `Cargo.lock`: `cargo install sqlx-cli --no-default-features --features rustls,postgres --version 0.9.0`.

| Переменная | Ключ | Назначение |
| --- | --- | --- |
| `DATABASE__URI` | `database.uri` | PostgreSQL connection URI `postgresql://user:password@host:5432/db` (или схема `postgres://`); только из Secret. Пусто, libpq key=value или другая схема — ошибка загрузки конфига; база недоступна или миграция не применилась — ошибка startup |
| `DATABASE__MAX_CONNS` | `database.max_conns` | Размер пула процесса, по умолчанию `10`; `1..100`. Миграции при старте открывают отдельное одно соединение (`MIGRATE_MAX_CONNS`) |
| `DATABASE__MIN_CONNS` | `database.min_conns` | Idle-минимум, по умолчанию `0`; `0..1` и не больше `max_conns` |
| `DATABASE__MAX_LIFETIME` | `database.max_lifetime` | Соединение старше закрывается при возврате в пул, по умолчанию `30m`; положительный |
| `DATABASE__IDLE_TIMEOUT` | `database.idle_timeout` | Простаивающее дольше соединение закрывается (выше `min_conns`), по умолчанию `5m`; положительный и не больше `max_lifetime` |
| `DATABASE__ACQUIRE_TIMEOUT` | `database.acquire_timeout` | Ожидание соединения пула, затем `UNAVAILABLE` и `db.client.connection.timeouts`. По умолчанию `5s`; положительный |

Невалидные значения завершают startup. Helm передаёт их из `database.pool.*` (`ops/helm/values.yaml`), URI — из Secret `database.uriSecret`.

### Пулер PostgreSQL

Перед базой стоит пулер соединений, и он обязан работать в **session mode**: миграции при старте берут session-level advisory lock на единственном соединении, sqlx кэширует prepared statements на соединении. Transaction mode ломает это: lock и prepared statements окажутся на чужом server-соединении. Unit of Work и запись Outbox живут внутри транзакции и от режима не зависят; `LISTEN/NOTIFY` и временные таблицы не используются. Коннектор Debezium читает WAL через слот логической репликации напрямую из PostgreSQL, не через пулер.

Каждый процесс держит собственный пул sqlx (`DATABASE__*`), в session mode каждое его соединение занимает server-соединение пулера. Сумма Σ(replicas × maxConns) всех release должна помещаться в `pool_size` пулера для базы `delivery`:

| Процесс | values | maxConns | minConns |
| --- | --- | --- | --- |
| `delivery` (gRPC, reconciler, истечение offers) | `values.yaml` | 10 | 0 |
| миграции при старте `delivery` | — (`MIGRATE_MAX_CONNS`) | 1 (одно соединение под advisory lock, закрывается до открытия пула) | 0 |

При одной реплике (`replicaCount: 1`) это 10 server-соединений плюс одно на время миграций в каждом стартующем pod `delivery`. Пул экспортирует OTel semconv `db.client.connection.*` (`count` по `state`, `max`, `idle.min`, `pending_requests`, `timeouts`, `wait_time`, `use_time`, `create_time`; `db.client.connection.pool.name=delivery`), инвентарь — [`docs/observability/metric-inventory.md`](../../docs/observability/metric-inventory.md#db).

Тесты хранилища идут в настоящий PostgreSQL: каждый тестовый бинарь поднимает свой контейнер через testcontainers (`postgres:18.6-alpine3.24`, как в ops) и удаляет его при выходе; нужен только запущенный Docker. Unit-тесты use cases работают через `TestStore` (`infrastructure/repository/test_support.rs`) — `Store` над отдельной базой на тест (копия мигрированной template-базы) с тестовыми хуками: отключение хранилища, сбой commit, счётчики Unit of Work и закоммиченные Outbox-сообщения. In-memory варианта хранилища нет. Integration-тесты (`tests/store.rs`: CRUD, конфликт двух конкурентных `save`, атомарность агрегата и Outbox, схема Outbox и publication по контракту ADR 37, retention при записи Outbox, read models, reconciliation, оба вида runtime) используют общую мигрированную базу контейнера (`tests/support`), идентификаторы уникальны:

```bash
DOCKER_CONFIG=$(mktemp -d) cargo test --test store
```

## Минимальная доменная модель

Для учебной реализации достаточно агрегатов `Courier` и `Delivery`.

| Тип | Минимальные поля | Назначение |
| --- | --- | --- |
| `Courier` — aggregate root | `id`, `name`, `marketId`, `status` | Хранит доступность курьера для назначения в его Market |
| `Delivery` — aggregate root | `id`, `orderId`, `marketId`, `courierId`, `status`, `failureReason`, `expectedPickupAt`, `expectedDeliveredAt` | Управляет назначением курьера и выполнением доставки |
| `DeliveryOffer` — aggregate root | `id`, `deliveryId`, `courierId`, `offeredAt`, `expiresAt`, `state` | Ограниченное по времени предложение одному курьеру |

Состояния `Courier`: `available`, `busy`, `unavailable` (вне линии, не получает новых назначений). Состояния `Delivery`: `waiting`, `assigned`, `pickedUp`, `delivered`, `failed`, `cancelled`. В `DeliveryState` контракта `nomnom.delivery.v1` значения для `cancelled` пока нет: отменённая доставка не бывает текущей доставкой курьера. Состояния `DeliveryOffer`: `pending`, `accepted`, `rejected`, `withdrawn`, `expired`. Pending offer после `expiresAt` уже закрыт для принятия и отклонения; переход `Expire` (только из `pending` и только при `now > expiresAt`) сохраняет это как `expired`. Повторный `Expire` — `AlreadyApplied`, `Expire` из других состояний и `Accept`/`Reject`/`Withdraw` из `expired` — `Invalid`.

Инварианты:

- назначить можно только доступного курьера на доставку в состоянии `waiting`;
- offer и назначение получает только курьер того же Market, что и доставка (ADR 41); Market курьера и доставки не меняется;
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
| `DeliveryFailureReason` | Код закрытого каталога `DeliveryFailureCode` — `customer_unreachable`, `address_not_found`, `no_access`, `customer_refused`, `order_damaged`, `courier_incident`, `other` — и необязательное пояснение (`detail`): пробелы нормализуются, не длиннее 500 символов. Неизвестный или пустой код — ошибка валидации, а не `other`. Строки, записанные до каталога свободным текстом, читаются как `other` с прежним текстом в `detail`. Retryability кодов не классифицируется |
| `ExpectedTime` | Срок, названный курьером (FR-610), целые epoch seconds: `expectedPickupAt` — при Accept Delivery Offer, `expectedDeliveredAt` — при Confirm Pickup. Новый срок строго позже текущего времени (injected `Clock`), иначе `ExpectedTimeNotInFuture` → `INVALID_ARGUMENT`; дробная секунда `Timestamp` округляется вверх. Не назван — пусто, а не подстановка. Срок Pickup принадлежит Courier Assignment: Reassignment и отмена его сбрасывают, новое назначение несёт свой. Хранится в nullable `deliveries.expected_pickup_at`/`expected_delivered_at` (миграция `0005`) |
| `MarketId` | Slug Market `^[a-z][a-z0-9-]{1,31}$` (ADR 41), без trim и приведения регистра; пусто — `Empty`, иначе `InvalidFormat` → `INVALID_ARGUMENT` |
| `Location` | Широта в [-90, 90] и долгота в [-180, 180], конечные значения; последняя позиция курьера хранится вне aggregate |

Идентификаторы и статусы пока остаются типизированными значениями и перечислениями, а не отдельными value objects. Маршрут, зоны и расчёт расстояния не входят в модель: расстояние до pickup, принадлежность зоне и число активных назначений поставляет порт `CourierLocator`.

### Domain policies и Specification Pattern

Переиспользуемые eligibility rules вне state реализованы как:

- `CourierEligibleForDelivery` — Market курьера совпадает с Market доставки (`OtherMarket`, ADR 41), зона, расстояние, capacity и наличие активных назначений;
- `DeliveryOfferAcceptable` — срок действия offer, расстояние до pickup и лимиты активных назначений.

`Courier.status` и `Delivery.status` являются business state: допустимость назначения, pickup и completion остаётся только в FSM/domain behavior и не дублируется в specs. Application загружает `Courier` и `Delivery`, получает координаты, `now` и assignment snapshot и передаёт их domain service значениями. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## Dependency Injection

В `delivery` граф зависимостей описывается модулями [`shaku`](https://github.com/AzureMarker/shaku), которые проверяются при компиляции. Handlers по-прежнему получают зависимости явно (`&dyn Trait`) и создаются в тестах без контейнера.

- `slices/<slice>/di` — shaku-модуль слайса: интерфейс внешних портов (`CourierPorts`, `DeliveryPorts`; хранилище `DeliveryPorts` видно только ролями `TransactionalStore` — Unit of Work, репозитории, занятость курьера, Outbox — и `DeliveryReadModels`), интерфейс use cases (`CourierUseCases`, `DeliveryUseCases`) и их реализация, которая собирает handler на каждый вызов. Порты слайс получает через submodule (`CourierDependencies`, `DeliveryDependencies`).
- `di` — корневой composition root, по модулю на процесс:
  - `di::infrastructure` — `InfrastructureModule`: реализации портов обоих слайсов (роли одного хранилища `ProcessStores`: `CourierStore`, `TransactionalStore`, `DeliveryReadModels`; генератор id, locator курьеров, одна авторизация, один writer relationships, `SystemClock`, `AssignmentPolicy`); реализации приходят параметрами компонентов, memory-адаптеры — только в тестовом графе;
  - `di::service` — модули слайсов над `InfrastructureModule` и итоговый `ServiceModule` (use cases); `di::build_over` и тестовые графы — над PostgreSQL (в unit-тестах `TestStore`) с тестовыми адаптерами `infrastructure::memory`. Транспорт получает use cases через `ServiceModule::couriers()` / `deliveries()` и shaku не импортирует;
  - `di::server` — `ServerModule` процесса `delivery`: `compose` выбирает `SpiceDbAuthorization` с revisions из хранилища (роль `RevisionReader`), `SpiceDbRelationships`, `Store` с mapper Integration Events (перед подключением `di::store` применяет встроенные миграции), `UuidIdGenerator` (UUIDv7, FR-541), `StoredCourierLocator` (последние позиции доступных курьеров, FR-542) и resolver service identity; каждая роль хранилища разрешается отдельно над одним `Arc<Store>`; компоненты `TonicTransport` (use cases, identity, бизнес-порт), `SpiceDbReconciliation` (граф SpiceDB и то же хранилище в роли `ReconciliationStore`), provider `DeliveryWorkerProvider` — gRPC worker с reconciliation, Simulator и consumer Delivery Duration Statistics; Temporal-клиент `TemporalAssignment` закрывает порты Assignment Workflow обоих слайсов;
  - `di::worker` — процесс `delivery-worker`: `compose` подключает `Store` (без миграций) и Temporal worker, шаги назначения — handlers Advance Assignment и Expire Delivery Offer над `ObservedStore`, `StoredCourierLocator`, UUIDv7 и `SystemClock`;
- `cmd/*` — загружают конфигурацию (невалидная завершает процесс до открытия портов), вызывают `compose` своего модуля, открывают технический порт `pkg::lifecycle::Lifecycle`, затем получают worker из модуля и передают его lifecycle.

Ресурсы: клиент SpiceDB, пул PostgreSQL, resolver identity и MeterProvider создаёт `compose` один раз на процесс, ими владеет модуль; транзакция живёт один use case (Unit of Work хранилища); worker создаётся provider'ом один раз после открытия технического порта, поэтому бизнес-порт открывается последним. Shaku-компоненты собираются синхронно и без ошибок, поэтому подключение к инфраструктуре (fail fast) выполняет `compose`, а не компонент. `Lifecycle` (технический порт, сигналы, drain) остаётся в `cmd`: это runtime contract процесса, а не зависимость use cases.

Shaku импортируют только модули `di`; `domains/`, `applications/`, `infrastructure/` и `cmd/` от него не зависят. Shaku требует `Send + Sync` от интерфейсов и создаёт отдельный экземпляр на каждый компонент, поэтому порты, которые обслуживает одно хранилище, собраны в интерфейс-агрегатор слайса. Generic `UnitOfWork` передаётся через object-safe `TransactionScope` и адаптер `ScopedUnitOfWork` из `pkg/unit_of_work`.

Решение зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Runtime contract

`pkg/lifecycle` реализует [runtime contract](../../docs/cloud-native-runtime.md) ([ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md)):

- технический порт `management.port` (по умолчанию `9083`, переменная `MANAGEMENT__PORT`, отличается от `server.port`) отдаёт `GET /health/startup`, `/health/live`, `/health/ready` с JSON `{"status":"ok"}` или `{"status":"unavailable","reason":"initializing|shutting_down"}`;
- ready наступает после открытия бизнес-порта и запуска worker; ошибка открытия порта или завершение worker без сигнала — non-zero exit;
- `SIGTERM`/`SIGINT`: not ready (liveness остаётся 200) → `shutdown.readiness_delay` (по умолчанию `5s`, `SHUTDOWN__READINESS_DELAY`, ноль допустим) → worker закрывает listener и ждёт начатые соединения в пределах `shutdown.drain_timeout` (`20s`, `SHUTDOWN__DRAIN_TIMEOUT`) → технический порт закрывается последним → exit `0`; истёкший deadline прерывает соединения и даёт non-zero exit;
- повторный сигнал во время shutdown прерывает worker и завершает процесс с non-zero exit: tokio не позволяет вернуть обработчик сигнала по умолчанию.

## Событийная интеграция

Consumers интеграционных событий `delivery` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение состояния доставки, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной локальной транзакции. Kafka offset подтверждается только после commit.

Хранилище `delivery` — PostgreSQL ([ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md), [ADR 0001 контекста](docs/adr/0001-postgresql-хранилище-delivery.md)): Unit of Work, optimistic concurrency, таблицы Outbox и Inbox реализованы; см. [документацию Unit of Work](../../docs/unit-of-work.md#delivery) и [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md).

### Kafka consumer

Процесс `delivery` читает Kafka одной группой `KAFKA__CONSUMER_GROUP` (chart: `delivery`) на `rdkafka` (`infrastructure/messaging`, worker `di::ConsumingWorker`): брокеры — `KAFKA__BROKERS`, начальный offset новой группы — `KAFKA__INITIAL_OFFSET` (`oldest` или `newest`), Schema Registry записей стрима — `SCHEMA_REGISTRY__URL` (http(s), FR-715); все четыре обязательны, пустое или неизвестное значение завершает startup. Offset сообщения подтверждается после обработки:

- успех, повтор по Inbox или тип, который consumer не читает, — commit;
- нет `event_id`, `event_type`, `occurred_at` (RFC 3339 UTC) или payload, value не в wire format Schema Registry, id схемы неизвестен или её subjects не содержат `event_type`, payload не декодируется, permanent-ошибка use case — сообщение уходит в `<stream>.dlq` с ключом, payload, исходными заголовками и `reason_poisoned`, `topic_poisoned`, `consumer_group_poisoned` (как у `orders` и `restaurants`, [разбор DLQ](../../docs/dlq.md)), затем commit;
- transient-ошибка (Schema Registry или PostgreSQL недоступен, исход commit неизвестен, конкурентное изменение) — повтор с backoff 1s…30s, partition ждёт зависимость; при остановке процесса offset не подтверждается.

Inbox `delivery_inbox` (`consumer`, `event_id`) общий для consumers процесса; запись удаляется через семь дней тем же statement, что пишет новую. Команды разбора DLQ группы `delivery` (`dlq list/redrive/discard`) пока нет.

### Delivery Duration Statistics

Read model собственных фактов Delivery по образцу Wait Time Statistics `restaurants` ([FR-676](../../openspec/changes/add-scheduled-orders/specs/delivery-duration-estimate/spec.md), [design](../../openspec/changes/add-scheduled-orders/design.md#оценки)), не часть агрегата `Delivery`. Consumer `delivery.duration-statistics` читает собственный стрим `delivery.v1.delivery-events` и выполняет Record Delivery Duration:

| Событие | Изменение |
| --- | --- |
| `DeliveryPickedUp` | `courier_lead` = `occurred_at` − `deliveries.created_at`; transit доставки открыт с `occurred_at` (`delivery_open_transits`) |
| `DeliveryCompleted` | `transit` = `occurred_at` − Pickup открытого transit, transit закрыт |
| `DeliveryCancelled`, `DeliveryFailed` | transit закрыт без длительности |
| остальные | commit без обработки |

Длительности хранятся по Market и виду в `delivery_duration_samples` (одна длительность вида на доставку); после вставки остаются последние `DELIVERY_ESTIMATE__SAMPLE_SIZE`. Доставка, сохранённая до появления `created_at`, не даёт `courier_lead`; вручение без открытого transit (Pickup до появления статистики) — `transit`; отрицательный интервал (часы процесса и базы разошлись) не записывается. Market берётся из доставки: у `Delivery` нет адресов, статистика общая по Market.

Estimate Delivery Duration отдаёт медиану каждого вида, когда в выборке Market не меньше `DELIVERY_ESTIMATE__SAMPLE_SIZE` длительностей, иначе — `DELIVERY_ESTIMATE__BASELINE_COURIER_LEAD` или `DELIVERY_ESTIMATE__BASELINE_TRANSIT`; виды переключаются на медиану независимо. Все три параметра обязательны, значений по умолчанию нет; пустое или нечисловое значение завершает загрузку конфигурации, нулевое — сборку процесса (`DeliveryEstimatePolicy`), до открытия портов. Значения chart — 50, 15m и 20m.

### Публикация Integration Events

Topic `delivery.v1.delivery-events`, контракт `infrastructure/grpc/nomnom/delivery/v1/events.proto` ([ADR 25](../../docs/adr/0025-integration-event-publication.md), [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md), [ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)).

Каждая запись курьера, оффера и доставки, затронувшей курьера, кладёт в тот же Outbox Courier Change Notification — JSON `{"__typename":"Courier","id":…}` в `delivery.v1.courier-changes`. Это не Integration Event: его читает только Cosmo Router BFF для subscription `courierChanged` ([ADR 36](../../docs/adr/0036-graphql-subscriptions-via-cosmo-streams.md)). Запись курьера и уведомление — один SQL-оператор, поэтому они атомарны и без Unit of Work. Каждая запись курьера, оффера и доставки также кладёт Courier Pool Change Notification — `{"__typename":"CourierPool","id":<market_id>}` в `delivery.v1.courier-pool-changes` — пул Market курьера или Delivery (у оффера — Market его Delivery, ADR 41) для subscription `courierPoolChanged` ([FR-621](../../openspec/changes/add-cook-and-courier-pools/)); у курьера — в том же SQL-операторе.

Каждое Integration Event несёт `market_id` — Market доставки (ADR 41); Domain Events доставки несут его же.

| Domain Event | Integration Event (`event_type`) |
| --- | --- |
| `CourierAssignedToDelivery` | `nomnom.delivery.v1.CourierAssignedToDelivery`: `expected_pickup_at` (поле 4) — срок Pickup, названный курьером; пусто — не назван |
| `DeliveryPickedUp` | `nomnom.delivery.v1.DeliveryPickedUp`: `expected_delivered_at` (поле 3) — срок вручения, названный курьером; пусто — не назван |
| `DeliveryCompleted` | `nomnom.delivery.v1.DeliveryCompleted` |
| `DeliveryFailed` | `nomnom.delivery.v1.DeliveryFailed`: `reason` — код каталога (до каталога — свободный текст), `detail` (поле 4) — пояснение, пустое, если не указано |
| `CourierUnassignedFromDelivery` | `nomnom.delivery.v1.CourierUnassignedFromDelivery` (курьер снят без замены) |
| `DeliveryCancelled` | `nomnom.delivery.v1.DeliveryCancelled` (с `reason`, пустой — причина не указана) |

`orders` удаляет `order#delivery` по `DeliveryCancelled` — use case Record Courier Release; доступ курьера к заказу отзывает сам `delivery` через `delivery#courier`.

Mapper — явный allowlist: новый Domain Event не публикуется, пока для него нет контракта (match без wildcard не скомпилируется). Все события одной транзакции получают один `aggregate_version`; `event_id` различается через `event_type` (ADR 31).

Публикует Outbox не процесс `delivery`, а коннектор Debezium `delivery-outbox` в общем Kafka Connect (CDC, [ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)):

- **источник** — WAL базы `delivery`: publication `delivery_outbox` (`publish = 'insert'`, создаёт миграция `0005_outbox_cdc.sql`) и слот логической репликации `delivery_outbox` (создаёт коннектор). Запись попадает в Kafka сразу после commit транзакции агрегата; удаления в поток не попадают;
- **сообщение** — топик из поля `topic`, key — `aggregate_id`, value — `payload` как есть (`ByteArrayConverter`), Event Envelope — заголовки из полей записи (Outbox Event Router); доставка at-least-once, consumers дедуплицируют по `event_id`;
- **head-of-line** — запись, которую коннектор не может опубликовать, останавливает его: `GET /connectors/delivery-outbox/status`, после исправления — `POST /connectors/delivery-outbox/restart?includeTasks=true`;
- **retention** — каждая запись Outbox тем же SQL-оператором удаляет строки старше часа по `created_at` (`FOR UPDATE SKIP LOCKED`, конкурентные записи не ждут друг друга); фонового процесса нет, слот держит WAL непрочитанных записей независимо от таблицы.

Kafka headers те же, что слал relay, и совпадают с `orders/infrastructure/messaging/envelope.go`: `_watermill_message_uuid` и `event_id` = `event_id`, `event_type`, `occurred_at` (RFC 3339 UTC), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent` (пустой, если команда шла без trace). Конфигурацию коннектора держит chart (`cdc.*` в `ops/helm/values.yaml`), регистрирует hook Job release (`ops/README.md`).

Бизнес-метрики процесса `delivery` (раздел domain [инвентаря](../../docs/observability/metric-inventory.md), имена — `ops/semconv/model/domain.yaml`): `slices/delivery/infrastructure/observability` — декоратор портов Unit of Work и Outbox (`ObservedOutbox`; в `di::infrastructure` роль `TransactionalStore` слайса закрывает `ObservedStore`, так что Unit of Work и Outbox handlers идут через декоратор). Domain Events, переданные в Outbox внутри Unit of Work, записываются только после commit внешней Unit of Work: rollback и идемпотентный повтор без новых событий ничего не меняют. `DeliveryCreated` → `nomnom.delivery.created`; `CourierAssignedToDelivery` → `assignments` и `assignment.latency` (от `created_at`); `CourierUnassignedFromDelivery` → `reassignments` (`reason=other`: доменной причины снятия нет); `DeliveryOfferAccepted`/`DeliveryOfferRejected`/`DeliveryOfferExpired` → `offers` (`outcome` accepted/rejected/expired); `DeliveryPickedUp` → `pickups`; `DeliveryCompleted` → `completed` и `duration` (от `created_at`); `DeliveryFailed` → `failed` (`reason` — код каталога Delivery Failure Reason; `detail` никогда не становится атрибутом метрики). Каждый закоммиченный `DeliveryFailed` также пишется одной JSON-записью в stdout (`slices/delivery/infrastructure/failure_log.rs`: общая схема логов, `severity=INFO`, `operation=delivery.failure_reported`, `delivery_id`, `order_id`, `courier_id`, `reason`, `detail`, `trace_id`/`span_id` span use case `report_delivery_failure`) и ставит на активный span атрибуты `nomnom.delivery.failure.reason` и `nomnom.delivery.failure.detail` (до 256 символов). `DeliveryCreated` и события offer не имеют контракта и не публикуются. `service.name` — `delivery`.

### Observability

Контракт — [docs/observability.md](../../docs/observability.md); pipeline — `pkg/telemetry`, по образцу `orders/pkg/telemetry`. Процесс `delivery` использует resource: `service.namespace=nomnom`, `service.name` (имя процесса или `OTEL_SERVICE_NAME`), `service.version`, `service.instance.id`, `deployment.environment.name` из `OTEL_RESOURCE_ATTRIBUTES` (Helm: `telemetry.*`, pod name и image tag).

| Сигнал | Что есть |
| --- | --- |
| Traces | W3C `traceparent`/`tracestate` (baggage не передаётся). TracerProvider с batch OTLP gRPC exporter — только при `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`. Server span `package.Service/Method` на каждый gRPC-вызов (`infrastructure/grpc/tracing.rs`, продолжает входящий trace); span `usecase <name>` на каждый use case (`slices/<slice>/infrastructure/observed_use_cases.rs`); client span и `traceparent` в metadata на каждый вызов SpiceDB; client span PostgreSQL на statement внутри trace (`db.system.name`, `db.operation.name`, `db.collection.name`, без текста и параметров); Outbox хранит `traceparent` span'а, записавшего событие: gRPC server span продолжает входящий `traceparent` и остаётся текущим контекстом, пока handler синхронно выполняет Unit of Work, а запись Outbox (`infrastructure/repository/outbox.rs`) пишет его `traceparent` в колонку `traceparent` строки (без span — `''`; `tests/outbox_tracing.rs`); Debezium передаёт его в Kafka header `traceparent`, consumer продолжает trace команды. |
| Metrics | При `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`: `rpc.server.call.duration` и `rpc.client.call.duration` (`rpc.system.name`, `rpc.method`, `rpc.response.status_code` — имя кода; client — `server.address`, `server.port`, `error.type`), `nomnom.usecase.duration` (`nomnom.usecase.name`, `nomnom.usecase.kind`, `nomnom.outcome`, `error.type`), `db.client.operation.duration` и `db.client.connection.*` пула, `process.cpu.time`, `process.memory.usage`, `process.thread.count` (procfs) и бизнес-метрики; публикацию Outbox измеряют метрики Kafka Connect и Debezium. Длительности — явные buckets в секундах. |
| Logs | Одна JSON-запись на строку в stdout (`pkg/telemetry/log.rs`, `tracing`): `timestamp` (UTC), `severity`, `message`, поля resource, `trace_id`/`span_id` только внутри span, `operation`, `outcome`, `error.type`, `error.message`. Библиотеки — от `WARN` (`sqlx` — `ERROR`). Токены, ключи, payload и bind parameters не логируются. |

Классификация use cases: `rejected` — `permission_denied`, `invalid_argument`, `not_found`, `already_exists`, `failed_precondition`; `error` — `concurrent_modification`, `unavailable`, `transaction`, `internal`. `Telemetry::shutdown` выгружает spans и точки после drain.


Integration-тест `tests/outbox_tracing.rs` (`traceparent` gRPC-команды в колонке `traceparent` строки Outbox) поднимает PostgreSQL через testcontainers:

```bash
DOCKER_CONFIG=$(mktemp -d) cargo test --test outbox_tracing
```

Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

## Observability

Инструментирование Rust транспорта, хранилища, Kafka и внешних клиентов размещается в `infrastructure/`, а lifecycle SDK и profiler — в bootstrap процесса. Rust profiling зависит от выбранного client/backend и целевой платформы и проверяется до добавления зависимости. Общие потоки и правила описаны в [документации observability](../../docs/observability.md).

## Статус реализации

Реализованы:

- доменная модель: `Courier` (включая `unavailable`), `Location`, табличные FSM `Delivery` (иерархическая: 6 leaf states × 6 triggers, включая `Cancelled`/`Cancel`, compound states `ActiveAssignment` и `Closed`, см. [FSM](../../docs/fsm.md#delivery)) и `DeliveryOffer` (4 × 3) с тестами полноты, domain events `DeliveryOffered`, `CourierAssignedToDelivery`, `CourierUnassignedFromDelivery`, `DeliveryPickedUp`, `DeliveryCompleted`, `DeliveryFailed`, `DeliveryCancelled`; policies `CourierEligibleForDelivery` и `DeliveryOfferAcceptable`; контракты репозиториев рядом с моделями;
- application handlers всех 19 use cases в `slices/<slice>/applications/<usecase>/mod.rs` с unit-тестами;
- порты: `UnitOfWork`, `Clock`, `IdGenerator`, `Subject`/`AuthorizationError` (`pkg/unit_of_work`, `pkg/usecase`, `pkg/authz`), `RepositoryError` (`pkg/persistence`), `CourierAuthorization` и `DeliveryAuthorization`, порт занятости курьера `CourierAssignment`, `DeliveryOutbox`, `DeliveryInbox`, `DeliveryDurationStatistics`, `CourierLocator`, `CourierLocationStore`, read models `CourierCurrentDeliveryReadModel`, `PendingDeliveryOffersReadModel` и `DeliveryDurationSamples`, параметры `AssignmentPolicy` и `DeliveryEstimatePolicy`;
- `ServiceTokenSubjectResolver`: service identity по projected ServiceAccount token (ADR 27) и user identity по assertion BFF (ADR 32);
- `SpiceDbAuthorization`: SpiceDB-адаптер `CourierAuthorization` и `DeliveryAuthorization` (ADR 18, FR-435), выбирается при заданном `SPICEDB__ENDPOINT`;
- `Store` (`infrastructure/repository`): все порты хранилища на `sqlx`, optimistic concurrency по `version`, Unit of Work, Outbox по контракту ADR 37 (retention при записи), встроенные миграции при старте процесса;
- публикация Integration Events: mapper `DeliveryIntegrationEvents` (4 события `events.proto`), публикует коннектор Debezium `delivery-outbox` (CDC, ADR 37, Helm `cdc.*`);
- `UuidIdGenerator` (`infrastructure/ids.rs`): UUIDv7-идентификаторы `Delivery` и offer, уникальные между перезапусками и репликами (FR-541);
- `StoredCourierLocator` (`infrastructure/locator.rs`): кандидаты — доступные курьеры с последней позицией из `CourierLocationStore` (роль `CourierPositions`, FR-542). Delivery не хранит место pickup, поэтому курьер с известной позицией считается в зоне на расстоянии 0, и среди равных выбор решает id курьера;
- тестовые адаптеры не-storage портов в `infrastructure/memory` (статическая authorization, запись relationships, управляемые clock и id generator, статический locator) — только для тестов; хранилище в тестах — PostgreSQL (testcontainers, `TestStore`).

Не реализованы: команда разбора DLQ группы `delivery`, расчёт геопозиции/зон, BDD `.feature`. По gRPC опубликованы все use cases, кроме Find Courier, Offer Delivery и Reassign Courier (шаги процесса назначения) и шагов Assignment Workflow Advance Assignment и Expire Delivery Offer.

Момент начала поиска курьера — запуск Assignment Workflow из Create Delivery (см. «Assignment Workflow»). Допущения по остальным открытым продуктовым вопросам (retryability причин неудачи, параметры позиции и политики назначения) зафиксированы в [design.md](../../openspec/changes/archive/2026-10-25-implement-delivery-application-layer/design.md#допущения-по-открытым-вопросам) change.

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
