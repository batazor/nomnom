# Get Courier Current Delivery

## Сценарий

- **Тип:** Query.
- **Actor:** Курьер.
- **Намерение:** Получить свою активную доставку.
- **Результат:** Возвращено разрешённое представление текущей `Delivery` либо отсутствие активной доставки.

## Границы

- **Владелец:** `delivery/slices/courier`.
- **Домен:** `domains/courier`.
- **Взаимодействие:** Читает query model и проверяет принадлежность курьеру.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-304.
- **Порты:** `CourierAuthorization` (`ViewCourierCurrentDelivery` над `Courier(id)`), `CourierRepository`, `CourierCurrentDeliveryReadModel`.
- **Транзакция:** Query без UoW; активной считается `assigned` или `pickedUp` доставка курьера.

## Acceptance

BDD-сценарии размещаются в `acceptance/get_courier_current_delivery.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

