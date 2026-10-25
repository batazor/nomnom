# Update Courier Location

## Сценарий

- **Тип:** Command.
- **Actor:** Курьерское приложение.
- **Намерение:** Обновить последнюю известную позицию курьера.
- **Результат:** Сохранена актуальная доступная `delivery` позиция.

## Границы

- **Владелец:** `delivery/slices/courier`.
- **Домен:** `domains/courier`.
- **Взаимодействие:** Частота, точность и retention определяются отдельным SDD.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-303.
- **Порты:** `CourierAuthorization` (`UpdateCourierLocation` над `Courier(id)`), `CourierRepository`, `CourierLocationStore`, `Clock`.
- **Транзакция:** Без UoW: высокочастотная одиночная запись последней позиции; доступность `Courier` не меняется.
- **Допущение:** Частота, точность, retention и порядок устаревших обновлений не ограничиваются: хранится последняя позиция со временем clock `delivery`.

## Acceptance

BDD-сценарии размещаются в `acceptance/update_courier_location.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

