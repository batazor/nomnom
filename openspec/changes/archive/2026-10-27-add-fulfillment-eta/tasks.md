# Tasks

- [x] 1.1 Proto: `expected_ready_at` в `AcceptRestaurantOrderRequest`/`RestaurantOrderAccepted`; `expected_pickup_at`/`expected_delivered_at` в `AcceptDeliveryOfferRequest`, `ConfirmPickupRequest`, `CourierAssignedToDelivery`, `DeliveryPickedUp`; сроки в `orders.v1.Order`.
- [x] 2.1 `restaurants`: срок при принятии, событие, эмулятор (`FR-609`, `AC-769`).
- [x] 2.2 `delivery`: сроки в Delivery, события, эмулятор (`FR-610`, `AC-770`).
- [x] 2.3 `orders`: consumer и Order (`FR-611`, `AC-771`).
- [x] 3.1 BFF и UI: поля Order, прогресс-бары (`FR-612`, `AC-772`).
- [x] 4.1 Проверки, `openspec validate add-fulfillment-eta --strict`, деплой в kind.
