# Get Order Delivery

## Сценарий

- **Тип:** Query.
- **Actor:** Тот, кому доступен `order:<id>#view`, прежде всего покупатель заказа.
- **Намерение:** Узнать курьера заказа и следить за доставкой.
- **Результат:** Возвращены имя курьера и, пока доставка назначена или забрана, отслеживание: Tracking Channel, Virtual City, точки забора и вручения, сохранённая позиция курьера. Состояние не меняется.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Читает read model доставки заказа. Курьер остаётся видимым после вручения. Tracking Channel — capability, которую выдаёт только этот query.

## Реализация

- **Handler:** `mod.rs` — `Input` (`order_id`), `Output`, `Handler::execute(subject, input)`. Право проверяется один раз, до чтения.
- **Порты:** `DeliveryAuthorization`, `OrderDeliveryReadModel`.

## Acceptance

FR-626, FR-660.

Общий каталог: [Delivery README](../../../../README.md).
