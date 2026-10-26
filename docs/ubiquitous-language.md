# Ubiquitous Language

Этот документ фиксирует обязательный Ubiquitous Language проекта nomnom. Он не создаёт единую доменную модель: каждый bounded context владеет своим значением термина, данными и инвариантами. Совпадающее написание в разных контекстах не означает общий тип или общую реализацию.

Определения собраны из SDD/OpenSpec, ADR, context README, glossary, GraphQL schema и `.feature`. Если согласованного определения нет, это указано прямо: запись резервирует термин, но не придумывает продуктовую семантику.

## Основные правила

- Внутри bounded context один термин имеет одно значение; синонимы не вводятся.
- Между контекстами выполняется явный перевод на application/infrastructure boundary: `orders.Order → Integration Event → restaurants.Restaurant Order`. Импорт доменной модели соседнего контекста запрещён.
- Published Language передаётся только через Protobuf-контракты. Общими между контекстами являются только контрактные identifiers (`OrderID` как типизированная строка), `Money` и `Event Envelope`; context-specific payload не становится общей моделью. Доменные типы ими не заменяются, Shared domain package по умолчанию не создаётся.
- Собственные BSR-модули контекстов самодостаточны и не импортируют друг друга. Общий Protobuf-модуль допустим только для подтверждённого совместного контракта и не становится общей доменной моделью.
- `Term` пишется по-английски; `Definition` — по-русски. Gherkin и SDD используют английский термин без перевода.
- `Where used` описывает найденное или нормативное применение, но не делает legacy-вариант каноническим. Такие варианты перечислены в [аудите](#аудит-существующих-имён).

## Грамматика именования

| Вид | Правило | Примеры |
| --- | --- | --- |
| Aggregate / Entity | Существительное | `Order`, `Delivery` |
| Command | Imperative `<Verb><Aggregate>` | `PlaceOrder`, `CancelOrder` |
| Query | `Get<X>` или `List<X>` | `GetOrder`, `ListCustomerOrders` |
| Domain / Integration Event | Past tense `<Aggregate><Fact>`; owner — publisher | `OrderPlaced`, `RestaurantOrderAccepted` |
| FSM State | Существительное/причастие в `UPPER_SNAKE_CASE` или PascalCase по языку | `PREPARING`, `Cancelled` |
| Compound State | Как FSM State; называет общее для группы states, а не перечисляет их | `IN_KITCHEN`, `CLOSED` |
| Trigger | Глагол | `Cancel`, `Accept` |
| Specification | Eligibility-форма | `PaymentRefundable`, `CourierEligibleForDelivery` |

`State`, `Stage` и `Status` не являются синонимами: `State` — business state aggregate; `Stage` — orchestration-состояние Workflow; `Status` — customer-facing presentation, которая может объединять несколько states/stages. `Phase` не используется.

## Global

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Customer | Пользователь, который оформляет `Order` и получает его. | `orders` для связи; identity — общий contract | Order, Subject | Courier, Restaurant | Orders model/README, authorization | Межконтекстно передаётся ID, а не модель. |
| Restaurant | Участник, который предлагает Menu и готовит принятые Restaurant Order. | `restaurants` | Menu, Restaurant Order | `orders.Order` | Restaurants glossary/README; future proto/GraphQL | Соседи используют контрактные данные/ID. |
| Courier | Исполнитель с именем, доступный для Delivery или занятый ею. | `delivery` | Delivery, Assignment | Customer, driver | Delivery glossary/domain | Authorization relation не создаёт общую модель. |
| Order | Зафиксированное намерение Customer получить выбранные Order Item одного Restaurant. | `orders` | Order Item, Fulfillment | Restaurant Order, Delivery, Payment | Orders glossary/domain/SDD | Неквалифицированный `Order` означает aggregate `orders`. |
| Restaurant Order | Локальное представление заказа для решения Restaurant и Preparation. | `restaurants` | Acceptance, Preparation | `orders.Order` | Канонический термин; future proto/events | Текущий `Kitchen Order` — legacy. |
| Delivery | Перемещение заказа от Restaurant к Customer назначенным Courier. | `delivery` | Assignment, Pickup, Delivery Completion | Order, shipment | Delivery glossary/domain | Связь с Order — через contract ID. |
| Payment | Денежная операция на сумму, связанная с одним Order. | `payments` | Authorization, Capture, Refund | Order, произвольная transaction | Payments glossary/domain | Не решает lifecycle Order. |
| Order ID | Стабильный типизированный строковый identifier Order в Published Language. | `orders` публикует | Order, Partition Key | Импорт Order model | Proto/events/code (`OrderId`/`OrderID`) | Spelling следует языку; семантика ID общая. |
| Money | Точная decimal-сумма и currency; арифметика только в одной валюте. | Owner операции; обмен — Published Language | Payment, Order Item | `float`, shared domain object | Orders/Payments domain; `google.type.Money` planned | На boundary переводится в local value object. |
| Market | Город, в котором работает доставка: его Restaurant, Courier, Order, Delivery и Payment обслуживаются вместе и не пересекают его границу. | `controlplane` (реестр); `market_id` — атрибут агрегатов контекстов | Cell, Market Placement | tenant, регион облака | [ADR 41](adr/0041-cell-based-architecture-with-control-plane.md), proto всех контекстов, заголовок `X-Nomnom-Market` | `market_id` — slug, задаётся при создании агрегата и не меняется. |
| Cell | Полный изолированный экземпляр data plane (контексты, их хранилища, Kafka, internal gateway), обслуживающий один или несколько Market. | `controlplane` | Market, Market Placement | Kubernetes namespace, bounded context | [ADR 41](adr/0041-cell-based-architecture-with-control-plane.md) | Контексты Cell не знают о других Cell и не вызывают control plane. |
| Market Placement | Размещение Market ровно в одной Cell; по нему BFF выбирает gateway Cell для запроса. | `controlplane` | Market, Cell, Placement Map | Партиция Kafka | [ADR 41](adr/0041-cell-based-architecture-with-control-plane.md), `GetPlacementMap` | Placement Map — все Market Placement с адресами gateway. |

## Orders

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Order | Aggregate общего lifecycle оформленного клиентом заказа. | `orders` | Order Item, Order State, Fulfillment Fact | Restaurant Order | Orders domain/glossary/SDD | Полное определение — Global. Domain Events: `OrderPlaced`, `OrderCancelled` (с причиной), `OrderCompleted`. |
| Order Item | Позиция Order с Menu Item, количеством и ценой на момент оформления. | `orders` | Order Snapshot, Menu Item | Текущий restaurants.Menu Item | Orders glossary/domain | Не импортирует restaurants model. |
| Order Snapshot | Зафиксированные при оформлении внешние сведения Restaurant/Menu, нужные Order после изменения источника. | `orders` | Order Item, Money | Live Menu, persistence snapshot | Orders README/open question | Точный состав не согласован SDD. |
| Order State | Business state aggregate, определяющее допустимые FSM transitions. | `orders` | State, Cancellation, Completion | Workflow Stage, Status | Orders FSM | Набор states меняется вместе с SDD/FSM. |
| Open Order | Compound State `open`: `placed` и `confirmed`. Fulfillment не завершён: Order можно отменить и записать в него Fulfillment Fact. | `orders` | Cancellation Request, Fulfillment Fact, Closed Order | Open Restaurant Order, Workflow Stage | [FSM](fsm.md#order), `fsm.go` (`CompoundOpen`) | Не сохраняется и не публикуется. Заменяет ручные проверки `placed`/`confirmed` (`AcceptsCancellation()`). |
| Closed Order | Compound State `closed`: Terminal States `cancelled` и `completed`. | `orders` | Terminal State, Cancellation, Completion, Open Order | Closed Restaurant Order | [FSM](fsm.md#order), `fsm.go` (`CompoundClosed`) | Не сохраняется и не публикуется. `IsFinal()` — принадлежность к `closed`. |
| Cancellation Request | Авторизованное намерение прекратить незавершённый Order, переданное workflow для решения и compensations. | `orders` | Cancellation, Signal | Выполненная Cancellation | `Request Order Cancellation`, SDD | Принятие запроса не гарантирует отмену. |
| Cancellation | Окончательное прекращение Order до Completion после domain decision и compensations. | `orders` | Cancellation Request, Compensation | Refund, `CancelWorkflow` | Orders glossary/FSM/SDD | Не означает удаление. |
| Completion | Успешный terminal outcome Order после подтверждённых результатов контекстов. | `orders` | Delivery Completion, Fulfillment | Delivery Completion, Capture | Orders glossary/FSM/README | Delivery Completion не финализирует Order автоматически. |
| Failure | Отрицательный terminal outcome Order с причиной, отличный от явной Cancellation. | `orders` | Cancellation, business failure | Technical retry/exception | `Fail or Cancel Order`, orchestration docs | Причины и отдельный State не согласованы. Текущее допущение реализации: Failure использует Order State `cancelled`, вид итога (`failure`) и код причины сохраняются в `Order`, см. [design.md change `implement-orders-application-layer`](../openspec/changes/archive/2026-10-25-implement-orders-application-layer/design.md). |
| Fulfillment Fact | Сообщённый соседним context результат шага Fulfillment (`restaurant_rejected`, `preparation_started`, `ready_for_pickup`, `courier_assigned`, `picked_up`, `delivery_completed`, `delivery_failed`), сохранённый в Order как данные. | `orders` | Order, Inbox, Signal | Order State, Workflow Stage, Integration Event | Orders domain (`FulfillmentFact`), [design.md change `implement-orders-application-layer`](../openspec/changes/archive/2026-10-25-implement-orders-application-layer/design.md) | Запись допустима в незавершённом Order; повтор — `FactAlreadyRecorded`. Не меняет Order State: ожидания и stages принадлежат Workflow. |
| Fulfillment | Многошаговый процесс от оформления Order до Completion, Cancellation или Failure с участием соседей. | `orders` | Order Fulfillment Workflow, Saga | Delivery, Preparation | Orders README/SDD | Последовательность и compensation policy не согласованы. |
| Order Fulfillment Workflow | Temporal Workflow одного логического Order, хранящий только orchestration Stage. | `orders` | Workflow, Fulfillment | Order aggregate, Order State | SDD; `OrderFulfillmentWorkflow` scaffold | Workflow ID `order:<order_id>`. |

## Restaurants

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Menu | Актуальное предложение Restaurant для выбора Customer. | `restaurants` | Menu Item, Availability | Order Snapshot | Restaurants README/use cases | Детальная модель не согласована. |
| Menu Item | Доступное блюдо с названием и текущей ценой в Menu. | `restaurants` | Modifier, Availability | `orders.Order Item` | Restaurants glossary/README | При оформлении переводится в Order Item/Snapshot. |
| Modifier | Согласованного определения нет. | `restaurants` | Menu Item | Order Item | Reserved for future SDD/proto/GraphQL | Смысл, структура, ограничения и цена должны быть определены SDD. |
| Availability | Доступность Restaurant/Menu Item для нового Restaurant Order с учётом времени, capacity и ограничений. | `restaurants` | RestaurantAcceptingOrders, MenuItemAvailable | Courier availability, UI visibility | Specifications docs/code | State checks не дублируются spec. |
| Restaurant Order | Локальный order, которым управляются Acceptance, Rejection и Preparation; единственный aggregate кухонного lifecycle в MVP. | `restaurants` | Acceptance, Kitchen Order, Preparation, Ready for Pickup | `orders.Order` | Canonical glossary term; [Kitchen](kitchen.md); future contracts | Заменяет `Kitchen Order` как имя aggregate; Commands и Events именуются от `RestaurantOrder`. |
| Kitchen Order | Роль Restaurant Order в процессе кухни после Acceptance: заказ, над которым работает кухня. | `restaurants` | Restaurant Order, Kitchen Queue | Отдельный aggregate/entity, `orders.Order` | [Kitchen](kitchen.md#один-aggregate-restaurantorder) | Не тип и не документ; legacy-использование как имени aggregate — в аудите. Отдельный aggregate — открытый вопрос. |
| Kitchen Queue | Бизнес-очередь принятых Restaurant Order, ожидающих начала Preparation. | `restaurants` | Queue Restaurant Order, priority | Kafka topic, Temporal task queue, job queue | [Kitchen](kitchen.md#kitchen-queue) | MVP — projection по `accepted_at`/`priority`; ручной порядок сделает её aggregate. |
| Kitchen Station | Участок кухни, где готовится часть заказа: grill, drinks, desserts, assembly. | `restaurants` | Preparation Item, Kitchen Capacity | Deployable/infrastructure station | [Kitchen](kitchen.md#preparation-item) | Доменное понятие; модель вводится позже. |
| Preparation Item | Позиция Restaurant Order в работе кухни: Menu Item принятого состава, количество и options. | `restaurants` | Required Preparation Item, Kitchen Station | Menu Item, `orders.Order Item` | [Kitchen](kitchen.md#preparation-item); FSM | Без цены; собственный lifecycle — только при item-level model. |
| Required Preparation Item | Preparation Item принятого состава, не исключённый согласованным решением; условие готовности заказа. | `restaurants` | Preparation Item, Assembly | Optional modifier | [Kitchen](kitchen.md#инварианты) | Набор правил исключения — открытый вопрос. |
| Assembly | Шаг после приготовления всех required items: сборка позиций в один заказ (позже — упаковка, проверка, sealing). | `restaurants` | Mark Assembled, Ready for Pickup | Pickup, Preparation Item completion | [Kitchen](kitchen.md#happy-path); FSM | В MVP может быть мгновенной, но остаётся явным шагом перед Ready. |
| Kitchen Capacity | Сколько Restaurant Order кухня готовит одновременно: число свободных Cook на линии Cook Pool ресторана. | `restaurants` | Cook Pool, Start Preparation, Kitchen Station | Courier capacity | [Kitchen](kitchen.md#kitchen-capacity), [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Acceptance не зависит от линии; влияние на Expected Ready At — второй этап. |
| Cook | Повар ресторана с именем: единица Kitchen Capacity, которой Start Preparation назначает Restaurant Order. Занят, пока его заказ в Active Preparation. | `restaurants` | Cook Pool, On Line, Start Preparation | Subject, relation `staff`, роль authorization | [Kitchen](kitchen.md#kitchen-capacity), [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Занятость выводится из `cook_id` заказов, флага в Cook нет. Команды кухни выполняет subject, а не Cook. |
| Cook Pool | Невыбывшие Cook ресторана. | `restaurants` | Cook, On Line, Pool Load, Retire | Courier Pool, Restaurant staff | [Kitchen](kitchen.md#kitchen-capacity), [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Управляет `restaurant#manage_cooks`. |
| On Line | Cook принимает работу: Start Preparation может его назначить. | `restaurants` | Cook, `Put Cook On Line`, `Take Cook Off Line` | Restaurant open, Courier On Line | [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Снять с линии можно только свободного Cook (`CookBusy`). |
| Pool Load | Загрузка Cook Pool: занятые Cook из стоящих на линии и Kitchen Queue как backlog. | `restaurants` | Cook Pool, Kitchen Queue, `Get Cook Pool` | Kitchen Capacity, метрика | [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Read model, не хранится. |
| Retire | Вывод Cook вне линии из Cook Pool; запись остаётся в истории заказов. | `restaurants` | Cook Pool, `Retire Cook` | Take Cook Off Line, удаление | [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Для Cook на линии — `CookOnLine`; выбывший Cook не возвращается. |
| Acceptance | Согласие открытого Restaurant приготовить все доступные позиции Restaurant Order. | `restaurants` | RestaurantOrderAccepted | Order Completion, Payment Authorization | Restaurants glossary/README/events | Event именуется past tense. |
| Rejection | Отказ Restaurant принять Restaurant Order с причиной, если определена. | `restaurants` | Acceptance | Order Failure/Cancellation | README/testing | Причины и retryability не согласованы. |
| Preparation | Период от начала приготовления Restaurant Order из Kitchen Queue до готовности. | `restaurants` | Preparation Item, Assembly, Ready for Pickup | Fulfillment, Delivery | Restaurants glossary/README/[Kitchen](kitchen.md) | Не Workflow Stage `orders`. |
| Preparation Delay | Сообщение кухни, что Restaurant Order будет готов позже; результат — изменённый Expected Ready At. | `restaurants` | Expected Ready At, PreparationDelayed | Delivery delay, technical retry | [Kitchen](kitchen.md#preparation-delay) | Причины домену не обязательны. |
| Expected Ready At | Ожидаемое кухней время готовности Restaurant Order. | `restaurants` | Preparation Delay, timeline | Courier ETA, Order ETA для Customer | [Kitchen](kitchen.md#timeline) | Способ расчёта не согласован. |
| Ready for Pickup | Terminal State `READY` Restaurant Order после Assembly: заказ можно передать Courier. | `restaurants` | Assembly, Pickup | Order Completion, Pickup | README/testing/[Kitchen](kitchen.md) | Event `RestaurantOrderReady`; короткая форма `Ready` допустима для State. |
| Open Restaurant Order | Compound State `OPEN`: `RECEIVED`, `ACCEPTED` и `IN_KITCHEN`. Работа кухни над Restaurant Order не завершена, заказ можно отменить. | `restaurants` | Cancel Restaurant Order, Closed Restaurant Order | `orders.Order` до Completion | [FSM](fsm.md#kitchen--preparation), `fsm.ts` | Не сохраняется и не публикуется. |
| In Kitchen | Compound State `IN_KITCHEN`: `QUEUED` и `ACTIVE_PREPARATION`. Restaurant Order занял место в Kitchen Queue и имеет Expected Ready At; допустима Preparation Delay. | `restaurants` | Kitchen Queue, Preparation Delay | Kitchen Order (роль), Kitchen Station | [FSM](fsm.md#kitchen--preparation), `fsm.ts` | `ACCEPTED` не входит: место в очереди ещё не занято. |
| Active Preparation | Compound State `ACTIVE_PREPARATION`: `PREPARING` и `ASSEMBLED`. Ресурсы кухни уже тратятся; возможна Preparation Failure, Cancel — по политике отмены во время Preparation. | `restaurants` | Preparation, Assembly, List Active Preparations | Preparation как период целиком | [FSM](fsm.md#kitchen--preparation), `fsm.ts`, kitchen board | Фильтр read model — `leavesOf(ACTIVE_PREPARATION)`. |
| Closed Restaurant Order | Compound State `CLOSED`: Terminal States `READY`, `REJECTED`, `CANCELLED`, `FAILED`. | `restaurants` | Terminal State, Ready for Pickup | Order Completion | [FSM](fsm.md#kitchen--preparation), `fsm.ts` | Заменяет ручной список terminal states. |

## Delivery

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Delivery | Aggregate назначения Courier и выполнения доставки. | `delivery` | Delivery Request, Assignment, Pickup | Order | Delivery domain/glossary | Полное определение — Global. |
| Courier | Aggregate исполнителя доставки с именем: доступность и участие в Assignment. | `delivery` | Courier Availability, Assignment, Courier Pool | Subject identity | Delivery domain/glossary | Полное определение — Global. Имя обязательно, задаётся в Register Courier. |
| Courier Pool | Невыбывшие Courier платформы. | `delivery` | Courier, On Line, Pool Load, Retire | Cook Pool | [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Управляет `platform#manage_couriers` (operator, dispatcher). |
| On Line | Courier принимает работу: Courier Availability `available` или `busy`. | `delivery` | Courier Availability, `Set Courier Availability` | Cook On Line | [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Вне линии — `unavailable`. |
| Pool Load | Загрузка Courier Pool: busy Courier из стоящих на линии и Delivery в `Waiting` как backlog. | `delivery` | Courier Pool, `Get Courier Pool` | Метрика | [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Read model, не хранится. |
| Retire | Вывод unavailable Courier из Courier Pool; Courier остаётся в истории Delivery и больше не получает Delivery Offer. | `delivery` | Courier Pool, `Retire Courier` | Set Courier Availability `unavailable`, удаление | [change `add-cook-and-courier-pools`](../openspec/changes/add-cook-and-courier-pools/design.md) | Для Courier на линии — `CourierOnLine`. |
| Courier Availability | Может ли Courier получать новые Assignment: `available`, `busy` (занят Delivery) или `unavailable` (вне линии). | `delivery` | Courier, Assignment, `Set Courier Availability` | restaurants Availability, позиция Courier | Delivery README/domain | Текущие допущения реализации: новый Courier available, busy Courier не может стать unavailable, см. [design.md change `implement-delivery-application-layer`](../openspec/changes/implement-delivery-application-layer/design.md). В коде — legacy `CourierStatus` (см. аудит). |
| Delivery Request | Запрос Fulfillment создать или подобрать Delivery для Order. | `delivery` получает; `orders` инициирует | Delivery, Delivery Offer | Адресный Delivery Offer | `Create Delivery`/`Find Courier` | Отдельная entity/contract ещё не согласована. |
| Delivery Offer | Ограниченное по времени предложение Courier принять Delivery; не закрепляет Courier. | `delivery` | Assignment, DeliveryOfferAcceptable | Assignment, Delivery Request | Delivery glossary/README/spec | Expired/rejected offer не создаёт Assignment. States: `pending`, `accepted`, `rejected`, `withdrawn` (закрыт при принятии другого offer той же Delivery), `expired` (не принят и не отклонён до `expires_at`). Pending offer после `expires_at` уже закрыт для ответа; фоновая задача Expire Delivery Offers сохраняет это как `expired` (внутренний Domain Event `DeliveryOfferExpired`, не публикуется). |
| Assignment | Закрепление доступного Courier за ожидающей Delivery. | `delivery` | Delivery Offer, Reassignment | Offer, booking | Сейчас `Courier Assignment` | Канонический короткий термин. Domain Events: `CourierAssignedToDelivery`, при Reassignment — `CourierUnassignedFromDelivery`. |
| Pickup | Получение назначенным Courier заказа у Restaurant. | `delivery` | Assignment, Delivery Completion | Acceptance | Delivery glossary/FSM/use cases | Event именуется past tense. |
| Delivery Completion | Успешный terminal outcome Delivery после передачи заказа Customer. | `delivery` | Pickup, Completion | Order Completion | Glossary/domain event `DeliveryCompleted` | Orders отражает факт и отдельно выполняет Completion. |
| Delivery Failure | Отрицательный terminal outcome Delivery (State `failed`) с причиной, когда назначенная доставка не может быть завершена. | `delivery` | Delivery Completion, Failure | Order Failure, technical error | `Report Delivery Failure`, domain event `DeliveryFailed` | Фиксируется для assigned/picked up Delivery. Причина — Delivery Failure Reason: код закрытого каталога (`customer_unreachable`, `address_not_found`, `no_access`, `customer_refused`, `order_damaged`, `courier_incident`, `other`) и необязательное пояснение свободным текстом (`detail`, не метка метрик). Неизвестный код — ошибка ввода, а не `other`. Retryability кодов не классифицируется, решение о повторе — у `orders`, см. [design.md change `implement-delivery-application-layer`](../openspec/changes/implement-delivery-application-layer/design.md). |
| Reassignment | Прекращение неактуального Assignment и возврат Delivery в поиск замены. | `delivery` | Assignment, Delivery Offer | Event redelivery | Delivery README/use cases | Алгоритм принадлежит delivery. |
| Active Courier Assignment | Compound State `ActiveAssignment` Delivery: `Assigned` и `PickedUp`. Courier закреплён за Delivery, действует по ней и видит её order (FR-444); возможна Delivery Failure. | `delivery` | Assignment, Pickup, Delivery Failure | Assignment как факт, сохранённый `courier_id` закрытой Delivery | [FSM](fsm.md#delivery), `fsm.rs`, storage adapters | Не сохраняется и не публикуется. Связь `delivery#courier` существует только в нём; фильтры хранилища — `State::leaves_of(ActiveAssignment)`. |
| Closed Delivery | Compound State `Closed` Delivery: Terminal States `Delivered`, `Failed`, `Cancelled`. | `delivery` | Terminal State, Delivery Completion, Delivery Failure | Order Completion | [FSM](fsm.md#delivery), `fsm.rs`, Cancel Delivery | Заменяет ручной список terminal states; Cancel Delivery из него — бизнес-отказ `AlreadyClosed`. |

## Payments

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Payment | Aggregate финансовой операции для Order. | `payments` | Payment Operation, Authorization | Order | Payments glossary/domain | Полное определение — Global. Domain Events: `PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded`, `PaymentFailed`. |
| Authorization | Подтверждение Provider возможности последующего Capture суммы. | `payments` | Capture, Cancel Authorization | Security Authorization | Payments glossary/FSM/README | Контекст обязателен. |
| Capture | Списание ранее авторизованной суммы. | `payments` | Authorization, Refund | Authorization | Payments glossary/FSM/README | Минимальная модель — полный Capture. |
| Refund | Возврат суммы, ранее списанной Capture. | `payments` | Capture, PaymentRefundable | Cancellation, Cancel Authorization | Payments glossary/FSM/README | Сейчас только полный Refund. |
| Cancel Authorization | Освобождение авторизованной, но не списанной суммы. | `payments` | Authorization, Capture | Refund, Order Cancellation | `Cancel Authorization`, FSM, domain event `PaymentAuthorizationCancelled` | Public имя — `Cancel Authorization` (`CancelAuthorization`); `Void` используется только в маппинге на Provider, см. [design.md change `implement-payments-application-layer`](../openspec/changes/implement-payments-application-layer/design.md). |
| Payment Operation | Одна логическая попытка Authorization, Capture, Cancel Authorization или Refund с Idempotency Key и стабильным результатом. | `payments` | Idempotency Key, Provider Reference | Payment целиком | README/glossary/`List Payment Operations`/domain `PaymentOperation` | Multiple attempts не реализованы. States `pending → succeeded \| declined`; отказ — domain event `PaymentOperationDeclined`, см. [design.md change `implement-payments-application-layer`](../openspec/changes/implement-payments-application-layer/design.md). |
| Provider | Внешняя платёжная система, исполняющая Payment Operation. | `payments` | Provider Reference | payments context | README/adapters | Вызов вне local UoW. |
| Provider Reference | ID успешной операции на стороне Provider. | `payments` | Provider, Payment Operation | Payment ID, Order ID | Payments glossary/domain | Не раскрывается без необходимости. |
| Idempotency Key | Стабильный ключ логической Payment Operation, независимый от retry attempt/run ID. | `payments` принимает; caller формирует | Payment Operation | Event ID | Orchestration SDD/README | Для workflow: Order ID + logical step. |
| Uncaptured Payment | Compound State `uncaptured`: `pending` и `authorized`. Деньги ещё не списаны; Payment может завершиться Failure (`fail`). | `payments` | Authorization, Cancel Authorization, Capture | `pending`, Payment Operation `pending` | [FSM](fsm.md#payment), `fsm.py` | Не сохраняется и не публикуется: Payment всегда в одном Leaf State. |
| Closed Payment | Compound State `closed`: Terminal States `refunded` и `failed`. | `payments` | Terminal State, Refund | `captured`, Order Completion | [FSM](fsm.md#payment), `fsm.py` | `captured` не входит: после Capture возможен Refund. |

## Orchestration (Temporal)

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Workflow | Durable orchestration долгоживущего процесса с ожиданиями, retries, timers или compensations. | `orders` для fulfillment | Activity, Stage, Saga | Aggregate/local transaction | Temporal ADR/SDD/code | Deterministic; не владеет соседними доменами. |
| Activity | Повторяемая операция Workflow с внешним side effect или вызовом API/application boundary. | Workflow adapter | Workflow, Idempotency Key | Domain method | Temporal SDD/code | Нужна business idempotency. |
| Signal | Асинхронное сообщение существующему Workflow без синхронного business result. | `orders` orchestration | Update, Event ID | Integration Event, Command | Temporal SDD | Event сначала проходит Inbox. |
| Update | Взаимодействие, когда caller нужен подтверждённый результат принятия/отклонения. | `orders` orchestration | Signal, Command | Query | Temporal SDD | Выбор зависит от семантики. |
| Timer | Durable ожидание Workflow до business deadline. | `orders` orchestration | Stage, timeout | Sleep в Activity | Temporal SDD/testing | Product timeouts открыты. |
| Compensation | Идемпотентное business action, нейтрализующее подтверждённый шаг Saga. | Owner операции; coordination — `orders` | Saga, Activity | DB rollback, workflow cancellation | Temporal SDD/design | Policy определяет обратный порядок. |
| Saga | Orchestration-based последовательность local operations/compensations без distributed transaction. | `orders` orchestration | Workflow, Compensation | UoW/DB transaction | Temporal SDD | Инварианты остаются у owners. |
| Workflow Stage | Сериализуемый orchestration progress ожиданий, шагов и compensations. | `orders` orchestration | Workflow, Timer | Order State, UI Status | Temporal design/code `Stage` | Не source of truth domain State. |

## Event-Driven Architecture

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Domain Event | Внутренний бизнес-факт domain model одного context в domain types; без side effects и transport. Aggregate накапливает его в памяти и отдаёт через `PullEvents()`; порядок — по записи и `aggregate_version`. | Aggregate owner | Integration Event, Outbox Record | Kafka message, Protobuf, DTO | Domain code/[Domain Events](domain-events.md)/ADR 21, 25 | Не публикуется напрямую и не сохраняется в Outbox; не каждый становится Integration Event. |
| Integration Event | Публичное Protobuf-сообщение факта для других contexts (`nomnom.orders.v1.OrderCancelled`); Transport DTO исходящего события. State-carrying по умолчанию. | Publisher context — контракт (Published Language); infrastructure — маппинг | Domain Event, Event Envelope, Outbox Record | Domain Event, serialized aggregate, Outbox Record | BSR contracts/Kafka/[Domain Events](domain-events.md)/ADR 25 | Application решает, публиковать ли факт; mapper строит событие, маппинг не обязан быть 1:1. Версионируется additive-first; breaking — новая major-версия и dual publish. |
| Event ID | Стабильный UUID логического Integration Event и deduplication key. | Publisher | Event Envelope, Inbox | Operation Idempotency Key | Kafka header/envelope/Inbox/Outbox | По умолчанию UUIDv5 от `(aggregate_type, aggregate_id, aggregate_version, event_type)`, иной способ — явно в SDD; не меняется при retry; от повтора операции дополнительно защищает её идемпотентность (ADR 13, 25). |
| Event Envelope | Published Language с Event ID, type, aggregate ref/version, time и correlation/causation metadata. | Общий integration contract; заполняет infrastructure publisher | Integration Event, Outbox Record | Domain Event | Future proto; EDA metadata/ADR 25 | Domain и application его не видят. Proto schema и размещение headers/payload ещё не выбраны. |
| Outbox | Durable хранилище Outbox Records, атомарное с business state и публикуемое после commit. | Publisher context | Outbox Record, Integration Event, at-least-once | Kafka publish in transaction | EDA/ADR 13 | У каждого context свой Outbox. |
| Outbox Record | Запись Outbox: сериализованный Integration Event с Event Envelope и техническим состоянием публикации. | Publisher context | Outbox, Event Envelope | Domain Event, aggregate snapshot | EDA/ADR 13, 25 | Transport-ready; publisher не загружает aggregate и не выполняет domain mapping. |
| Inbox | Consumer record об Event ID, атомарная с local business state. | Consumer context | Idempotent Consumer | Shared cross-service table | EDA/ADR 11 | Unique `(consumer,event_id)`. |
| At-least-once Delivery | Event не теряется, но может публиковаться/доставляться повторно. | Integration runtime | Outbox, Inbox | Exactly-once delivery | EDA docs | Дубликаты нормальны. |
| Effectively-once Business Processing | Один logical event меняет local state consumer не более одного раза. | Consumer context | Inbox, Idempotent Consumer | Global exactly-once | EDA docs | Ограничено local transaction/retention. |
| Idempotent Consumer | Логический handler, дедуплицирующий Event ID и превращающий повтор в no-op. | Consumer context | Inbox | Pod/process instance | EDA docs | Rename требует migration. |
| Partition Key | Domain identifier, сохраняющий Kafka order внутри partition. | Publisher | Order ID, aggregate version | Event ID | Kafka key/EDA | Для Order events — Order ID. |

## Authorization

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Subject | Verified identity `user:<id>` или `service:<id>`, запрашивающая действие. | Identity contract | Permission, Resource | Непроверенный actor text | Security/SpiceDB | Всемогущего `system` нет. |
| Resource | Объект типа и ID, над которым проверяется Permission. | Data context | Subject, Relation | Aggregate implementation | Security/SpiceDB | Owner делает authoritative check. |
| Relation | Именованная связь graph, выраженная существительным. | Resource definition owner | Permission, Resource | Автоматически domain association | Security/future `.zed` | `owner`, `staff`, `customer`, `courier`. |
| Permission | Глагол из SDD: может ли Subject действовать над Resource. | Resource definition owner | Relation, Authorization | Domain rule | Security/future `.zed` | `view`, `cancel`, `accept`, `assign`. |
| Authentication | Проверка credentials/token и установление identity. | Identity/BFF | Subject | Authorization | README/security | SpiceDB не выполняет Authentication. |
| Authorization | Решение о Permission Subject над Resource. | Resource owner; engine SpiceDB | Subject, ZedToken | Authentication, Payment Authorization, domain rule | Security/ADR 18 | До domain operation. |
| ZedToken | SpiceDB revision token для consistency/read-your-writes. | Authorization infrastructure | Authorization | Access token | Security/metadata | Не входит в domain/spec. |

## FSM

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| State | Business state aggregate в полной transition table. | Aggregate owner | Trigger, Transition | Workflow Stage, UI Status | FSM docs/domain | Persistence хранит State. |
| Trigger | Глагольное action, запрашивающее Transition. | Aggregate owner | State, Guard | Command целиком | FSM docs/domain | Application вызывает domain method. |
| Transition | Чистое `State × Trigger × inputs → next State или typed error`. | Aggregate owner | State, Trigger, Guard | Workflow step | FSM/ADR 21 | Table — source of truth. |
| Guard | Чистая проверка входа Transition без I/O/hidden time. | Aggregate owner | Transition, Specification | Authorization/repository lookup | FSM/domain | Reusable policy может стать Specification. |
| Terminal State | State без дальнейших изменяющих business transitions. | Aggregate owner | Completion, Cancellation | Process shutdown | FSM/README | Повтор может дать `AlreadyInState`. |
| Leaf State | State, в котором aggregate находится; aggregate всегда ровно в одном Leaf State. | Aggregate owner | Compound State | Compound State | FSM/ADR 21 | Только Leaf State сохраняется, публикуется и попадает в метрики. |
| Compound State | Именованная группа Leaf или Compound States; Transition, объявленная на ней, действует во всех вложенных states, ближайшая объявленная побеждает. | Aggregate owner | Leaf State, Transition | Workflow Stage, UI Status, ручной список states | [FSM](fsm.md#statecharts-иерархия-состояний)/ADR 21 | Не сохраняется и не передаётся наружу; имя — термин контекста. |
| Targetless Transition | Transition с target `stay`: aggregate остаётся в текущем Leaf State и меняет только свои данные. | Aggregate owner | Transition | `AlreadyInState` | FSM/ADR 21 | `ReportDelay`, `RecordFact`. |

## Application Layer

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Command | Intent изменить State через use case; без business logic. | Use-case package | Handler, Transport DTO | Go `cmd/`, event | ADR 6/use cases | Суффикс `Command` необязателен. |
| Query | Чтение без изменения State. | Use-case package | Handler, Application DTO | Command/GraphQL transport op | ADR 6/use cases | `Get<X>`/`List<X>`. |
| Handler | Координирует authorization, load, domain behavior, persistence и result одного use case. | Use-case package | Command, UoW | Domain/transport handler | ADR 6/UoW | Не владеет invariants. |
| Transport DTO | Форма adapter: Protobuf, GraphQL, Kafka, webhook, Activity. | Adapter owner | Command, Application DTO | Domain Model | ADR 6/infrastructure | Mapping на infrastructure boundary. |
| Application DTO | Use-case input/output, когда Domain Model не подходит; Query read model тоже DTO. | Use-case package | Query, Handler | Transport DTO/domain object | ADR 6/[application models](application-models.md) | Без domain behavior. Для исходящего события — только при enrichment сверх Domain Event. |
| Domain Model | Aggregates/entities/value objects/services/events/errors одного context. | Bounded context | State, Domain Event | DTO/model соседа | ADR 6/context code | Не зависит от transports/frameworks. |
| Unit of Work | Local transaction boundary одного use case и storage boundary. | Shared application layer | Handler, Inbox, Outbox | Distributed transaction/Saga | UoW/ADR 20 | Не включает Kafka/Temporal/SpiceDB/provider. |
| Domain Specification | Чистая eligibility-policy с business meaning и typed reasons. | Domain owner | Guard | Repository Query Specification, Authorization | Specification docs/code | Не для простого State check. |
| Repository Query Specification | Техническое описание persistence query/predicate. | Infrastructure/read port | Query | Domain Specification | Specification docs | Не маскируется под policy. |
| Composition Root | Bootstrap process, собирающий dependencies и lifecycle. | Deployable process | Deployable Role | Service locator в domain | Runtime/DI docs | У каждой role свой root. |

## Integration & Runtime

| Term | Definition | Owner (bounded context) | Related terms | Not the same as | Where used | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Bounded Context | Граница языка, Domain Model, данных и responsibility. | Соответствующая команда | Vertical Slice, Published Language | Обязательно deployable process | GLOSSARY-MAP/boundaries | Orders/Restaurants/Delivery/Payments. |
| Vertical Slice | Cohesive business capability module вокруг behavior, не layers. | Bounded context | Command, Handler | Каждый use case автоматически | ADR 3/6 | Новый slice — для самостоятельной capability. |
| BFF | Client application boundary с GraphQL, API coordination и presentation. | `bff` | Public API, Status | Bounded Context/domain owner | BFF README/schema | Не управляет Workflow. |
| Public API | Единственная внешняя business API клиентов — GraphQL BFF. | `bff` | BFF, Transport DTO | Internal gRPC | BFF schema/README | Сейчас только `bffInfo`. |
| Internal Gateway | Внутрикластерная маршрутизация синхронных business API calls. | Platform/runtime | Infrastructure Connection | BFF/service mesh/direct address | README/ADR 15/ops | Не принимает business decisions. |
| Infrastructure Connection | Прямое owner-подключение к Kafka, Temporal, DB, SpiceDB или telemetry. | Deployable process | Internal Gateway | Sync business API call | README/ops | Не идёт через gateway по умолчанию. |
| Deployable Role | Запускаемая role: API, consumer, Outbox relay или Temporal worker со своим lifecycle/root. | Application boundary | Composition Root | Bounded Context | Runtime/boundaries/Helm ADR | Один context может иметь несколько roles. |

## Обязательность и поддержание

Язык обязателен в SDD/OpenSpec, ADR, `.feature`, именах Commands/Queries/Events, domain types/FSM, Protobuf, GraphQL и domain-oriented logs/metrics/traces/dashboards. Новый или изменённый термин в Protobuf, GraphQL или SDD обновляет словарь в том же PR. Изменение термина обновляет затронутую документацию и проходит совместимую миграцию contracts, persisted data, event retention, Inbox consumer names и observability queries. Синоним нельзя вводить как временное новое имя: до migration он документируется как legacy mapping.

Context glossary остаются локальными индексами. При конфликте нормативным является этот документ вместе с ADR 24; glossary приводится к нему отдельным изменением затронутого термина.

## Аудит существующих имён

Кандидаты на переименование; в этом шаге код, существующие ADR, SDD и `.feature` не исправлялись.

| Файл / контракт | Текущее имя | Канонический термин / правило |
| --- | --- | --- |
| `docs/testing.md`, orchestration SDD, slice READMEs, gRPC reasons `KITCHEN_ORDER_*` в Restaurants README | `Kitchen Order` / `KitchenOrder` как имя aggregate | `Restaurant Order` / `RestaurantOrder`; `Kitchen Order` допустим только как роль |
| Orders glossary/README, testing/SDD | `Order Confirmation`, `Confirm Order` для решения ресторана | `Acceptance` принадлежит restaurants; event `RestaurantOrderAccepted`; orders Command должен явно отражать факт |
| Orders glossary | `Order Cancellation`, `Order Completion` рядом с короткими формами | Внутри `orders`: `Cancellation`, `Completion`; qualification только при неоднозначности |
| Delivery glossary/README/use cases/SDD | `Courier Assignment` | `Assignment`; event — конкретный past-tense fact; в коде `delivery` — `CourierAssignedToDelivery` и `CourierUnassignedFromDelivery` |
| Context README и `docs/use-cases.md` | Type names с пробелами (`Place Order`, `Get Order`, …) | Contract/code names `PlaceOrder`, `GetOrder`, `AcceptRestaurantOrder`, …; prose может быть естественным |
| Use cases/Orders README | `Fail or Cancel Order` | Не объединять два outcomes в одном Command; после SDD — `FailOrder`/`CancelOrder` или явно именованный result |
| Orders `.feature` | `restaurant confirmation`, `restaurant acceptance`, `Confirm order` | Один термин `Acceptance` и явно названный owner context |
| Orders/Restaurants/Delivery/Payments README; delivery code | Aggregate field `status`, `CourierStatus` | Domain value — `State`; `Status` только customer-facing |
| UI customer-journey preview/OpenSpec | `DeliveryStage`, `presentation stage` | Customer-facing `Delivery Status`; `Stage` только Workflow |
| Temporal scaffold (архивный change `add-use-case-slice-stubs`) | `WorkflowState` при поле `Stage` | `Workflow Stage`; technical container не размывает State/Stage |
| Payments glossary/README/code | `Cancel Authorization` без связи с `Void` | Public имя — `CancelAuthorization`; `Void` — только provider mapping (change `implement-payments-application-layer`) |
| Cross-language code | `OrderId` / `OrderID` | Published term `Order ID`; spelling следует language convention |

`Phase` в domain/orchestration sources не найден; vendor/browser artifacts к Ubiquitous Language не относятся.

## Термины без согласованного определения

| Term | Что отсутствует |
| --- | --- |
| Order Snapshot | Поля Restaurant/Menu, момент фиксации, правила обновления/неизменности. |
| Failure | Причины, отдельный Order State, граница с Cancellation и technical failure. Реализация временно использует State `cancelled` с видом итога. |
| Fulfillment | Точная последовательность, payment timing, параллелизм Assignment, compensation policy. |
| Delivery Request vs Delivery Offer | Является ли Request самостоятельным domain concept/contract; Offer уже определён как адресное временное предложение Courier. |
| Modifier | Структура, обязательность, price impact, ограничения выбора. |
| Rejection | Полный набор причин и retryability; допустимость после Acceptance. |
| Kitchen Capacity | Модель параллельности, влияние на Acceptance и Expected Ready At. |
| Kitchen Station | Модель станций и маршрутизации Preparation Item. |
| Expected Ready At | Способ расчёта начального значения. |
| Event Envelope | Логический состав и ответственность за заполнение согласованы (ADR 13, 25); Protobuf schema и размещение headers/payload отсутствуют. |
| Payment Operation | Модель нескольких attempts. Lifecycle одной попытки (`pending → succeeded \| declined`) реализован в `payments`. |

## Покрытие источников и ограничения

- Проверены context glossary/README, все root/local ADR, FSM, EDA, Specification, Testing, UoW, application-model rules ADR 6, Temporal ADR/SDD, security/runtime docs, GraphQL SDL и `.feature`.
- Public GraphQL schema содержит только технический `BffInfo`; domain vocabulary ещё отсутствует. Generated plugin `service.proto` описывает `QueryBffInfo` и не является Published Language bounded contexts.
- README/ADR объявляют BSR-модули `buf.build/nomnom/{orders,restaurants,delivery,payments}`, но `buf export` для каждого вернул `resource ... was not found`; context README также сообщают, что реальные `.proto` и Buf configs ещё не реализованы. Поэтому proto vocabulary нельзя подтвердить, и отсутствующие contract names не придуманы.
