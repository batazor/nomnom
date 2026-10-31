# Get Courier Current Delivery

## Сценарий

- **Тип:** Query.
- **Actor:** Курьер.
- **Намерение:** Получить свою активную доставку.
- **Результат:** Возвращено разрешённое представление текущей `Delivery` либо отсутствие активной доставки.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Читает query model `Delivery`; право курьера проверяет порт `CourierAuthorization` слайса `courier`, регистрацию курьера — порт `CourierAssignment`.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-304.
- **Порты:** `CourierAuthorization` (`ViewCourierCurrentDelivery` над `Courier(id)`), `CourierAssignment` (`ensure_registered`), `CourierCurrentDeliveryReadModel`.
- **Транзакция:** Query без UoW; активной считается `assigned` или `pickedUp` доставка курьера.

## Acceptance

BDD-сценарии размещаются в `acceptance/get_courier_current.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../../README.md#use-cases).

