# Use Cases

Этот документ сводит основные прикладные сценарии `orders`, `restaurants`, `delivery` и `payments`. Каждый сценарий принадлежит одному bounded context и является кандидатом на отдельный vertical slice или явно разделяемый application flow. Названия описывают бизнес-намерение, а не транспорт или CRUD-операцию.

`Temporal` показывает участие сценария в fulfillment workflow одного заказа. `Kafka` показывает естественную роль интеграционных событий; конкретные event names, topics и payloads здесь не проектируются. Публикация бизнес-фактов выполняется через Transactional Outbox, а изменение локального состояния по входящему событию — через Transactional Inbox. `no` означает, что Kafka не нужна для самого сценария, но не запрещает появление отдельно обоснованного бизнес-факта.

## Matrix

| Boundary | Use Case | Type | Actor | Temporal | Kafka |
| --- | --- | --- | --- | --- | --- |
| `orders` | Place Order | Command | Клиент | starts fulfillment workflow | Outbox: факт оформления при необходимости |
| `orders` | Get Order | Query | Клиент или внутренний авторизованный actor | no | no |
| `orders` | List Customer Orders | Query | Клиент или внутренний авторизованный actor | no | no |
| `orders` | Request Order Cancellation | Command | Клиент или другой авторизованный actor | signals existing workflow | no |
| `orders` | Confirm Order | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes restaurant acceptance |
| `orders` | Reject Order | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes restaurant rejection |
| `orders` | Record Preparation Started | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes preparation start |
| `orders` | Record Order Ready | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes restaurant readiness |
| `orders` | Record Courier Assignment | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes courier assignment |
| `orders` | Record Pickup | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes Pickup |
| `orders` | Record Delivery Completion | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes Delivery Completion |
| `orders` | Record Delivery Failure | Command | Kafka consumer `orders` | signals workflow | Inbox: consumes delivery failure |
| `orders` | Complete Order | Command | Temporal fulfillment workflow | finalizes successful workflow result | Outbox: publishes business fact |
| `orders` | Fail or Cancel Order | Command | Temporal fulfillment workflow | finalizes negative workflow result | Outbox: publishes business fact |
| `restaurants` | Register Restaurant | Command | Оператор платформы или владелец ресторана | no | no |
| `restaurants` | Update Restaurant | Command | Авторизованный управляющий ресторана | no | Outbox: бизнес-факт при необходимости |
| `restaurants` | Open Restaurant | Command | Авторизованный управляющий ресторана | no | Outbox: publishes business fact |
| `restaurants` | Close Restaurant | Command | Авторизованный управляющий ресторана | no | Outbox: publishes business fact |
| `restaurants` | Create Menu | Command | Авторизованный управляющий ресторана | no | no |
| `restaurants` | Add Menu Item | Command | Авторизованный управляющий ресторана | no | Outbox: бизнес-факт при необходимости |
| `restaurants` | Update Menu Item | Command | Авторизованный управляющий ресторана | no | Outbox: бизнес-факт при необходимости |
| `restaurants` | Set Menu Item Availability | Command | Авторизованный управляющий ресторана | no | Outbox: publishes business fact |
| `restaurants` | Get Restaurant | Query | Клиент или внутренний actor | no | no |
| `restaurants` | List Restaurants | Query | Клиент | no | no |
| `restaurants` | Get Menu | Query | Клиент или внутренний actor | no | no |
| `restaurants` | Receive Order Request | Command | Fulfillment process `orders` | invoked by Activity | no |
| `restaurants` | Accept Order | Command | Авторизованный сотрудник ресторана | workflow observes result asynchronously | Outbox: `RestaurantOrderAccepted` |
| `restaurants` | Reject Order | Command | Авторизованный сотрудник ресторана | workflow observes result asynchronously | Outbox: `RestaurantOrderRejected` |
| `restaurants` | Queue Restaurant Order | Command | Система `restaurants` | no | no: локальный факт |
| `restaurants` | Start Preparation | Command | Авторизованный сотрудник кухни | workflow observes status asynchronously | Outbox: `PreparationStarted` |
| `restaurants` | Start / Complete Preparation Item | Command | Авторизованный сотрудник станции | no | no: только при item-level model |
| `restaurants` | Mark Assembled | Command | Авторизованный сотрудник кухни | no | no: локальный факт |
| `restaurants` | Mark Ready for Pickup | Command | Авторизованный сотрудник кухни | workflow observes status asynchronously | Outbox: `RestaurantOrderReady` |
| `restaurants` | Report Preparation Delay | Command | Авторизованный сотрудник кухни | workflow should observe delay (сигнала пока нет) | Outbox: `PreparationDelayed` |
| `restaurants` | Report Preparation Failure | Command | Авторизованный сотрудник кухни | workflow should observe failure (сигнала пока нет) | Outbox: `PreparationFailed` |
| `restaurants` | Cancel Restaurant Order | Command | Fulfillment process `orders` | invoked by Activity; synchronous result by stage | Outbox: `RestaurantOrderCancelled` if state changes |
| `restaurants` | Reject After Acceptance | Command | Авторизованный сотрудник ресторана | workflow must handle rejection after Acceptance | Outbox: `RestaurantOrderRejected`; продуктовое решение не принято |
| `restaurants` | Get Kitchen Queue | Query | Авторизованный сотрудник ресторана | no | no |
| `restaurants` | Get Restaurant Order | Query | Авторизованный сотрудник ресторана или внутренний actor | no | no |
| `restaurants` | List Active Preparations | Query | Авторизованный сотрудник ресторана | no | no |
| `restaurants` | List Ready Orders | Query | Авторизованный сотрудник ресторана | no | Inbox: informational courier projection from `delivery` |
| `delivery` | Register Courier | Command | Оператор платформы | no | no |
| `delivery` | Set Courier Availability | Command | Курьер или авторизованный диспетчер | no | no |
| `delivery` | Update Courier Location | Command | Курьерское приложение | no | no |
| `delivery` | Create Delivery | Command | Fulfillment process `orders` | invoked by Activity | no |
| `delivery` | Find Courier | Command | Процесс назначения `delivery` | may be requested by workflow | no |
| `delivery` | Offer Delivery | Command | Процесс назначения `delivery` | no | no |
| `delivery` | Accept Delivery Offer | Command | Курьер | workflow observes assignment asynchronously | Outbox: publishes assignment result |
| `delivery` | Reject Delivery Offer | Command | Курьер | no | no |
| `delivery` | Reassign Courier | Command | Процесс назначения или авторизованный диспетчер | may be invoked by Activity | Outbox: publishes result if assignment changes |
| `delivery` | Confirm Pickup | Command | Назначенный курьер | workflow observes status asynchronously | Outbox: publishes Pickup status |
| `delivery` | Confirm Delivery | Command | Назначенный курьер | workflow observes status asynchronously | Outbox: publishes Delivery Completion |
| `delivery` | Report Delivery Failure | Command | Назначенный курьер или авторизованный диспетчер | workflow observes failure asynchronously | Outbox: publishes delivery failure |
| `delivery` | Get Courier Current Delivery | Query | Курьер | no | no |
| `delivery` | List Available Delivery Offers | Query | Курьер | no | no |
| `payments` | Create Payment | Command | Fulfillment process `orders` | invoked by Activity | no |
| `payments` | Authorize Payment | Command | Fulfillment process `orders` | invoked by Activity; workflow awaits result | Outbox: publishes operation result |
| `payments` | Capture Payment | Command | Fulfillment process `orders` | invoked by Activity; workflow awaits result | Outbox: publishes operation result |
| `payments` | Cancel Authorization | Command | Fulfillment process `orders` | invoked by compensation Activity | Outbox: publishes operation result |
| `payments` | Refund Payment | Command | Fulfillment process `orders` или авторизованный оператор | invoked by compensation Activity when applicable | Outbox: publishes operation result |
| `payments` | Apply Provider Callback | Command | Payment provider | workflow observes resulting fact asynchronously | Provider callback in; Outbox result out |
| `payments` | Apply Provider Operation Result | Command | Асинхронный provider adapter | workflow observes resulting fact asynchronously | Outbox: publishes operation result |
| `payments` | Get Payment Status | Query | Fulfillment process, клиент или авторизованный оператор | may be read by Activity for reconciliation | no |
| `payments` | List Payment Operations | Query | Авторизованный оператор или разрешённый пользовательский сценарий | no | no |

## API composition scenarios

BFF на WunderGraph Cosmo не является bounded context. Он предоставляет клиентские GraphQL-сценарии, делегирует commands и queries сервисам-владельцам и при необходимости собирает клиентское представление из нескольких ответов. BFF не читает базы контекстов, не участвует в Kafka и не управляет Temporal workflow.

| Scenario | Type | Actor | Composition result | Owner operations |
| --- | --- | --- | --- | --- |
| **Browse Restaurants** | Query | Клиент | Список доступных ресторанов | `restaurants`: List Restaurants |
| **View Restaurant Menu** | Query | Клиент | Представление ресторана и актуального меню | `restaurants`: Get Restaurant, Get Menu |
| **Place Order** | Mutation | Клиент | Запрос передан владельцу, возвращены `order_id` и начальное представление | `orders`: Place Order |
| **View Order** | Query | Клиент | Представление заказа с заранее определёнными optional enrichment-полями | `orders`: Get Order; при необходимости queries других владельцев |
| **List My Orders** | Query | Клиент | История заказов текущего клиента | `orders`: List Customer Orders |
| **Cancel Order** | Mutation | Клиент | Запрос отмены передан владельцу заказа | `orders`: Request Order Cancellation |
| **Courier View** | Query и Mutation | Курьер | Доступные offers, текущая доставка и делегированные действия курьера | `delivery`: offer/current-delivery queries, accept/reject, Pickup, Delivery Completion |

## Responsibility handoffs

Пересечений с двойным владением не обнаружено. Есть намеренные handoff-точки:

- `orders` владеет общим жизненным циклом и orchestration, но не решает, может ли ресторан приготовить заказ, какой курьер подходит или успешна ли финансовая операция;
- `restaurants` владеет `Restaurant Order`, Kitchen Queue и Preparation; его принятие не является `Order Confirmation`, успешной оплатой или назначением курьера; кухонные сценарии — в [Kitchen](kitchen.md);
- `delivery` владеет `Delivery Offer`, `Courier Assignment`, Pickup и Delivery Completion; завершение доставки ещё не подменяет финализацию `Order`;
- `payments` владеет Authorization, Capture и Refund; финансовая операция не определяет правила отмены `Order`;
- BFF владеет только API composition и клиентским представлением, а authoritative authorization и бизнес-инварианты остаются у владельца команды или данных.

## Open questions

Следующие продуктовые решения намеренно не приняты этим документом и должны быть уточнены до соответствующих SDD-спецификаций:

- когда именно выполняется Authorization;
- когда выполняется Capture;
- какие сроки authorization window и refund window проверяют `PaymentCapturable` и `PaymentRefundable` (текущее допущение реализации: policies в handlers не подключены, Capture и Refund выполняются на полную сумму, см. [design.md change `implement-payments-application-layer`](../openspec/changes/implement-payments-application-layer/design.md));
- можно ли принять `Restaurant Order`, если курьер ещё не назначен;
- на какой стадии начинается поиск курьера (текущее допущение реализации: Create Delivery, Reject и Reassign не вызывают Find Courier, последовательность Find → Offer → Accept запускает внешний процесс назначения, см. [design.md change `implement-delivery-application-layer`](../openspec/changes/implement-delivery-application-layer/design.md#допущения-по-открытым-вопросам));
- на какой стадии Request Order Cancellation больше не может быть удовлетворён;
- какая компенсация применяется после начала Preparation;
- что происходит при запросе отмены после Pickup;
- какие причины отклонения рестораном и неудачи доставки являются retryable (текущее допущение реализации `delivery`: причина Delivery Failure хранится без классификации, `Failed` терминален для этой `Delivery`, решение о повторе принимает `orders`, см. [design.md change `implement-delivery-application-layer`](../openspec/changes/implement-delivery-application-layer/design.md#допущения-по-открытым-вопросам));
- что делает Create Delivery при повторной доставке того же `Order` после Delivery Failure, если идемпотентность Create Delivery определяется по `OrderId` (например, номер попытки в ключе); см. [design.md change `implement-delivery-application-layer`](../openspec/changes/implement-delivery-application-layer/design.md#допущения-по-открытым-вопросам);
- нужен ли отдельный Order State для Failure (текущее допущение реализации: Failure использует State `cancelled`, вид итога и код причины сохраняются, см. [design.md change `implement-orders-application-layer`](../openspec/changes/archive/2026-10-25-implement-orders-application-layer/design.md#допущения-по-открытым-вопросам));
- нужна ли поддержка partial refund сверх полного Refund текущей минимальной модели;
- какие данные `Restaurant` и `Menu Item` сохраняются как snapshot в `Order`.
- может ли ресторан отказаться от `Restaurant Order` после Acceptance;
- как решается недоступность позиции после Acceptance;
- как упорядочивается Kitchen Queue и допускается ли ручная перестановка;
- как рассчитывается `expected_ready_at` и как `orders` реагирует на Preparation Delay.

Полный список открытых вопросов кухни — в [Kitchen](kitchen.md#открытые-вопросы).
