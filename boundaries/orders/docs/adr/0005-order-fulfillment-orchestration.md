# 5. Order Fulfillment Orchestration

Date: 2026-10-25

## Status

Accepted

Уточняет [2. Использовать Temporal для оркестрации заказов](0002-use-temporal-to-orchestrate-orders.md)

Связан с [4. Использование CQRS в orders](0004-использование-cqrs-в-orders.md) и общесистемными ADR [11. Transactional Inbox and Idempotent Event Processing](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md), [13. Transactional Outbox for Integration Event Publishing](../../../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md), [15. Internal gateway](../../../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md), [17. Cloud Native Application Runtime](../../../../docs/adr/0017-cloud-native-application-runtime.md) и [18. SpiceDB for Authorization](../../../../docs/adr/0018-spicedb-for-authorization.md)

## Context

[ADR 2](0002-use-temporal-to-orchestrate-orders.md) выбирает Temporal, но не определяет границу его применения, identity workflow, безопасный старт после оформления `Order`, протокол входящих событий, компенсации и разделение состояния `Order` и workflow. Без этого реализация может хранить заказ в Event History, использовать Temporal для CRUD или event delivery, повторять финансовые и компенсационные эффекты при at-least-once доставке.

Application-слой `orders` уже видит workflow только через порт `slices/order/applications/fulfillment` (`Starter`, `Notifier`, `CancellationRequester`). Этот ADR задаёт контракт Temporal-адаптера за портом. Требования и сценарии — в [спецификации `order-fulfillment-orchestration`](../../../../openspec/specs/order-fulfillment-orchestration/spec.md).

## Decision

### Scope Temporal

Temporal применяется только к многошаговому процессу одного `Order`, который содержит внешние зависимости, ожидания, retries, бизнес-таймеры или компенсации и должен переживать перезапуск worker. Операция, укладывающаяся в `request → transaction → response`, выполняется без workflow.

Temporal не используется для:

- CQRS queries — клиентские чтения (`GetOrder`, `ListCustomerOrders`) обслуживает read model по [ADR 4](0004-использование-cqrs-в-orders.md);
- локальных транзакций и хранения агрегатов — `Order` хранится в PostgreSQL `orders`;
- доставки событий между bounded contexts — это Outbox → Kafka → Inbox ([ADR 11](../../../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md), [ADR 13](../../../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md));
- authorization — решения принимает SpiceDB в защищённом use case владельца ([ADR 18](../../../../docs/adr/0018-spicedb-for-authorization.md));
- универсальных фоновых заданий и job queue.

### Identity и топология

Один Order Fulfillment Workflow на один логический `Order`, без child workflows. Workflow ID — `order:<order_id>`, namespace — отдельный на окружение, task queue — `orders`. Повторный старт для того же `Order` адресует существующий workflow (`USE_EXISTING`). Будущая декомпозиция допустима только с сохранением этого внешнего identity.

Процесс — отдельная бизнес-возможность со своей моделью, поэтому он выделен в слайс `fulfillment` (OpenSpec change `extract-order-fulfillment-slice`): правила принимает доменная модель `slices/fulfillment/domains/fulfillment` без Temporal SDK, а workflow, Activities и client — адаптер `slices/fulfillment/infrastructure/temporal`. Порт workflow остаётся в слайсе `order` (`slices/order/applications/fulfillment`), `order` не импортирует `fulfillment`. Если workflow начнёт охватывать несколько слайсов помимо `order`, он переносится в `boundaries/orders/infrastructure/temporal/` по [общесистемному ADR 6](../../../../docs/adr/0006-ddd-structure-inside-vertical-slices.md#входы-в-application).

### Старт

Основной путь: Place Order атомарно сохраняет `Order` и `OrderPlaced` в Outbox; consumer `orders` получает собственное событие через Kafka и Inbox и выполняет `SignalWithStart(order:<order_id>, OrderPlaced, USE_EXISTING)`. Порядок start и первого Signal становится несущественным, а crash между commit и стартом не теряет процесс.

Синхронный старт после commit (текущий `fulfillment.Starter` в `placeorder`) допустим как latency optimization: он повторяется с тем же Workflow ID и не заменяет `OrderPlaced` в Outbox. Старт до commit запрещён.

### Входящие события и идемпотентность

Факты `restaurants`, `delivery` и `payments` проходят путь `Kafka → orders consumer → Inbox → application handler → Signal/Update`. Соседние контексты не вызывают Temporal напрямую.

Дедупликация двухуровневая: Inbox по `(consumer, event_id)` защищает локальные эффекты consumer; workflow хранит обработанные `event_id` и превращает повтор Signal RPC в no-op. Имя Signal совпадает с именем Integration Event; перевод `fulfillment.Kind` в имя Signal выполняет адаптер.

Положительное событие, которое текущий Workflow Stage не ожидает, логируется и игнорируется без падения workflow. Отрицательные факты (`RestaurantOrderRejected`, `PreparationFailed`, `DeliveryFailed`, `PaymentFailed`) в незавершённом workflow молча не игнорируются: они ведут в Saga или в эскалацию с диагностикой.

### Signal, Update, Query

- Signal — асинхронный факт без ответа.
- Update — команда, которой нужен результат validation относительно Workflow Stage; клиентская отмена — Update `CancellationRequested`. Допустимость Cancellation решает домен `Order`, компенсации — workflow.
- Query — только техническая диагностика.
- Temporal `CancelWorkflow` — аварийная операция и не моделирует Order Cancellation.

### Activities

Workflow детерминирован и не выполняет gRPC, HTTP, DB, Kafka, SpiceDB или иных side effects — только через Activities. Activity получает сериализуемый input с `order_id`, логическим шагом и idempotency key; ключ финансовых и компенсационных операций — `order_id + step`, не зависит от attempt и run ID.

Activity, меняющая `orders`, вызывает handler слайса, который атомарно пишет `Order` и Outbox; прямой publish в Kafka запрещён. Вызов API другого контекста идёт через `INTERNAL_GATEWAY_ADDR` от `service` identity worker'а ([ADR 15](../../../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md), [ADR 18](../../../../docs/adr/0018-spicedb-for-authorization.md)); целевой контекст авторизует эту identity сам.

Каждая Activity имеет явные Start-To-Close timeout и Retry Policy; длительные — heartbeat. Business rejection возвращается результатом, не technical retry.

### Ожидания и таймеры

Ожидание ресторана, курьера и доставки — Signal плюс Temporal timer; polling запрещён. Timer выражает продуктовый срок и ведёт к бизнес-ветке (replan, escalation, cancellation, compensation). Workflow-level timeout не задаётся. `PreparationDelayed` продлевает таймер ожидания Preparation.

### Saga

Компенсации — orchestration-based Saga. Workflow ведёт журнал подтверждённых шагов и вызывает только применимые compensation Activities (`CancelRestaurantOrder`, `CancelPaymentAuthorization`, `RefundPayment`), каждая со стабильным idempotency key, timeout, retry policy и telemetry.

### State ownership

| Что | Владелец |
| --- | --- |
| Order State (`placed`, `confirmed`, `cancelled`, `completed`), Fulfillment Facts, инварианты | `Order` в PostgreSQL `orders` |
| Workflow Stage: стадия, результаты шагов, обработанные `event_id`, таймеры, журнал compensation | Temporal Event History |
| Доставка интеграционных событий | Kafka + Outbox/Inbox |
| Клиентские чтения | CQRS read model |
| Authorization | SpiceDB в use case владельца |

Workflow не читает БД. Stage публикуется низкокардинальным Search Attribute и не становится Order State.

### Runtime

API, Kafka consumer и Temporal worker — управляемые процессы по [ADR 17](../../../../docs/adr/0017-cloud-native-application-runtime.md). Worker при shutdown прекращает poll, даёт Activities завершиться, закрывает clients и flush telemetry. Crash worker не является business failure и не запускает compensation.

### Отложенные продуктовые решения

Этот ADR не выбирает следующие решения; каждое принимается отдельным SDD change до реализации соответствующего шага:

- продуктовые timeout Order Acceptance, Assignment, OperatorDecision, Preparation, Pickup и Delivery Completion и предел продления через `PreparationDelayed`;
- момент Authorization/Capture и условия Refund вместо отмены Authorization;
- параллельный запуск Assignment относительно Acceptance или Preparation;
- policy каждого события, пришедшего после завершения workflow;
- допускает ли `restaurants` отказ после Acceptance (workflow готов его принять);
- Continue-As-New threshold и retention обработанных `event_id`.

Логическая схема `Order placed → Authorize → Acceptance → Assignment → Preparation → Pickup → Capture → Delivery Completion → Completion` — карта coordination points, а не утверждённая последовательность.

## Consequences

Старт, входящие события, внешние эффекты и компенсации безопасны при at-least-once повторах и crash, а ответственность `payments`, `restaurants` и `delivery` не переходит в `orders`. `Order` остаётся source of truth и доступен без Temporal.

Цена: два уровня дедупликации, consumer собственного `OrderPlaced`, рост Event History на длинных заказах (потребуется Continue-As-New), versioning workflow code и replay tests в CI до первого production rollout. Откат worker допустим только к версии, совместимой с записанными histories.
