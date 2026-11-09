# Orders

## Назначение

Контекст `orders` отвечает за оформленный заказ и координацию его общего жизненного цикла.

## Язык

Go.

Стратегия unit, BDD acceptance, integration, Temporal workflow и system E2E проверок описана в [общей документации тестирования](../../docs/testing.md).

## Данные и состояния

Контекст владеет заказом, его составом и стоимостью, текущим состоянием общего жизненного цикла, запросами отмены и отражёнными результатами взаимодействия с другими контекстами.

## Границы ответственности

`orders` координирует процесс заказа, но не владеет приготовлением, назначением курьера, выполнением доставки или платёжными операциями. Эти действия принадлежат соответственно `restaurants`, `delivery` и `payments`; `orders` отражает их результаты в состоянии заказа.

Temporal используется для оркестрации длительного процесса заказа: ожиданий, таймеров и взаимодействия с другими контекстами.

Workflow относится к прикладному процессу. Доменные правила и инварианты заказа остаются независимыми от Temporal SDK.

Внешние вызовы и операции с побочными эффектами выполняются через Activities. Повторяемые операции должны быть идемпотентными.

Контракт orchestration описан в разделе [Order Fulfillment Workflow](#order-fulfillment-workflow) и [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md); продуктовые таймауты и точная последовательность шагов определяются отдельными SDD-спецификациями.

## Use Cases

Статус реализации: application handlers всех перечисленных Commands и Queries реализованы поверх PostgreSQL store и адаптеров портов (см. [Статус реализации](#статус-реализации)); transport, persistence, Kafka и Temporal adapters реализованы, новые создаются вместе с SDD changes; пустые программные scaffolds не используются.

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Place Order** | Клиент | Оформить заказ из выбранных позиций одного ресторана, сразу или к Delivery Window | Создан `Order`, зафиксированы состав, цены, необходимые snapshot-данные и окно доставки запланированного заказа, назначен `order_id`, `OrderPlaced` записан в Outbox; заказ ещё не считается принятым рестораном или оплаченным | Читает необходимые сведения ресторана и меню через согласованный API; с `delivery_window_start` резервирует окно в `restaurants` (`ReserveScheduledSlot`) до транзакции; Temporal не вызывает — workflow стартует Start Fulfillment |
| **Start Fulfillment** | Kafka consumer `orders` | Запустить fulfillment process по собственному `OrderPlaced` | У незавершённого `Order` есть workflow `order:<order_id>`; финальный `Order` не запускается заново | Integration event `OrderPlaced` из `orders.v1.order-events`, Transactional Inbox, `SignalWithStart` после commit (и на дубликате Inbox); ошибка Temporal — повторная доставка, не DLQ |
| **Request Order Cancellation** | Клиент или другой авторизованный actor | Запросить прекращение незавершённого заказа | Проверены права и правила допустимости, запрос передан существующему fulfillment workflow; компенсации не выполняются самим use case | Authoritative authorization в `orders`; сигнал Temporal workflow |
| **Confirm Order** | Kafka consumer `orders` | Отразить Order Acceptance ресторана | `Order` подтверждён один раз, fulfillment workflow может продолжиться | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Reject Order** | Kafka consumer `orders` | Отразить отказ ресторана принять `Kitchen Order` | Отказ сохранён один раз, fulfillment workflow получил бизнес-результат | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Record Preparation Started** | Kafka consumer `orders` | Отразить начало Preparation | Стадия приготовления отражена в `Order` один раз | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Record Order Ready** | Kafka consumer `orders` | Отразить готовность `Kitchen Order` к Pickup | Готовность отражена в `Order` один раз | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Record Preparation Delay** | Kafka consumer `orders` | Отразить задержку Preparation | Новый срок готовности заменил прежний в `Order`, workflow продлил ожидание Preparation | Integration event `PreparationDelayed` от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Record Courier Assignment** | Kafka consumer `orders` | Отразить Courier Assignment | Назначение отражено в `Order` один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Record Courier Release** | Kafka consumer `orders` | Отразить снятие курьера без замены или отмену Delivery | Курьер снят с сохранённого назначения; при отмене Delivery удалена `order#delivery` (доступ курьера отзывает `delivery`) | Integration events `CourierUnassignedFromDelivery`/`DeliveryCancelled` от `delivery`, Transactional Inbox; `Order` и workflow не меняются |
| **Record Pickup** | Kafka consumer `orders` | Отразить Pickup | Получение заказа курьером отражено один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Record Delivery Completion** | Kafka consumer `orders` | Отразить Delivery Completion | Завершение доставки отражено один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Record Delivery Failure** | Kafka consumer `orders` | Отразить неудачу Delivery | Бизнес-неудача доставки и причина отражены один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Record Payment Authorization** | Temporal fulfillment workflow | Зафиксировать одобренную Authorization до запроса ресторана | `Order` перешёл в `paid` переходом `authorize_payment` (только из `placed`), клиент видит статус `PAID`; повтор — no-op | Вызывается Activity `RecordPaymentAuthorization` между `AuthorizePayment` и `RequestRestaurantOrder`; `order_details` и Change Notification, без Integration Event |
| **Plan Schedule** | Temporal fulfillment workflow | Рассчитать план запланированного заказа | `planned_ready_at`, `start_at`, `courier_request_at`, `replan_at` по свежим оценкам; `Order` не меняется, план хранит Event History | Activity `PlanSchedule`: базовое время ресторана (`restaurants.GetRestaurant.baseline_preparation`) и оценка доставки Market (`delivery.EstimateDeliveryDuration`) через internal gateway; domain-политика `order.PlanSchedule`, `SCHEDULED_ORDER__*` (FR-677) |
| **Complete Order** | Temporal fulfillment workflow | Зафиксировать успешный итог после выполнения всех необходимых условий | `Order` переведён в окончательное успешное состояние | Вызывается orchestration после подтверждённых результатов участвующих контекстов |
| **Fail or Cancel Order** | Temporal fulfillment workflow | Зафиксировать окончательный негативный итог после orchestration и необходимых компенсаций | `Order` переведён в соответствующее окончательное состояние с сохранением причины | Workflow координирует компенсации через Activities; use case не присваивает правила `restaurants`, `delivery` или `payments` |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Order** | Клиент или внутренний авторизованный actor | Получить текущее представление одного заказа | Возвращено доступное actor представление заказа без изменения состояния | Read model `orders`; проверка права просмотра |
| **List Customer Orders** | Клиент или внутренний авторизованный actor | Получить историю заказов клиента | Возвращён доступный actor список заказов клиента без изменения состояния | Read model `orders`; проверка принадлежности и права просмотра |

Основной Temporal Workflow — fulfillment одного заказа. `Start Fulfillment` запускает его по собственному `OrderPlaced`, записанному Place Order; use cases `Confirm Order`, `Reject Order`, `Record …` и `Request Order Cancellation` передают ему конкретные бизнес-результаты или запросы; `Complete Order` и `Fail or Cancel Order` фиксируют выбранный workflow итог. Отдельный Workflow на каждый use case не создаётся. Результаты Authorization, Capture и Refund принадлежат `payments` и поступают непосредственно в состояние fulfillment workflow через Temporal adapter; отдельными состояниями агрегата `Order` они не являются. Одобренную Authorization workflow отражает в `Order` переходом `authorize_payment` (Record Payment Authorization) и только после этого запрашивает Restaurant Order; `ReceiveOrderRequest` несёт `priority` заказа (Premium Priority, FR-650), вход workflow при этом не меняется — Activity читает его из Order. Вызовы платёжных операций, создание и отмена `Restaurant Order`, создание доставки и повторный поиск курьера выполняются workflow через Activities и публичные API контекстов-владельцев.

Kafka естественно входит только во входные use cases, отражающие конкретный бизнес-факт: событие проходит Transactional Inbox до изменения `Order` и сигнала workflow. Исходящие бизнес-факты `orders` публикуются через существующую Outbox-модель; конкретные имена и payload событий здесь не определяются. Кроме того, каждая запись `Order` (`ProcessStore.Add`/`Save`) кладёт в тот же Outbox Order Change Notification — JSON `{"__typename":"Order","id":…}` в `orders.v1.order-changes`. Это не Integration Event: его читает только Cosmo Router BFF для GraphQL subscriptions ([ADR 36](../../docs/adr/0036-graphql-subscriptions-via-cosmo-streams.md)).

## Структура слайсов

Слайсы `orders` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста). Модули находятся в `slices/<slice>/`; внутри `applications/` каждый use case имеет отдельный Go package, например `slices/order/applications/fulfillmentsteps/confirm`. Только shared packages находятся в `pkg/`; код отдельного слайса или entrypoint туда не помещается. Единственная каноническая модель `Order` и её инварианты используются всеми use cases модуля `order`.

| Слайс | Содержание |
| --- | --- |
| `order` | Агрегат `Order`, его use cases (включая `order/complete` и `order/failorcancel`, которые вызывают Activities), порт Order Fulfillment Workflow `applications/fulfillment`, persistence, SpiceDB, Kafka и catalog |
| [`fulfillment`](slices/fulfillment/README.md) | Процесс Order Fulfillment: доменная модель `domains/fulfillment` (маршрут, факты, сроки, отмена, компенсации), Temporal-адаптер `infrastructure/temporal`, client-адаптеры соседей `infrastructure/neighbours`, provider set worker'а `di` |
| `simulator` | Демо-Simulator (FR-604..FR-606): switch `applications/switchsimulator` (`GetSimulator`/`SetSimulatorEnabled` требуют `platform#operate_simulator`, иначе `PERMISSION_DENIED`; состояние — однострочная таблица `simulator_state`, нет строки — выключен) и покупатель `applications/customersimulator`: пока включён, через случайную задержку в `[simulator.minDelay, simulator.maxDelay]` оформляет заказ настоящим Place Order от user `simulator.customerId` в случайном открытом ресторане (ListRestaurants по каждому Market из `simulator.marketIds` и GetMenu через internal gateway; 1–3 доступные позиции, количество 1–2; Market заказа берёт Place Order из выбранного ресторана) и в 10% случаев после следующей задержки запрашивает отмену, пока заказ `placed` или `paid`. Тики каждые `simulator.interval` выполняет `SimulatorRunner` процесса `cmd/orders` (им владеет `App`); ошибки use cases пишутся в лог. Все пять ключей `spec.simulator` обязательны для `cmd/orders`. Каждая реплика API ведёт своего покупателя |

Зависимости направлены от `fulfillment` к `order`: адаптер реализует порт `order/applications/fulfillment` и вызывает use cases `order` из Activities; `order` не импортирует `fulfillment`, а доменная модель `fulfillment` — ничего, кроме стандартной библиотеки.

Temporal workflow относится к прикладному процессу по ответственности, но вместе с Activities и транспортными обработчиками является инфраструктурным адаптером, поэтому правила процесса принимает доменная модель слайса `fulfillment`, а workflow в `slices/fulfillment/infrastructure/temporal` только переводит Signals и Update в её вызовы и исполняет её решения как Activities и таймеры. Workflow не знает о Commands, Queries, handlers и `Order`; Activity преобразует payload в Command или Query и вызывает handler слайса `order`, а use case обращается к workflow (start, Signal, Update) через порт в `applications/`. Ни Temporal SDK, ни Watermill, ни Google Wire не импортируются доменной моделью и handlers. Правила моделей и entrypoints — в [общесистемном ADR 6](../../docs/adr/0006-ddd-structure-inside-vertical-slices.md#типы-моделей).

## gRPC-адаптер

`orders` владеет API `nomnom.orders.v1` и публикует его как `buf.build/nomnom/orders`. Исходные messages находятся в `infrastructure/grpc/nomnom/orders/v1/orders.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер генерирует серверные Go stubs из локальной схемы и преобразует request messages в commands/queries, а прикладные результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделями `Order`, handlers или Temporal workflow. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API. Вид ошибки (`errorkind.Kind`: invalid input, not found, already exists,
conflict, domain rule, unavailable, permission, internal) определяет одна функция
[`errorkind.Classify`](slices/order/applications/errorkind/errorkind.go) слайса `order`; gRPC, Kafka consumer,
порт Order слайса `fulfillment` и телеметрия переводят вид своими таблицами, тест каждой проверяет полноту.
Уточнения этого API (`RESTAURANT_*`, `INVALID_ORDER`, metadata `ORDER_NOT_ORDERABLE`, правило окна
отмены) остаются в gRPC-адаптере.

| Ошибка `orders` | gRPC code | `ErrorInfo.reason` |
| --- | --- | --- |
| `Order` не найден | `NOT_FOUND` | `ORDER_NOT_FOUND` |
| Ресторан или его меню не найдены (Place Order) | `NOT_FOUND` | `RESTAURANT_NOT_FOUND` |
| Ресторан не принимает заказы (`accepting_orders = false`: выключен или вне расписания, Place Order) | `FAILED_PRECONDITION` | `RESTAURANT_NOT_ACCEPTING_ORDERS` |
| `priority = PREMIUM` у ресторана без `premium_fee` (Place Order, FR-650) | `FAILED_PRECONDITION` | `PREMIUM_UNAVAILABLE` |
| Окно доставки занято (Place Order, FR-692) | `FAILED_PRECONDITION` | `SLOT_UNAVAILABLE` + деталь `SlotUnavailable{alternatives}` после `ErrorInfo` |
| Ресторан без запланированных заказов или не принимает заказы сейчас (Place Order с окном) | `FAILED_PRECONDITION` | `SCHEDULED_ORDERS_UNAVAILABLE` |
| Позиции нельзя заказать (политика `OrderItemsOrderable`, Place Order) | `FAILED_PRECONDITION` | `ORDER_NOT_ORDERABLE` + metadata `codes`, `menu_item_ids` |
| Некорректный состав | `INVALID_ARGUMENT` | `INVALID_ORDER` |
| Текущее состояние запрещает переход | `FAILED_PRECONDITION` | `ORDER_STATE_CONFLICT` |
| Окно отмены ещё не открыто или уже закрыто | `FAILED_PRECONDITION` | `CANCELLATION_WINDOW_NOT_OPEN`, `CANCELLATION_WINDOW_EXPIRED` |
| `Order` с таким идентификатором уже существует | `ALREADY_EXISTS` | `ORDER_ALREADY_EXISTS` |
| Неожиданная внутренняя ошибка | `INTERNAL` | `INTERNAL_ERROR` |
| Внешняя зависимость недоступна | `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` |

Недоступная зависимость (SpiceDB, catalog, отсутствующий workflow) проверяется раньше отказа
доступа: fail-closed check оборачивает её в отказ, но вызывающий получает retryable `UNAVAILABLE`.
`INVALID_ORDER` — ошибки value objects, пустой состав и повтор `menu_item_id`;
`ORDER_NOT_ORDERABLE` (`order.NotOrderable`) передаёт причины в `ErrorInfo.metadata` через запятую:
`codes` — каждый код один раз в верхнем регистре (`ORDER_ITEM_UNAVAILABLE`,
`ORDER_TOTAL_BELOW_MINIMUM`, `ORDER_TOTAL_ABOVE_MAXIMUM`), `menu_item_ids` — позиции, к которым
относятся причины (пусто, если причины касаются только суммы);
`ORDER_STATE_CONFLICT` — запрет FSM (включая нарушенный guard `GuardFailed`) и отказ workflow принять Cancellation Request; отказ по окну
отмены (`CancellationNotAllowed`) называет нарушенное правило, чтобы клиент отличил его от
завершённого заказа.

В `google.rpc.ErrorInfo.domain` передаётся `nomnom.orders.v1`. Реализованы Buf-модуль,
сгенерированные Go stubs и RPC `GetOrder`, `ListCustomerOrders`, `PlaceOrder` и
`RequestOrderCancellation`; все вызывают application handlers и используют структурированные
`ErrorInfo`. Остальные Commands в API не публикуются: Confirm/Reject Order, Record … и Relay
Preparation Issue вызывает Kafka consumer, Complete Order и Fail or Cancel Order — только
fulfillment workflow.

### Market (ADR 41)

`Order` несёт обязательный неизменяемый `market_id` ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)) —
Market ресторана из catalog snapshot Place Order (`restaurants.Restaurant.market_id`; пустой — ошибка,
Market по умолчанию нет). `GetOrder` и каждый `OrderSummary.order` отдают `Order.market_id`;
`ListCustomerOrdersRequest.market_id` обязателен (пустой или не slug `^[a-z][a-z0-9-]{1,31}$` —
`INVALID_ARGUMENT`) и ограничивает историю этим Market. `OrderPlaced`, `OrderCompleted` и
`OrderCancelled` несут `market_id`; Activities workflow передают его в `payments.CreatePayment` и
`delivery.CreateDelivery`, читая из `Order`, а не из входа workflow.

`PlaceOrder` оформляет заказ для verified subject: поля customer в запросе нет. Customer — user
subject из user assertion BFF ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)):
BFF передаёт свой service token и `x-nomnom-user-assertion`; service subject без assertion
(в том числе сам BFF) получает `PERMISSION_DENIED`. Catalog snapshot
до транзакции читает адаптер [`slices/order/infrastructure/catalog`](slices/order/infrastructure/catalog)
(FR-497): `restaurants.GetRestaurant` (`accepting_orders` — флаг ресторана и его расписание в момент чтения, FR-699) и `GetMenu` через internal gateway
(`internalGateway.address`; без адреса процесс не стартует) с projected token `nomnom.restaurants` API orders (`identity.tokenDir`, если задан) и общим
deadline `catalog.timeout`. В snapshot попадают только запрошенные позиции; отсутствующие в меню
недоступны. Закрытый ресторан — `RESTAURANT_NOT_ACCEPTING_ORDERS`, `NOT_FOUND` ресторана или меню и
пустое меню — `RESTAURANT_NOT_FOUND`, цены в разных валютах — `INVALID_ORDER`, любая другая ошибка
`restaurants` (transport, deadline, identity, отсутствующий route) — `UNAVAILABLE`. Валюта snapshot —
валюта меню; минимум и максимум суммы — `catalog.minimumTotal`/`catalog.maximumTotal` в этой
валюте (placeholders, пока `restaurants` их не публикует). `order_id` — UUIDv7. `PlaceOrderRequest.delivery_window_start` делает заказ запланированным (FR-681, `PREMIUM` допустим):
после catalog snapshot и генерации `order_id` адаптер [`scheduledslots`](slices/order/infrastructure/scheduledslots)
вызывает `restaurants.ReserveScheduledSlot` от service identity orders (deadline `catalog.timeout`), `Order` хранит окно из ответа,
`OrderPlaced` несёт его. `slot_unavailable` — `SLOT_UNAVAILABLE` с альтернативами, `FAILED_PRECONDITION` c `ErrorInfo.reason`
`SCHEDULED_ORDERS_UNAVAILABLE` — `SCHEDULED_ORDERS_UNAVAILABLE`, `INVALID_ARGUMENT` окна — `INVALID_ARGUMENT`; `Order` не сохраняется,
hold истекает в `restaurants`. Повтор с тем же idempotency key возвращает сохранённый `Order` без новой резервации. `PlaceOrder` Temporal не вызывает:
workflow запускает consumer собственного `OrderPlaced` (Start Fulfillment); поле
`PlaceOrderResponse.fulfillment_started` удалено (`reserved 3`). Temporal client API lazy: процесс
стартует без Temporal, а `RequestOrderCancellation` до старта workflow или без Temporal отвечает `UNAVAILABLE`.
`RequestOrderCancellation` возвращает outcome `REQUESTED` или `ALREADY_CANCELLED` и использует тот
же экземпляр cancellation policy, что `GetOrder`.

`GetOrder` возвращает `Order.cancellation_window { opens_at, closes_at }`, пока Order State
допускает Order Cancellation (compound state `in_progress`); для `cancelled` и `completed` поля нет.
Окно задаёт та же cancellation policy, что использует Request Order Cancellation: оно
открывается в момент Place Order и длится `spec.cancellation.window` (по умолчанию `5m` — placeholder до продуктового решения; `0s` или неразбираемое значение
останавливают процесс на старте). У запланированного заказа окно закрывается в
`window_start − spec.cancellation.scheduledCutoff` (FR-462, обязателен, неположительный — ошибка старта).
`Order.delivery_window { start, end }` отдаётся у запланированного заказа. Открыто ли окно сейчас, решает потребитель по своему
clock, а сам запрос отмены проверяется заново. Если политика вернёт ошибку, `GetOrder`
отдаёт заказ без окна, а декоратор `cancellationpolicy.Observed` пишет ошибку в log и span. `SubjectResolver` проверяет projected
ServiceAccount token и user assertion (ADR 27, ADR 32); без `IDENTITY__*` процесс не стартует, а произвольным metadata
gateway не доверяет. Политика контрактов, генерации и совместимости описана в
[ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md).

## Минимальная доменная модель

Для учебной реализации достаточно одного агрегата `Order`.

| Тип | Минимальные поля | Назначение |
| --- | --- | --- |
| `Order` — aggregate root | `id`, `customerId`, `restaurantId`, `items`, `total`, `status` | Владеет составом, стоимостью и общим состоянием заказа |
| `OrderItem` — entity внутри `Order` | `menuItemId`, `quantity`, `unitPrice` | Фиксирует заказанную позицию, количество и цену на момент оформления |

Состояния `Order` — на языке покупателя: `placed`, `paid`, `confirmed`, `preparing`, `ready`, `picked_up`, `rejected`, `delivery_failed`, `delivered`, `cancelled`, `completed`; compound states `open` (`in_progress` и `settling`) и `closed`, см. [FSM](../../docs/fsm.md#order). Свои автоматы есть у кухни (`RestaurantOrder`) и доставки (`Delivery`), `Order` их не копирует. Переходы монотонны: факт может перепрыгнуть несообщённый шаг, отставший только дописывается в журнал.

Запланированный заказ хранит Delivery Window — value object `DeliveryWindow` (`[start, end)`, UTC), зафиксированный резервацией при Place Order; нулевое окно — ASAP-заказ. План запланированного заказа считает чистая политика `order.PlanSchedule` (`planned_ready_at = window_start − transit`, `start_at = planned_ready_at − preparation − buffer`, `courier_request_at = planned_ready_at − courier_lead`, `replan_at = start_at − replan_lead`).

Кроме state, `Order` хранит время оформления, историю переходов FSM (`AppliedTransition{Trigger, OccurredAt}`, таблица `order_transitions`) и причину отрицательного итога. Положение заказа — только state; история хранит, когда применён каждый trigger, и пишется вместе с переходом, в том числе при `stay`; повтор шага — ошибка `TransitionAlreadyApplied`. Статусы соседей `Order` не хранит: авторизация — в `payments`, назначение курьера — в `delivery`; `CourierAssignment` — копия для доступа и порядка событий, строка FSM `update_details` на compound state `open` только допускает её изменение. Причины отказа и неудачи остаются у владельцев и идут в workflow сигналом. `authorize_payment` применяет Record Payment Authorization в `placed` и `paid`. `confirm` применяет Confirm Order с моментом события Order Acceptance; у заказов, подтверждённых раньше, его нет. `rejected` и `delivery_failed` — промежуточные: Cancellation и Failure завершаются одним state `cancelled` с сохранённым видом итога (`cancellation`/`failure`) и кодом причины, которые задаёт Saga. Domain Events: `OrderPlaced`, `OrderCancelled`, `OrderCompleted` (с моментом завершения `CompletedAt`, который передаёт `Complete`). Назначение курьера — value object `CourierAssignment` с правилами `Supersedes` (событие старше сохранённого устарело), `Displaced` (что вытесняет переназначение) и `Release` (снятие сохраняет своё время); контракт хранения `CourierAssignments` объявлен в домене. Сроки Fulfillment (FR-611) — value object `ExpectedTimes{ReadyAt, PickupAt, DeliveredAt}`, последние названные владельцами: готовность — из `RestaurantOrderAccepted` и каждого `PreparationDelayed`, Pickup — из последнего по Delivery version `CourierAssignedToDelivery` (новое назначение заменяет срок, в том числе на пустой), вручение — из `DeliveryPickedUp`; нулевое время — срок не назван, финальный `Order` сроки не принимает (строка FSM `update_details`). Сроки хранятся в nullable-колонках `orders.expected_*`, отдаются в `GetOrder` (`Order.expected_ready_at`/`expected_pickup_at`/`expected_delivered_at`), а их изменение сохраняет `Order` и тем самым кладёт Order Change Notification для `orderChanged` (ADR 36).

Инварианты:

- заказ содержит хотя бы одну позицию;
- `menuItemId` не повторяется внутри одного заказа;
- все позиции относятся к одному `restaurantId`;
- `total` равен сумме `unitPrice × quantity` и вычисляется моделью, а не принимается от клиента;
- отменённый или завершённый заказ больше не изменяется;
- завершить можно только подтверждённый заказ.

### Value objects

| VO | Значение и инварианты |
| --- | --- |
| `Money` | Точная десятичная сумма на основе [`shopspring/decimal`](https://github.com/shopspring/decimal) и код валюты; сумма неотрицательна, арифметика разрешена только для одной валюты |
| `Quantity` | Положительное целое число |

`Money` создаётся из строки или другого точного представления, но не из `float64`. Требуемые масштаб и режим округления явно задаются доменной операцией.

`OrderId`, `CustomerId`, `RestaurantId` и `MenuItemId` на первом этапе остаются типизированными идентификаторами, а `OrderStatus` — перечислением. Для них не создаются отдельные value objects без дополнительных правил.

### Domain policies и Specification Pattern

Переиспользуемые условия вне state оформляются как чистые domain Specifications рядом с canonical `Order`. Реализованы:

- `WithinCancellationWindow` — проверяет `now` относительно времени оформления/подтверждения и cancellation policy;
- `OrderItemsOrderable` — проверяет переданный snapshot доступности позиций, минимальную/максимальную сумму и применимые лимиты.

Допустимость `Cancel` или `Complete` из текущего `OrderStatus` остаётся только в FSM. Application получает время и внешние snapshots и передаёт их в aggregate; он не вызывает принадлежащую поведению `Order` spec вместо domain method. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## CQRS

В контексте `orders` применяется CQRS. Команды выполняют бизнес-действия и изменяют состояние заказа только после проверки доменных правил и инвариантов. Запросы используют отдельные модели чтения и возвращают DTO, подходящие конкретному сценарию отображения или использования данных.

На начальном этапе командная сторона и модели чтения используют одно хранилище внутри контекста. Локальный CQRS ADR не выбирает конкретную СУБД; ADR 8 ниже конкретизирует это хранилище как PostgreSQL, не меняя границы CQRS.

В соответствии с Vertical Slice Architecture команды и запросы организуются по сценариям, например `request-order-cancellation`, `get-order-details` и `list-customer-orders`. Глобальные каталоги `commands/` и `queries/` не становятся основным каркасом контекста. Общие доменные правила и инварианты переиспользуются между срезами, а не дублируются в каждом сценарии.

Карточку заказа и историю покупателя читают из проекции `OrderDetails` (таблица `order_details`): репозиторий пишет её в транзакции каждого изменения `Order`, `GetOrder` и `ListCustomerOrders` читают только её ([ADR 4](docs/adr/0004-использование-cqrs-в-orders.md#проекция-orderdetails); catch-up и перестроение — в [Статусе реализации](#статус-реализации)).

Решение зафиксировано в [ADR 4](docs/adr/0004-использование-cqrs-в-orders.md).

## Хранилище: PostgreSQL

PostgreSQL — основное логическое хранилище `orders`. Реляционная модель и транзакции подходят для хранения `Order` и его `Order Item`, снимков названий, цен, выбранных вариантов и дополнительных опций на момент оформления, согласованного изменения связанных данных, контроля конкурентных изменений, уникальности и ограничений целостности.

Доменная модель определяет бизнес-инварианты `Order`; ограничения базы дополняют, но не заменяют эти проверки. Реализация репозитория в `infrastructure` преобразует persistence-модели в доменные типы и сохраняет изменения агрегата в явно определённой транзакционной границе. Реализация — [`slices/order/infrastructure/repository`](slices/order/infrastructure/repository): PostgreSQL 18, pgx v5, запросы собираются squirrel, миграции golang-migrate (`migrations/NNNNN_name.up.sql`, только вперёд, встроены в бинарь через `iofs`, драйвер `pgx/v5`). Миграция лежит рядом с репозиторием своей таблицы ([ADR 39](../../docs/adr/0039-data-access-without-orm.md)): у каждого набора своя нумерация с `00001` и своя таблица версий `schema_migrations_<id>`. Id набора (`migration.Set.ID`) — часть контракта схемы базы: строковый литерал рядом с миграциями набора, назван по данным, которые набор хранит, и никогда не выводится из имени пакета, каталога или слайса; runner строит имя таблицы версий только из него, поэтому рефакторинг кода таблицы базы не переименовывает, а смена id требует миграции его таблицы версий. Слайс `order` — id `orders` в [`slices/order/infrastructure/repository/migrations`](slices/order/infrastructure/repository/migrations) (`schema_migrations_orders`: `orders` и все её `ALTER`, `order_items`, `order_transitions`, `inbox`, `idempotency_keys` и Outbox `orders_outbox` с индексом и publication для Debezium — их репозитории в этом пакете), слайс `simulator` — id `simulator` в [`slices/simulator/infrastructure/repository/migrations`](slices/simulator/infrastructure/repository/migrations) (`schema_migrations_simulator`: `simulator_state`); у `fulfillment` таблиц нет. Миграции слайса меняют только его таблицы, межслайсовых foreign keys нет. Каждый набор встраивает свои файлы в своём пакете (`//go:embed`); явный список наборов без автопоиска — [`di/schema`](di/schema), его применяют процессы (`di.NewStore`) и шаблонная база тестов (`repositorytest`). Новый слайс с таблицами заводит свой набор и добавляет его в этот список. Один runner [`infrastructure/migration`](infrastructure/migration) на процесс: каждый процесс при старте применяет недостающие миграции всех наборов под одним advisory lock до запуска остального, применённые пропускает; совместимости со старой общей таблицей `schema_migrations` нет, схема рассчитана на новую базу. Lock берётся на отдельном соединении, миграция идёт на втором с `lock_timeout = 5s`: `ALTER TABLE`, вставший в очередь за транзакцией живого трафика во время rolling update, не замораживает таблицу, а падает, и pod повторяет миграцию после рестарта. Версию, помеченную `dirty` (файл упал или процесс умер посреди него), старт возвращает к предыдущей и применяет файл заново: драйвер отправляет файл одним simple-protocol запросом, PostgreSQL выполняет его одной неявной транзакцией, поэтому упавший файл изменений не оставляет. Отсюда правило: файл миграции не содержит команд, недопустимых в транзакции (`CREATE INDEX CONCURRENTLY` и т.п.). Таблицы `orders` (с `version` и `authorization_revision`), `order_items`, `order_transitions`, `inbox`, `idempotency_keys`, `simulator_state` и Outbox `orders_outbox` (publication `orders_outbox` для Debezium, ADR 37). Unit of Work — `READ COMMITTED` транзакция в context, вложенный `Do` присоединяется к внешней; `Save` проверяет `version` и при расхождении возвращает `ErrConcurrencyConflict`. API, worker и consumer работают с одной базой; `POSTGRES__DSN` обязателен.

`orders` — основной потребитель Unit of Work: `Order` + Outbox, Inbox + `Order` (+ Outbox) и idempotent commands фиксируются одной транзакцией PostgreSQL с `READ COMMITTED` по умолчанию и optimistic concurrency по `version`; queries выполняются без UoW. Workflow транзакцией не владеет, каждая Activity вызывает handler со своим UoW. Общий UoW-контракт находится в `pkg/unitofwork`; use-case-specific ports — рядом с package владельца в `slices/<slice>/applications/<usecase>`, реализация — в infrastructure модуля. Правила и список слайсов — в [документации Unit of Work](../../docs/unit-of-work.md#orders--postgresql), решение — в [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md).

Ссылки на Restaurant, Menu Item, Payment, Delivery и другие сущности соседних bounded contexts хранятся как идентификаторы без межконтекстных foreign keys. Локальные foreign keys допустимы между таблицами, принадлежащими `orders`, например между заказом и его позициями.

CQRS остаётся логическим разделением command- и query-сценариев: первоначально обе стороны используют одну PostgreSQL-базу `orders`. Отдельное физическое read store на этом этапе не вводится.

Бизнес-данные заказов и техническое хранилище Temporal разделены. Репозитории приложения работают с PostgreSQL-базой `orders`; внутренними данными выполнения workflow владеет Temporal. Приложение не использует Temporal storage как репозиторий `Order` и не управляет его внутренними таблицами напрямую.

Решение — [ADR 8](../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md); контрактные тесты адаптеров — `make test-postgres` (PostgreSQL поднимается testcontainers в `TestMain`).

### Пулер PostgreSQL

Перед базой стоит пулер соединений, и он обязан работать в **session mode**: `orders` опирается на состояние server-сессии. Миграции берут session-level advisory lock (`pg_advisory_lock`) на своём соединении, pgx по умолчанию кэширует prepared statements на соединении (`QueryExecModeCacheStatement`). Transaction mode их ломает: lock и prepared statements окажутся на чужом server-соединении. Остальное от режима не зависит: Unit of Work и `FOR UPDATE` (назначение курьера) живут внутри одной транзакции; `LISTEN/NOTIFY`, `SET` вне транзакции и временные таблицы не используются.

Каждый процесс держит собственный пул pgx (`spec.postgres`): `maxConns` по конкурентности роли, `minConns` 0–1, `maxConnLifetime` 30m с `maxConnLifetimeJitter` 5m (реплики не переоткрывают соединения одновременно), `maxConnIdleTime` 5m, `connectTimeout` 5s, `healthCheckPeriod` 1m; недопустимое значение — ошибка startup. В session mode каждое соединение пула занимает server-соединение пулера, поэтому сумма Σ(replicas × maxConns) всех ролей должна помещаться в `pool_size` пулера для базы `orders`:

| Роль | values | maxConns | minConns |
| --- | --- | --- | --- |
| API | `values.yaml` | 10 | 1 |
| worker | `values-worker.yaml` | 5 | 1 |
| consumer | `values-consumer.yaml` | 5 | 1 |
| миграции при старте процесса | — | 2 (lock + миграция), закрывается до старта | 0 |

При одной реплике каждой роли это 22 server-соединения плюс 2 на время миграций. Пул экспортирует OTel semconv `db.client.connection.*` (`count` по `state`, `max`, `idle.min`, `pending_requests`, `timeouts`, `wait_time`, `use_time`, `create_time`; `db.client.connection.pool.name=orders`), инвентарь — [`docs/observability/metric-inventory.md`](../../docs/observability/metric-inventory.md#db).

## Dependency Injection

В `orders` используется compile-time DI через [`github.com/google/wire`](https://github.com/google/wire). Зависимости передаются обычными Go-конструкторами, а Wire генерирует код их сборки в composition root каждого исполняемого процесса. Provider sets группируются по функциональности и необходимым инфраструктурным компонентам.

Каждый слайс описывает свои provider sets в собственном пакете `slices/<slice>/di`: у `order` это инфраструктурные `RepositorySet` (один `ProcessStore` процесса и его привязка к ролям хранилища) и `TelemetrySet` (инструменты use cases), а также наборы use cases процессов — `APISet` (`Module` API) и `ConsumerSet` (handlers Kafka consumer); у `fulfillment` — `WorkerSet` (адаптеры соседей и Activities). Вместо одного интерфейса хранилища потребители объявляют роли рядом с собой: `PlacementStore` и `ReadModels` у `Module`, `InboxStore` у `Consumer`, `fulfillmentdi.Store` у Activities, `spicedb.RevisionReader` у authorizer; Wire связывает их с одним `ProcessStore` — PostgreSQL, у которого Unit of Work и Outbox идут через декоратор бизнес-метрик `observability.ObservedStore`. Порты набора собираются в структуру зависимостей через `wire.Struct` (`Dependencies`, `Consumer`), поэтому новый порт — это поле структуры и provider, а не новый позиционный параметр у всех вызовов. Итоговая сборка процессов находится в корневом пакете `di`: `wire.go` объявляет provider sets процесса (секции конфигурации, Temporal client API, gRPC) и по injector'у на каждый процесс — `InitializeApp`, `InitializeWorker`, `InitializeConsumer`; store процесса (`NewStore`, `storeSet`) и список наборов миграций (`di/schema`) тоже здесь, потому что схема общая для всех слайсов; `wire_gen.go` генерируется командой `make generate` и коммитится. `cmd/*` только загружают файл конфигурации своей роли (`config.Load`), настраивают logger, вызывают свой injector и передают результат в `pkg/lifecycle`. Ресурсы, созданные при сборке (пулы PostgreSQL, клиенты SpiceDB и Temporal API, соединение с internal gateway), освобождает cleanup injector'а; ресурсы, которым нужен контекст `Start` или pipeline метрик процесса (telemetry, Temporal client worker'а и consumer'а, Kafka, Watermill Router), открывает и закрывает сам lifecycle.Worker процесса. Lifecycle реализует [runtime contract](../../docs/cloud-native-runtime.md): `/health/startup`, `/health/live`, `/health/ready` на техническом порту `spec.management.port` (по умолчанию `9081`), а по `SIGTERM`/`SIGINT` переводит readiness в `503`, выжидает `spec.shutdown.readinessDelay` (по умолчанию `5s`), прекращает приём запросов и дожидается начатых в пределах `spec.shutdown.drainTimeout` (по умолчанию `20s`). Штатный shutdown — exit `0`; ошибка startup, остановка сервера до сигнала или превышение drain deadline — non-zero. Поведение проверяется `make runtime-contract` из корня репозитория.

Импорты Wire и объявления сборки не входят в доменную модель и обработчики use cases. Composition root явно владеет запуском, остановкой и освобождением ресурсов. Доменные компоненты и обработчики можно создавать в тестах напрямую через конструкторы.

При запуске Temporal worker зависимости внедряются в Activities и компоненты worker. Детерминированный workflow-код не обращается к DI-контейнеру и не получает через DI клиенты внешних систем: внешние операции выполняются через Activities.

Решение, включая риск архивного статуса Google Wire, зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Событийная интеграция

Для публикации и обработки интеграционных событий через Apache Kafka выбран [Watermill с адаптером `watermill-kafka`](https://watermill.io/pubsubs/kafka/). Выбор зафиксирован в [ADR 3](docs/adr/0003-use-watermill-for-event-integration-in-orders.md).

Consumers интеграционных событий `orders` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение `Order`, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной транзакции PostgreSQL-базы `orders`. Уникальность `(consumer, event_id)` обеспечивается уникальным ограничением. Watermill подтверждает сообщение только после commit. In-memory дедупликация не заменяет durable Inbox.

Temporal Activity retries не заменяют Inbox Pattern. Kafka-событие, которое запускает workflow или отправляет ему Signal, сначала проходит идемпотентную boundary: повторное событие не создаёт второй логический workflow и не применяет Signal повторно, если это нарушает бизнес-семантику. Связь `event_id`, Workflow ID и Signal описана в разделе [Order Fulfillment Workflow](#входящие-события).

Публикация интеграционных событий `orders` использует Transactional Outbox по общесистемному [ADR 13](../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md). Изменение `Order` и строка Outbox (`orders_outbox`) записываются в одной транзакции PostgreSQL; в Kafka их доставляет Debezium из WAL ([ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md)): топик — колонка `topic`, value — `payload`, заголовки Event Envelope — колонки строки. Отдельного процесса публикации нет. `event_id` строки передаётся в Kafka headers `event_id` и `_watermill_message_uuid` (UUID сообщения Watermill у consumers). Kafka key — колонка `aggregate_id` (`order_id`): события одного заказа идут в одну партицию в порядке commit. Activity, изменяющая `Order` и Outbox, идемпотентна по ключу, производному от Workflow ID и шага процесса, или по ожидаемой `aggregate_version`; workflow-код не публикует события.

Domain Events переводятся в Integration Events `nomnom.orders.v1` (`OrderPlaced`, `OrderCompleted`, `OrderCancelled`, [`events.proto`](infrastructure/grpc/nomnom/orders/v1/events.proto)) в той же транзакции; каждое событие несёт `market_id` Order ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)); `OrderPlaced` несёт состав заказа `items`; его читает только `orders-consumer` (Start Fulfillment), а Restaurant Order создаёт workflow RPC `ReceiveOrderRequest` после оплаты; `event_id` детерминирован (UUIDv5 от `order:<id>:<version>:<index>`), `occurred_at` берётся из Domain Event (для `OrderCompleted` — момент завершения). Перевод делает `IntegrationMapper` в `slices/order/infrastructure/messaging/outbound` (таблица Domain Event → контракт); PostgreSQL Outbox только сохраняет готовые сообщения; Debezium переносит их из WAL в Kafka `orders.v1.order-events` с ключом `order_id` (ADR 37). Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

Архитектурные решения контекста хранятся в [локальном журнале ADR](docs/adr/). Выбор Temporal зафиксирован в [ADR 2](docs/adr/0002-use-temporal-to-orchestrate-orders.md), контракт orchestration — в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md).

## Order Fulfillment Workflow

Решение — [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md), требования и сценарии — [спецификация `order-fulfillment-orchestration`](../../openspec/specs/order-fulfillment-orchestration/spec.md). Application-слой видит workflow только через порт [`fulfillment`](slices/order/applications/fulfillment/README.md). Правила процесса — маршрут, реакция на факты, сроки, допустимость Cancellation Request и план компенсации — реализует доменная модель слайса [`fulfillment`](slices/fulfillment/README.md) без Temporal SDK, часов и случайности (`FR-483`); всё ниже относится к процессу и его Temporal-адаптеру [`slices/fulfillment/infrastructure/temporal`](slices/fulfillment/infrastructure/temporal/README.md), который переводит Signals и Update в вызовы модели и исполняет её решения.

### Когда применяется Temporal

Temporal применяется к процессу, который одновременно многошаговый, зависит от других контекстов, ждёт внешних фактов или сроков, требует retries или компенсаций и должен пережить перезапуск worker. Таков fulfillment одного `Order`. Use case, который проверяет, меняет `Order` и делает commit в одной короткой транзакции, workflow не запускает.

### Identity и старт

| Параметр | Значение |
| --- | --- |
| Workflow | Один Order Fulfillment Workflow на `Order`, без child workflows |
| Workflow ID | `order:<order_id>`; повторный старт — `USE_EXISTING` |
| Namespace | Отдельный на окружение |
| Task queue | `orders` |

Единственный старт:

```text
Place Order: UoW { save Order; Outbox(OrderPlaced) }
  → Kafka orders.v1.order-events → orders-consumer (группа orders.order-events)
  → Start Fulfillment: UoW { Inbox(orders.startfulfillment); read Order }
  → после commit: SignalWithStart(order:<order_id>, OrderPlaced, USE_EXISTING)
```

Place Order Temporal не вызывает. Дубликат Inbox тоже выполняет `SignalWithStart` — так лечится сбой между commit Inbox и стартом. `Order` в `completed`/`cancelled` (redrive DLQ, сброс offset) не запускается: история закрытого workflow могла уйти из namespace. Ошибка Temporal — transient, сообщение доставляется повторно и в DLQ не уходит. Собственные `OrderCompleted` и `OrderCancelled` группа подтверждает явным no-op.

Базовая логическая схема — карта coordination points, а не утверждённая последовательность:

```text
Order placed → Authorize payment → Record payment in Order → Request Acceptance
→ wait (accepted | rejected | timer) → Create Delivery / wait Assignment
→ wait Preparation → wait Pickup → Record Pickup in kitchen → Capture payment
→ wait Delivery Completion → Complete Order
```

### Запланированный заказ

Order с Delivery Window идёт веткой `AuthorizePayment → RecordPaymentAuthorization → ConfirmSlotReservation → PlanSchedule → RequestRestaurantOrder(window, planned_ready_at)`; после `RestaurantOrderAccepted` workflow ждёт в stage `scheduled` таймеров `replan_at` (`PlanSchedule` с тем же `planned_ready_at`), `start_at` (`QueueRestaurantOrder`) и `courier_request_at` (`CreateDelivery`), затем идёт обычным маршрутом. Deadline Preparation считается от `QueueRestaurantOrder`, Assignment — от `CreateDelivery`. Ветка введена под patch `scheduled-route`; подробности — [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md#запланированный-заказ).

### Входящие события

```text
Kafka(event_id) → orders consumer → Transactional Inbox → application handler
  → Signal/Update(event_id) → dedupe в workflow + проверка Workflow Stage
```

Два уровня дедупликации: Inbox по `(consumer, event_id)` защищает изменения `Order`; workflow хранит обработанные `event_id`, потому что Signal RPC может примениться, а ответ потеряться. Соседние контексты никогда не вызывают Temporal напрямую.

Имя Signal совпадает с именем Integration Event; адаптер переводит `fulfillment.Kind` в это имя.

| Signal / Update | Источник | Use case `orders` |
| --- | --- | --- |
| `OrderPlaced` (`SignalWithStart`) | `orders` | Start Fulfillment |
| `RestaurantOrderAccepted` / `RestaurantOrderRejected` | `restaurants` | Confirm Order / Reject Order |
| `PreparationStarted` | `restaurants` | Record Preparation Started |
| `PreparationDelayed` | `restaurants` | Record Preparation Delay (срок готовности в `Order`) |
| `PreparationFailed` | `restaurants` | Relay Preparation Issue (`Order` не меняется) |
| `RestaurantOrderReady` | `restaurants` | Record Order Ready |
| `CourierAssignedToDelivery` | `delivery` | Record Courier Assignment |
| `CourierUnassignedFromDelivery` / `DeliveryCancelled` | `delivery` | Record Courier Release (не сигнал workflow: снятие возвращает Delivery в поиск, отмену запрашивает сам workflow) |
| `DeliveryPickedUp` / `DeliveryCompleted` / `DeliveryFailed` | `delivery` | Record Pickup / Delivery Completion / Delivery Failure |
| `CancellationRequested` (Update) | клиент через `orders` | Request Order Cancellation |

Положительное событие, которого текущий Workflow Stage не ждёт, логируется и игнорируется. Отрицательные факты (`RestaurantOrderRejected`, в том числе после Acceptance, `PreparationFailed`, `DeliveryFailed`) в незавершённом workflow ведут в Saga; отказ оплаты ведёт в неё через результат Activity. Событие после завершения workflow обрабатывается по policy, которую документирует каждый use case; сейчас такие use cases возвращают `ignored_late`.

### Signal, Update, Query

| Механизм | Когда |
| --- | --- |
| Signal | Асинхронный факт, ответ не нужен |
| Update | Нужен результат validation относительно Workflow Stage — клиентская Cancellation |
| Query | Только техническая диагностика |
| CQRS read model | Все клиентские чтения (`GetOrder`, `ListCustomerOrders`) — проекция `OrderDetails` ([ADR 4](docs/adr/0004-использование-cqrs-в-orders.md#проекция-orderdetails)) |

Business cancellation — Update `CancellationRequested`: домен `Order` решает допустимость (`CheckCancellation`), workflow — компенсации. Temporal `CancelWorkflow` — аварийная техническая операция, а не Cancellation.

### Activities, retry и timeout

- Workflow детерминирован: gRPC, HTTP, DB, Kafka и SpiceDB — только в Activities.
- Каждая Activity имеет Start-To-Close timeout и Retry Policy; длительная — heartbeat. Transport failure (`UNAVAILABLE`, timeout) повторяется, business rejection возвращается результатом.
- Idempotency key финансовых и компенсационных операций — `order_id + step`, не зависит от attempt и run ID.
- Activity, меняющая `orders`, вызывает handler слайса со своим UoW (`Order` + Outbox); прямой publish в Kafka запрещён.
- Вызовы других контекстов идут через [internal gateway](../../ops/kind/README.md#internal-gateway) (`internalGateway.address`) от `service` identity worker'а; authorization выполняет целевой контекст по [security](../../docs/security.md).
- Ожидания — Signal + Temporal timer, polling запрещён. Timer ведёт к бизнес-ветке (cancellation, compensation); workflow-level timeout не задаётся. Courier Assignment workflow ждёт без срока (FR-709): поиском владеет Assignment Workflow `delivery`. `PreparationDelayed` продлевает таймер Preparation.

### Saga

Workflow ведёт журнал подтверждённых шагов и при отказе вызывает только применимые компенсации — `CancelDelivery`, `CancelRestaurantOrder`, `ReleaseSlotReservation` (запланированный заказ до Pickup), `CancelPaymentAuthorization` или `RefundPayment` — каждая отдельной идемпотентной Activity со своим ключом, timeout, retry и telemetry. `RestaurantOrderCancelled`, `PaymentAuthorizationCancelled` и `PaymentRefunded` подтверждают уже полученный результат и Saga повторно не запускают. Ресторан запрашивается только после одобренной Authorization и шага `RecordPaymentAuthorization` (у него компенсации нет — факт остаётся историей), поэтому при отказе оплаты Restaurant Order не существует и `CancelRestaurantOrder` не вызывается.

### Runtime

API, Kafka consumer и Temporal worker — отдельные процессы с собственными composition roots по [Cloud Native Runtime](../../docs/cloud-native-runtime.md). Worker при shutdown прекращает poll, даёт Activities завершиться, закрывает clients и flush telemetry; liveness не зависит от Temporal Server. Crash worker не business failure: Temporal восстановит workflow replay'ем.

Изменение кода workflow несовместимо с replay ранее начатых histories: versioning не ведётся, пока нет production-развёртываний. Перед выкаткой нового worker'а в kind дождитесь завершения тестовых workflow или прервите их (`temporal workflow terminate --query 'WorkflowType="OrderFulfillment" AND ExecutionStatus="Running"'`); так было и при выделении слайса `fulfillment`.

### Где Temporal не используется

- CQRS queries и клиентские чтения;
- локальный `request → transaction → response`;
- доставка событий между контекстами — это Outbox, Kafka и Inbox по [событийной интеграции](../../docs/event-driven-architecture.md);
- authorization — SpiceDB;
- хранение `Order` — PostgreSQL;
- универсальные фоновые задания и job queue.

| Что | Где живёт | Не является |
| --- | --- | --- |
| Order State, история переходов, инварианты | `Order` в PostgreSQL `orders` | — |
| Workflow Stage, обработанные `event_id`, таймеры, журнал compensation | Temporal Event History | Source of truth `Order` |
| Интеграционные события | Kafka + Outbox/Inbox | Workflow state |
| Клиентские представления | CQRS read model | Temporal Query |
| Authorization | SpiceDB в use case владельца | Решение workflow |
| Task queue `orders` | Temporal | Job queue общего назначения |

Workflow Stage публикуется низкокардинальным Search Attribute для Temporal UI и не становится Order State.

Отложенные продуктовые решения (таймауты, момент Capture, параллельность Assignment, late-event policy, отказ ресторана после Acceptance, Continue-As-New) перечислены в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md#отложенные-продуктовые-решения).

## Observability

Для `orders` observability охватывает gRPC, PostgreSQL, Watermill/Kafka, Temporal workflows и Activities, API и workers. W3C Trace Context передаётся через gRPC metadata и Watermill message metadata/Kafka headers; telemetry при replay workflow не должна повторно считаться бизнес-результатом.

`pkg/telemetry` собирает три сигнала каждого процесса с одним resource из файла конфигурации (`spec.telemetry`, `spec.tracing`, `spec.metrics`, `spec.logging`; `OTEL_*` и `LOG__LEVEL` процесс не читает): `service.namespace=nomnom`, `service.name` — `telemetry.serviceName` (по умолчанию роль: `orders`, `orders-worker`, `orders-consumer`), `service.version` (VCS revision сборки; Helm задаёт image tag в `telemetry.resourceAttributes`), атрибуты окружения из `telemetry.resourceAttributes` (например `deployment.environment.name`; `service.name` и `service.instance.id` в них — ошибка старта) и `service.instance.id` из env `POD_NAME` (обязателен). OTLP коллектор — `telemetry.endpoint` (базовый URL), протокол `telemetry.protocol`: `grpc` (по умолчанию) или `http/protobuf` (пути `/v1/traces`, `/v1/metrics` добавляет процесс).

| Процесс | Traces | Metrics | Logs |
| --- | --- | --- | --- |
| `orders` (API) | gRPC server, use cases, PostgreSQL, SpiceDB, catalog через internal gateway, Temporal client (Update отмены) | gRPC server/client, use cases, domain, `db.client.*`, runtime | JSON |
| `orders-worker` | Temporal workflow task и Activity, use cases, PostgreSQL, вызовы соседей через gateway | Temporal SDK, use cases, domain, `db.client.*`, gRPC client, runtime | JSON, Temporal SDK |
| `orders-consumer` | Kafka receive (otelsarama) → process (parent из headers), use cases, PostgreSQL, SpiceDB, Temporal client | Watermill consumer, `nomnom.messaging.*`, use cases, `db.client.*`, Temporal SDK, runtime | JSON, Watermill, Temporal SDK |

- **Traces** экспортируются по OTLP, только если `tracing.enabled: true` (тогда `telemetry.endpoint` обязателен); без флага trace context всё равно передаётся соседям. W3C Trace Context (без baggage для Kafka и Temporal) идёт через gRPC metadata, Temporal headers (`contrib/opentelemetry` interceptor client'а, worker его наследует) и Kafka: Outbox записывает `traceparent`/`tracestate` контекста use case в metadata события (`messaging.InjectTraceContext`), строка Outbox хранит `traceparent`, Debezium отдаёт его заголовком, consumer извлекает его до span обработки. PostgreSQL — `otelpgx` поверх pgx: client spans только внутри trace, с `db.namespace` и параметризованным текстом statement (`db.query.text`, по нему Grafana открывает запрос в Database Observability), без bind parameters. SpiceDB — client spans `otelgrpc` без RPC-метрик.
- **Metrics** — по OTLP при `metrics.enabled: true` раз в `metrics.exportInterval` (по умолчанию `60s`); endpoint без флагов ничего не экспортирует. `db.client.operation.duration` (`otelpgx`) — с labels `db.system.name`, `db.operation.name`; пул — `db.client.connection.*` (`repository/pool_metrics.go`).
- **Logs** — одна JSON-запись на строку в stdout: `timestamp` (UTC, RFC 3339), `severity` (`DEBUG`…`ERROR`), `message`, resource-поля `service.*` и `deployment.environment.name`, `trace_id`/`span_id` только внутри span, ошибки — `error.message` и `error.type`. Через тот же handler идут `slog`, `log.Printf`, gRPC (`grpclog`, info/warning — на уровне debug), Watermill и Temporal SDK. Redaction: поля с именами `*token*`, `*password*`, `*secret*`, `*dsn*`, `authorization`, `cookie`, `payload` и байтовые значения заменяются на `[REDACTED]`; userinfo URL/DSN, `password=`, bearer/basic credentials и JWT вырезаются из любого текста. `logging.level` — `debug`, `info` (по умолчанию), `warn`, `error`; до загрузки файла ошибки старта пишутся тем же JSON на уровне `info`.

В kind traces API уходят в приёмник проверок `trace-capture` ([`ops/kind`](../../ops/kind/README.md#проверки)). Общие правила и путь Pyroscope для Go описаны в [общесистемной документации observability](../../docs/observability.md).

## Статус реализации

Реализован in-memory доменный агрегат `Order`: value objects, вычисление total, чистая табличная FSM, типизированные ошибки переходов и забор domain events. Также реализованы чистые policies `WithinCancellationWindow` и `OrderItemsOrderable` с типизированными reasons и unit tests.

OpenSpec change `implement-orders-application-layer` добавил application-слой (FR-100…FR-117, AC-100…AC-130):

- handlers всех 12 Commands и 2 Queries в `slices/order/applications/<usecase>`; `OrderItemsOrderable` подключена в `order.Place`, `WithinCancellationWindow` — в `Order.CheckCancellation`;
- shared порты `UnitOfWork`, `Inbox` и verified `Subject` в `pkg/unitofwork` и `pkg/authz`; общих application errors нет: у слайса `order` они в `applications/failure` и `applications/errorkind`, у `fulfillment` — в порте `applications/orders`; `Clock` объявляет use case, которому нужно время; контракт `order.Repository` в `domains/order`; порт Order Fulfillment Workflow и общий Inbox-поток входящих фактов в [`slices/order/applications/fulfillment`](slices/order/applications/fulfillment/README.md); узкие порты authorization, catalog snapshot, cancellation policy, Outbox, idempotency и read models — рядом с use case;
- тесты handlers работают против настоящего PostgreSQL ([`slices/order/infrastructure/repository/repositorytest`](slices/order/infrastructure/repository/repositorytest): testcontainers, отдельная база на тест из мигрированного template); внешние системы (SpiceDB, catalog, Temporal workflow, sync связей) подменяются mockery-моками, сгенерированными в тестовый пакет потребителя (`mocks*_test.go`), часы, генератор id и окно отмены — stand-ins из `slices/order/infrastructure/fake`; fixtures агрегата — `domains/order/ordertest`. In-memory адаптеров хранения нет.

Контракт Temporal-адаптера за портом `fulfillment` зафиксирован в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md) и разделе [Order Fulfillment Workflow](#order-fulfillment-workflow). OpenSpec change `implement-order-fulfillment-temporal-adapter` реализовал его на `go.temporal.io/sdk` v1.50.0: workflow с deadlines из `Policy`, Saga и Update `CancellationRequested`; Activities поверх `order/complete`, `order/failorcancel` и client-портов `Payments`, `Restaurants`, `Delivery`; реализацию порта `fulfillment` на Temporal client. Значения `DefaultPolicy` — placeholders до продуктовых решений. OpenSpec change `extract-order-fulfillment-slice` выделил процесс в слайс [`fulfillment`](slices/fulfillment/README.md): правила — в доменной модели `domains/fulfillment` с табличными unit-тестами, workflow — тонкий адаптер [`infrastructure/temporal`](slices/fulfillment/infrastructure/temporal/README.md) с прежними именами workflow, Signals, Update, Activities, task queue, Search Attribute и idempotency key шагов.

OpenSpec change `add-orders-temporal-worker` добавил процесс `cmd/orders-worker`: Wire injector `InitializeWorker`, конфигурацию `temporal.*` и `fulfillment.*`, проверку и регистрацию Search Attribute `OrderFulfillmentStage` при старте, адаптеры соседей `unavailable` (по умолчанию) и `simulated` (теперь в `slices/fulfillment/infrastructure/neighbours`), values-worker.yaml Helm chart и integration-тест против Temporal dev server в testcontainers (`make test`).

Реализован общий Kafka transport на Watermill: конфигурация brokers/client/group,
publisher с обязательным partition key `order_id`, subscriber consumer group и
единый mapping `event_id` в UUID/metadata.

OpenSpec change `add-orders-postgresql` перевёл API, worker и consumer на общий PostgreSQL store
([`slices/order/infrastructure/repository`](slices/order/infrastructure/repository)): Unit of Work,
repository с optimistic concurrency, Inbox, idempotency keys, read models, authorization revision
и Transactional Outbox (доставляет Debezium, ADR 37); миграции каждый процесс применяет при старте.
Market заказа (ADR 41) хранится в обязательной колонке `orders.market_id` (CHECK по формату slug) и
колонке `order_details.market_id`; миграция `00008_market` один раз проставила существующим строкам
стартовый Market `berlin` и сделала колонки `NOT NULL`, значения по умолчанию нет. История клиента
читается по индексу `order_details_customer_market_history (customer_id, market_id, placed_at DESC, order_id DESC)`.
Premium Priority (FR-650) — колонки `orders.priority` (`STANDARD`/`PREMIUM`, обязательна, без default;
миграция `00009_premium_priority` проставила существующим заказам `STANDARD`) и
`premium_fee_amount`/`premium_fee_currency` (CHECK: у премиума положительная доплата, у стандартного нет);
документ `order_details` несёт `priority` и `premium_fee` (projection version 2, catch-up переписывает старые строки).
Delivery Window запланированного заказа (FR-681) — nullable `orders.delivery_window_start`/`delivery_window_end`
(миграция `00010_delivery_window`, CHECK: обе NULL — ASAP-заказ, иначе `start < end`; значения существующим строкам не подставляются)
и `delivery_window` документа `order_details` (projection version 3).
Чтения идут из проекции `order_details`: репозиторий пишет её в транзакции каждого `Add`/`Save`, процесс
API после старта в фоне догоняет устаревшие строки (catch-up) и становится ready только после этого, догон по требованию — `orders-admin projections catch-up order-details`, исправление дефекта проектора поднимает `projection_version` ([ADR 4](docs/adr/0004-использование-cqrs-в-orders.md#перестроение-проекции)).

Операторская команда `cmd/orders-admin` входит в образ и запускается разово в pod `orders-consumer`
(`kubectl -n nomnom-orders exec deploy/orders-consumer -- /orders-admin …`): `dlq list|redrive|discard`
для dead letters группы `orders` ([Разбор DLQ](../../docs/dlq.md)), `projections catch-up order-details` и `authz resync` (связи order всех заказов в SpiceDB заново из PostgreSQL).
Тесты, которым нужно хранилище, используют этот же store через `repositorytest` (Docker обязателен для `make test`).

OpenSpec change `add-order-command-rpcs-and-relationships` (FR-464…FR-470, AC-508…AC-524)
опубликовал `PlaceOrder` и `RequestOrderCancellation` и добавил relationships заказа
(`authorization/schema.zed`):

| Relation | Writer | Когда |
| --- | --- | --- |
| `order#customer@user`, `order#restaurant@restaurant` | Place Order | один `WriteRelationships` до commit заказа |
| `order#delivery@delivery` | Record Courier Assignment, Record Courier Release (`orders-consumer`) | из `CourierAssignedToDelivery` (`TOUCH`; смена Delivery — `TOUCH` новой и `DELETE` прежней одним запросом) и `DeliveryCancelled` (`DELETE`) внутри Inbox unit of work до commit |

`view = customer + restaurant->view_orders + delivery->act_as_courier`, `cancel` — только
customer. Relation `order#courier` нет (change `scope-courier-access-to-assignment`): курьер видит
заказ, пока `delivery` держит `delivery#courier` активного назначения, — отзыв доступа целиком на
стороне владельца факта. Назначение хранится как бизнес-данные в `orders.delivery_id`, `courier_id`,
`courier_assigned_at` (миграция `00002`) и `courier_delivery_version` (миграция `00004`, Delivery
`aggregate_version` события); правила — в `order.CourierAssignment`. Переназначение в пределах той же
Delivery запросов к SpiceDB не делает и `authorization_revision` не меняет; событие с версией Delivery
ниже сохранённой пропускается (`occurred_at` порядок commit не задаёт); ZedToken записи
`order#delivery` сохраняется в `authorization_revision`. Ошибка SpiceDB откатывает Inbox record, и
redelivery повторяет событие.

Снятие курьера без замены (`CourierUnassignedFromDelivery`) только очищает `courier_id` без запроса
к SpiceDB, отмена Delivery (`DeliveryCancelled`) удаляет `order#delivery` и очищает
`delivery_id`; событие применяется, если относится к сохранённой Delivery и его версия не ниже
сохранённой, и снимает сохранённого курьера. Снятие сохраняется со своей версией (миграции `00003`, `00004`), даже если
назначение ещё не обработано, поэтому более старое назначение после него пропускается. См. [Record Courier Release](slices/order/applications/recordcourierrelease/README.md).

Связи `order#customer`, `order#restaurant` и `order#delivery` в SpiceDB ([ADR 18](../../docs/adr/0018-spicedb-for-authorization.md))
use cases не пишут. Place Order, Record Courier Assignment (новая Delivery) и Record Courier Release
(отмена Delivery) кладут `OrderRelationshipsChanged` во внутренний стрим `orders.v1.order-relationships`
через Outbox своей транзакции. `orders-consumer` (группа `orders.order-relationships`) запускает
[Sync Order Relationships](slices/order/applications/syncrelationships): под `FOR SHARE` строки
заказа читает `customer_id`, `restaurant_id`, `delivery_id`, одной записью приводит связи в SpiceDB ровно к
ним (`TOUCH` нужных, `DELETE` лишних) и сохраняет ZedToken в `authorization_revision`. Синхронизация идёт по
состоянию, а не по событию, поэтому повтор, опоздание и redrive из DLQ сходятся к тем же связям; Inbox не
нужен. До первой синхронизации Customer читает и отменяет свой заказ по `customer_id` без SpiceDB. После
потери данных SpiceDB связи всех заказов восстанавливает `orders-admin authz resync`. Query handlers зарегистрированы в DI и доступны
через gRPC. `.feature` юзкейсов исполняются `godog` под `go test` против PostgreSQL (testcontainers).

## Запуск

```bash
make run
```

Temporal worker `cmd/orders-worker` и consumer `cmd/orders-consumer` локально не запускаются: worker
разворачивает Temporal Worker Controller ([ADR 5](docs/adr/0005-order-fulfillment-orchestration.md#versioning-workflow-code)),
поэтому оба процесса работают только в kind (`make kind-deploy` в корне репозитория).

Несекретная конфигурация процесса — только файл `CONFIG_FILE` ([`config.example.yaml`](config.example.yaml),
`make run` подставляет его): `apiVersion: config.nomnom.dev/v1alpha1`, `kind: Orders`, `metadata.name` — роль
процесса (`orders`, `orders-worker`, `orders-consumer`), `spec` — секции ниже. Процесс не стартует (non-zero exit до
открытия портов), если `CONFIG_FILE` не задан или файла нет, `apiVersion` или `kind` другие, `metadata.name` не его
роль или в файле есть неизвестный ключ на любом уровне (ошибка называет путь, например `spec.shutdown.drainTimout`).
Весь `spec` проверяется на неизвестные ключи, секции других ролей допустимы. `orders-admin` запускается в pod
`orders-consumer` и сверяет только `apiVersion` и `kind`. Переменные `SECTION__KEY` для несекретных настроек не
читаются. Ключи — camelCase, длительности — строки Go (`500ms`, `5s`, `2m`).

| Ключ `spec` | По умолчанию | Назначение |
| --- | --- | --- |
| `server.port`, `management.port` | `8081`, `9081` | Бизнес-порт gRPC и технический порт health/metrics; должны различаться |
| `shutdown.readinessDelay`, `shutdown.drainTimeout` | `5s`, `20s` | Бюджет graceful shutdown; `drainTimeout` положительный |
| `temporal.hostPort`, `temporal.namespace` | `localhost:7233`, `default` | Адрес Temporal Frontend и namespace окружения |
| `temporal.connectTimeout` | `10s` | Предел подключения и проверки Search Attribute в startup |
| `temporal.registerSearchAttributes` | `false` | Создать `OrderFulfillmentStage`, если его нет (нужны права оператора) |
| `fulfillment.*` | см. `config.example.yaml` | `Policy` workflow (placeholders) и `publishStage` |
| `catalog.minimumTotal`, `catalog.maximumTotal`, `catalog.timeout` | `"0"`, `"100000"`, `2s` | Лимиты суммы Place Order в валюте меню (placeholders) и deadline чтения `GetRestaurant` + `GetMenu` |
| `cancellation.window` | `5m` | Окно отмены от Place Order (placeholder) |
| `cancellation.scheduledCutoff` | — (обязателен) | Окно отмены запланированного заказа закрывается в `window_start − cutoff` (FR-462) |
| `scheduledOrder.preparationBuffer`, `scheduledOrder.replanLead` | — (обязательны) | Буфер `start_at` и опережение контрольной точки плана запланированного заказа (FR-677, FR-693); пустое или неположительное — ошибка старта всех процессов |
| `internalGateway.address` | — (обязателен) | Internal gateway для синхронных вызовов других контекстов |
| `kafka.brokers`, `kafka.clientId`, `kafka.consumerGroup`, `kafka.initialOffset` | `[localhost:9092]`, `orders`, `orders`, `oldest` | Kafka consumer'а; `clientId` — service.name процесса |
| `schemaRegistry.url`, `schemaRegistry.timeout` | —, `5s` | Schema Registry (FR-715); `url` обязателен `orders-consumer` |
| `identity.issuer`, `identity.audience`, `identity.jwksFile` | —, `nomnom.orders`, — | Проверка входящего projected ServiceAccount token ([ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md)); без них API не стартует |
| `identity.serviceAccounts` | — | Allowlist `namespace/serviceaccount=service-name,…` |
| `identity.tokenDir` | — | Каталог projected tokens `<audience>` для вызовов соседей (worker; API — catalog `restaurants`); пусто — без credentials |
| `identity.userIssuer`, `identity.userJwksFile`, `identity.userDelegates` | —, —, — | User assertion BFF ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)): ожидаемый `iss`, JWKS BFF (только EC P-256) и services-делегаты через запятую (в развёртываниях `bff`). Обязательны для gRPC API вместе с `identity.issuer`: пустая настройка или неразбираемый JWKS — ошибка startup |
| `spicedb.endpoint` | — | SpiceDB authorization ([`slices/order/infrastructure/spicedb`](slices/order/infrastructure/spicedb)): `order#view`/`cancel`, `customer#view_orders`, `TOUCH order#customer`/`restaurant` при оформлении, `order#delivery` в `orders-consumer`; обязателен для API и consumer: без endpoint они не стартуют |
| `spicedb.insecure`, `spicedb.timeout` | `false`, `2s` | Plaintext (только kind/локально) и deadline проверки; ошибка или timeout — отказ |
| `postgres.maxConns`, `postgres.minConns` | `10`, `0` | Пул pgx процесса ([Пулер PostgreSQL](#пулер-postgresql)); `minConns` — 0 или 1 |
| `postgres.maxConnLifetime`, `maxConnLifetimeJitter`, `maxConnIdleTime`, `connectTimeout`, `healthCheckPeriod` | `30m`, `5m`, `5m`, `5s`, `1m` | Лимиты пула; длительности положительные |
| `simulator.*` | — (обязательны `cmd/orders`) | Demo customer Simulator: `interval`, `minDelay`, `maxDelay`, `customerId`, `marketIds` |
| `telemetry.endpoint`, `telemetry.protocol` | —, `grpc` | OTLP коллектор ([Observability](#observability)); endpoint обязателен, если включён `tracing` или `metrics`; протокол `grpc` или `http/protobuf` |
| `telemetry.serviceName`, `telemetry.resourceAttributes` | роль, — | `service.name` и атрибуты resource окружения |
| `tracing.enabled`, `metrics.enabled`, `metrics.exportInterval` | `false`, `false`, `60s` | Экспорт spans и метрик |
| `logging.level` | `info` | Минимальный уровень JSON-логов: `debug`, `info`, `warn`, `error` |

Секреты и значения среды — только env, полей в `spec` для них нет (секрет в файле — неизвестный ключ):

| Переменная | Назначение |
| --- | --- |
| `CONFIG_FILE` | Путь к файлу конфигурации; обязателен |
| `POD_NAME` | `service.instance.id` процесса (Downward API); обязателен |
| `POSTGRES__DSN` | PostgreSQL orders, обязателен всем процессам; локально — `make postgres-dev`; схему процесс создаёт сам при старте |
| `SPICEDB__PRESHARED_KEY` | Ключ SpiceDB; обязателен, если задан `spicedb.endpoint` |
| `TEMPORAL_DEPLOYMENT_NAME`, `TEMPORAL_WORKER_BUILD_ID` | Worker Deployment Version worker'а; задаёт Temporal Worker Controller, без них worker не стартует |

Проверка caller разделена по ответственности: [`jwks.go`](infrastructure/grpc/jwks.go) читает ключи,
[`service_token.go`](infrastructure/grpc/service_token.go) (`serviceTokenVerifier`) проверяет service token,
[`user_assertion.go`](infrastructure/grpc/user_assertion.go) (`userAssertionVerifier`) — user assertion и
делегатов, resolver в [`identity.go`](infrastructure/grpc/identity.go) соединяет их. Порядок: service token
(`authorization`, ADR 27) → без metadata `x-nomnom-user-assertion` результат `Subject{service}` → service
входит в `identity.userDelegates` → assertion: только ES256 с `kid` из JWKS BFF, `iss`, `aud` содержит
`identity.audience`, `exp`/`nbf` с допуском 30 s, обязательный `iat` и `exp - iat ≤ 60 s`, непустые `sub` и
`jti` → `Subject{user, sub}`. Повторный или пустой assertion и любое нарушение — `UNAUTHENTICATED`; принятие и
отказ пишутся в log с делегатом и `jti`, без самих tokens.

Activities вызывают командные API `payments`, `restaurants`, `delivery` через `internalGateway.address` клиентами [`neighbours/grpcclient`](slices/fulfillment/infrastructure/neighbours/grpcclient). Подключение к gateway, projected token целевого контекста из `identity.tokenDir` (ADR 27) и сгенерированные клиенты соседей лежат в общем пакете [`infrastructure/gateway`](infrastructure/gateway) (Go-код генерируется `make proto-generate` из workspace-модулей до публикации в BSR): им пользуются и Activities fulfillment, и каталог Place Order, поэтому слайс `order` не зависит от слайса `fulfillment`; `UNAUTHENTICATED` и `PERMISSION_DENIED` клиент считает retryable.

## Проверка

Установка линтера на macOS:

```bash
brew install golangci-lint
```

```bash
make check
```

Place Order end to end без кластера — [`infrastructure/grpc/placeorder_e2e_test.go`](infrastructure/grpc/placeorder_e2e_test.go):
production resolver по общим векторам `tests/identity/fixtures` (service token BFF и user assertion) и
адаптер catalog против fake `restaurants` по gRPC; входит в `make test`.

Автоформатирование:

```bash
make format
```

`make test` (`go test ./...`) запускает все тесты без build tags и переменных окружения: PostgreSQL, Kafka, SpiceDB и Temporal dev server поднимаются testcontainers (нужен Docker).

Порты, которые тесты подменяют, — моки [mockery](https://github.com/vektra/mockery) (шаблон testify): список интерфейсов в [`.mockery.yml`](.mockery.yml), сгенерированные `mocks*_test.go` коммитятся и обновляются `make generate` (`go tool mockery`). Моки вручную не пишутся.

## Контейнер

Образ собирается из каталога `boundaries/orders`. Доступные targets: `build` и
`runtime`; итоговая стадия — `runtime`:

```bash
docker build --target runtime -t nomnom/orders:local boundaries/orders
```

Образ содержит статический Go-бинарник и TLS CA, запускается как distroless-пользователь
`nonroot`. Несекретная конфигурация — файл, подключённый read-only и указанный через `CONFIG_FILE`;
секреты и имя pod — env:

```bash
docker run --rm -p 8081:8081 -p 9081:9081 \
  -v "$PWD/boundaries/orders/config.example.yaml:/etc/nomnom/config.yaml:ro" \
  -e CONFIG_FILE=/etc/nomnom/config.yaml -e POD_NAME=orders-local \
  -e POSTGRES__DSN=postgres://orders:orders@host.docker.internal:5433/orders?sslmode=disable \
  -e SPICEDB__PRESHARED_KEY=devkey \
  nomnom/orders:local
```

gRPC-процесс подключается к PostgreSQL и Temporal; Kafka читает отдельный процесс
`orders-consumer`.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.
