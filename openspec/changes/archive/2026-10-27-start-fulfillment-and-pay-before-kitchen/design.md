# Design

## Start Fulfillment

Use case `slices/order/applications/startfulfillment`, consumer `orders.startfulfillment` в группе `orders` на `orders.v1.order-events`.

1. `AcceptOnce` Inbox по `(consumer, event_id)` и чтение Order — одна UoW.
2. Order в `completed` или `cancelled` — исход `IgnoredLate`, сигнал не отправляется: если retention namespace уже удалил историю закрытого workflow, `SignalWithStart` создал бы его заново (redrive DLQ, сброс offset).
3. Иначе после commit — `Starter.Start(StartRequest{OrderID, EventID, OccurredAt})`. Адаптер выполняет `SignalWithStartWorkflow(order:<order_id>, OrderPlaced, fact)` с `USE_EXISTING` и `REJECT_DUPLICATE`; `AlreadyStarted` — успех. Это нынешний `StartFromOrderPlaced`, переведённый на порт; путь через `ExecuteWorkflow` удаляется.
4. Дубликат Inbox тоже отправляет сигнал после commit (как Reflector): так лечится сбой между commit и `SignalWithStart`. Повтор в работающий workflow отсекает дедупликация по `event_id`.
5. Ошибка Temporal — transient: сообщение не подтверждается и доставляется Kafka повторно с backoff, в DLQ не уходит (ADR 28).

`OrderCompleted` и `OrderCancelled` того же топика группа `orders` подтверждает явными no-op handlers. Без них `rejectUnknownEvents` отправил бы их в `orders.v1.order-events.dlq`.

Workflow начинает маршрут при старте и `OrderPlaced` не ждёт; сигнал — подтверждение без изменения маршрута, как сейчас.

### Rollout

Группа `orders` впервые читает `orders.v1.order-events` с `KAFKA__INITIAL_OFFSET: oldest` и обработает весь retained `OrderPlaced`: завершённые заказы пропускаются, активные получают лишний сигнал (no-op), заказы без workflow запускаются — это и есть восстановление. Повтор `AuthorizePayment` защищён ключом `order:<id>:authorize_payment`.

## Кухня после оплаты

Restaurant Order создаёт только RPC `ReceiveOrderRequest` из Activity `RequestRestaurantOrder`, которая уже передаёт `restaurant_id`, `order_id` и позиции. Шаг идёт после `AuthorizePayment` и `RecordPaymentAuthorization`, поэтому:

- `RestaurantOrderAccepted` и остальные факты кухни не могут опередить свой шаг: кухня не видит заказ, пока workflow его не запросил;
- при отказе оплаты Restaurant Order не создаётся, и Saga нечего отменять;
- факты доставки и так идут после `CreateDelivery`.

Consumer `OrderPlaced` в `restaurants` удаляется вместе с маршрутом в `di/consumer.ts`; Inbox `restaurants` и процесс `restaurants-consumer` остаются для `delivery.v1.delivery-events` и обновления кеша каталога. Исторические записи Inbox с consumer `OrderPlaced` истекают по TTL.

Цена: кухня видит заказ позже на время авторизации (синхронный вызов `payments`, обычно доли секунды; дольше — при перезапуске Kafka Connect, потому что старт теперь идёт через Debezium и Kafka).

## Оплата в Order

- Fulfillment Fact `payment_authorized` в `slices/order/domains/order/fact.go`; `RecordPaymentAuthorization(occurredAt)` записывает факт в `placed`; повтор — `FactAlreadyRecorded`, который use case превращает в успешный no-op.
- Use case `recordpaymentauthorization` вызывается Activity `RecordPaymentAuthorization` worker'а (ADR 5: Activity, меняющая `orders`, идёт через handler слайса). Он записывает факт, обновляет `order_details` и Change Notification `orderChanged` в одной транзакции. Integration Event не публикуется: соседним контекстам факт не нужен.
- Шаг `StepRecordPaymentAuthorization` в `route.go`: `Next` выдаёт его после `authorized`, `Confirm` ставит `paymentRecorded`; `RequestRestaurantOrder` ждёт `paymentRecorded`. Компенсации у шага нет: факт остаётся в Order как история.
- `orders.proto`: факт `payment_authorized` в `FulfillmentFact`.

## Статус `PAID` в BFF

`OrderStatus` — клиентское представление: BFF вычисляет его в `ordertracking` из состояния Order и фактов, как `PREPARING`. Состояние FSM `payment_authorized` не вводится: факт — данные Order, ожидание — стадия workflow (`fact.go`). Поэтому окно отмены (`placed`) и Saga не меняются.

| Состояние Order | Факты | `status` |
| --- | --- | --- |
| `placed` | нет `payment_authorized` | `RECEIVED` |
| `placed` | `payment_authorized` | `PAID` |
| `confirmed` и далее | как сейчас | `CONFIRMED` … `DELIVERED` |

- Milestone `PAID` — момент факта `payment_authorized`.
- `OrderEndReason.PAYMENT_DECLINED` — для reason code `payment_declined` и `payment_capture_declined`; сейчас они попадают в `NOT_FULFILLED`.

## UI

- Флоу-борд, `OrderTimeline`: шаги `RECEIVED → PAID → CONFIRMED → PREPARING → READY_FOR_PICKUP → PICKED_UP → DELIVERED`; actor шага `PAID` — «Оплата», а не колонка доски: действие выполняет система. Чип заказа показывает «Оплачен».
- Экран заказа: тот же шаг в прогрессе и milestone «Оплачен» в истории; `PAYMENT_DECLINED` — локализованное сообщение без обещаний о возврате денег (FR-457).
- Обновление — subscriptions `orderChanged` (ADR 36), без polling.

## Риски

- Код workflow меняется (новый шаг маршрута): replay незавершённых histories несовместим. Versioning не ведётся; в kind перед выкаткой worker'а прервать незавершённые workflow.
- `RequestOrderCancellation` в окне между commit и стартом workflow получает `ErrWorkflowNotFound` → `UNAVAILABLE`; клиент повторяет запрос.
- Удаление поля proto ломает `buf breaking` против main один раз; потребитель — только BFF, он меняется в этом change.
