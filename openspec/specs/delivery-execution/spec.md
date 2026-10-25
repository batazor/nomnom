# delivery-execution Specification

## Purpose
Определяет, как назначенный курьер или авторизованный диспетчер фиксирует Pickup, Delivery Completion и Delivery Failure, и как эти бизнес-факты становятся доступны `orders` через Outbox.

## Requirements

### Requirement: Authoritative authorization сценариев выполнения
**ID:** FR-314

Сценарии выполнения доставки SHALL проверять право subject действовать от имени указанного курьера, а для действий диспетчера — право над `Delivery`, до доменной операции. Отказ authorization MUST NOT менять `Delivery`, `Courier` и Outbox.

#### Scenario: Отказ authorization при Pickup
**ID:** AC-335

- **WHEN** subject без права действовать от имени назначенного курьера подтверждает Pickup
- **THEN** сценарий отклоняется как permission denied, `Delivery` остаётся assigned и Outbox пуст

### Requirement: Confirm Pickup
**ID:** FR-315

Назначенный курьер SHALL подтверждать Pickup назначенной `Delivery`; доставка MUST перейти в picked up, а факт Pickup MUST быть записан в Outbox в той же транзакции. Курьер, не назначенный на `Delivery`, MUST получать отказ без изменения состояния.

#### Scenario: Назначенный курьер подтверждает Pickup
**ID:** AC-336

- **WHEN** назначенный курьер подтверждает Pickup assigned `Delivery`
- **THEN** `Delivery` picked up и факт Pickup записан в Outbox

#### Scenario: Pickup подтверждает другой курьер
**ID:** AC-337

- **WHEN** курьер, не назначенный на `Delivery`, подтверждает её Pickup
- **THEN** сценарий отклоняется как нарушение precondition, `Delivery` остаётся assigned и Outbox пуст

### Requirement: Confirm Delivery
**ID:** FR-316

Назначенный курьер SHALL подтверждать Delivery Completion забранной `Delivery`; доставка MUST перейти в delivered, курьер MUST снова стать available, а факт Delivery Completion MUST быть записан в Outbox в той же транзакции. Delivered `Delivery` MUST NOT изменяться дальше.

#### Scenario: Курьер передал заказ клиенту
**ID:** AC-338

- **WHEN** назначенный курьер подтверждает Delivery Completion picked up `Delivery`
- **THEN** `Delivery` delivered, курьер available и факт Delivery Completion записан в Outbox

#### Scenario: Completion до Pickup
**ID:** AC-339

- **WHEN** назначенный курьер подтверждает Delivery Completion ещё не забранной `Delivery`
- **THEN** сценарий отклоняется как нарушение precondition и состояние не меняется

### Requirement: Report Delivery Failure
**ID:** FR-317

Назначенный курьер или авторизованный диспетчер SHALL фиксировать неудачу assigned или picked up `Delivery` с непустой причиной: доставка MUST стать failed, курьер MUST стать available, а факт неудачи с причиной MUST быть записан в Outbox. Классификация причины как retryable MUST NOT выполняться в `delivery`.

#### Scenario: Курьер сообщает о неудаче
**ID:** AC-340

- **WHEN** назначенный курьер сообщает о неудаче picked up `Delivery` с причиной
- **THEN** `Delivery` failed с этой причиной, курьер available и факт неудачи с причиной записан в Outbox

#### Scenario: Диспетчер сообщает о неудаче
**ID:** AC-341

- **WHEN** авторизованный диспетчер сообщает о неудаче assigned `Delivery`
- **THEN** `Delivery` failed, курьер available и факт неудачи записан в Outbox

#### Scenario: Неудача завершённой доставки
**ID:** AC-342

- **WHEN** о неудаче сообщается для delivered `Delivery`
- **THEN** сценарий отклоняется как нарушение precondition и `Delivery` остаётся delivered

### Requirement: Атомарная публикация фактов выполнения
**ID:** FR-318

Изменение `Delivery`, освобождение `Courier` и запись факта в Outbox SHALL фиксироваться одной локальной транзакцией. При неуспешном commit MUST NOT сохраняться ни изменение состояния, ни запись Outbox.

#### Scenario: Commit Delivery Completion не удался
**ID:** AC-343

- **WHEN** транзакция подтверждения Delivery Completion не может быть зафиксирована
- **THEN** сценарий завершается ошибкой, `Delivery` остаётся picked up, курьер busy и Outbox пуст
