# Estimate Delivery Duration

## Сценарий

- **Тип:** Query.
- **Actor:** Fulfillment process `orders` (право `platform#coordinate_delivery`).
- **Намерение:** Узнать ожидаемые `courier_lead` (Create Delivery → Pickup) и `transit` (Pickup → вручение) Market, чтобы спланировать Scheduled Order.
- **Результат:** Возвращена оценка обоих участков; состояние не меняется.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Читает Delivery Duration Statistics Market. Каждый участок — медиана последних `sample_size` длительностей, а пока выборка не набрана — базовое значение из конфига.

## Реализация

- **Handler:** `mod.rs` — `Input` (`market_id`), `Handler::execute(subject, input)`. Некорректный `market_id` отвергается до проверки прав.
- **Порты:** `DeliveryAuthorization`, `DeliveryDurationSamples`.

## Acceptance

FR-691, AC-904, AC-905 change [`add-scheduled-orders`](../../../../../../openspec/changes/add-scheduled-orders/).

Общий каталог: [Delivery README](../../../../README.md).
