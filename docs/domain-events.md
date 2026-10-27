# Domain Events и Integration Events

Документ описывает путь бизнес-факта от aggregate до Kafka и обратно: что такое Domain Event и Integration Event, какой слой за что отвечает, как строятся Event Envelope и Outbox record и как эволюционируют контракты. Решение зафиксировано в [ADR 25: Integration Event Publication](adr/0025-integration-event-publication.md). Документ дополняет [Outbox ADR](adr/0013-transactional-outbox-for-integration-event-publishing.md), [Inbox ADR](adr/0011-transactional-inbox-and-idempotent-event-processing.md), [FSM](fsm.md), [Unit of Work](unit-of-work.md), [application models](application-models.md) и [документацию событийного взаимодействия](event-driven-architecture.md).

На этом шаге фиксируются правила. Protobuf-схемы, mappers, порты и adapters не создаются.

## Термины

| Термин | Что это | Чем не является |
| --- | --- | --- |
| Domain Event | Внутренний бизнес-факт bounded context в domain types. Создаётся aggregate, накапливается в памяти и забирается через `PullEvents()` или аналог. | Kafka message, Protobuf, DTO, команда другому slice |
| Integration Event | Публичное Protobuf-сообщение о факте для других контекстов, например `nomnom.orders.v1.OrderCancelled`. Контракт принадлежит bounded context, маппинг реализует infrastructure. Это и есть Transport DTO исходящего события. | Сериализованный Domain Event или aggregate |
| Event Envelope | Метаданные Integration Event: identity, тип, ссылка на aggregate, время, trace и causation. Заполняется infrastructure. | Часть Domain Event |
| Outbox Record | Сериализованный Integration Event вместе с Event Envelope и техническим состоянием публикации в Outbox контекста. | Domain Event, сохранённый «на потом» |

Нормативные определения — в [Ubiquitous Language](ubiquitous-language.md#event-driven-architecture). Отдельного термина для «транспортного» события нет: Integration Event и есть его транспортная форма.

## Ответственность слоёв

```text
domains        → бизнес-факт (Domain Event)
applications   → решение: публиковать ли, какой факт, какие данные разрешены наружу
infrastructure → маппинг в Integration Event, Event Envelope, сериализация, Outbox, Kafka
```

| Слой | Делает | Не знает / не делает |
| --- | --- | --- |
| `domains/` | Создаёт Domain Event в domain method, когда факт значим; хранит события в памяти; отдаёт их через `PullEvents()`. | Topic, Protobuf package, версия схемы, headers, сериализация, `event_id`, Outbox, Kafka, Watermill |
| `applications/` | Забирает события после domain operation, решает, какие публикуются, передаёт их в порт Outbox внутри UoW. При необходимости собирает Application DTO исходящего события. | Сериализация, Protobuf, Kafka, Event Envelope |
| `infrastructure/` | Маппит вход порта в Integration Event, заполняет Event Envelope, сериализует, пишет Outbox Record в текущую транзакцию, публикует Outbox в Kafka. | Бизнес-решение о публикации; загрузка aggregate в publisher |

FSM не создаёт событие автоматически на каждый переход ([ADR 21](adr/0021-pure-fsm-transitions.md#domain-methods-и-events)): выбор Domain Event — явное решение domain method.

## Цепочка публикации

```text
order.Cancel(requestedAt, policy)
  → aggregate записывает OrderCancelled (Domain Event, в памяти)
handler (cancel use case)
  → UoW {
      load Order → domain method → save Order
      → events := order.PullEvents()
      → решение: публикуем OrderCancelled
      → Outbox port (Domain Event + aggregate ref)
          infrastructure mapper → nomnom.orders.v1.OrderCancelled
          → Event Envelope → serialize → Outbox Record в текущую транзакцию
    }
  → commit
Outbox publisher → Kafka → mark published
```

Пример handler'а в Go (иллюстрация, не реализация):

```go
err := h.uow.Do(ctx, func(ctx context.Context) error {
	order, err := h.orders.Get(ctx, cmd.OrderID)
	if err != nil {
		return err
	}
	if err := order.Cancel(cmd.RequestedAt, h.policy); err != nil {
		return err
	}
	if err := h.orders.Save(ctx, order); err != nil {
		return err
	}
	ref := AggregateRef{Type: "Order", ID: order.ID(), Version: order.Version()}
	return h.outbox.Add(ctx, ref, order.PullEvents()...)
})
```

- Порт Outbox находится в `applications/` и принимает domain types или Application DTO исходящего события; Protobuf в его сигнатуре нет.
- Handler передаёт в порт только публикуемые события. Domain Event, не переданный в порт, наружу не уходит.
- Событие, переданное в порт без зарегистрированного маппинга, — ошибка программы: порт возвращает ошибку, UoW откатывается. Молча пропускать такое событие нельзя.
- Если сценарий публикует факт, но aggregate его не записал (например, сценарий без доменного перехода), application не создаёт Domain Event вместо домена: это сигнал, что домену не хватает факта, либо что нужна Application DTO исходящего события ([application models](application-models.md#исходящие-события)).

### Порядок

Aggregate отдаёт события в порядке их записи. Outbox Records одной UoW вставляются в этом порядке и получают одну `aggregate_version` — версию, зафиксированную этим commit. Между UoW порядок задаёт возрастающая `aggregate_version`. Publisher сохраняет порядок вставки для одного partition key ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#publisher-и-ordering)).

Consumer, проверяющий `aggregate_version` ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md#ordering)), учитывает, что несколько событий одного commit имеют одинаковую версию: событие с равной версией не считается устаревшим только по этому признаку.

## Маппинг

Маппинг не обязан быть 1:1:

| Кардинальность | Пример |
| --- | --- |
| 1 → 0 | Domain Event используется только внутри контекста и не публикуется. |
| 1 → 1 | `OrderCancelled` → `nomnom.orders.v1.OrderCancelled`. |
| 1 → N | Один факт публикуется в двух формах, например v1 и v2 на период миграции, или как два события для разных audience. |
| N → 1 | Несколько Domain Events одной UoW сворачиваются в одно Integration Event. |

Структуры Domain Event и Integration Event не обязаны совпадать: поля переименовываются, value objects разворачиваются в контрактные типы (`Money` → `google.type.Money`, `OrderID` → строка), внутренние поля не публикуются. Кардинальность и соответствие полей решаются только в mapper и фиксируются в SDD сценария.

Правила mapper:

- чистая функция от входа порта и уже известных значений Event Envelope; без хранилищ, сетевых вызовов, часов и random;
- не загружает aggregate и не добавляет данных, которых нет во входе порта; данные сверх Domain Event приходят через Application DTO исходящего события;
- размещается в `infrastructure/` рядом с messaging adapter slice; глобальный mapper layer, общий для контекста, и reflection-based automapping не используются ([ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md#mapping));
- покрывается unit tests: Domain Event → Integration Event для каждой поддерживаемой версии контракта.

## Event Envelope

Логический состав соответствует записи Outbox из [ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#запись-outbox):

| Поле | Источник |
| --- | --- |
| `event_id` | Вычисляется infrastructure при создании Outbox Record, см. [`event_id`](#event_id). |
| `event_type` | Полное имя Protobuf-сообщения контракта, например `nomnom.orders.v1.OrderCancelled`. |
| `occurred_at` | Время факта из Domain Event, переданное в domain method значением ([ADR 21](adr/0021-pure-fsm-transitions.md#time-random-и-identifiers)); не время записи Outbox. |
| `aggregate_type`, `aggregate_id`, `aggregate_version` | Aggregate ref, переданная handler'ом в порт Outbox. |
| `payload` | Сериализованный Integration Event. |
| `metadata` | W3C trace context, `correlation_id`, `causation_id` (`event_id` входящего события, если запись создана его обработчиком). Берутся из `ctx`. |

Infrastructure заполняет весь envelope; domain и application его не видят. Protobuf-схема envelope и размещение полей в Kafka headers или payload пока не выбраны ([открытые вопросы](event-driven-architecture.md#открытые-вопросы-реализации)). `event_id` в любом случае передаётся в Kafka header `event_id`.

## `event_id`

По умолчанию `event_id` детерминирован: UUIDv5 в namespace проекта от `(aggregate_type, aggregate_id, aggregate_version, event_type)`, где `event_type` — полное имя Protobuf-сообщения. Его вычисляет infrastructure при создании Outbox Record; повторная публикация его не меняет ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#стабильность-event_id)).

- Иной способ — только явно, в SDD сценария. Он нужен, когда кортеж не уникален (одна UoW публикует для одного aggregate несколько событий одного `event_type`) или неприменим (событие без aggregate).
- Конфликт уникальности `event_id` при записи — ошибка: UoW откатывается.
- При маппинге 1 → N и при dual publish v1 и v2 у событий разный `event_type`, поэтому и разный `event_id`. События разных типов одного commit (например, `RestaurantOrderAccepted` и `RestaurantOrderQueued`) не конфликтуют.

Детерминированный `event_id` не заменяет идемпотентность операции: повтор, выполненный против уже изменённого aggregate, получит новую `aggregate_version` и новый `event_id`. Поэтому операция, пишущая Outbox, по-прежнему идемпотентна по idempotency key или ожидаемой `aggregate_version`.

## Содержание события

По умолчанию Integration Event **state-carrying**: несёт факт и данные, которые нужны consumers для реакции без обратного вызова publisher. Событие, которое несёт только `order_id` и вынуждает consumer запрашивать `orders`, является исключением и требует обоснования в SDD. Конкретный состав полей, например `nomnom.orders.v1.OrderCancelled`, определяется SDD сценария, а не этим документом.

Минимизация:

- payload содержит только данные, которые нужны известным consumers;
- PII (адрес, телефон, имя Customer) публикуется только при подтверждённой потребности consumer и с явным обоснованием в SDD;
- внутренние поля aggregate, persistence-модель, технические флаги и `Workflow Stage` не публикуются.

Состав payload — часть контракта: он фиксируется в SDD и меняется по правилам [эволюции](#versioning-и-эволюция).

## Outbox и publisher

```text
UoW {
  persist business state
  insert Outbox Record (envelope + serialized Integration Event)
}
commit
Outbox publisher: read record → Kafka → mark published
```

- В Outbox хранится transport-ready событие. Маппинг и сериализация выполняются до commit, внутри UoW: ошибка маппинга откатывает бизнес-изменение, а не обнаруживается publisher'ом позже.
- Business state и Outbox Record — одна Unit of Work ([ADR 20](adr/0020-unit-of-work-for-local-transactions.md#inbox-и-outbox)).
- Publisher читает готовую запись, публикует её и фиксирует результат. Он не загружает aggregate, не видит Domain Events и не выполняет domain mapping; в `orders` это Watermill Forwarder ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#orders-go-postgresql-и-watermill)).

## Domain Events внутри контекста

| Реакция | Механизм |
| --- | --- |
| Другой slice обновляет локальный state того же хранилища | Синхронно, in-process, в той же UoW, без внешних вызовов. Ошибка обработчика откатывает всю UoW. |
| Реакция за пределами транзакции: другой контекст, Temporal, SpiceDB, внешний провайдер | Integration Event → Outbox → Kafka → Inbox. |
| Реакция контекста на собственный факт за пределами транзакции | Тоже Integration Event + Inbox. Пример — старт fulfillment workflow по собственному `OrderPlaced` через `SignalWithStart` ([ADR 5 orders](../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md#старт)). |

Запрещены:

- асинхронные in-process обработчики Domain Events (goroutine, event emitter, очередь в памяти);
- dispatcher, выполняющий обработчики Domain Events после commit: сбой процесса между commit и обработкой теряет реакцию;
- внешние вызовы из in-process обработчика Domain Event ([UoW: только локальное хранилище](unit-of-work.md#только-локальное-хранилище)).

Явное действие самого use case после commit через порт — Signal или start Temporal workflow ([UoW и Temporal](unit-of-work.md)) — обработкой Domain Event не является и остаётся по правилам ADR 6 и ADR 20.

## Входящие события

```text
Kafka → Transport DTO (proto) → infrastructure mapper → Command → handler → domain
```

- Consumer оборачивается Inbox ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md)); Inbox, business state и Outbox — одна UoW.
- Mapper входящего события находится в `infrastructure/` consumer'а и переводит Integration Event соседа в Command своего контекста, например `MarkRestaurantAcceptedCommand`.
- Transport DTO и Integration Event соседа не передаются в domain и не превращаются в Domain Event consumer'а напрямую: Domain Event появляется только как результат domain method.

## Versioning и эволюция

Версионируется контракт Integration Event, а не Domain Event. Domain Event — внутренний код и меняется вместе с моделью; mapper сохраняет контракт неизменным.

| Изменение | Правило |
| --- | --- |
| Новое поле | Additive: optional, без смены смысла существующих полей. |
| Удаление или переименование поля, смена типа или смысла | Breaking: новая major-версия package `nomnom.orders.v2.*`. |
| Удалённое поле в рамках версии | Номер поля и имя резервируются (`reserved`), не переиспользуются. |
| Проверка совместимости | `buf breaking` против последнего опубликованного commit BSR-модуля ([ADR 7](adr/0007-grpc-contracts-and-buf-schema-registry.md)). |

Breaking change проходит так:

1. Новая версия контракта публикуется в BSR рядом с прежней.
2. Mapper строит обе версии; publisher выполняет dual publish v1 и v2 — два Integration Event с разными `event_type` и `event_id` в одной UoW.
3. Consumers переходят на v2; срок вывода v1 фиксируется в SDD изменения.
4. После срока mapper перестаёт строить v1.

Topic и партиционирование новой версии определяются вместе с размещением event-схем в Buf.

## Сравнение

| Свойство | Domain Event | Integration Event |
| --- | --- | --- |
| Owner | Domain model контекста | Bounded context (контракт), infrastructure (маппинг) |
| Audience | Внутри контекста | Другие контексты и собственные асинхронные реакции |
| Types | Domain types | Protobuf |
| Versioning | Внутреннее, вместе с кодом | Публичное, additive-first, major-версия при breaking change |
| Kafka / serialization | Нет | Да |
| Side effects | Нет | Доставка через infrastructure |
| Outbox | Не сохраняется | Сохраняется как Outbox Record |

## Размещение в коде

```text
<slice>/
  domains/<domain>/<domain>.<ext>   — aggregate, Domain Events, PullEvents()
  applications/<usecase>/           — handler, решение о публикации, порт Outbox,
                                      [Application DTO исходящего события]
  infrastructure/messaging/         — mappers исходящих и входящих событий,
                                      Kafka consumers, реализация порта Outbox slice
```

Имена каталогов внутри `infrastructure/messaging/` следуют идиомам языка контекста. Общие компоненты Outbox и Event Envelope выносятся в shared infrastructure boundary контекста только при реальном переиспользовании ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md#vsa-и-ddd)). Размещение generated-кода event-схем определяется вместе с размещением схем в Buf.

## Тестирование

- Domain: domain method записывает ожидаемый Domain Event, `PullEvents()` очищает буфер — без инфраструктуры.
- Mapper: unit tests Domain Event / Application DTO → Integration Event для каждой поддерживаемой версии, включая отсутствие PII и внутренних полей.
- Контракт: `buf lint` и `buf breaking` в CI.
- Outbox: integration tests с реальным хранилищем — state и Outbox Record в одной транзакции, rollback при ошибке маппинга, fault scenarios из [документации событийного взаимодействия](event-driven-architecture.md#тестирование).

## Каталог кандидатов: `restaurants` / Kitchen

Кандидаты для aggregate `RestaurantOrder`, не окончательный контракт. Бизнес-смысл, payload и сверка с сигналами Temporal — в [Kitchen](kitchen.md#события).

| Domain Event | Маппинг | Integration Event `nomnom.restaurants.v1.*` | Consumer |
| --- | --- | --- | --- |
| `RestaurantOrderReceived` | 1 → 0 | — | — |
| `RestaurantOrderAccepted` | 1 → 1 | `RestaurantOrderAccepted` | `orders` |
| `RestaurantOrderRejected` | 1 → 1 | `RestaurantOrderRejected` | `orders` |
| `RestaurantOrderQueued` | 1 → 0 | — | — |
| `PreparationStarted` | 1 → 1 | `PreparationStarted` | `orders` |
| `PreparationItemStarted`, `PreparationItemCompleted` | 1 → 0 | — | — |
| `RestaurantOrderAssembled` | 1 → 0 | — | — |
| `RestaurantOrderReady` | 1 → 1 | `RestaurantOrderReady` | `orders`; потенциально `delivery` |
| `RestaurantOrderPickedUp` | 1 → 0 | — | — (Record Pickup по `DeliveryPickedUp`; заказ уходит с kitchen board) |
| `PreparationDelayed` | 1 → 1 | `PreparationDelayed` | `orders`; потенциально `delivery` |
| `RestaurantOrderCancelled` | 1 → 1 | `RestaurantOrderCancelled` | `orders` (подтверждение синхронного результата) |
| `RestaurantOrderFailed` | 1 → 1, переименование в mapper | `PreparationFailed` | `orders` |

- Публикуются только факты, нужные `orders` и `delivery`; кухонная детализация (очередь, позиции, Assembly) остаётся внутри контекста.
- В MVP Accept Order и Queue Restaurant Order выполняются одной UoW: aggregate записывает `RestaurantOrderAccepted` и `RestaurantOrderQueued`, handler передаёт в Outbox port только первый.
- Повтор команды, который FSM возвращает как `AlreadyInState` и application превращает в no-op, не записывает Domain Event и не создаёт Outbox Record.
- Имена `PreparationStarted`, `PreparationDelayed`, `PreparationFailed` не следуют форме `<Aggregate><Fact>` из [словаря](ubiquitous-language.md#грамматика-именования); окончательные имена выбираются при создании контракта.

## Каталог: `restaurants` / Restaurant и Menu

Опубликованные контракты aggregates `Restaurant` и `Menu` (`nomnom/restaurants/v1/events.proto`, change [`publish-restaurant-and-menu-events`](../openspec/changes/archive/2026-10-25-publish-restaurant-and-menu-events/)). Факты передаются в Outbox port application-слоем; у каждого aggregate свой стрим, key — `aggregate_id`.

| Domain Event | Маппинг | Integration Event `nomnom.restaurants.v1.*` | Topic | Consumer |
| --- | --- | --- | --- | --- |
| `RestaurantRegistered`, `RestaurantRenamed` | 1 → 0 | — | — | — |
| `RestaurantOpened` | 1 → 1 | `RestaurantOpened` (`restaurant_id`) | `restaurants.v1.restaurant-events` | `restaurants-consumer` (кеш каталога, ADR 43) |
| `RestaurantClosed` | 1 → 1 | `RestaurantClosed` (`restaurant_id`) | `restaurants.v1.restaurant-events` | `restaurants-consumer` (кеш каталога, ADR 43) |
| `MenuCreated` | 1 → 0 | — | — | — |
| `MenuItemAdded` | 1 → 1 | `MenuItemAdded` (`menu_id`, `restaurant_id`, `menu_item_id`) | `restaurants.v1.menu-events` | `restaurants-consumer` (кеш GetMenu, ADR 43) |
| `MenuItemUpdated` | 1 → 1 | `MenuItemUpdated` (`menu_id`, `restaurant_id`, `menu_item_id`) | `restaurants.v1.menu-events` | `restaurants-consumer` (кеш GetMenu, ADR 43) |
| `MenuItemAvailabilityChanged` | 1 → 1, `isAvailable` → `available` | `MenuItemAvailabilityChanged` (`menu_id`, `restaurant_id`, `menu_item_id`, `available`) | `restaurants.v1.menu-events` | `restaurants-consumer` (кеш GetMenu, ADR 43) |

- `MenuItemAvailabilityChanged` записывают и Set Menu Item Availability, и Update Menu Item при изменении доступности, чтобы факт не зависел от команды.
- `MenuItemAdded` и `MenuItemUpdated` несут только ключи: новые значения читаются через GetMenu. Update Menu Item, изменивший и данные, и доступность, записывает оба факта.
- Повтор Open/Close Restaurant — no-op без Domain Event.
- Назначение и отзыв staff — relationships SpiceDB, Domain Events не порождают.

## Каталог кандидатов: `orders`, `delivery`, `payments`

Domain Events, которые application-слой передаёт в Outbox port (changes `implement-{orders,delivery,payments}-application-layer`). Integration Event контракты, имена в `nomnom.<context>.v1.*` и consumers ещё не определены; маппинг по умолчанию — кандидат 1 → 1.

| Context | Domain Event | Use case | Примечание |
| --- | --- | --- | --- |
| `orders` | `OrderPlaced` | Place Order | Опубликован как `nomnom.orders.v1.OrderPlaced` с составом `items` (`menu_item_id`, `quantity`, `unit_price`). Consumer `restaurants-consumer` создаёт по нему `RECEIVED` Restaurant Order через Inbox (Receive Order Request, идемпотентно по `order_id` вместе с RPC `ReceiveOrderRequest`). Основной старт fulfillment workflow — consumer собственного `OrderPlaced` + `SignalWithStart` (не реализован); сейчас workflow запускается портом после commit |
| `orders` | `OrderCompleted` | Complete Order | |
| `orders` | `OrderCancelled` | Fail or Cancel Order | Содержит вид итога (Cancellation или Failure) и код причины |
| `delivery` | `CourierAssignedToDelivery` | Accept Delivery Offer | Источник relation `delivery#courier` по [security](security.md#relationship-lifecycle) |
| `delivery` | `CourierUnassignedFromDelivery` | Reassign Courier | Опубликован как `nomnom.delivery.v1.CourierUnassignedFromDelivery`; `orders` удаляет `order#courier` (Record Courier Release) |
| `delivery` | `DeliveryPickedUp` | Confirm Pickup | Опубликован как `nomnom.delivery.v1.DeliveryPickedUp`; `orders` записывает Pickup, `restaurants-consumer` переводит `READY` Restaurant Order в `PICKED_UP` (Record Pickup, Inbox `restaurants.delivery-picked-up`) |
| `delivery` | `DeliveryCompleted` | Confirm Delivery | |
| `delivery` | `DeliveryFailed` | Report Delivery Failure | Опубликован как `nomnom.delivery.v1.DeliveryFailed`: `reason` — код каталога Delivery Failure Reason, `detail` — необязательное пояснение; retryability не классифицируется |
| `delivery` | `DeliveryOfferExpired` | Expire Delivery Offers | Внутренний: не публикуется, только метрика `nomnom.delivery.offers{nomnom.outcome="expired"}` |
| `delivery` | `DeliveryCancelled` | Cancel Delivery | Опубликован как `nomnom.delivery.v1.DeliveryCancelled` (пустой `reason` — не указана); `orders` удаляет `order#courier` и `order#delivery` (Record Courier Release) |
| `payments` | `PaymentAuthorized`, `PaymentFailed` | Authorize Payment | Отказ Authorization записывает `PaymentFailed` и `PaymentOperationDeclined` |
| `payments` | `PaymentCaptured` | Capture Payment | |
| `payments` | `PaymentAuthorizationCancelled` | Cancel Authorization | |
| `payments` | `PaymentRefunded` | Refund Payment | |
| `payments` | `PaymentOperationDeclined` | Все финансовые операции, Apply Provider Callback, Apply Provider Operation Result | Отказ Provider без смены State `Payment` (кроме Authorization) |

- Факты `orders` входящих сценариев (Fulfillment Fact) не создают Domain Event: они отражаются в `Order` и передаются workflow.
- Create Payment событие не публикует.

## Открытые вопросы

- Protobuf-схема Event Envelope и размещение metadata: Kafka headers или payload.
- Размещение event-схем в Buf, topics и naming topics для major-версий.
- Механизм проверки, что каждый публикуемый Domain Event имеет mapper (тест полноты или регистрация mapper'ов).
