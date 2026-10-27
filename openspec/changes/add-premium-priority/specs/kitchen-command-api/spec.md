# Spec Delta

## MODIFIED Requirements

### Requirement: Состояние и валидация команд кухни
**ID:** FR-482

Запрещённый переход MUST отвечать `FAILED_PRECONDITION`, `NoCookAvailable` — `RESOURCE_EXHAUSTED`, оба без записи в Outbox. Пустая Kitchen Queue MUST NOT быть ошибкой: `StartNextPreparation` отвечает без `restaurant_order`. Пустые `restaurant_id`, `restaurant_order_id` у команд над заказом, `request_id` у `StartNextPreparation`, `reason` у `ReportPreparationFailure` и отсутствующий `expected_ready_at` у `ReportPreparationDelay` MUST отвечать `INVALID_ARGUMENT`. RPC `StartPreparation(restaurant_order_id)` MUST быть удалён вместе с его сообщениями. Повтор `StartNextPreparation` с тем же `request_id` и повтор `MarkRestaurantOrderReady` SHALL отвечать текущим заказом без нового факта.

#### Scenario: Валидация полей
**ID:** AC-543

- **WHEN** команда над заказом вызывается без `restaurant_order_id`, `StartNextPreparation` — без `request_id`, `ReportPreparationDelay` — без `expected_ready_at`, `ReportPreparationFailure` — с пустым `reason`
- **THEN** вызов отклоняется `INVALID_ARGUMENT`

#### Scenario: Повтор начала Preparation и готовности
**ID:** AC-544

- **WHEN** сотрудник повторяет `StartNextPreparation` с тем же `request_id` и `MarkRestaurantOrderReady` для `READY` заказа
- **THEN** ответ содержит текущее состояние, Outbox не пополняется
