# Design

## Decisions

- Команды адресуют заказ парой `restaurant_id` + `restaurant_order_id` и требуют `restaurant:<id>#operate_kitchen` (`owner + staff`) через существующий `KitchenAccessControl.canOperateKitchen`. Subject — из текущего `SubjectResolver`; resolution identity пользователя (ADR 32) этим изменением не меняется.
- Ответ — `RestaurantOrder` (`state`, `expected_ready_at`, `version`) после команды. Отдельного request id нет: команды идемпотентны естественным ключом заказа и FSM. Повтор `StartPreparation` и `MarkRestaurantOrderReady` — политика `succeed-without-event` (повтор UI безопасен, факт не дублируется); повтор остальных команд — `FAILED_PRECONDITION` (`already_in_state` / `invalid_transition`).
- Ошибки — существующий маппинг `service.ts`: пустые обязательные поля и отсутствующий `expected_ready_at` → `INVALID_ARGUMENT`; заказ другого ресторана → `NOT_FOUND`; запрещённый переход или guard → `FAILED_PRECONDITION`; нет права → `PERMISSION_DENIED`; SpiceDB недоступен → `UNAVAILABLE`; конфликт версии → `ABORTED`.
- `MarkRestaurantOrderAssembled` пишет один документ без Unit of Work и Outbox: `RestaurantOrderAssembled` — локальный факт.
- Accept Order требует портов открытых решений. До модели расписания и capacity используется `UnconstrainedAcceptanceConditions` (Acceptance зависит только от `Restaurant.isOpen` и `MenuItem.isAvailable`), начальный `expected_ready_at` — `FixedPreparationTimeEstimator` (20 минут, константа composition root).
- Reject After Acceptance, Start / Complete Preparation Item и Queue Restaurant Order не публикуются: первые два не разрешены или не входят в MVP, Queue выполняется в одной UoW с Accept Order. Query use cases kitchen board остаются вне этого изменения.
