# Proposal

## Why

Каждый контекст проверен по отдельности, но путь заказа от оформления до доставки нигде не проходит целиком, и в kind пройти его нельзя:

- `orders-worker` (Order Fulfillment Workflow) и `orders-forwarder` (Outbox `orders` → Kafka) не развёрнуты: оформленный заказ не получает ни оплаты, ни запроса в ресторан.
- Поиск курьера никто не запускает: Create Delivery его не начинает (открытый вопрос), и первое предложение появляется только после Replan по таймауту назначения.
- Курьер не может действовать: из команд `delivery` по gRPC опубликованы только Create, Replan и Cancel Delivery и Get Courier Current Delivery. Регистрация курьера, позиция, доступность, предложения, Pickup, Completion и Failure есть в application-слое, но не в transport.
- Сотрудник кухни не видит новых заказов: Get Kitchen Queue не опубликован, а команды кухни требуют `restaurant_order_id`.
- Команды кухни и курьера не опубликованы на internal gateway, а user assertion принимаются только от BFF, у которого нет операций кухни и курьера.

## What Changes

- **delivery**: gRPC API курьера и администрирования курьеров — Register Courier, Update Courier Location, Set Courier Availability, List Available Delivery Offers, Accept/Reject Delivery Offer, Confirm Pickup, Confirm Delivery, Report Delivery Failure — с прежними authorization и Outbox.
- **delivery**: Dispatch — фоновая работа процесса `delivery`, как истечение предложений: ожидающей доставке без активного предложения выбирается курьер, которому её ещё не предлагали, и отправляется предложение. Поиск начинается сразу после Create Delivery и повторяется после отказа или истечения предложения. Replan из `orders` остаётся эскалацией по таймауту.
- **restaurants**: Get Kitchen Queue публикуется по gRPC.
- **kind**:
  - развёртываются `orders-worker` и `orders-forwarder`;
  - internal gateway публикует команды кухни, Get Kitchen Queue, API курьера и Register Courier/Restaurant;
  - fixture `e2e-driver` играет клиентов кухни и курьера, которых ещё нет: ему, и только в kind, разрешено передавать user assertion в `restaurants` и `delivery`;
  - kind-only bootstrap SpiceDB выдаёт `platform:nomnom#operator` тестовому оператору.
- **e2e**: Chainsaw-тест `order-lifecycle`. Оператор регистрирует ресторан и курьера, владелец заводит меню и открывает ресторан, курьер выходит на линию. Покупатель оформляет заказ через BFF. Кухня принимает и готовит заказ, курьер принимает предложение, забирает и доставляет его. Заказ в BFF — `DELIVERED`, оплата списана, workflow завершён.

Не входит: операции кухни и курьера в BFF (клиентов ещё нет), выбор курьера по расстоянию, реальный платёжный провайдер.

## Capabilities

### New Capabilities

- `courier-command-api`: gRPC API курьера и администрирования курьеров в `delivery`.

### Modified Capabilities

- `delivery-assignment`: автоматический Dispatch ожидающих доставок.
- `kitchen-board`: Get Kitchen Queue по gRPC.
- `kubernetes-test-environment`: fulfillment-процессы `orders` в kind и сквозной e2e пути заказа.

## Impact

- `boundaries/delivery`: proto `nomnom.delivery.v1`, gRPC service, use case Dispatch Deliveries и его worker, репозиторий (выбор ожидающих доставок).
- `boundaries/restaurants`: proto и gRPC `GetKitchenQueue`.
- `ops/kind`: values и Skaffold-модули `orders-worker`/`orders-forwarder`, gateway routes, fixture `e2e-driver`, kind bootstrap SpiceDB, `e2e/order-lifecycle`.
- Протоколы расширяются обратно совместимо.
