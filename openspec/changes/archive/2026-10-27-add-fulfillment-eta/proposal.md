# Proposal

## Why

Покупатель не видит, сколько ждать: в домене кухни есть `expected_ready_at`, но наружу он выходит только в `PreparationDelayed`; у доставки срока нет вовсе. Эмулятор (add-demo-simulator) выбирает свои задержки, но они нигде не видны. Нужен прогресс-бар готовки и доставки на экране заказа.

## What Changes

- **Сроки — факты, а не тики.** Срок публикуется один раз, когда его называет участник, и заново — только при изменении. Периодических событий «осталось X» нет; UI досчитывает прогресс локальным таймером между событиями (не polling, ADR 36).
- **restaurants**: `AcceptRestaurantOrder` принимает необязательный `expected_ready_at` от кухни (без него — Ready Time Estimator, как сейчас); `RestaurantOrderAccepted` несёт `expected_ready_at`. Эмулятор кухни при принятии планирует всю готовку и передаёт итоговый срок; задержка — `PreparationDelayed`, как сейчас.
- **delivery**: курьер называет `expected_pickup_at` при `AcceptDeliveryOffer` и `expected_delivered_at` при `ConfirmPickup`; они в `CourierAssignedToDelivery` и `DeliveryPickedUp`. Эмулятор курьера передаёт запланированные моменты.
- **orders**: consumer сохраняет последние сроки в Order; `Order` и `orderChanged` отдают их.
- **BFF/UI**: `Order.expectedReadyAt`, `expectedPickupAt`, `expectedDeliveredAt`; на экране заказа — прогресс-бары «Готовка» и «Доставка».

Не входит: событие пересмотра срока доставки курьером (нет use case), расчёт ETA по расстоянию.

## Capabilities

### New Capabilities

- `fulfillment-eta`.

## Impact

Proto `restaurants` (service, events), `delivery` (service, events), `orders` (orders) и их generated-копии; домены RestaurantOrder, Delivery, Order; consumer `orders`; эмуляторы; схема BFF; экран заказа в UI.
