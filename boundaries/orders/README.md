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

Статус реализации: application handlers всех перечисленных Commands и Queries реализованы поверх in-memory адаптеров портов (см. [Статус реализации](#статус-реализации)); transport, persistence, Kafka и Temporal adapters создаются отдельными SDD changes; пустые программные scaffolds не используются.

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Place Order** | Клиент | Оформить заказ из выбранных позиций одного ресторана | Создан `Order`, зафиксированы состав, цены и необходимые snapshot-данные, назначен `order_id`, запущен fulfillment process; заказ ещё не считается принятым рестораном или оплаченным | Читает необходимые сведения ресторана и меню через согласованный API; запускает Temporal fulfillment workflow |
| **Request Order Cancellation** | Клиент или другой авторизованный actor | Запросить прекращение незавершённого заказа | Проверены права и правила допустимости, запрос передан существующему fulfillment workflow; компенсации не выполняются самим use case | Authoritative authorization в `orders`; сигнал Temporal workflow |
| **Confirm Order** | Kafka consumer `orders` | Отразить Order Acceptance ресторана | `Order` подтверждён один раз, fulfillment workflow может продолжиться | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Reject Order** | Kafka consumer `orders` | Отразить отказ ресторана принять `Kitchen Order` | Отказ сохранён один раз, fulfillment workflow получил бизнес-результат | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Record Preparation Started** | Kafka consumer `orders` | Отразить начало Preparation | Стадия приготовления отражена в `Order` один раз | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Record Order Ready** | Kafka consumer `orders` | Отразить готовность `Kitchen Order` к Pickup | Готовность отражена в `Order` один раз | Integration event от `restaurants`, Transactional Inbox, сигнал Temporal workflow |
| **Record Courier Assignment** | Kafka consumer `orders` | Отразить Courier Assignment | Назначение отражено в `Order` один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Record Pickup** | Kafka consumer `orders` | Отразить Pickup | Получение заказа курьером отражено один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Record Delivery Completion** | Kafka consumer `orders` | Отразить Delivery Completion | Завершение доставки отражено один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Record Delivery Failure** | Kafka consumer `orders` | Отразить неудачу Delivery | Бизнес-неудача доставки и причина отражены один раз | Integration event от `delivery`, Transactional Inbox, сигнал Temporal workflow |
| **Complete Order** | Temporal fulfillment workflow | Зафиксировать успешный итог после выполнения всех необходимых условий | `Order` переведён в окончательное успешное состояние | Вызывается orchestration после подтверждённых результатов участвующих контекстов |
| **Fail or Cancel Order** | Temporal fulfillment workflow | Зафиксировать окончательный негативный итог после orchestration и необходимых компенсаций | `Order` переведён в соответствующее окончательное состояние с сохранением причины | Workflow координирует компенсации через Activities; use case не присваивает правила `restaurants`, `delivery` или `payments` |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Order** | Клиент или внутренний авторизованный actor | Получить текущее представление одного заказа | Возвращено доступное actor представление заказа без изменения состояния | Read model `orders`; проверка права просмотра |
| **List Customer Orders** | Клиент или внутренний авторизованный actor | Получить историю заказов клиента | Возвращён доступный actor список заказов клиента без изменения состояния | Read model `orders`; проверка принадлежности и права просмотра |

Основной Temporal Workflow — fulfillment одного заказа. `Place Order` запускает его; use cases `Confirm Order`, `Reject Order`, `Record …` и `Request Order Cancellation` передают ему конкретные бизнес-результаты или запросы; `Complete Order` и `Fail or Cancel Order` фиксируют выбранный workflow итог. Отдельный Workflow на каждый use case не создаётся. Результаты Authorization, Capture и Refund принадлежат `payments` и поступают непосредственно в состояние fulfillment workflow через Temporal adapter; отдельными состояниями или use cases агрегата `Order` они не являются. Вызовы платёжных операций, создание и отмена `Restaurant Order`, создание доставки и повторный поиск курьера выполняются workflow через Activities и публичные API контекстов-владельцев.

Kafka естественно входит только во входные use cases, отражающие конкретный бизнес-факт: событие проходит Transactional Inbox до изменения `Order` и сигнала workflow. Исходящие бизнес-факты `orders` публикуются через существующую Outbox-модель; конкретные имена и payload событий здесь не определяются.

## Структура слайсов

Слайсы `orders` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста). Модули находятся в `slices/<slice>/`; внутри `applications/` каждый use case имеет отдельный Go package, например `slices/order/applications/confirmorder`. Только shared packages находятся в `pkg/`; код отдельного слайса или entrypoint туда не помещается. Единственная каноническая модель `Order` и её инварианты используются всеми use cases модуля `order`.

Temporal workflow относится к прикладному процессу по ответственности, но вместе с Activities и транспортными обработчиками является инфраструктурным адаптером. При реализации fulfillment workflow и типы Activity payloads размещаются в `slices/order/infrastructure/temporal/fulfillorder`. Workflow не знает о Commands, Queries, handlers и `Order`; Activity преобразует payload в Command или Query и вызывает handler слайса, а use case обращается к workflow (start, Signal, Update) через порт в `applications/`. Ни Temporal SDK, ни Watermill, ни Google Wire не импортируются доменной моделью и handlers. Правила моделей и entrypoints — в [общесистемном ADR 6](../../docs/adr/0006-ddd-structure-inside-vertical-slices.md#типы-моделей).

## gRPC-адаптер

`orders` владеет API `nomnom.orders.v1` и публикует его как `buf.build/nomnom/orders`. Исходные messages находятся в `infrastructure/grpc/nomnom/orders/v1/orders.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер генерирует серверные Go stubs из локальной схемы и преобразует request messages в commands/queries, а прикладные результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделями `Order`, handlers или Temporal workflow. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API:

| Ошибка `orders` | gRPC code | `ErrorInfo.reason` |
| --- | --- | --- |
| `Order` не найден | `NOT_FOUND` | `ORDER_NOT_FOUND` |
| Некорректный состав или Delivery Address | `INVALID_ARGUMENT` | `INVALID_ORDER` |
| Текущее состояние запрещает переход | `FAILED_PRECONDITION` | `ORDER_STATE_CONFLICT` |
| `Order` с таким идентификатором уже существует | `ALREADY_EXISTS` | `ORDER_ALREADY_EXISTS` |
| Неожиданная внутренняя ошибка | `INTERNAL` | `INTERNAL_ERROR` |
| Внешняя зависимость недоступна | `UNAVAILABLE` | `DEPENDENCY_UNAVAILABLE` |

В `google.rpc.ErrorInfo.domain` передаётся `nomnom.orders.v1`. Реализованы Buf-модуль,
сгенерированные Go stubs и RPC `GetOrder` / `ListCustomerOrders`. Оба RPC вызывают
application handlers и используют структурированные `ErrorInfo`. Composition root по
умолчанию устанавливает deny-by-default `SubjectResolver`: пока не подключён adapter
проверки internal identity, вызов завершается `UNAUTHENTICATED`, а произвольным metadata
gateway не доверяет. Политика контрактов, генерации и совместимости описана в
[ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md).

## Минимальная доменная модель

Для учебной реализации достаточно одного агрегата `Order`.

| Тип | Минимальные поля | Назначение |
| --- | --- | --- |
| `Order` — aggregate root | `id`, `customerId`, `restaurantId`, `items`, `deliveryAddress`, `total`, `status` | Владеет составом, стоимостью и общим состоянием заказа |
| `OrderItem` — entity внутри `Order` | `menuItemId`, `quantity`, `unitPrice` | Фиксирует заказанную позицию, количество и цену на момент оформления |

Минимальные состояния `Order`: `placed`, `confirmed`, `cancelled`, `completed`. На первом этапе агрегат поддерживает оформление, подтверждение, отмену и завершение заказа.

Кроме state, `Order` хранит время оформления, отражённые факты fulfillment (`restaurant_rejected`, `preparation_started`, `ready_for_pickup`, `courier_assigned`, `picked_up`, `delivery_completed`, `delivery_failed`) и причину отрицательного итога. Факты — данные `Order`, а не новые states: допустимость их записи по state задаёт строка FSM `record_fact` (только `placed`/`confirmed`), повтор факта — ошибка `FactAlreadyRecorded`. Cancellation и Failure пока завершаются одним state `cancelled` с сохранённым видом итога (`cancellation`/`failure`) и кодом причины; отдельный `failed` state не согласован. Domain Events: `OrderPlaced`, `OrderCancelled`, `OrderCompleted`.

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
| `DeliveryAddress` | Непустая нормализованная строка; детальную структуру адреса пока не моделируем |

`Money` создаётся из строки или другого точного представления, но не из `float64`. Требуемые масштаб и режим округления явно задаются доменной операцией.

`OrderId`, `CustomerId`, `RestaurantId` и `MenuItemId` на первом этапе остаются типизированными идентификаторами, а `OrderStatus` — перечислением. Для них не создаются отдельные value objects без дополнительных правил.

### Domain policies и Specification Pattern

Переиспользуемые условия вне state оформляются как чистые domain Specifications рядом с canonical `Order`. Реализованы:

- `WithinCancellationWindow` — проверяет `now` относительно времени оформления/подтверждения и cancellation policy;
- `OrderItemsOrderable` — проверяет переданный snapshot доступности позиций, минимальную/максимальную сумму и применимые лимиты.

Допустимость `Cancel` или `Complete` из текущего `OrderStatus` остаётся только в FSM. Application получает время и внешние snapshots и передаёт их в aggregate; он не вызывает принадлежащую поведению `Order` spec вместо domain method. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## CQRS

В контексте `orders` применяется CQRS. Команды выполняют бизнес-действия и изменяют состояние заказа только после проверки доменных правил и инвариантов. Запросы используют отдельные модели чтения и возвращают DTO, подходящие конкретному сценарию отображения или использования данных.

На начальном этапе командная сторона и модели чтения используют одно хранилище внутри контекста. Локальный CQRS ADR не выбирает конкретную СУБД; предложенный ADR 8 ниже конкретизирует это хранилище как PostgreSQL, не меняя границы CQRS.

В соответствии с Vertical Slice Architecture команды и запросы организуются по сценариям, например `request-order-cancellation`, `get-order-details` и `list-customer-orders`. Глобальные каталоги `commands/` и `queries/` не становятся основным каркасом контекста. Общие доменные правила и инварианты переиспользуются между срезами, а не дублируются в каждом сценарии.

В дальнейшем для карточки заказа может быть выделена событийная проекция `OrderDetails`, объединяющая необходимые сведения. На текущем этапе эта проекция не проектируется и не реализуется.

Решение зафиксировано в [ADR 4](docs/adr/0004-использование-cqrs-в-orders.md).

## Хранилище: PostgreSQL

PostgreSQL предлагается как основное логическое хранилище `orders`. Реляционная модель и транзакции подходят для хранения `Order` и его `Order Item`, снимков названий, цен, выбранных вариантов и дополнительных опций на момент оформления, согласованного изменения связанных данных, контроля конкурентных изменений, уникальности и ограничений целостности.

Доменная модель определяет бизнес-инварианты `Order`; ограничения базы дополняют, но не заменяют эти проверки. Реализация репозитория в `infrastructure` преобразует persistence-модели в доменные типы и сохраняет изменения агрегата в явно определённой транзакционной границе. Конкретные таблицы, isolation level и механизм контроля конкуренции будут спроектированы вместе с SDD-сценариями.

`orders` — основной потребитель Unit of Work: `Order` + Outbox, Inbox + `Order` (+ Outbox) и idempotent commands фиксируются одной транзакцией PostgreSQL с `READ COMMITTED` по умолчанию и optimistic concurrency по `version`; queries выполняются без UoW. Workflow транзакцией не владеет, каждая Activity вызывает handler со своим UoW. Когда общий UoW-контракт будет реализован, его shared package размещается в `pkg/application`; use-case-specific ports — рядом с package владельца в `slices/<slice>/applications/<usecase>`, реализация — в infrastructure модуля. Правила и список слайсов — в [документации Unit of Work](../../docs/unit-of-work.md#orders--postgresql), решение — в [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md).

Ссылки на Restaurant, Menu Item, Payment, Delivery и другие сущности соседних bounded contexts хранятся как идентификаторы без межконтекстных foreign keys. Локальные foreign keys допустимы между таблицами, принадлежащими `orders`, например между заказом и его позициями.

CQRS остаётся логическим разделением command- и query-сценариев: первоначально обе стороны используют одну PostgreSQL-базу `orders`. Отдельное физическое read store на этом этапе не вводится.

Бизнес-данные заказов и техническое хранилище Temporal разделены. Репозитории приложения работают с PostgreSQL-базой `orders`; внутренними данными выполнения workflow владеет Temporal. Приложение не использует Temporal storage как репозиторий `Order` и не управляет его внутренними таблицами напрямую.

Решение имеет статус Proposed в [ADR 8](../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md). Версия PostgreSQL, драйвер, ORM, migrations и реализация репозитория относятся к следующим шагам.

## Dependency Injection

В `orders` используется compile-time DI через [`github.com/google/wire`](https://github.com/google/wire). Зависимости передаются обычными Go-конструкторами, а Wire генерирует код их сборки в composition root каждого исполняемого процесса. Provider sets группируются по функциональности и необходимым инфраструктурным компонентам.

Каждый слайс описывает свои provider sets в собственном пакете `slices/<slice>/di`. Итоговая сборка процесса находится в корневом пакете `di`: `wire.go` объявляет injector, а `wire_gen.go` генерируется командой `make generate` и коммитится. `cmd/orders` только загружает конфигурацию, вызывает injector и передаёт `App` в `pkg/lifecycle`. Lifecycle реализует [runtime contract](../../docs/cloud-native-runtime.md): `/health/startup`, `/health/live`, `/health/ready` на техническом порту `management.port` (по умолчанию `9081`, `MANAGEMENT__PORT`), а по `SIGTERM`/`SIGINT` переводит readiness в `503`, выжидает `shutdown.readiness_delay` (по умолчанию `5s`), прекращает приём запросов и дожидается начатых в пределах `shutdown.drain_timeout` (по умолчанию `20s`). Штатный shutdown — exit `0`; ошибка startup, остановка сервера до сигнала или превышение drain deadline — non-zero. Поведение проверяется `make runtime-contract` из корня репозитория.

Импорты Wire и объявления сборки не входят в доменную модель и обработчики use cases. Composition root явно владеет запуском, остановкой и освобождением ресурсов. Доменные компоненты и обработчики можно создавать в тестах напрямую через конструкторы.

При запуске Temporal worker зависимости внедряются в Activities и компоненты worker. Детерминированный workflow-код не обращается к DI-контейнеру и не получает через DI клиенты внешних систем: внешние операции выполняются через Activities.

Решение, включая риск архивного статуса Google Wire, зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Событийная интеграция

Для публикации и обработки интеграционных событий через Apache Kafka выбран [Watermill с адаптером `watermill-kafka`](https://watermill.io/pubsubs/kafka/). Выбор зафиксирован в [ADR 3](docs/adr/0003-use-watermill-for-event-integration-in-orders.md).

Consumers интеграционных событий `orders` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение `Order`, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной транзакции PostgreSQL-базы `orders`. Уникальность `(consumer, event_id)` обеспечивается уникальным ограничением. Watermill подтверждает сообщение только после commit. In-memory дедупликация не заменяет durable Inbox.

Temporal Activity retries не заменяют Inbox Pattern. Kafka-событие, которое запускает workflow или отправляет ему Signal, сначала проходит идемпотентную boundary: повторное событие не создаёт второй логический workflow и не применяет Signal повторно, если это нарушает бизнес-семантику. Связь `event_id`, Workflow ID и Signal описана в разделе [Order Fulfillment Workflow](#входящие-события).

Публикация интеграционных событий `orders` использует Transactional Outbox по общесистемному [ADR 13](../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md). Изменение `Order` и сообщение Outbox записываются в одной транзакции PostgreSQL через `watermill-sql` publisher, созданный на `*sql.Tx`; [Watermill Forwarder](https://watermill.io/advanced/forwarder/) доставляет сообщения в Kafka через `watermill-kafka`. UUID сообщения Watermill равен `event_id` и передаётся в Kafka header `event_id`. Kafka key `order_id` передаётся через metadata и извлекается partitioning marshaler Kafka publisher'а. Forwarder работает как single active instance: replicas служат failover, а не throughput. Activity, изменяющая `Order` и Outbox, идемпотентна по ключу, производному от Workflow ID и шага процесса, или по ожидаемой `aggregate_version`; workflow-код не публикует события.

Схема Inbox и Outbox, миграции, Forwarder и Watermill middleware относятся к следующим шагам. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

Архитектурные решения контекста хранятся в [локальном журнале ADR](docs/adr/). Выбор Temporal зафиксирован в [ADR 2](docs/adr/0002-use-temporal-to-orchestrate-orders.md), контракт orchestration — в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md).

## Order Fulfillment Workflow

Решение — [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md), требования и сценарии — [спецификация `order-fulfillment-orchestration`](../../openspec/specs/order-fulfillment-orchestration/spec.md). Application-слой видит workflow только через порт [`fulfillment`](slices/order/applications/fulfillment/README.md); всё ниже относится к Temporal-адаптеру `slices/order/infrastructure/temporal/fulfillorder`.

### Когда применяется Temporal

Temporal применяется к процессу, который одновременно многошаговый, зависит от других контекстов, ждёт внешних фактов или сроков, требует retries или компенсаций и должен пережить перезапуск worker. Таков fulfillment одного `Order`. Use case, который проверяет, меняет `Order` и делает commit в одной короткой транзакции, workflow не запускает.

### Identity и старт

| Параметр | Значение |
| --- | --- |
| Workflow | Один Order Fulfillment Workflow на `Order`, без child workflows |
| Workflow ID | `order:<order_id>`; повторный старт — `USE_EXISTING` |
| Namespace | Отдельный на окружение |
| Task queue | `orders` |

Основной старт:

```text
Place Order: UoW { save Order; Outbox(OrderPlaced) }
  → Kafka → orders consumer → Inbox
  → SignalWithStart(order:<order_id>, OrderPlaced, USE_EXISTING)
```

Синхронный `fulfillment.Starter` после commit — допустимая альтернатива для latency: повторяется с тем же Workflow ID и не заменяет `OrderPlaced` в Outbox. Старт до commit запрещён: workflow увидел бы несуществующий `Order`.

Базовая логическая схема — карта coordination points, а не утверждённая последовательность:

```text
Order placed → Authorize payment → Request Acceptance
→ wait (accepted | rejected | timer) → Create Delivery / wait Assignment
→ wait Preparation → wait Pickup → Capture payment
→ wait Delivery Completion → Complete Order
```

### Входящие события

```text
Kafka(event_id) → orders consumer → Transactional Inbox → application handler
  → Signal/Update(event_id) → dedupe в workflow + проверка Workflow Stage
```

Два уровня дедупликации: Inbox по `(consumer, event_id)` защищает изменения `Order`; workflow хранит обработанные `event_id`, потому что Signal RPC может примениться, а ответ потеряться. Соседние контексты никогда не вызывают Temporal напрямую.

Имя Signal совпадает с именем Integration Event; адаптер переводит `fulfillment.Kind` в это имя.

| Signal / Update | Источник | Use case `orders` |
| --- | --- | --- |
| `OrderPlaced` (`SignalWithStart`) | `orders` | Place Order |
| `PaymentAuthorized`, `PaymentFailed` | `payments` | — |
| `RestaurantOrderAccepted` / `RestaurantOrderRejected` | `restaurants` | Confirm Order / Reject Order |
| `PreparationStarted` | `restaurants` | Record Preparation Started |
| `PreparationDelayed`, `PreparationFailed` | `restaurants` | — (только workflow) |
| `RestaurantOrderReady` | `restaurants` | Record Order Ready |
| `CourierAssignedToDelivery` | `delivery` | Record Courier Assignment |
| `DeliveryPickedUp` / `DeliveryCompleted` / `DeliveryFailed` | `delivery` | Record Pickup / Delivery Completion / Delivery Failure |
| `CancellationRequested` (Update) | клиент через `orders` | Request Order Cancellation |
| `OperatorDecision` | оператор через `orders` | — |

Положительное событие, которого текущий Workflow Stage не ждёт, логируется и игнорируется. Отрицательные факты (`RestaurantOrderRejected`, в том числе после Acceptance, `PreparationFailed`, `DeliveryFailed`, `PaymentFailed`) в незавершённом workflow ведут в Saga или эскалацию. Событие после завершения workflow обрабатывается по policy, которую документирует каждый use case; сейчас `Reflector` возвращает `ignored_late`.

### Signal, Update, Query

| Механизм | Когда |
| --- | --- |
| Signal | Асинхронный факт, ответ не нужен |
| Update | Нужен результат validation относительно Workflow Stage — клиентская Cancellation |
| Query | Только техническая диагностика |
| CQRS read model | Все клиентские чтения (`GetOrder`, `ListCustomerOrders`) |

Business cancellation — Update `CancellationRequested`: домен `Order` решает допустимость (`CheckCancellation`), workflow — компенсации. Temporal `CancelWorkflow` — аварийная техническая операция, а не Cancellation.

### Activities, retry и timeout

- Workflow детерминирован: gRPC, HTTP, DB, Kafka и SpiceDB — только в Activities.
- Каждая Activity имеет Start-To-Close timeout и Retry Policy; длительная — heartbeat. Transport failure (`UNAVAILABLE`, timeout) повторяется, business rejection возвращается результатом.
- Idempotency key финансовых и компенсационных операций — `order_id + step`, не зависит от attempt и run ID.
- Activity, меняющая `orders`, вызывает handler слайса со своим UoW (`Order` + Outbox); прямой publish в Kafka запрещён.
- Вызовы других контекстов идут через [internal gateway](../../ops/kind/README.md#internal-gateway) (`INTERNAL_GATEWAY_ADDR`) от `service` identity worker'а; authorization выполняет целевой контекст по [security](../../docs/security.md).
- Ожидания — Signal + Temporal timer, polling запрещён. Timer ведёт к бизнес-ветке (replan, escalation, cancellation, compensation); workflow-level timeout не задаётся. `PreparationDelayed` продлевает таймер Preparation.

### Saga

Workflow ведёт журнал подтверждённых шагов и при отказе вызывает только применимые компенсации — `CancelRestaurantOrder`, `CancelPaymentAuthorization` или `RefundPayment` — каждая отдельной идемпотентной Activity со своим ключом, timeout, retry и telemetry. `RestaurantOrderCancelled`, `PaymentAuthorizationCancelled` и `PaymentRefunded` подтверждают уже полученный результат и Saga повторно не запускают.

### Runtime

API, Kafka consumer и Temporal worker — отдельные процессы с собственными composition roots по [Cloud Native Runtime](../../docs/cloud-native-runtime.md). Worker при shutdown прекращает poll, даёт Activities завершиться, закрывает clients и flush telemetry; liveness не зависит от Temporal Server. Crash worker не business failure: Temporal восстановит workflow replay'ем.

### Где Temporal не используется

- CQRS queries и клиентские чтения;
- локальный `request → transaction → response`;
- доставка событий между контекстами — это Outbox, Kafka и Inbox по [событийной интеграции](../../docs/event-driven-architecture.md);
- authorization — SpiceDB;
- хранение `Order` — PostgreSQL;
- универсальные фоновые задания и job queue.

| Что | Где живёт | Не является |
| --- | --- | --- |
| Order State, Fulfillment Facts, инварианты | `Order` в PostgreSQL `orders` | — |
| Workflow Stage, обработанные `event_id`, таймеры, журнал compensation | Temporal Event History | Source of truth `Order` |
| Интеграционные события | Kafka + Outbox/Inbox | Workflow state |
| Клиентские представления | CQRS read model | Temporal Query |
| Authorization | SpiceDB в use case владельца | Решение workflow |
| Task queue `orders` | Temporal | Job queue общего назначения |

Workflow Stage публикуется низкокардинальным Search Attribute для Temporal UI и не становится Order State.

Отложенные продуктовые решения (таймауты, момент Authorization/Capture, параллельность Assignment, late-event policy, отказ ресторана после Acceptance, Continue-As-New) перечислены в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md#отложенные-продуктовые-решения).

## Observability

Для `orders` observability охватывает gRPC, PostgreSQL, Watermill/Kafka, Temporal workflows и Activities, API и workers. W3C Trace Context передаётся через gRPC metadata и Watermill message metadata/Kafka headers; telemetry при replay workflow не должна повторно считаться бизнес-результатом. Общие правила и путь Pyroscope для Go описаны в [общесистемной документации observability](../../docs/observability.md).

## Статус реализации

Реализован in-memory доменный агрегат `Order`: value objects, вычисление total, чистая табличная FSM, типизированные ошибки переходов и забор domain events. Также реализованы чистые policies `WithinCancellationWindow` и `OrderItemsOrderable` с типизированными reasons и unit tests.

OpenSpec change `implement-orders-application-layer` добавил application-слой (FR-100…FR-117, AC-100…AC-130):

- handlers всех 12 Commands и 2 Queries в `slices/order/applications/<usecase>`; `OrderItemsOrderable` подключена в `order.Place`, `WithinCancellationWindow` — в `Order.CheckCancellation`;
- shared порты `UnitOfWork`, `Inbox`, `Clock`, verified `Subject` и application errors в `pkg/application`; контракт `order.Repository` в `domains/order`; порт Order Fulfillment Workflow и общий Inbox-поток входящих фактов в [`slices/order/applications/fulfillment`](slices/order/applications/fulfillment/README.md); узкие порты authorization, catalog snapshot, cancellation policy, Outbox, idempotency и read models — рядом с use case;
- in-memory адаптеры в `slices/order/infrastructure/memory` и fixtures `domains/order/ordertest` для unit-тестов.

Контракт Temporal-адаптера за портом `fulfillment` зафиксирован в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md) и разделе [Order Fulfillment Workflow](#order-fulfillment-workflow). OpenSpec change `implement-order-fulfillment-temporal-adapter` реализовал его в [`slices/order/infrastructure/temporal/fulfillorder`](slices/order/infrastructure/temporal/fulfillorder/README.md) на `go.temporal.io/sdk` v1.50.0: workflow с deadlines из `Policy`, replan и эскалацией Assignment, Saga и Update `CancellationRequested`; Activities поверх `completeorder`, `failorcancelorder` и client-портов `Payments`, `Restaurants`, `Delivery`; реализацию порта `fulfillment` на Temporal client. Значения `DefaultPolicy` — placeholders до продуктовых решений.

OpenSpec change `add-orders-temporal-worker` добавил процесс `cmd/orders-worker`: Wire injector `InitializeWorker`, конфигурацию `temporal.*` и `fulfillment.*`, проверку и регистрацию Search Attribute `OrderFulfillmentStage` при старте, адаптеры соседей `unavailable` (по умолчанию) и `simulated` в `slices/order/infrastructure/neighbours`, values-worker.yaml Helm chart и integration-тест против Temporal dev server (`make test-integration`). До PostgreSQL у API и worker'а отдельные in-memory store: worker видит только заказы своего процесса, поэтому в окружениях он не исполняет реальные заказы.

Реализован общий Kafka transport на Watermill: конфигурация brokers/client/group,
publisher с обязательным partition key `order_id`, subscriber consumer group и
единый mapping `event_id` в UUID/metadata. Не реализованы: PostgreSQL,
Watermill SQL Inbox/Outbox и Forwarder, конкретные Kafka topics и event contracts,
runnable consumers, SpiceDB, production `SubjectResolver`, остальные command RPC,
verified caller identity для вызовов соседей. Query handlers зарегистрированы в DI и доступны
через gRPC. `.feature` сценарии `confirmorder` и
`recordcourierassignment` покрыты unit-тестами handlers; Gherkin runner не подключён.

## Запуск

```bash
make run
```

Temporal worker `cmd/orders-worker` опрашивает task queue `orders`. Локально — против Temporal dev server в Docker (`temporalio/temporal:1.9.1`, UI на `http://localhost:8233`):

```bash
make temporal-dev
```

```bash
make run-worker
```

`run-worker` включает регистрацию Search Attribute и `FULFILLMENT__NEIGHBOURS=simulated`, management port — `9082`. Остановить dev server:

```bash
make temporal-dev-stop
```

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `TEMPORAL__HOST_PORT` | `localhost:7233` | Адрес Temporal Frontend |
| `TEMPORAL__NAMESPACE` | `default` | Namespace окружения |
| `TEMPORAL__CONNECT_TIMEOUT` | `10s` | Предел подключения и проверки Search Attribute в startup |
| `TEMPORAL__REGISTER_SEARCH_ATTRIBUTES` | `false` | Создать `OrderFulfillmentStage`, если его нет (нужны права оператора) |
| `FULFILLMENT__<KEY>` | см. `config.example.yaml` | `Policy` workflow (placeholders), `PUBLISH_STAGE`, `NEIGHBOURS` |

`FULFILLMENT__NEIGHBOURS=unavailable` (по умолчанию) — вызовы соседей завершаются retryable ошибкой, workflow ждёт на текущем шаге; `simulated` одобряет вызовы без эффекта и предназначен только для локальной проверки; `grpc` вызывает командные API `payments`, `restaurants`, `delivery` через `INTERNAL_GATEWAY_ADDR` клиентами [`neighbours/grpcclient`](slices/order/infrastructure/neighbours/grpcclient) (Go-код генерируется `make generate` из workspace-модулей до публикации в BSR). Пока verified caller identity не спроектирована, серверы соседей отвечают `UNAUTHENTICATED`; клиент считает это retryable.

## Проверка

Установка линтера на macOS:

```bash
brew install golangci-lint
```

```bash
make check
```

Автоформатирование:

```bash
make format
```

Integration-тест worker'а против Temporal dev server в Docker (поднимает и останавливает контейнер):

```bash
make test-integration
```

## Контейнер

Образ собирается из каталога `boundaries/orders`. Доступные targets: `build` и
`runtime`; итоговая стадия — `runtime`:

```bash
docker build --target runtime -t nomnom/orders:local boundaries/orders
docker run --rm -p 8081:8081 -p 9081:9081 \
  -e SERVER__PORT=8081 \
  nomnom/orders:local
```

Образ содержит статический Go-бинарник и TLS CA, запускается как distroless-пользователь
`nonroot`. Конфигурация передаётся переменными окружения. Локальный YAML-файл можно
подключить read-only и указать через `CONFIG_FILE`:

```bash
docker run --rm -p 8081:8081 \
  -v "$PWD/boundaries/orders/config.example.yaml:/config/orders.yaml:ro" \
  -e CONFIG_FILE=/config/orders.yaml \
  nomnom/orders:local
```

Текущий gRPC-процесс не подключается к PostgreSQL, Kafka или Temporal. Kafka
transport используется будущими отдельными composition roots Forwarder и consumers;
соответствующие подключения станут обязательными при их запуске.

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.
