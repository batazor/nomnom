# Spec Delta

## ADDED Requirements

### Requirement: Команды кухни доступны по gRPC и публикуют факты через Outbox
**ID:** FR-480

`restaurants` SHALL публиковать по gRPC команды кухни над `Restaurant Order`: Accept, Reject, Start Preparation, Mark Assembled, Mark Ready, Report Preparation Delay и Failure. Команда SHALL возвращать `RestaurantOrder` после изменения, а опубликованные факты MUST записываться в Outbox в одной транзакции с заказом.

#### Scenario: Поток кухни записывает Integration Events
**ID:** AC-540

- **WHEN** сотрудник последовательно вызывает `AcceptRestaurantOrder`, `StartPreparation`, `ReportPreparationDelay`, `MarkRestaurantOrderAssembled` и `MarkRestaurantOrderReady` в процессе с MongoDB
- **THEN** Outbox содержит записи `RestaurantOrderAccepted`, `PreparationStarted`, `PreparationDelayed`, `RestaurantOrderReady` в этом порядке, а Assembly записи не создаёт

#### Scenario: Ответ Acceptance содержит ожидаемое время готовности
**ID:** AC-545

- **WHEN** сотрудник вызывает `AcceptRestaurantOrder` для `RECEIVED` заказа
- **THEN** ответ содержит `state = QUEUED`, `expected_ready_at` и `version`

### Requirement: Команды кухни требуют operate_kitchen и проверенной identity
**ID:** FR-481

Команды кухни MUST проверять `restaurant:<id>#operate_kitchen` для `restaurant_id` запроса до изменения заказа. Заказ другого ресторана MUST отвечать `NOT_FOUND`.

#### Scenario: Без identity
**ID:** AC-541

- **WHEN** команда кухни вызывается с `RejectingSubjectResolver`
- **THEN** вызов отклоняется `UNAUTHENTICATED`, заказ и Outbox не меняются

#### Scenario: Заказ другого ресторана
**ID:** AC-542

- **WHEN** сотрудник ресторана A вызывает `AcceptRestaurantOrder` с `restaurant_id` A и заказом ресторана B
- **THEN** вызов отклоняется `NOT_FOUND`

### Requirement: Состояние и валидация команд кухни
**ID:** FR-482

Запрещённый переход MUST отвечать `FAILED_PRECONDITION` без записи в Outbox. Пустые `restaurant_id`, `restaurant_order_id`, `reason` у `ReportPreparationFailure` и отсутствующий `expected_ready_at` у `ReportPreparationDelay` MUST отвечать `INVALID_ARGUMENT`. Повтор `StartPreparation` и `MarkRestaurantOrderReady` SHALL отвечать текущим заказом без нового факта.

#### Scenario: Валидация полей
**ID:** AC-543

- **WHEN** команда вызывается без `restaurant_order_id`, `ReportPreparationDelay` — без `expected_ready_at`, `ReportPreparationFailure` — с пустым `reason`
- **THEN** вызов отклоняется `INVALID_ARGUMENT`

#### Scenario: Повтор начала Preparation и готовности
**ID:** AC-544

- **WHEN** сотрудник повторяет `StartPreparation` для `PREPARING` и `MarkRestaurantOrderReady` для `READY` заказа
- **THEN** ответ содержит текущее состояние, Outbox не пополняется
