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

Статус реализации: application handlers всех перечисленных Commands и Queries реализованы поверх PostgreSQL store и адаптеров портов (см. [Статус реализации](#статус-реализации)); transport, persistence, Kafka и Temporal adapters создаются отдельными SDD changes; пустые программные scaffolds не используются.

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
| **Record Courier Release** | Kafka consumer `orders` | Отразить снятие курьера без замены или отмену Delivery | Курьер снят с сохранённого назначения; при отмене Delivery удалена `order#delivery` (доступ курьера отзывает `delivery`) | Integration events `CourierUnassignedFromDelivery`/`DeliveryCancelled` от `delivery`, Transactional Inbox; `Order` и workflow не меняются |
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

| Слайс | Содержание |
| --- | --- |
| `order` | Агрегат `Order`, его use cases (включая `completeorder` и `failorcancelorder`, которые вызывают Activities), порт Order Fulfillment Workflow `applications/fulfillment`, persistence, SpiceDB, Kafka и catalog |
| [`fulfillment`](slices/fulfillment/README.md) | Процесс Order Fulfillment: доменная модель `domains/fulfillment` (маршрут, факты, сроки, replan и эскалация, отмена, компенсации), Temporal-адаптер `infrastructure/temporal`, client-адаптеры соседей `infrastructure/neighbours`, provider set worker'а `di` |

Зависимости направлены от `fulfillment` к `order`: адаптер реализует порт `order/applications/fulfillment` и вызывает use cases `order` из Activities; `order` не импортирует `fulfillment`, а доменная модель `fulfillment` — ничего, кроме стандартной библиотеки.

Temporal workflow относится к прикладному процессу по ответственности, но вместе с Activities и транспортными обработчиками является инфраструктурным адаптером, поэтому правила процесса принимает доменная модель слайса `fulfillment`, а workflow в `slices/fulfillment/infrastructure/temporal` только переводит Signals и Update в её вызовы и исполняет её решения как Activities и таймеры. Workflow не знает о Commands, Queries, handlers и `Order`; Activity преобразует payload в Command или Query и вызывает handler слайса `order`, а use case обращается к workflow (start, Signal, Update) через порт в `applications/`. Ни Temporal SDK, ни Watermill, ни Google Wire не импортируются доменной моделью и handlers. Правила моделей и entrypoints — в [общесистемном ADR 6](../../docs/adr/0006-ddd-structure-inside-vertical-slices.md#типы-моделей).

## gRPC-адаптер

`orders` владеет API `nomnom.orders.v1` и публикует его как `buf.build/nomnom/orders`. Исходные messages находятся в `infrastructure/grpc/nomnom/orders/v1/orders.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер генерирует серверные Go stubs из локальной схемы и преобразует request messages в commands/queries, а прикладные результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделями `Order`, handlers или Temporal workflow. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API. Вид ошибки (`application.ErrorKind`: invalid input, not found, already exists,
conflict, domain rule, unavailable, permission, internal) определяет одна функция
[`errorkind.Classify`](slices/order/applications/errorkind/errorkind.go); gRPC, Kafka consumer,
Temporal Activities и телеметрия переводят вид своими таблицами, тест каждой проверяет полноту.
Уточнения этого API (`RESTAURANT_*`, `INVALID_ORDER`, metadata `ORDER_NOT_ORDERABLE`, правило окна
отмены) остаются в gRPC-адаптере.

| Ошибка `orders` | gRPC code | `ErrorInfo.reason` |
| --- | --- | --- |
| `Order` не найден | `NOT_FOUND` | `ORDER_NOT_FOUND` |
| Ресторан или его меню не найдены (Place Order) | `NOT_FOUND` | `RESTAURANT_NOT_FOUND` |
| Ресторан закрыт (`is_open = false`, Place Order) | `FAILED_PRECONDITION` | `RESTAURANT_NOT_ACCEPTING_ORDERS` |
| Позиции нельзя заказать (политика `OrderItemsOrderable`, Place Order) | `FAILED_PRECONDITION` | `ORDER_NOT_ORDERABLE` + metadata `codes`, `menu_item_ids` |
| Некорректный состав или Delivery Address | `INVALID_ARGUMENT` | `INVALID_ORDER` |
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

`PlaceOrder` оформляет заказ для verified subject: поля customer в запросе нет. Customer — user
subject из user assertion BFF ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)):
BFF передаёт свой service token и `x-nomnom-user-assertion`; service subject без assertion
(в том числе сам BFF) получает `PERMISSION_DENIED`. Catalog snapshot
до транзакции читает адаптер [`slices/order/infrastructure/catalog`](slices/order/infrastructure/catalog)
(FR-497): `restaurants.GetRestaurant` (`is_open`) и `GetMenu` через internal gateway
(`INTERNAL_GATEWAY_ADDR`; без адреса при `FULFILLMENT__NEIGHBOURS=grpc` по умолчанию процесс не
стартует) с projected token `nomnom.restaurants` API orders (`IDENTITY__TOKEN_DIR`, если задан) и общим
deadline `CATALOG__TIMEOUT`. В snapshot попадают только запрошенные позиции; отсутствующие в меню
недоступны. Закрытый ресторан — `RESTAURANT_NOT_ACCEPTING_ORDERS`, `NOT_FOUND` ресторана или меню и
пустое меню — `RESTAURANT_NOT_FOUND`, цены в разных валютах — `INVALID_ORDER`, любая другая ошибка
`restaurants` (transport, deadline, identity, отсутствующий route) — `UNAVAILABLE`. Валюта snapshot —
валюта меню; минимум и максимум суммы — `CATALOG__MINIMUM_TOTAL`/`CATALOG__MAXIMUM_TOTAL` в этой
валюте (placeholders, пока `restaurants` их не публикует). При `FULFILLMENT__NEIGHBOURS=unavailable`
или `simulated` catalog fail-closed: цены не симулируются, `PlaceOrder` отвечает
`UNAVAILABLE`/`DEPENDENCY_UNAVAILABLE`. `order_id` — UUIDv7. Temporal client API lazy: процесс
стартует без Temporal; если start workflow не удался после commit, ответ содержит
`fulfillment_started=false`, а повтор с тем же `idempotency_key` повторяет start.
`RequestOrderCancellation` возвращает outcome `REQUESTED` или `ALREADY_CANCELLED` и использует тот
же экземпляр cancellation policy, что `GetOrder`.

`GetOrder` возвращает `Order.cancellation_window { opens_at, closes_at }`, пока Order State
допускает Order Cancellation (`placed`, `confirmed`); для `cancelled` и `completed` поля нет.
Окно задаёт та же cancellation policy, что использует Request Order Cancellation: оно
открывается в момент Place Order и длится `cancellation.window` (`CANCELLATION__WINDOW`,
по умолчанию `5m` — placeholder до продуктового решения; `0s` или неразбираемое значение
останавливают процесс на старте). Открыто ли окно сейчас, решает потребитель по своему
clock, а сам запрос отмены проверяется заново. Если политика вернёт ошибку, `GetOrder`
отдаёт заказ без окна, а декоратор `cancellationpolicy.Observed` пишет ошибку в log и span. Composition root по
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

Кроме state, `Order` хранит время оформления, отражённые факты fulfillment (`restaurant_accepted`, `restaurant_rejected`, `preparation_started`, `ready_for_pickup`, `courier_assigned`, `picked_up`, `delivery_completed`, `delivery_failed`) и причину отрицательного итога. Факты — данные `Order`, а не новые states: допустимость их записи по state задаёт строка FSM `record_fact` (только `placed`/`confirmed`), повтор факта — ошибка `FactAlreadyRecorded`. `restaurant_accepted` записывает Confirm Order вместе с переходом `placed → confirmed` и моментом события Order Acceptance; заказы, подтверждённые раньше, его не имеют. Cancellation и Failure пока завершаются одним state `cancelled` с сохранённым видом итога (`cancellation`/`failure`) и кодом причины; отдельный `failed` state не согласован. Domain Events: `OrderPlaced`, `OrderCancelled`, `OrderCompleted` (с моментом завершения `CompletedAt`, который передаёт `Complete`). Назначение курьера — value object `CourierAssignment` с правилами `Supersedes` (событие старше сохранённого устарело), `Displaced` (что вытесняет переназначение) и `Release` (снятие сохраняет своё время); контракт хранения `CourierAssignments` объявлен в домене.

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

PostgreSQL — основное логическое хранилище `orders`. Реляционная модель и транзакции подходят для хранения `Order` и его `Order Item`, снимков названий, цен, выбранных вариантов и дополнительных опций на момент оформления, согласованного изменения связанных данных, контроля конкурентных изменений, уникальности и ограничений целостности.

Доменная модель определяет бизнес-инварианты `Order`; ограничения базы дополняют, но не заменяют эти проверки. Реализация репозитория в `infrastructure` преобразует persistence-модели в доменные типы и сохраняет изменения агрегата в явно определённой транзакционной границе. Реализация — [`slices/order/infrastructure/postgres`](slices/order/infrastructure/postgres): PostgreSQL 18, pgx v5, миграции goose (`migrations/`, применяет `cmd/orders-migrate` под advisory lock). Таблицы `orders` (с `version` и `authorization_revision`), `order_items`, `order_facts`, `inbox`, `idempotency_keys` и Outbox `watermill_orders_outbox`. Unit of Work — `READ COMMITTED` транзакция в context, вложенный `Do` присоединяется к внешней; `Save` проверяет `version` и при расхождении возвращает `ErrConcurrencyConflict`. API, worker, consumer и forwarder работают с одной базой; `POSTGRES__DSN` обязателен.

`orders` — основной потребитель Unit of Work: `Order` + Outbox, Inbox + `Order` (+ Outbox) и idempotent commands фиксируются одной транзакцией PostgreSQL с `READ COMMITTED` по умолчанию и optimistic concurrency по `version`; queries выполняются без UoW. Workflow транзакцией не владеет, каждая Activity вызывает handler со своим UoW. Когда общий UoW-контракт будет реализован, его shared package размещается в `pkg/application`; use-case-specific ports — рядом с package владельца в `slices/<slice>/applications/<usecase>`, реализация — в infrastructure модуля. Правила и список слайсов — в [документации Unit of Work](../../docs/unit-of-work.md#orders--postgresql), решение — в [ADR 20](../../docs/adr/0020-unit-of-work-for-local-transactions.md).

Ссылки на Restaurant, Menu Item, Payment, Delivery и другие сущности соседних bounded contexts хранятся как идентификаторы без межконтекстных foreign keys. Локальные foreign keys допустимы между таблицами, принадлежащими `orders`, например между заказом и его позициями.

CQRS остаётся логическим разделением command- и query-сценариев: первоначально обе стороны используют одну PostgreSQL-базу `orders`. Отдельное физическое read store на этом этапе не вводится.

Бизнес-данные заказов и техническое хранилище Temporal разделены. Репозитории приложения работают с PostgreSQL-базой `orders`; внутренними данными выполнения workflow владеет Temporal. Приложение не использует Temporal storage как репозиторий `Order` и не управляет его внутренними таблицами напрямую.

Решение — [ADR 8](../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md); контрактные тесты адаптеров — `make test-postgres` (PostgreSQL поднимается testcontainers в `TestMain`; `POSTGRES_DSN` — использовать готовую базу).

### Пулер PostgreSQL

Перед базой стоит пулер соединений, и он обязан работать в **session mode**: `orders` опирается на состояние server-сессии. Миграции goose берут session-level advisory lock (`lock.NewPostgresSessionLocker`) на отдельном соединении, pgx по умолчанию кэширует prepared statements на соединении (`QueryExecModeCacheStatement`). Transaction mode их ломает: lock и prepared statements окажутся на чужом server-соединении. Остальное от режима не зависит: Unit of Work и `FOR UPDATE` (назначение курьера, offsets Outbox forwarder'а watermill-sql) живут внутри одной транзакции, `pg_advisory_xact_lock` при инициализации схемы Outbox — transaction-scoped; `LISTEN/NOTIFY`, `SET` вне транзакции и временные таблицы не используются.

Каждый процесс держит собственный пул pgx (`POSTGRES__*`): `MAX_CONNS` по конкурентности роли, `MIN_CONNS` 0–1, `MAX_CONN_LIFETIME` 30m с `MAX_CONN_LIFETIME_JITTER` 5m (реплики не переоткрывают соединения одновременно), `MAX_CONN_IDLE_TIME` 5m, `CONNECT_TIMEOUT` 5s, `HEALTH_CHECK_PERIOD` 1m; недопустимое значение — ошибка startup. В session mode каждое соединение пула занимает server-соединение пулера, поэтому сумма Σ(replicas × maxConns) всех ролей должна помещаться в `pool_size` пулера для базы `orders`:

| Роль | values | maxConns | minConns |
| --- | --- | --- | --- |
| API | `values.yaml` | 10 | 1 |
| worker | `values-worker.yaml` | 5 | 1 |
| consumer | `values-consumer.yaml` | 5 | 1 |
| forwarder | `values-forwarder.yaml` | 2 | 1 |
| reconciler | `values-reconciler.yaml` | 2 | 0 |
| init container migrate | — | 2 (lock + миграция) | 0 |

При одной реплике каждой роли это 24 server-соединения плюс 2 на время миграций. Пул экспортирует OTel semconv `db.client.connection.*` (`count` по `state`, `max`, `idle.min`, `pending_requests`, `timeouts`, `wait_time`, `use_time`, `create_time`; `db.client.connection.pool.name=orders`), инвентарь — [`docs/observability/metric-inventory.md`](../../docs/observability/metric-inventory.md#db).

## Dependency Injection

В `orders` используется compile-time DI через [`github.com/google/wire`](https://github.com/google/wire). Зависимости передаются обычными Go-конструкторами, а Wire генерирует код их сборки в composition root каждого исполняемого процесса. Provider sets группируются по функциональности и необходимым инфраструктурным компонентам.

Каждый слайс описывает свои provider sets в собственном пакете `slices/<slice>/di`: у `order` это инфраструктурные `PostgresSet` (один `ProcessStore` процесса и его привязка к ролям хранилища) и `TelemetrySet` (инструменты use cases), а также наборы use cases процессов — `APISet` (`Module` API) и `ConsumerSet` (handlers Kafka consumer); у `fulfillment` — `WorkerSet` (адаптеры соседей и Activities). Вместо одного интерфейса хранилища потребители объявляют роли рядом с собой: `PlacementStore` и `ReadModels` у `Module`, `InboxStore` у `Consumer`, `fulfillmentdi.Store` у Activities, `spicedb.RevisionReader` у authorizer; Wire связывает их с одним `ProcessStore` — PostgreSQL, у которого Unit of Work и Outbox идут через декоратор бизнес-метрик `observability.ObservedStore`. Порты набора собираются в структуру зависимостей через `wire.Struct` (`Dependencies`, `Consumer`), поэтому новый порт — это поле структуры и provider, а не новый позиционный параметр у всех вызовов. Итоговая сборка процессов находится в корневом пакете `di`: `wire.go` объявляет provider sets процесса (секции конфигурации, Temporal client API, gRPC, SpiceDB reconciler'а) и по injector'у на каждый процесс — `InitializeApp`, `InitializeWorker`, `InitializeConsumer`, `InitializeForwarder`, `InitializeReconciler`, `InitializeMigrator`; `wire_gen.go` генерируется командой `make generate` и коммитится. `cmd/*` только загружают конфигурацию, вызывают свой injector и передают результат в `pkg/lifecycle` (`orders-migrate` вызывает `Run` и завершается). Ресурсы, созданные при сборке (пулы PostgreSQL, клиенты SpiceDB и Temporal API, соединение с internal gateway), освобождает cleanup injector'а; ресурсы, которым нужен контекст `Start` или pipeline метрик процесса (telemetry, Temporal client worker'а и consumer'а, Kafka, Watermill Router), открывает и закрывает сам lifecycle.Worker процесса. Lifecycle реализует [runtime contract](../../docs/cloud-native-runtime.md): `/health/startup`, `/health/live`, `/health/ready` на техническом порту `management.port` (по умолчанию `9081`, `MANAGEMENT__PORT`), а по `SIGTERM`/`SIGINT` переводит readiness в `503`, выжидает `shutdown.readiness_delay` (по умолчанию `5s`), прекращает приём запросов и дожидается начатых в пределах `shutdown.drain_timeout` (по умолчанию `20s`). Штатный shutdown — exit `0`; ошибка startup, остановка сервера до сигнала или превышение drain deadline — non-zero. Поведение проверяется `make runtime-contract` из корня репозитория.

Импорты Wire и объявления сборки не входят в доменную модель и обработчики use cases. Composition root явно владеет запуском, остановкой и освобождением ресурсов. Доменные компоненты и обработчики можно создавать в тестах напрямую через конструкторы.

При запуске Temporal worker зависимости внедряются в Activities и компоненты worker. Детерминированный workflow-код не обращается к DI-контейнеру и не получает через DI клиенты внешних систем: внешние операции выполняются через Activities.

Решение, включая риск архивного статуса Google Wire, зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Событийная интеграция

Для публикации и обработки интеграционных событий через Apache Kafka выбран [Watermill с адаптером `watermill-kafka`](https://watermill.io/pubsubs/kafka/). Выбор зафиксирован в [ADR 3](docs/adr/0003-use-watermill-for-event-integration-in-orders.md).

Consumers интеграционных событий `orders` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение `Order`, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной транзакции PostgreSQL-базы `orders`. Уникальность `(consumer, event_id)` обеспечивается уникальным ограничением. Watermill подтверждает сообщение только после commit. In-memory дедупликация не заменяет durable Inbox.

Temporal Activity retries не заменяют Inbox Pattern. Kafka-событие, которое запускает workflow или отправляет ему Signal, сначала проходит идемпотентную boundary: повторное событие не создаёт второй логический workflow и не применяет Signal повторно, если это нарушает бизнес-семантику. Связь `event_id`, Workflow ID и Signal описана в разделе [Order Fulfillment Workflow](#входящие-события).

Публикация интеграционных событий `orders` использует Transactional Outbox по общесистемному [ADR 13](../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md). Изменение `Order` и сообщение Outbox записываются в одной транзакции PostgreSQL через `watermill-sql` publisher, созданный на `*sql.Tx`; [Watermill Forwarder](https://watermill.io/advanced/forwarder/) доставляет сообщения в Kafka через `watermill-kafka`. UUID сообщения Watermill равен `event_id` и передаётся в Kafka header `event_id`. Kafka key `order_id` передаётся через metadata и извлекается partitioning marshaler Kafka publisher'а. Forwarder работает как single active instance: replicas служат failover, а не throughput. Activity, изменяющая `Order` и Outbox, идемпотентна по ключу, производному от Workflow ID и шага процесса, или по ожидаемой `aggregate_version`; workflow-код не публикует события.

Domain Events переводятся в Integration Events `nomnom.orders.v1` (`OrderPlaced`, `OrderCompleted`, `OrderCancelled`, [`events.proto`](infrastructure/grpc/nomnom/orders/v1/events.proto)) в той же транзакции; `OrderPlaced` несёт состав заказа `items` — по нему `restaurants-consumer` создаёт Restaurant Order; `event_id` детерминирован (UUIDv5 от `order:<id>:<version>:<index>`), `occurred_at` берётся из Domain Event (для `OrderCompleted` — момент завершения). Перевод делает `IntegrationMapper` в `slices/order/infrastructure/messaging/outbound` (таблица Domain Event → контракт); PostgreSQL Outbox только сохраняет готовые сообщения, Forwarder (`postgres/forwarder.go`) — отдельно. Процесс `cmd/orders-forwarder` переносит их из Outbox в Kafka `orders.v1.order-events` с ключом `order_id`. Общие правила описаны в [документации событийного взаимодействия](../../docs/event-driven-architecture.md).

Архитектурные решения контекста хранятся в [локальном журнале ADR](docs/adr/). Выбор Temporal зафиксирован в [ADR 2](docs/adr/0002-use-temporal-to-orchestrate-orders.md), контракт orchestration — в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md).

## Order Fulfillment Workflow

Решение — [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md), требования и сценарии — [спецификация `order-fulfillment-orchestration`](../../openspec/specs/order-fulfillment-orchestration/spec.md). Application-слой видит workflow только через порт [`fulfillment`](slices/order/applications/fulfillment/README.md). Правила процесса — маршрут, реакция на факты, сроки, replan и эскалация, допустимость Cancellation Request и план компенсации — реализует доменная модель слайса [`fulfillment`](slices/fulfillment/README.md) без Temporal SDK, часов и случайности (`FR-483`); всё ниже относится к процессу и его Temporal-адаптеру [`slices/fulfillment/infrastructure/temporal`](slices/fulfillment/infrastructure/temporal/README.md), который переводит Signals и Update в вызовы модели и исполняет её решения.

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
| `CourierUnassignedFromDelivery` / `DeliveryCancelled` | `delivery` | Record Courier Release (не сигнал workflow: снятие возвращает Delivery в поиск, отмену запрашивает сам workflow) |
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
| Order State, Fulfillment Facts, инварианты | `Order` в PostgreSQL `orders` | — |
| Workflow Stage, обработанные `event_id`, таймеры, журнал compensation | Temporal Event History | Source of truth `Order` |
| Интеграционные события | Kafka + Outbox/Inbox | Workflow state |
| Клиентские представления | CQRS read model | Temporal Query |
| Authorization | SpiceDB в use case владельца | Решение workflow |
| Task queue `orders` | Temporal | Job queue общего назначения |

Workflow Stage публикуется низкокардинальным Search Attribute для Temporal UI и не становится Order State.

Отложенные продуктовые решения (таймауты, момент Authorization/Capture, параллельность Assignment, late-event policy, отказ ресторана после Acceptance, Continue-As-New) перечислены в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md#отложенные-продуктовые-решения).

## Observability

Для `orders` observability охватывает gRPC, PostgreSQL, Watermill/Kafka, Temporal workflows и Activities, API и workers. W3C Trace Context передаётся через gRPC metadata и Watermill message metadata/Kafka headers; telemetry при replay workflow не должна повторно считаться бизнес-результатом.

`pkg/telemetry` собирает три сигнала каждого процесса с одним resource: `service.namespace=nomnom`, `service.name` роли (`orders`, `orders-worker`, `orders-consumer`, `orders-forwarder`, `orders-reconciler`, `orders-migrate`; `OTEL_SERVICE_NAME` переопределяет), `service.version` (VCS revision сборки, Helm — image tag через `OTEL_RESOURCE_ATTRIBUTES`), `deployment.environment.name` и `service.instance.id` из `OTEL_RESOURCE_ATTRIBUTES`.

| Процесс | Traces | Metrics | Logs |
| --- | --- | --- | --- |
| `orders` (API) | gRPC server, use cases, PostgreSQL, SpiceDB, catalog через internal gateway, Temporal client (start/signal) | gRPC server/client, use cases, domain, `db.client.*`, runtime | JSON |
| `orders-worker` | Temporal workflow task и Activity, use cases, PostgreSQL, вызовы соседей через gateway | Temporal SDK, use cases, domain, `db.client.*`, gRPC client, runtime | JSON, Temporal SDK |
| `orders-consumer` | Kafka receive (otelsarama) → process (parent из headers), use cases, PostgreSQL, SpiceDB, Temporal client | Watermill consumer, `nomnom.messaging.*`, use cases, `db.client.*`, Temporal SDK, runtime | JSON, Watermill, Temporal SDK |
| `orders-forwarder` | Kafka producer span — дочерний к операции, записавшей событие в Outbox | Watermill `outbox_forwarder`, `db.client.*`, runtime | JSON, Watermill |
| `orders-reconciler` | span прохода сверки, PostgreSQL, SpiceDB | `db.client.*`, runtime | JSON |
| `orders-migrate` | — | — | JSON |

- **Traces** экспортируются по OTLP gRPC, только если задан `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`; без них trace context всё равно передаётся соседям. W3C Trace Context (без baggage для Kafka и Temporal) идёт через gRPC metadata, Temporal headers (`contrib/opentelemetry` interceptor client'а, worker его наследует) и Kafka: Outbox записывает `traceparent`/`tracestate` контекста use case в metadata события (`messaging.InjectTraceContext`), Forwarder переносит их в headers, consumer извлекает их до span обработки. PostgreSQL — `otelpgx` поверх pgx: client spans только внутри trace, без текста statement и bind parameters. SpiceDB — client spans `otelgrpc` без RPC-метрик.
- **Metrics** — по OTLP при `OTEL_EXPORTER_OTLP_ENDPOINT` (или `..._METRICS_ENDPOINT`). `db.client.operation.duration` (`otelpgx`) — с labels `db.system.name`, `db.operation.name`; пул — `db.client.connection.*` (`postgres/pool_metrics.go`).
- **Logs** — одна JSON-запись на строку в stdout: `timestamp` (UTC, RFC 3339), `severity` (`DEBUG`…`ERROR`), `message`, resource-поля `service.*` и `deployment.environment.name`, `trace_id`/`span_id` только внутри span, ошибки — `error.message` и `error.type`. Через тот же handler идут `slog`, `log.Printf`, gRPC (`grpclog`, info/warning — на уровне debug), Watermill и Temporal SDK. Redaction: поля с именами `*token*`, `*password*`, `*secret*`, `*dsn*`, `authorization`, `cookie`, `payload` и байтовые значения заменяются на `[REDACTED]`; userinfo URL/DSN, `password=`, bearer/basic credentials и JWT вырезаются из любого текста. `LOG__LEVEL` — `debug`, `info` (по умолчанию), `warn`, `error`.

В kind traces API уходят в приёмник проверок `trace-capture` ([`ops/kind`](../../ops/kind/README.md#проверки)). Общие правила и путь Pyroscope для Go описаны в [общесистемной документации observability](../../docs/observability.md).

## Статус реализации

Реализован in-memory доменный агрегат `Order`: value objects, вычисление total, чистая табличная FSM, типизированные ошибки переходов и забор domain events. Также реализованы чистые policies `WithinCancellationWindow` и `OrderItemsOrderable` с типизированными reasons и unit tests.

OpenSpec change `implement-orders-application-layer` добавил application-слой (FR-100…FR-117, AC-100…AC-130):

- handlers всех 12 Commands и 2 Queries в `slices/order/applications/<usecase>`; `OrderItemsOrderable` подключена в `order.Place`, `WithinCancellationWindow` — в `Order.CheckCancellation`;
- shared порты `UnitOfWork`, `Inbox`, `Clock`, verified `Subject` и application errors в `pkg/application`; контракт `order.Repository` в `domains/order`; порт Order Fulfillment Workflow и общий Inbox-поток входящих фактов в [`slices/order/applications/fulfillment`](slices/order/applications/fulfillment/README.md); узкие порты authorization, catalog snapshot, cancellation policy, Outbox, idempotency и read models — рядом с use case;
- тесты handlers работают против настоящего PostgreSQL ([`slices/order/infrastructure/postgres/postgrestest`](slices/order/infrastructure/postgres/postgrestest): testcontainers, отдельная база на тест из мигрированного template); внешние системы (SpiceDB, catalog, Temporal workflow, часы, генератор id) подменяются stand-ins из `slices/order/infrastructure/fake`; fixtures агрегата — `domains/order/ordertest`. In-memory адаптеров хранения нет.

Контракт Temporal-адаптера за портом `fulfillment` зафиксирован в [ADR 5](docs/adr/0005-order-fulfillment-orchestration.md) и разделе [Order Fulfillment Workflow](#order-fulfillment-workflow). OpenSpec change `implement-order-fulfillment-temporal-adapter` реализовал его на `go.temporal.io/sdk` v1.50.0: workflow с deadlines из `Policy`, replan и эскалацией Assignment, Saga и Update `CancellationRequested`; Activities поверх `completeorder`, `failorcancelorder` и client-портов `Payments`, `Restaurants`, `Delivery`; реализацию порта `fulfillment` на Temporal client. Значения `DefaultPolicy` — placeholders до продуктовых решений. OpenSpec change `extract-order-fulfillment-slice` выделил процесс в слайс [`fulfillment`](slices/fulfillment/README.md): правила — в доменной модели `domains/fulfillment` с табличными unit-тестами, workflow — тонкий адаптер [`infrastructure/temporal`](slices/fulfillment/infrastructure/temporal/README.md) с прежними именами workflow, Signals, Update, Activities, task queue, Search Attribute и idempotency key шагов.

OpenSpec change `add-orders-temporal-worker` добавил процесс `cmd/orders-worker`: Wire injector `InitializeWorker`, конфигурацию `temporal.*` и `fulfillment.*`, проверку и регистрацию Search Attribute `OrderFulfillmentStage` при старте, адаптеры соседей `unavailable` (по умолчанию) и `simulated` (теперь в `slices/fulfillment/infrastructure/neighbours`), values-worker.yaml Helm chart и integration-тест против Temporal dev server (`make test-integration`).

Реализован общий Kafka transport на Watermill: конфигурация brokers/client/group,
publisher с обязательным partition key `order_id`, subscriber consumer group и
единый mapping `event_id` в UUID/metadata.

OpenSpec change `add-orders-postgresql` перевёл API, worker и consumer на общий PostgreSQL store
([`slices/order/infrastructure/postgres`](slices/order/infrastructure/postgres)): Unit of Work,
repository с optimistic concurrency, Inbox, idempotency keys, read models, authorization revision
и Transactional Outbox с процессом `cmd/orders-forwarder`; миграции — `cmd/orders-migrate`.
Тесты, которым нужно хранилище, используют этот же store через `postgrestest` (Docker обязателен для `make test`).

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

Reconciliation `order#customer`, `order#restaurant` и `order#delivery` (relation `courier` не сверяется: её нет в schema) —
[`slices/order/infrastructure/reconcile`](slices/order/infrastructure/reconcile)
и процесс `cmd/orders-reconciler` (Helm `values-reconciler.yaml`): для каждой relation orphan и
stale relationships удаляются, только если остались такими на двух проходах с интервалом не
меньше `RECONCILER__GRACE` (связь пишется до commit); missing relationship восстанавливается
`TOUCH` сразу, но только если subject, перечитанный под `FOR SHARE` lock строки заказа, всё ещё
совпадает: Record Courier Assignment/Release держат row lock от чтения назначения до commit и
удаляют прежнего курьера в SpiceDB до commit, поэтому перечитывание ждёт их commit и не
возвращает доступ снятому курьеру. `TOUCH` и `authorization_revision` пишутся в той же транзакции;
несовпавшие считаются `missing_superseded`. Business-сторона — колонки `customer_id`,
`restaurant_id`, `courier_id`, `delivery_id`; заказ без курьера не ожидает `courier`/`delivery`. Каждый проход пишет в log `checked`, `orphans_pending`, `orphans_deleted`,
`missing_repaired`, `missing_superseded`, `unrepairable`. Query handlers зарегистрированы в DI и доступны
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
| `FULFILLMENT__<KEY>` | см. `config.example.yaml` | `Policy` workflow (placeholders), `PUBLISH_STAGE`, `NEIGHBOURS` (также выбирает catalog Place Order) |
| `CATALOG__MINIMUM_TOTAL`, `CATALOG__MAXIMUM_TOTAL`, `CATALOG__TIMEOUT` | `0`, `100000`, `2s` | Лимиты суммы Place Order в валюте меню (placeholders) и deadline чтения `GetRestaurant` + `GetMenu` |
| `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE`, `IDENTITY__JWKS_FILE` | —, `nomnom.orders`, — | Проверка входящего projected ServiceAccount token ([ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md)); без них все вызовы `UNAUTHENTICATED` |
| `IDENTITY__SERVICE_ACCOUNTS` | — | Allowlist `namespace/serviceaccount=service-name,…` |
| `IDENTITY__TOKEN_DIR` | — | Каталог projected tokens `<audience>` для вызовов соседей (worker; API — catalog `restaurants`, его чтения identity не требуют); пусто — без credentials |
| `IDENTITY__USER_ISSUER`, `IDENTITY__USER_JWKS_FILE`, `IDENTITY__USER_DELEGATES` | —, —, — | User assertion BFF ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md)): ожидаемый `iss`, JWKS BFF (только EC P-256) и services-делегаты через запятую (в развёртываниях `bff`). Без всех трёх любой вызов с assertion — `UNAUTHENTICATED`, service-вызовы не затронуты; неразбираемый JWKS — ошибка startup |
| `SPICEDB__ENDPOINT`, `SPICEDB__PRESHARED_KEY` | — | SpiceDB authorization ([`slices/order/infrastructure/spicedb`](slices/order/infrastructure/spicedb)): `order#view`/`cancel`, `customer#view_orders`, `TOUCH order#customer`/`restaurant` при оформлении, `order#delivery` в `orders-consumer`; обязателен для API и consumer: без endpoint они не стартуют |
| `RECONCILER__INTERVAL`, `RECONCILER__GRACE`, `RECONCILER__PAGE_SIZE` | `5m`, `10m`, `500` | `cmd/orders-reconciler`: сверка relations `order` SpiceDB ↔ PostgreSQL |
| `POSTGRES__DSN`, `POSTGRES__MAX_CONNS` | —, `10` | PostgreSQL orders (обязателен для всех процессов); локально — `make postgres-dev`, `make migrate` |
| `POSTGRES__MIN_CONNS`, `POSTGRES__MAX_CONN_LIFETIME`, `POSTGRES__MAX_CONN_LIFETIME_JITTER`, `POSTGRES__MAX_CONN_IDLE_TIME`, `POSTGRES__CONNECT_TIMEOUT`, `POSTGRES__HEALTH_CHECK_PERIOD` | `0`, `30m`, `5m`, `5m`, `5s`, `1m` | Лимиты пула pgx процесса ([Пулер PostgreSQL](#пулер-postgresql)); `MIN_CONNS` — 0 или 1, длительности положительные |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_ENDPOINT` | имя роли, —, — | Resource и OTLP export traces/metrics ([Observability](#observability)); Helm задаёт `telemetry.serviceName`, `telemetry.resourceAttributes`, `telemetry.otlpEndpoint` |
| `LOG__LEVEL` | `info` | Минимальный уровень JSON-логов: `debug`, `info`, `warn`, `error` (Helm `telemetry.logLevel`) |
| `SPICEDB__INSECURE`, `SPICEDB__TIMEOUT` | `false`, `2s` | Plaintext (только kind/локально) и deadline проверки; ошибка или timeout — отказ |

Проверка caller разделена по ответственности: [`jwks.go`](infrastructure/grpc/jwks.go) читает ключи,
[`service_token.go`](infrastructure/grpc/service_token.go) (`serviceTokenVerifier`) проверяет service token,
[`user_assertion.go`](infrastructure/grpc/user_assertion.go) (`userAssertionVerifier`) — user assertion и
делегатов, resolver в [`identity.go`](infrastructure/grpc/identity.go) соединяет их. Порядок: service token
(`authorization`, ADR 27) → без metadata `x-nomnom-user-assertion` результат `Subject{service}` → service
входит в `IDENTITY__USER_DELEGATES` → assertion: только ES256 с `kid` из JWKS BFF, `iss`, `aud` содержит
`IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s, обязательный `iat` и `exp - iat ≤ 60 s`, непустые `sub` и
`jti` → `Subject{user, sub}`. Повторный или пустой assertion и любое нарушение — `UNAUTHENTICATED`; принятие и
отказ пишутся в log с делегатом и `jti`, без самих tokens.

`FULFILLMENT__NEIGHBOURS=unavailable` — явный fallback: вызовы соседей завершаются retryable ошибкой, workflow ждёт на текущем шаге, catalog Place Order отвечает `UNAVAILABLE`; `simulated` одобряет вызовы без эффекта и предназначен только для локальной проверки (catalog не симулируется); `grpc` (по умолчанию, требует `INTERNAL_GATEWAY_ADDR`) вызывает командные API `payments`, `restaurants`, `delivery` через `INTERNAL_GATEWAY_ADDR` клиентами [`neighbours/grpcclient`](slices/fulfillment/infrastructure/neighbours/grpcclient). Подключение к gateway, projected token целевого контекста из `IDENTITY__TOKEN_DIR` (ADR 27) и сгенерированные клиенты соседей лежат в общем пакете [`pkg/gateway`](pkg/gateway) (Go-код генерируется `make proto-generate` из workspace-модулей до публикации в BSR): им пользуются и Activities fulfillment, и каталог Place Order, поэтому слайс `order` не зависит от слайса `fulfillment`; `UNAUTHENTICATED` и `PERMISSION_DENIED` клиент считает retryable.

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
