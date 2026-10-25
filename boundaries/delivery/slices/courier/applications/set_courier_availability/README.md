# Set Courier Availability

## Сценарий

- **Тип:** Command.
- **Actor:** Курьер или авторизованный диспетчер.
- **Намерение:** Изменить доступность курьера для новых назначений.
- **Результат:** `Courier` отмечен available или unavailable; позиция не изменена.

## Границы

- **Владелец:** `delivery/slices/courier`.
- **Домен:** `domains/courier`.
- **Взаимодействие:** Изменение доступности отделено от обновления location.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-302.
- **Порты:** `CourierAuthorization` (`SetCourierAvailability` над `Courier(id)`), `CourierRepository`.
- **Транзакция:** Без UoW: одиночная запись; повтор действующей доступности — no-op с `changed: false`.
- **Допущение:** Busy курьер не может стать `unavailable`; «уйти после текущей доставки» не моделируется.

## Acceptance

BDD-сценарии размещаются в `acceptance/set_courier_availability.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

