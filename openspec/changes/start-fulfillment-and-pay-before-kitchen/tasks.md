# Tasks

## 1. orders: старт по OrderPlaced

- [ ] 1.1 Порт `fulfillment.Starter` — `StartRequest{OrderID, EventID, OccurredAt}`; `temporal.Client.Start` — `SignalWithStartWorkflow` (бывший `StartFromOrderPlaced`), путь `ExecuteWorkflow` удалить (`FR-638`).
- [ ] 1.2 Use case `slices/order/applications/startfulfillment` с README: Inbox + чтение Order в одной UoW, `IgnoredLate` для финального Order, старт после commit и на дубликате, transient-ошибка порта; unit и PostgreSQL testcontainers (`AC-819`–`AC-821`, `AC-827`, `AC-828`).
- [ ] 1.3 Группа `orders` на `orders.v1.order-events`: `OrderPlaced` → `startfulfillment`, явные no-op `OrderCompleted` и `OrderCancelled`, маппер envelope; тесты handler и Kafka integration (`AC-822`).
- [ ] 1.4 `placeorder` без порта старта и `FulfillmentStarted`; `service.proto` — `reserved 3; reserved "fulfillment_started";`, gRPC-адаптер и тесты; `orderdi.Workflow` API — только `CancellationRequester`, `wire_gen.go` (`AC-508`, `AC-826`).

## 2. orders: оплата в Order и маршрут

- [ ] 2.1 Факт `payment_authorized` и `RecordPaymentAuthorization` в домене Order; `FulfillmentFact` в `orders.proto`; `order_details` и Change Notification (`FR-639`).
- [ ] 2.2 Use case `recordpaymentauthorization` с README и тестами (`AC-829`, `AC-830`).
- [ ] 2.3 Слайс `fulfillment`: `StepRecordPaymentAuthorization` в `route.go` (`RequestRestaurantOrder` после него), Activity `RecordPaymentAuthorization` и её регистрация; тесты маршрута, Saga и workflow (`FR-636`, `FR-637`, `AC-823`–`AC-825`).

## 3. restaurants: кухня после оплаты

- [ ] 3.1 Удалить `slices/restaurantorder/infrastructure/integration/order-placed.ts`, его маршрут в `di/consumer.ts`, тесты consumer'а `OrderPlaced`; подписку на `orders.v1.order-events` в конфиге и values `restaurants-consumer`; проверить, что `FulfillmentCoordinatorAccess` остаётся только у RPC.

## 4. BFF и UI

- [ ] 4.1 BFF: перегенерировать клиент `orders`; `ordertracking` — `StatusPaid`, `MilestonePaid`, `EndPaymentDeclined` для `payment_declined` и `payment_capture_declined`; schema, presenter, тесты; убрать `fulfillment_started` из теста checkout (`AC-831`, `AC-832`).
- [ ] 4.2 UI: шаг `PAID` с исполнителем «Оплата» в `flow-board/order-flow.tsx`, «Оплачен» в чипе; шаг и milestone в `view-order`; сообщение `PAYMENT_DECLINED`; i18n, generated types, моки и fixtures, тесты (`AC-833`–`AC-836`).

## 5. ops и документация

- [ ] 5.1 Комментарий `ops/kind/manifests/messaging/dead-letter-topics.yaml`; kind e2e `ops/kind/e2e/order-lifecycle`: статус `PAID` до появления Restaurant Order, Delay и Failure кухни (`AC-739`).
- [ ] 5.2 ADR 5 orders (§ Старт, компенсации, Consequences) и ADR 11 (`event_id`, Workflow ID, `SignalWithStart`); `docs/domain-events.md`, `docs/use-cases.md`, `docs/kitchen.md`, `docs/fsm.md`; README `orders`, `restaurants`, `bff`, `ui` и затронутых слайсов.

## 6. Проверка

- [ ] 6.1 Проверки каждого сервиса, `make buf-breaking` (ожидаемо падает на `reserved 3`), `openspec validate start-fulfillment-and-pay-before-kitchen --strict`; в kind прервать незавершённые workflow перед выкаткой worker'а, затем прогон e2e.
