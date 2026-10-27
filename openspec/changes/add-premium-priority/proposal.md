# Proposal

## Why

Когда кухня загружена, покупатель видит повышенное ожидание (change `add-kitchen-wait-time`), но повлиять на него не может. Ресторан готов за доплату готовить заказ первым. Порядок Kitchen Queue сейчас — только рекомендация (`FifoKitchenQueueOrdering`), и кухня берёт заказ из очереди любой командой Start Preparation. Поэтому обещание «приготовим первым» сейчас нечем подкрепить.

## What Changes

- **Премиум-опция ресторана.** У ресторана появляется необязательная доплата `premium_fee` (Money в валюте ресторана, больше 0). Владелец задаёт и убирает её через Update Restaurant, для этого появляется gRPC `UpdateRestaurant`. Нет доплаты — нет опции.
- **Place Order с приоритетом.** `PlaceOrderRequest.priority` (`STANDARD`/`PREMIUM`), не задан — `STANDARD`, неизвестное значение — `INVALID_ARGUMENT`. Для `PREMIUM` доплата берётся из snapshot ресторана, фиксируется в Order отдельной строкой и входит в `total`. Если у ресторана нет доплаты — `FAILED_PRECONDITION`. Оплата не меняется: `payments` списывает `total`.
- **Приоритет на кухне.** `ReceiveOrderRequest` получает `priority` от workflow, Restaurant Order хранит его. Kitchen board и экран заказа показывают метку «Приоритет».
- **Строгая очередь.** **BREAKING:** `StartPreparation(restaurant_order_id)` заменяется `StartNextPreparation(restaurant_id, request_id)`: кухня не выбирает заказ, команда берёт голову очереди. Порядок:
  1. премиум-заказы и обычные, прождавшие в очереди дольше базового времени ресторана × `KITCHEN_QUEUE__AGING_FACTOR`;
  2. остальные обычные;
  3. внутри класса — FIFO по `queued_at`.

  Get Kitchen Queue возвращает тот же порядок.
- **UI покупателя.** При оформлении — чекбокс «Приоритет — приготовим первым, +N», если у ресторана есть доплата. Без отдельной оценки времени.
- **UI кухни.** Колонка очереди на флоу-борде: одна кнопка «Взять следующий» вместо кнопки на каждой карточке. Симулятор вызывает ту же команду.

Не входит:

- приоритет в доставке и назначении курьера — потом отдельно;
- гарантия срока и возврат доплаты при опоздании; при отмене заказа возвращается весь `total`, как сейчас;
- докупка премиума после оформления;
- пересчёт `expected_ready_at` заказов, которые обогнал премиум.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `restaurant-management`: доплата за премиум в Update Restaurant и публичном представлении.
- `restaurant-administration-api`: gRPC `UpdateRestaurant`.
- `order-use-cases`: приоритет и доплата в Place Order.
- `restaurant-order-lifecycle`: приоритет Restaurant Order, Start Next Preparation.
- `kitchen-board`: строгий порядок Kitchen Queue.
- `kitchen-command-api`: `StartNextPreparation` и его повтор.
- `customer-graphql-contract`: `premiumFee`, `priority`.
- `customer-web-application`: выбор приоритета при оформлении.
- `order-tracking-ui`: метка приоритета.
- `order-flow-board`: «Взять следующий» и метка приоритета на кухне.

## Impact

- **`boundaries/restaurants`:**
  - слайс `restaurant`: `premium_fee`, Update Restaurant, gRPC `UpdateRestaurant`;
  - слайс `restaurantorder`: `priority`, `StartNextPreparation`, политика очереди со старением (порт к базовому времени из `add-kitchen-wait-time`);
  - симулятор; proto `service.proto`, `restaurants.proto`;
  - migration документов Restaurant Order (`priority` для существующих — `STANDARD`; в kind хранилища сбрасываются).
- **`boundaries/orders`:**
  - Order: `priority`, `premium_fee`, `total`;
  - Place Order и catalog snapshot (доплата ресторана);
  - Activity `RequestRestaurantOrder` передаёт `priority`;
  - proto `service.proto`, `orders.proto`; migration PostgreSQL.
- **`boundaries/bff`:** клиенты `restaurants` и `orders`, `PlaceOrderInput.priority`, `Restaurant.premiumFee`, `Order.priority`, kitchen-мутация, тесты.
- **`boundaries/ui`:** оформление заказа, экран заказа, флоу-борд, i18n, моки.
- **`ops`:** `KITCHEN_QUEUE__AGING_FACTOR` в values `restaurants`; kind e2e (`StartNextPreparation`).
- **Документация:** `docs/kitchen.md` (порядок Kitchen Queue, Start Preparation), `docs/ubiquitous-language.md` (Premium Priority), README `restaurants`, `orders`, `bff`, `ui`.

Зависит от `add-kitchen-wait-time` (базовое время ресторана) и от реализованного `add-cook-and-courier-pools` (назначение Cook в Start Preparation).
