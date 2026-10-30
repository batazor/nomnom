# Record Delivery Duration

## Сценарий

- **Тип:** Command.
- **Actor:** Фоновый consumer собственных фактов Delivery, без subject вызывающего.
- **Намерение:** Пополнить Delivery Duration Statistics Market по факту Pickup, вручения или выбытия доставки.
- **Результат:** Pickup записывает `courier_lead` и открывает transit, вручение записывает `transit` и закрывает его, отмена и сбой закрывают transit без длительности.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Transactional Inbox (ADR 11): запись Inbox и изменение статистики фиксируются в одной Unit of Work, повтор `event_id` ничего не меняет. Факты одной Delivery приходят по порядку, потому что поток ключуется её id.

## Реализация

- **Handler:** `mod.rs` — `Input` (`event_id`, `event_type`, `occurred_at_ms`, `delivery_id`, `fact`), `Handler::execute(input)`; результат — `Recorded` или `Duplicate`.
- **Порты:** `DeliveryInbox`, `DeliveryDurationStatistics`, `DeliveryRepository`.

## Acceptance

FR-676, AC-935 change [`add-scheduled-orders`](../../../../../../openspec/changes/add-scheduled-orders/).

Общий каталог: [Delivery README](../../../../README.md).
