# BFF

## Назначение

BFF — отдельный сервис и единая прикладная точка входа для клиентского API nomnom. Сервис построен на [WunderGraph Cosmo](https://cosmo-docs.wundergraph.com/) и публикует GraphQL API через Cosmo Router. Mobile- и web-клиенты не используют внутренние gRPC/Connect API напрямую и не зависят от внутренних адресов, Protobuf-контрактов или топологии сервисов.

Сейчас используется один BFF. Отдельные BFF для mobile и web могут появиться только при устойчивом расхождении сценариев и требований. Router и plugin могут масштабироваться горизонтально; между запросами они не хранят пользовательские сессии или состояние бизнес-операций.

Выбор Cosmo зафиксирован в [локальном ADR 3](docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md), который заменяет первоначальное решение о REST/OpenAPI API.

## Текущее состояние

Проект инициализирован официальным `wgc router plugin init`:

- `wgc` закреплён как локальная dev-зависимость версии `0.133.3`;
- Cosmo Router использует локальную композицию без обязательного подключения к control plane;
- клиентский API описывается GraphQL SDL;
- резолверы реализуются Go router plugin через сгенерированный gRPC-контракт;
- generated-код и lock-файл контракта коммитятся;
- минимальный stateless-запрос `bffInfo` подтверждает сборку полного контура.

Реализованы слайсы `catalog`, `checkout`, `ordertracking`, `restaurantmanagement`, `kitchen`, `courier`, `courierpool` и `simulator` поверх `restaurants`, `orders` и `delivery` своей Cell через internal gateway ([BFF в Cell](#bff-в-cell)) (см. [Customer API](#customer-api)); операций «только схема» не осталось.

## Customer API

Публичная customer-схема в [`plugins/bff/src/schema.graphql`](plugins/bff/src/schema.graphql) зафиксирована раньше resolvers (OpenSpec change [`add-customer-graphql-contract`](../../openspec/changes/archive/2026-10-25-add-customer-graphql-contract/)), чтобы customer UI развивался параллельно с bounded contexts на MSW-моках той же схемы.

| Операция | Назначение | Статус |
| --- | --- | --- |
| `bffInfo` | метаданные BFF | реализована |
| `restaurants(first, after)` | рестораны с cursor-пагинацией | реализована (`catalog`) |
| `restaurant(id)` | ресторан с меню | реализована (`catalog`) |
| `order(id)` | заказ текущего покупателя; `null` для отсутствующего и чужого | реализована (`ordertracking`) |
| `customerOrders(first, after)` | история заказов покупателя от новых к старым (`orders.ListCustomerOrders` + `GetOrder` на каждый заказ) | реализована (`ordertracking`) |
| `placeOrder(input)` | Place Order, идемпотентно по `idempotencyKey` | реализована (`checkout`) |
| `requestOrderCancellation(input)` | Cancellation Request, идемпотентно по `requestId` | реализована (`ordertracking`) |
| `openRestaurant(restaurantId)`, `closeRestaurant(restaurantId)` | открытие ресторана владельцем, повтор — `changed = false` | реализованы (`restaurantmanagement`) |
| `myRestaurants(first, after)` | открытые и закрытые рестораны текущего пользователя (`restaurants.ListOwnedRestaurants`), `menu = null`; нужен access token | реализован (`restaurantmanagement`) |
| `simulator`, `setSimulatorEnabled(enabled)` | demo Simulator трёх контекстов, `enabled` — AND ответов; нужен оператор (`platform#operate_simulator`) | реализованы (`simulator`) |
| `kitchen(restaurantId)` | активные заказы ресторана для кухни; `null` для отсутствующего ресторана и кухни, которой пользователь не управляет | реализован (`kitchen`) |
| `applyKitchenAction(input)` | команда кухни над заказом ресторана; конфликт стадии — `problem` | реализована (`kitchen`) |
| `courier` | работа текущего пользователя как курьера; `null`, если он не курьер | реализован (`courier`) |
| `setCourierAvailability(availability)`, `acceptDeliveryOffer(offerId)`, `applyCourierDeliveryAction(deliveryId, action)` | команды курьера; payload — курьер после команды и `problem` | реализованы (`courier`) |

Операции «только схема» возвращают GraphQL error с gRPC `Unimplemented`: `BffService` встраивает `UnimplementedBffServiceServer`.

### Слайс `ordertracking`

Change [`add-bff-order-tracking-resolvers`](../../openspec/changes/archive/2026-10-25-add-bff-order-tracking-resolvers/). `plugins/bff/ordertracking/applications` строит представление заказа без Protobuf и GraphQL-типов: `OrderStatus` по таблице выше, `endReason` по `reason_code`, milestones из `placed_at`, Fulfillment Facts и `cancelled_at` (`CONFIRMED` — из факта `restaurant_accepted`; у заказов, подтверждённых до его появления, этого milestone нет), названия позиций — снимок `orders` на момент Place Order (`OrderItem.name`; у заказов, оформленных до снимка, — пустое имя, текущее меню не читается), `lineTotal` десятичной арифметикой `math/big`, `cancellation.availableUntil` по окну отмены и часам BFF, `expectedReadyAt`/`expectedPickupAt`/`expectedDeliveredAt` — последние сроки `Order` `orders` (FR-611, FR-612; не названный срок — `null`, его же несёт `orderChanged` через `LookupOrderById`). `ordertracking/infrastructure` вызывает `orders` от имени покупателя (service token и user assertion, ADR 32) и публичный `GetRestaurant` без пользователя; клиенты генерируются из workspace (`make proto-generate`, `plugins/bff/infrastructure/grpc/buf.gen.yaml`).

| Ответ контекста | GraphQL |
| --- | --- |
| нет или невалидный access token | error `UNAUTHENTICATED`, контексты не вызываются |
| `GetOrder`: `NOT_FOUND`, `PERMISSION_DENIED` | `order = null` |
| `GetOrder`: остальные коды, в том числе `FAILED_PRECONDITION` | error `UNAVAILABLE`: у чтения заказа нет бизнес-отказов, это не отказ отмены |
| `RequestOrderCancellation`: `CANCELLATION_WINDOW_NOT_OPEN` / `_EXPIRED` | `problem` с тем же кодом |
| `RequestOrderCancellation`: `ORDER_STATE_CONFLICT` | `problem = ORDER_ALREADY_FINAL` |
| `RequestOrderCancellation`: `NOT_FOUND`, `PERMISSION_DENIED` | error `NOT_FOUND` |
| остальные сбои | error `UNAVAILABLE` без внутренних деталей |

`customerOrders(first, after)`: `ListCustomerOrders` `orders` от имени покупателя (`customer_id` — subject access token) возвращает полную карточку каждого заказа страницы (`OrderSummary.order`, проекция `OrderDetails` `orders`), каждый узел — тот же `Order`, что у `order(id)`; `GetRestaurant` читается один раз на каждый ресторан страницы, `GetOrder` по заказам не вызывается. `first` — 1..100, иначе error `INVALID_ARGUMENT`; cursor ребра — `OrderSummary.cursor` `orders` (продолжение после этого заказа), `pageInfo.endCursor` — `next_page_token`, на последней странице — cursor последнего ребра, на пустой — `null`; чужой или неизвестный `after` — error `INVALID_ARGUMENT`; запись истории без карточки или с неизвестным состоянием — error, а не пропуск.

Адрес internal gateway — `BFF__INTERNAL_GATEWAY_ADDR` (Helm `internalGateway.address`); без него или без identity операции заказа отвечают `UNAVAILABLE` (`order operations are not available`)/`UNAUTHENTICATED`, `bffInfo` работает. Сквозная проверка через gateway — тест `bff-end-to-end` в `make kind-test` ([ops/kind](../../ops/kind/README.md#проверки)).

- **Курьер заказа** ([FR-626](../../openspec/changes/add-cook-and-courier-pools/)): `order(id)` и lookup `orderChanged` отдают `courier { name }` — `GetOrderCourier` `delivery` от имени покупателя (`order#view`, GRPCRoute `delivery-v1-courier-reads`), только когда у заказа есть milestone `COURIER_ASSIGNED`; без курьера (до Assignment, после Delivery Failure) — `null`. `customerOrders` его не читает и оставляет `null`. Сбой `delivery` — error, а не заказ без курьера. Имя повара покупателю не отдаётся.

### Слайс `catalog`

Change [`add-customer-checkout-flow`](../../openspec/changes/archive/2026-10-25-add-customer-checkout-flow/). `plugins/bff/catalog/applications` — сценарии List Restaurants и Get Restaurant с меню без Protobuf и GraphQL-типов; `catalog/infrastructure` вызывает публичные `ListRestaurants`, `GetRestaurant` и `GetMenu` `restaurants` с service token BFF и без user assertion (`identity.WithoutUser`). Resolvers каталога access token не требуют.

- `restaurants(first, after)` → `ListRestaurants` только открытых ресторанов (`include_closed = false`); `first` — 1..100, иначе error `INVALID_ARGUMENT`; `after` и `pageInfo.endCursor` — `next_page_token` `restaurants` как есть, cursor ребра — id ресторана (тот же ключ курсора), на последней странице `endCursor` — cursor последнего ребра, на пустой — `null`; `menu` каждого ресторана страницы читается из `GetMenu` параллельно (ресторан без меню — `null`, любая другая ошибка роняет запрос): customer UI показывает превью блюд на карточке ресторана.
- `restaurant(id)` → `GetRestaurant` + `GetMenu`: `NOT_FOUND` ресторана — `null`, `NOT_FOUND` меню — `menu = null`; `acceptingOrders` — `is_open`, пустое описание позиции — `null`.
- Без gateway или identity — error `UNAVAILABLE` (`catalog operations are not available`); остальные сбои `restaurants` — error `UNAVAILABLE` без внутренних деталей.

### Слайс `checkout`

Change [`add-customer-checkout-flow`](../../openspec/changes/archive/2026-10-25-add-customer-checkout-flow/). `plugins/bff/checkout/applications` вызывает `PlaceOrder` от имени покупателя и строит payload сценарием `GetOrder` слайса `ordertracking` (тот же `Order`, что у `order(id)`); `checkout/infrastructure` переводит отказы `orders` по `ErrorInfo`.

| Ответ `orders` `PlaceOrder` | GraphQL `placeOrder` |
| --- | --- |
| нет или невалидный access token | error `UNAUTHENTICATED`, `orders` не вызывается |
| нет gateway или identity | error `UNAVAILABLE` (`checkout operations are not available`) |
| пустые `restaurantId` или `idempotencyKey` | error `INVALID_ARGUMENT` |
| успех | `order` из `GetOrder` (`RECEIVED`), `problems = []` |
| `FAILED_PRECONDITION` / `ORDER_NOT_ORDERABLE` | `order = null`, problem на каждый известный код из metadata `codes`; `menu_item_ids` — у `ORDER_ITEM_UNAVAILABLE`, у лимитов суммы `menuItemIds = []` |
| `FAILED_PRECONDITION` / `RESTAURANT_NOT_ACCEPTING_ORDERS` | `order = null`, problem без позиций |
| `NOT_FOUND` / `RESTAURANT_NOT_FOUND` | error `NOT_FOUND`: в схеме нет problem для несуществующего ресторана, исправить это в корзине нельзя |
| `INVALID_ARGUMENT` (`INVALID_ORDER`, `INVALID_ARGUMENT`) | error `INVALID_ARGUMENT` без деталей `orders` |
| остальные сбои, в том числе отказ без известных кодов | error `UNAVAILABLE` без внутренних деталей |

Повтор с тем же `idempotencyKey` возвращает уже оформленный заказ.

Правила контракта:

- `Order.status` — customer-представление `OrderStatus`, которое BFF вычисляет из состояния заказа и Fulfillment Facts `orders`; назначение курьера — milestone `COURIER_ASSIGNED`. Имена FSM, Temporal и gRPC-контрактов в схему не попадают.
- Бизнес-отказы mutations возвращаются в payload (`problems`, `problem`), GraphQL errors — только transport, authentication и нереализованные операции.
- Покупатель не передаётся аргументом: subject определяется по аутентифицированному запросу.
- Моменты времени — `String` в RFC 3339, `Money.amount` — десятичная строка; unions и custom scalars не используются.
- `Order.cancellation` берётся из `Order.cancellation_window` ответа `orders` `GetOrder`: `availableUntil = closes_at`, если `opens_at ≤ now ≤ closes_at` по часам BFF, иначе `null`. `orders` возвращает окно, только пока Order State допускает отмену; сам запрос отмены `orders` проверяет заново.

### Слайс `simulator`

Change [`add-demo-simulator`](../../openspec/changes/add-demo-simulator/) (FR-607). `plugins/bff/simulator/applications` — сценарии Get Simulator и Set Simulator Enabled с портом `Simulator`; `simulator/infrastructure` вызывает `GetSimulator`/`SetSimulatorEnabled` у `orders`, `restaurants` и `delivery` по очереди от имени пользователя (service token BFF и user assertion, ADR 32). `enabled` — `true`, только если Simulator включён во всех трёх. Право `platform#operate_simulator` проверяет каждый контекст.

| Ответ контекстов | GraphQL `simulator` / `setSimulatorEnabled` |
| --- | --- |
| нет или невалидный access token | error `UNAUTHENTICATED`, контексты не вызываются |
| успех всех трёх | `Simulator { enabled }` — AND ответов |
| `PERMISSION_DENIED` хотя бы одного | error `PERMISSION_DENIED` |
| остальные сбои любого, в том числе `UNAVAILABLE` | error `UNAVAILABLE` без внутренних деталей |

Mutation останавливается на первом сбое: контексты, переключённые до него, остаются в новом состоянии, повтор доводит переключение.

### Слайс `restaurantmanagement`

Change [`add-restaurant-opening-management`](../../openspec/changes/add-restaurant-opening-management/). `plugins/bff/restaurantmanagement/applications` — сценарии Open Restaurant и Close Restaurant с портом `RestaurantOpener`; `restaurantmanagement/infrastructure` вызывает `OpenRestaurant`/`CloseRestaurant` `restaurants` (GRPCRoute `restaurants-v1-management`) от имени пользователя: service token BFF и user assertion (ADR 32), как `PlaceOrder`. Право `restaurant#manage` (владелец) проверяет `restaurants`; BFF владение не проверяет.

Payload `RestaurantOpeningPayload`: `restaurant` после команды (`acceptingOrders` — `is_open`, `menu = null`) и `changed` (`false` у повтора).

| Ответ `restaurants` | GraphQL `openRestaurant` / `closeRestaurant` |
| --- | --- |
| нет или невалидный access token | error `UNAUTHENTICATED`, `restaurants` не вызывается |
| пустой `restaurantId` | error `INVALID_ARGUMENT`, `restaurants` не вызывается |
| нет gateway или identity | error `UNAVAILABLE` (`restaurant management operations are not available`) |
| успех, в том числе повтор | payload, `changed` как в ответе |
| `NOT_FOUND` | error `NOT_FOUND` |
| `PERMISSION_DENIED` | error `PERMISSION_DENIED`: ресторан — публичные данные, скрывать его существование незачем |
| `INVALID_ARGUMENT` | error `INVALID_ARGUMENT` без деталей `restaurants` |
| остальные сбои, в том числе `UNAVAILABLE` | error `UNAVAILABLE` без внутренних деталей |

Бизнес-отказа, который пользователь исправит в форме, у открытия нет, поэтому отказы — GraphQL errors, а не `problems`. Router отдаёт gRPC-код в `errors[].extensions.errors[].extensions.code` (`PermissionDenied`, `NotFound`, …).

### Слайс `kitchen`

`plugins/bff/kitchen/applications` — сценарии Get Kitchen и Apply Kitchen Action без Protobuf и GraphQL-типов; `kitchen/infrastructure` вызывает `restaurants` от имени пользователя (service token BFF и user assertion, ADR 32): доска кухни — `ListActiveRestaurantOrders` и `GetKitchenQueue` (GRPCRoute `restaurants-v1-kitchen-reads`), команды — `AcceptRestaurantOrder`, `RejectRestaurantOrder`, `StartPreparation`, `MarkRestaurantOrderAssembled`, `MarkRestaurantOrderReady` (`restaurants-v1-kitchen`); публичные `GetRestaurant` и `GetMenu` — без пользователя. Право `restaurant#operate_kitchen` (персонал или владелец) проверяет `restaurants`.

- `kitchen(restaurantId)`: заказы в `RECEIVED`…`READY` из `ListActiveRestaurantOrders` (позиции, `received_at`, `accepted_at` и `expected_ready_at` → nullable `acceptedAt`/`expectedReadyAt` в RFC 3339, `null` до Acceptance; их же отдаёт lookup `kitchenChanged`); порядок — `RECEIVED` по `receivedAt`, затем `QUEUED` в порядке `GetKitchenQueue` (заказ, которого нет в очереди из-за гонки двух чтений, — после неё по `receivedAt`), затем остальные по `receivedAt`; названия позиций — из `GetMenu` (позиции, которой нет в меню, — пустое имя), `restaurant.menu = null`. `ListIncomingOrders` не нужен: новый RPC покрывает и `RECEIVED`.
- `applyKitchenAction`: после команды заказ перечитывается из `ListActiveRestaurantOrders` (ответ команды без позиций и `receivedAt`); у `REJECT` и у заказа, ушедшего с доски, `order = null`. Причина отказа `RejectRestaurantOrder` не передаётся: в схеме её нет.
- **Cook Pool** ([FR-622, FR-625](../../openspec/changes/add-cook-and-courier-pools/)): `Kitchen.cookPool` — `GetCookPool` (`restaurant#view_orders`): размер, на линии, занято, очередь и повара по имени с текущим заказом; `KitchenOrder.cook` — повар из пула по `cook_id` заказа (`null` до старта и у выбывшего повара). Mutations `hireCook`, `setCookOnLine`, `retireCook` вызывают `HireCook`, `PutCookOnLine`/`TakeCookOffLine`, `RetireCook` (`restaurant#manage_cooks`, GRPCRoute `restaurants-v1-cook-pool`) и отвечают `CookPoolPayload` — пул перечитывается после команды. Изменения поваров приходят тем же `kitchenChanged`.

| Ответ `restaurants` | GraphQL |
| --- | --- |
| нет или невалидный access token | error `UNAUTHENTICATED`, `restaurants` не вызывается |
| пустые `restaurantId`, `kitchenOrderId` или `action` | error `INVALID_ARGUMENT` |
| нет gateway или identity | error `UNAVAILABLE` (`kitchen operations are not available`) |
| доска: `NOT_FOUND`, `PERMISSION_DENIED`; `GetRestaurant`: `NOT_FOUND` | `kitchen = null` |
| команда: `FAILED_PRECONDITION` | `problem = ORDER_STAGE_CONFLICT`, `order = null`; у `ACCEPT` так же отвечают закрытый ресторан и недоступная позиция — у `restaurants` для них один reason |
| `StartPreparation`: `RESOURCE_EXHAUSTED` | `problem = NO_COOK_AVAILABLE`: на линии нет свободного повара, заказ в очереди |
| Cook Pool: `INVALID_ARGUMENT` (`hireCook`), `FAILED_PRECONDITION` | `problem = COOK_NAME_INVALID`; у `setCookOnLine(false)` — `COOK_BUSY`, `setCookOnLine(true)` — `COOK_RETIRED`, `retireCook` — `COOK_ON_LINE` |
| Cook Pool: `NOT_FOUND` | error `NOT_FOUND` |
| команда: `NOT_FOUND` | error `NOT_FOUND` |
| команда: `PERMISSION_DENIED` | error `PERMISSION_DENIED` |
| команда: `INVALID_ARGUMENT` | error `INVALID_ARGUMENT` без деталей `restaurants` |
| остальные сбои | error `UNAVAILABLE` без внутренних деталей |

### Слайс `courier`

`plugins/bff/courier/applications` — сценарии Get Courier, Set Availability, Accept Offer и Apply Delivery Action; `courier/infrastructure` вызывает RPC курьера `delivery` от имени пользователя (audience `nomnom.delivery`, GRPCRoute `delivery-v1-courier` и `delivery-v1-courier-reads`). Курьер — сам пользователь: `delivery` регистрирует курьера под user id (`RegisterCourier`), поэтому `courier_id` — subject access token; `courier#act` и принадлежность доставки или offer курьеру проверяет `delivery`.

- `courier`: `GetCourierCurrentDelivery` — статус курьера (`courier_status`) и текущая доставка с адресами; `offers` — `ListAvailableDeliveryOffers`, только пока курьер `AVAILABLE`, иначе `[]`; `orderId` — заказ доставки offer (`order_id`); `expiresAt` — из `expires_at_epoch_seconds`.
- Mutations отвечают `CourierPayload`: курьер перечитывается после команды, в том числе после бизнес-отказа.
- **Местоположение.** Dispatch предлагает доставку только курьеру с известной позицией (`UpdateCourierLocation`, e2e `order-lifecycle` вызывает его перед `SetCourierAvailability`). В схеме нет операции для позиции, и BFF её не выдумывает: `setCourierAvailability(AVAILABLE)` не меняет позицию, offers приходят курьеру, чья позиция уже известна `delivery`. Позицию курьерского приложения должна передавать отдельная mutation (например `reportCourierLocation(latitude, longitude)`) — её нужно добавить в контракт; до тех пор в kind позицию задаёт seed или fixture.

| Ответ `delivery` | GraphQL |
| --- | --- |
| нет или невалидный access token | error `UNAUTHENTICATED`, `delivery` не вызывается |
| `setCourierAvailability(BUSY)`, пустые `offerId`, `deliveryId` или `action` | error `INVALID_ARGUMENT` |
| нет gateway или identity | error `UNAVAILABLE` (`courier operations are not available`) |
| `GetCourierCurrentDelivery`: `NOT_FOUND`, `PERMISSION_DENIED` | `courier = null` |
| команды: `PERMISSION_DENIED` (или `NOT_FOUND` курьера) | error `PERMISSION_DENIED`: пользователь не курьер |
| `AcceptDeliveryOffer`: `NOT_FOUND`, `FAILED_PRECONDITION` | `problem = OFFER_NOT_AVAILABLE` |
| `ConfirmPickup`/`ConfirmDelivery`: `FAILED_PRECONDITION` | `problem = DELIVERY_STAGE_CONFLICT` |
| `ConfirmPickup`/`ConfirmDelivery`: `NOT_FOUND` | error `NOT_FOUND` |
| `SetCourierAvailability`: `FAILED_PRECONDITION` | error `FAILED_PRECONDITION`: в `CourierProblemCode` нет кода для отказа по статусу курьера |
| остальные сбои, в том числе `ABORTED` гонки | error `UNAVAILABLE` без внутренних деталей |

### Слайс `courierpool`

`plugins/bff/courierpool/applications` — сценарии Get Pool и Change Pool диспетчера ([FR-623](../../openspec/changes/add-cook-and-courier-pools/)); `courierpool/infrastructure` вызывает `GetCourierPool`, `RegisterCourier`, `UpdateCourierLocation`, `SetCourierAvailability`, `RetireCourier` `delivery` от имени пользователя. Право `platform#manage_couriers` (оператор или диспетчер) проверяет `delivery`.

- `courierPool`: `CourierPool @key(fields: "id")`, `id` — Market запроса (пул курьеров у каждого Market свой, ADR 41); размер, на линии, занято, доставки без курьера (`waiting`) и курьеры по имени с заказом оффера (`offeredOrderId`) и текущей доставки (`currentOrderId`). Пользователь без права получает error `PERMISSION_DENIED` — UI не показывает пул.
- `addCourier(name)`: новый курьер регистрируется под UUIDv7 (за ним не стоит пользователь, действует Simulator) и ставится в стартовую точку `BFF__COURIER_START_LATITUDE`/`BFF__COURIER_START_LONGITUDE` (Helm `courierPool.startLatitude`/`startLongitude`, обязательны: без них плагин не стартует, chart не рендерится) — иначе Dispatch не предложит ему доставку. `setCourierOnLine` и `retireCourier` меняют любого курьера пула.
- Mutations отвечают `CourierPoolPayload`: `INVALID_ARGUMENT` регистрации — `COURIER_NAME_INVALID`, `FAILED_PRECONDITION` доступности — `COURIER_STATUS_CONFLICT` (busy остаётся на линии, выбывший — вне её), `FAILED_PRECONDITION` выбытия — `COURIER_ON_LINE`; `NOT_FOUND` — error `NOT_FOUND`.
- Имя курьера покупателю — `Order.courier` слайса `ordertracking` (`GetOrderCourier`, `order#view`).

## Subscriptions

Решение — [ADR 36](../../docs/adr/0036-graphql-subscriptions-via-cosmo-streams.md). Подписки обслуживает сам Cosmo Router (Cosmo Streams): у gRPC plugin нет streaming.

- `kitchenChanged(restaurantId)` (`restaurants.v1.kitchen-changes`, `LookupKitchenByRestaurantId`, права кухни проверяет `restaurants`), `courierChanged(id)` (`delivery.v1.courier-changes`, `LookupCourierById`: только курьер с этим subject) и `courierPoolChanged(marketId)` (`delivery.v1.courier-pool-changes`, фильтр по `CourierPool.id = marketId`; `LookupCourierPoolById`: только диспетчер и только пул Market из `X-Nomnom-Market`, иначе `NOT_FOUND`) устроены так же; `Kitchen.restaurantId`, `Courier.id` и `CourierPool.id` — ключи сущностей.
- `orderChanged(id)` объявлен в event-driven subgraph [`events/schema.graphql`](events/schema.graphql) (`@edfs__kafkaSubscribe`, фильтр по `id`). Router читает `orders.v1.order-changes` (`events.providers.kafka`, `BFF__KAFKA_BROKERS`, Helm `kafka.brokers` обязателен).
- Событие — только ключ `{"__typename":"Order","id":…}`. Данные Router получает через `LookupOrderById` plugin (`Order @key(fields: "id")`) с `Authorization` подписчика: тот же сценарий, что `order(id)`. Чужой или отсутствующий заказ — error-событие `NOT_FOUND`.
- Клиенты подписываются по SSE (`POST /graphql`, `Accept: text/event-stream`). Первое событие приходит только после изменения, поэтому текущее состояние берётся query.
- Клиентская схема — композиция subgraphs: `npm run compose` пишет `config.json` и [`client-schema.graphql`](client-schema.graphql) для codegen UI.

## BFF в Cell

Router ячеек — публичный gateway, а не BFF ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md), [локальный ADR 2](docs/adr/0002-bff-as-the-client-entry-point.md#маршрутизация-по-ячейкам)): путь `/m/{market_id}/…` ведёт в Cosmo Router Cell Market. Router с plugin BFF развёрнут в каждой Cell и обслуживает только её.

- **Market запроса.** Gateway снимает префикс `/m/{market_id}` и ставит заголовок `X-Nomnom-Market` из маршрута; Router пробрасывает его в plugin правилом `headers.all.request: propagate` ([`config.yaml`](config.yaml), [`config.production.yaml`](config.production.yaml)). Браузер заголовок не шлёт, поэтому в CORS его нет. `market.From` (пакет `plugins/bff/infrastructure/market`) читает входящую metadata `x-nomnom-market`: нет заголовка, он повторён или не совпадает с `^[a-z][a-z0-9-]{1,31}$` — `INVALID_ARGUMENT`.
- **Соединения.** Одно `*grpc.ClientConn` на audience (`nomnom.orders`, `nomnom.restaurants`, `nomnom.delivery`) к internal gateway своей Cell (`BFF__INTERNAL_GATEWAY_ADDR`), `gateway.Dial` (service token, user assertion, otelgrpc). Слайсы получают его через `market.Require`: вызов без валидного Market отклоняется до контекста. Market не передаётся контекстам metadata: адаптеры `ListRestaurants`, `ListOwnedRestaurants`, `ListCustomerOrders`, `GetCourierPool` и `RegisterCourier` ставят `market_id` в запрос. Соединения закрываются после `plugin.Serve()`, когда Router останавливает plugin.
- **Ошибки.** Отказ по Market — `market.Refusal`, намеренно не gRPC status: адаптеры переводят коды контекстов в бизнес-ошибки (`NOT_FOUND` ресторана), и отказ за них не сойдёт. `internalError` отдаёт клиенту код отказа как есть.
- **Список Market** BFF не отдаёт и к control plane не обращается: UI читает его у публичного gateway (`GET /markets`).
- **Subscriptions** Cosmo Streams Router каждой Cell читают Kafka своей Cell ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md#развёртывание)).

## Identity покупателя

Решение — [ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md); код — пакет [`plugins/bff/identity`](plugins/bff/identity).

- **Access token.** Router передаёт заголовок `Authorization` в плагин правилом `headers.all.request: propagate` в [`config.yaml`](config.yaml); другие клиентские заголовки (например `x-user-id`) до плагина не доходят. Проверено на Router 0.363.0: без правила metadata `authorization` в плагине пустая. Router (`identity.providers` в chart → `authentication.jwt.jwks`) отклоняет token с неверной подписью кодом 401 до плагина. `AccessTokenVerifier` выбирает JWKS по `iss`, проверяет подпись (RS256/ES256, `kid`), `aud` BFF и срок с допуском 30 s; ID token (есть `nonce`) отклоняется — у Dex access token тоже без `scope` (ADR 35).
- **User assertion.** `AssertionSigner` подписывает ES256 JWT на каждый вызов контекста: `iss: nomnom-bff`, `aud` контекста, `sub` из проверенного token, срок 30 s, случайный `jti`.
- **Входящая и исходящая части.** `identity.Load` собирает `Identity`: `AccessTokens` (`AccessTokenVerifier`) проверяет входящие запросы, `Outgoing` (`OutgoingCredentials`: `ServiceTokens` и `Signer`) аутентифицирует вызовы контекстов; `gateway.Dial` получает только исходящую часть.
- **Вызовы контекстов.** `UnaryClientInterceptor(audience, tokens, signer)` (интерфейсы `TokenSource` и `Signer`) ставит `authorization: Bearer <projected token BFF для audience>` (ADR 27) и, если в контексте запроса есть покупатель (`identity.WithUser`), `x-nomnom-user-assertion`.

| Переменная | Назначение |
| --- | --- |
| `BFF__IDP_PROVIDERS` | JSON-массив identity providers: `issuer` и `jwksUrl` (обновляется, ADR 35) или `jwksFile`; ключи одного issuer не подтверждают token другого |
| `BFF__IDP_AUDIENCE` | audience access token (`nomnom-bff`) |
| `BFF__ASSERTION_KEY_FILE`, `BFF__ASSERTION_KEY_ID` | ключ подписи assertions (P-256, PEM) и его `kid` |
| `IDENTITY__TOKEN_DIR` | каталог projected ServiceAccount tokens, файл на audience |

Переменные и `BFF__INTERNAL_GATEWAY_ADDR` обязательны: без них или с частичной конфигурацией плагин не стартует. Helm: `identity.*` в [`ops/helm/values.yaml`](ops/helm/values.yaml) — providers с JWKS URL, ключ из Secret, projected tokens для `identity.tokenAudiences`. Ротация ключа: новый `kid` сначала в JWKS контекстов, затем в BFF, старый удаляется после 60 s. Тесты используют общие векторы `tests/identity` (`idp-vectors.json`, `bff-signing-key.pem`).

## Место в архитектуре

```text
mobile / web
      │ HTTPS + GraphQL
      ▼
edge ingress/gateway
      │
      ▼
Cosmo Router
      │ generated plugin gRPC
      ▼
BFF router plugin
      │ internal gRPC
      ├────────► orders
      ├────────► restaurants
      ├────────► delivery
      └────────► payments
```

Edge завершает внешний TLS, применяет rate limiting и базовую защиту от злоупотреблений. Cosmo Router является первой прикладной точкой. BFF остаётся сервисом клиентских сценариев, а не bounded context: у него нет собственной доменной модели или хранилища доменных данных.

Платёжные webhooks обслуживаются интеграционными адаптерами `payments`. Health checks, metrics и служебные интерфейсы имеют отдельные технические маршруты и правила доступности.

## Границы ответственности

BFF проверяет клиентский GraphQL-запрос и access token, формирует пользовательский subject context, координирует внутренние gRPC-вызовы и собирает клиентское представление. Клиентские заголовки с subject, ролями/scopes или tenant не считаются доверенным источником. Во внутренний вызов subject передаётся как исходный access token либо как короткоживущая signed internal identity; сервис-владелец проверяет token/signature, issuer, audience и expiry. Обычный внутренний header сам по себе доверенным не является.

При этом:

- семантическую валидацию доменной команды выполняет сервис-владелец;
- BFF может выполнять coarse-grained authorization для endpoint и раннего отказа, но authoritative check прав на доменное действие, принадлежности resource и tenant-изоляции остаётся у bounded context;
- доменные данные получаются только через gRPC API владельцев;
- BFF не читает базы данных bounded contexts;
- BFF не публикует Kafka-сообщения и не потребляет Integration Events; Router читает только топики уведомлений `orders.v1.order-changes`, `restaurants.v1.kitchen-changes` и `delivery.v1.courier-changes` для subscriptions ([ADR 36](../../docs/adr/0036-graphql-subscriptions-via-cosmo-streams.md));
- BFF не запускает и не управляет Temporal workflow напрямую;
- агрегация не создаёт общую транзакцию или атомарный снимок нескольких сервисов.

Внутренний сервис принимает subject context только после криптографической проверки выбранного механизма и отдельно авторизует действие через SpiceDB. Identity BFF, mTLS, internal gateway и NetworkPolicy являются дополнительными слоями и не заменяют проверку subject. Детали зафиксированы в [документации security](../../docs/security.md) и [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md).

## API composition scenarios

Эти сценарии описывают публичную GraphQL-композицию, а не отдельный bounded context. BFF не становится владельцем вызываемых бизнес-операций.

| Scenario | Actor | Intent | Result | Делегирование |
| --- | --- | --- | --- | --- |
| **Browse Restaurants** | Клиент | Найти рестораны для выбора | Собрано клиентское представление списка ресторанов | Query `List Restaurants` в `restaurants` |
| **View Restaurant Menu** | Клиент | Посмотреть ресторан и его актуальное меню | Собрано клиентское представление ресторана и меню | Queries `Get Restaurant` и `Get Menu` в `restaurants` |
| **Place Order** | Клиент | Оформить выбранные позиции | Mutation передана `orders`, клиент получил `order_id` и начальное представление заказа | Command `Place Order` в `orders`; BFF не запускает workflow самостоятельно |
| **View Order** | Клиент | Получить текущее клиентское представление заказа | Собрано разрешённое представление заказа и заранее определённые enrichment-поля | Query `Get Order` в `orders`, при необходимости queries других владельцев |
| **List My Orders** | Клиент | Получить свою историю заказов | Возвращён разрешённый список заказов клиента | Query `List Customer Orders` в `orders` |
| **Cancel Order** | Клиент | Запросить отмену заказа | Mutation передана владельцу заказа, возвращён результат принятия запроса | Command `Request Order Cancellation` в `orders`; BFF не выполняет компенсации |
| **Open/Close Restaurant** | Владелец ресторана | Начать или прекратить приём новых заказов | Команда передана `restaurants`, возвращено состояние открытия | Commands `Open Restaurant` и `Close Restaurant` в `restaurants`; authorization `restaurant#manage` у `restaurants` |
| **Courier View** | Курьер | Работать с предложениями и текущей доставкой | Собраны доступные offers и текущая доставка; команды accept/reject, Pickup и Delivery Completion делегированы владельцу | Queries и commands `delivery`; BFF не владеет assignment или доставкой |

## Структура проекта

```text
boundaries/bff/
  config.yaml                 # runtime-конфигурация Cosmo Router
  config.json                 # generated execution config
  config.production.yaml      # конфигурация Router в образе
  Dockerfile                  # образ BFF: Router + plugin + config.json
  graph.yaml                  # локальная композиция graph/plugin
  package.json                # закреплённый wgc и команды проекта
  plugins/bff/
    src/schema.graphql        # публичный GraphQL-контракт
    src/main.go               # запуск plugin и bffInfo; единственный log.Fatalf
    src/wiring.go             # конфигурация, соединения с контекстами Cell, сборка слайсов
    src/auth.go               # requireCustomer и requireSlice
    src/errors.go             # ошибки resolvers без внутренних деталей
    src/*_presenter.go        # перевод представлений в GraphQL-типы, таблицы enum
    src/<slice>.go            # только resolvers слайса
    src/*_test.go             # тесты plugin
    catalog/                  # слайс каталога: applications, infrastructure
    checkout/                 # слайс оформления заказа
    ordertracking/            # слайс отслеживания заказа
    restaurantmanagement/     # слайс открытия ресторана владельцем
    simulator/                # слайс переключателя demo Simulator
    identity/                 # access token, user assertion, service token
    infrastructure/gateway/   # соединение с internal gateway
    infrastructure/market/    # Market запроса и отказ вызова без него
    infrastructure/grpcerr/   # ErrorInfo и metadata ошибок контекстов
    infrastructure/grpc/      # gRPC-клиенты контекстов (generated)
    generated/                # generated gRPC-контракт и mapping
```

Код plugin организован vertical slices (`catalog`, `checkout`, `ordertracking`, `restaurantmanagement`). Слайсы содержат прикладную координацию и инфраструктурные адаптеры, но не собственную доменную модель. Порты объявлены рядом со сценариями (`OrderReader`, `CancellationRequester`, `RestaurantLister`, `RestaurantReader`, `OrderPlacer`, `RestaurantOpener`); адаптер реализует несколько портов и переводит ошибки каждой операции отдельно.

Композиция ([`refactor-bff-composition`](../../openspec/changes/archive/2026-10-25-refactor-bff-composition/), FR-551): `wiring.go` читает окружение один раз и даёт всем слайсам по одному соединению с internal gateway своей Cell (через `market.Require`) на `nomnom.orders`, `nomnom.restaurants` и `nomnom.delivery`; ошибки конфигурации возвращаются в `main`. Значения прикладного слоя переводятся в GraphQL-enum явными таблицами presenter с тестом полноты; неизвестное значение — error `UNAVAILABLE` resolver, а не `UNSPECIFIED`.

Внутренние клиенты будут генерироваться из закреплённых BSR commits согласно [общесистемному ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). Интеграция этих клиентов с plugin относится к следующему шагу.

## Требования

- Node.js LTS или новее;
- npm;
- Go 1.26 или новее;
- toolchain plugin generator (`protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`), который `wgc` может установить автоматически;
- Cosmo Router binary для локального запуска.

## Команды

Установить зависимости:

```bash
npm ci
```

Сгенерировать plugin-контракт, выполнить тесты, собрать plugin и локальную композицию:

```bash
npm run check
```

Отдельные команды:

```bash
npm run generate
npm test
npm run build
npm run compose
```

Скачать Router и запустить его:

```bash
npm run router:download
npm start
```

После запуска GraphQL Playground доступен на `http://localhost:3010`. Проверочный запрос:

```graphql
query BffInfo {
  bffInfo {
    name
    version
  }
}
```

## Статус контейнеризации

Образ BFF собирает [`Dockerfile`](Dockerfile) ([ADR 10](../../docs/adr/0010-использовать-multi-stage-container-builds.md)), build context — `boundaries/bff`:

| Stage | Что делает |
| --- | --- |
| `router` | скачивает release Cosmo Router `router@0.363.0` (`router-router@<version>-linux-<arch>.tar.gz`) и сверяет SHA-256, закреплённый для `linux/amd64` и `linux/arm64` (`ARG ROUTER_SHA256_*`); при несовпадении сборка падает |
| `plugin` | `go build` плагина в `plugins/bff/bin/<os>_<arch>` — путь, по которому Router ищет plugin |
| `config` | `config.json` командой `wgc router compose` закреплённого `wgc` из `package-lock.json` (локальная композиция, без сети и токенов) |
| `runtime` | `gcr.io/distroless/static-debian13:nonroot` (UID 65532): `/app/router`, plugin, `config.json` и [`config.production.yaml`](config.production.yaml) как `/app/config.yaml` |

Production config отличается от [`config.yaml`](config.yaml) для локального запуска: `listen_addr: 0.0.0.0:3010`, без `dev_mode`, Playground и introspection, без экспорта telemetry и usage metrics в Cosmo Cloud; traces — только в OTLP collector окружения при `TRACING_ENABLED=true` (в образе `false`) на `BFF__OTLP_TRACES_ENDPOINT` (OTLP/HTTP), с `service.name=bff`, `service.namespace=nomnom` и `deployment.environment.name` из `BFF__DEPLOYMENT_ENVIRONMENT`; Router продолжает W3C trace context вызывающего (spans Faro браузера). Plugin получает exporters от Router (`routerplugin.WithTracing`), пишет spans как `service.name=bff-plugin` (namespace ему добавляет Alloy: resource plugin задаёт router-plugin) и передаёт `traceparent` в internal gateway через otelgrpc client handler (`infrastructure/gateway/dial.go`), поэтому trace продолжается в restaurants и orders; правило `headers.all.request: propagate Authorization` то же. Router — PID 1: он сам обрабатывает `SIGTERM` (drain, остановка plugin, exit 0). Plugin получает окружение Router, в том числе переменные [identity](#identity-покупателя).

```bash
docker build --target runtime -t nomnom/bff:local boundaries/bff
docker run --rm -p 3010:3010 nomnom/bff:local   # /health/live, /health/ready, POST /graphql
```

Проверено на 0.363.0 для `linux/arm64` и `linux/amd64`: оба образа отвечают на `bffInfo` и health; сборка с неверным `--build-arg ROUTER_SHA256_ARM64` падает на `sha256sum -c` (AC-574). Обновление Router: новая версия и оба checksum вместе (SHA-256 архивов совпадает с digest assets на странице release), затем сборка и `make kind-test`. `plugins/bff/Dockerfile` — шаблон `wgc` для публикации plugin, а не образ BFF.

## Развёртывание

Helm chart и команды его проверки описаны в [ops/README.md](ops/README.md). Probes — штатные `/health/live` и `/health/ready` Router на порту `http`: отдельного технического порта для health у Router нет (исключение из ADR 17).

В kind ([ops/kind](../../ops/kind/README.md)) Skaffold собирает образ (artifact `nomnom/bff`) и ставит release `bff` в `nomnom-bff` с [`ops/kind/values/bff.yaml`](../../ops/kind/values/bff.yaml): ServiceAccount `bff`, контексты только через internal gateway, projected tokens для `nomnom.orders`, `nomnom.restaurants` и `nomnom.delivery`, `ReferenceGrant` для маршрутов публичного gateway (`publicGateway.namespace`). Ключ подписи user assertions выпускает cert-manager один раз на кластер (chart `ops/kind/bootstrap`, Secret `bff-assertion-key`, ключ `tls.key`), access tokens выпускает тестовый identity provider из `tests/identity/fixtures` — только для kind. Клиенты приходят через listener `api` публичного gateway (`https://api.nomnom.localhost:18085/m/berlin/graphql`); проверки `ops/kind/e2e/bff` обращаются к Service `bff` напрямую с заголовком `X-Nomnom-Market`.

## Следующие шаги

- определить production-схему публикации и получения execution config через Cosmo control plane;
- выбрать identity provider и способ получения access token браузером; identity между BFF и контекстами решена ADR 32;
- сгенерировать внутренние gRPC-клиенты из BSR;
- реализовать resolvers customer API и политику частичных результатов;
- настроить deadlines, cancellation, идемпотентность и наблюдаемость.
