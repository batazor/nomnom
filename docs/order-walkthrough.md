# Путь одного заказа

Что происходит от нажатия «Оформить» до доставки: кто что делает и где это лежит в коде. Показан основной маршрут в Market `berlin`, без отказов. Схема того же пути — `placeOrder` в [модели архитектуры](architecture/README.md) (`make arch-dev`).

Прочитав документ, вы увидите три механизма, на которых держится система: синхронные вызовы через internal gateway, события через Outbox и Kafka и оркестрацию в Temporal.

## 1. Клиент оформляет заказ

[Доска заказа](../boundaries/ui/src/features/flow-board/) отправляет GraphQL-мутацию `placeOrder` на `/m/berlin/graphql`. Публичный gateway по маршруту Market, который построил [control plane](../boundaries/controlplane/README.md), снимает префикс, ставит заголовок `X-Nomnom-Market` и передаёт запрос в Cosmo Router своей Cell ([ADR 41](adr/0041-cell-based-architecture-with-control-plane.md)).

Router проверяет access token, а [plugin BFF](../boundaries/bff/plugins/bff/slices/checkout/applications/placeorder.go) вызывает `orders` через internal gateway и подписывает user assertion ([ADR 32](adr/0032-user-identity-via-bff-signed-assertions.md)). Прав и инвариантов BFF не решает: это дело владельца операции.

## 2. `orders` создаёт заказ

Use case [Place Order](../boundaries/orders/slices/order/applications/placeorder/README.md) читает каталог `restaurants` через gateway, а затем одной транзакцией пишет `Order`, запись для синхронизации связей SpiceDB и `OrderPlaced` в Outbox ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md)). Клиент получает ответ. Весь дальнейший процесс идёт асинхронно.

## 3. Событие запускает Fulfillment

Debezium читает Outbox и публикует `OrderPlaced` в `orders.v1.order-events` ([ADR 37](adr/0037-outbox-delivery-via-debezium-cdc.md), [каталог событий](events-catalog.md)). `orders-consumer` читает собственное событие и через [Start Fulfillment](../boundaries/orders/slices/order/applications/startfulfillment/README.md) стартует workflow `order:<order_id>` командой `SignalWithStart`. Повтор доставки безопасен: Inbox и уже запущенный workflow не дают сделать работу дважды ([ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md)).

## 4. Workflow ведёт заказ

[Order Fulfillment Workflow](../boundaries/orders/slices/fulfillment/infrastructure/temporal/README.md) выполняется в `orders-worker`. Решения принимает доменная модель, а Activities вызывают соседей через internal gateway. Факты от соседей приходят событиями: `orders-consumer` переводит их в Signals workflow.

| Шаг | Кто | Как |
| --- | --- | --- |
| Authorize Payment | `orders-worker` → [`payments`](../boundaries/payments/README.md) | gRPC, результат — в ответе |
| Record Payment Authorization | `orders-worker` | Локально в `Order` |
| Request Restaurant Order | `orders-worker` → [`restaurants`](../boundaries/restaurants/README.md) | gRPC `ReceiveOrderRequest` |
| Ресторан принял заказ | `restaurants` → `orders-consumer` | Событие `RestaurantOrderAccepted` |
| Create Delivery | `orders-worker` → [`delivery`](../boundaries/delivery/README.md) | gRPC; поиск курьера ведёт отдельный Assignment Workflow в `delivery-worker` |
| Курьер назначен, заказ готов | `delivery` и `restaurants` → `orders-consumer` | События `CourierAssignedToDelivery` и `RestaurantOrderReady`, в любом порядке |
| Курьер забрал заказ | `delivery` → `orders-consumer` | Событие `DeliveryPickedUp` |
| Capture Payment | `orders-worker` → `payments` | gRPC |
| Заказ доставлен | `delivery` → `orders-consumer` | Событие `DeliveryCompleted` |
| Complete Order | `orders-worker` | Локально: `Order` и `OrderCompleted` в Outbox |

Если что-то пошло не так, workflow отменяет сделанное в обратном порядке (Saga): доставку, заказ на кухне, платёж, — а затем завершает `Order` отрицательным итогом. Сроки и правила отмены описаны в [README адаптера](../boundaries/orders/slices/fulfillment/infrastructure/temporal/README.md).

Обратите внимание на асимметрию: команды соседям идут синхронно через gateway, а их факты возвращаются событиями. У `payments.v1.payment-events` потребителей нет, потому что исход платежа `orders` получает ответом gRPC.

## 5. Клиент видит изменения

UI не опрашивает сервер ([ADR 36](adr/0036-graphql-subscriptions-via-cosmo-streams.md)). Каждое изменение заказа пишет Change Notification в Outbox, Debezium публикует его в `orders.v1.order-changes`, Cosmo Router читает topic сам, запрашивает актуальную сущность через plugin и отправляет её клиенту по SSE. Схема — `subscription`.

Позиции курьера идут другим путём: `delivery` публикует их в Valkey, Router подписан на канал напрямую.

## Куда смотреть дальше

- Запустить всё локально — [`ops/kind/README.md`](../ops/kind/README.md).
- Найти запрос в telemetry — [Observability](observability.md).
- Что-то сломалось — [Runbooks](runbooks.md).
- Термины — [Ubiquitous Language](ubiquitous-language.md).
- Почему так решено — [индекс ADR](adr/README.md).
