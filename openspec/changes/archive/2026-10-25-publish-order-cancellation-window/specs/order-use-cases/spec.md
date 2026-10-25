# Spec Delta

## MODIFIED Requirements

### Requirement: Get Order возвращает доступное представление
**ID:** FR-115

Get Order SHALL проверять право `view` verified subject над `Order` до чтения read model и возвращать представление заказа без изменения состояния. Отказ доступа MUST возвращаться независимо от существования `Order`. Для `Order`, Order State которого допускает Order Cancellation, представление SHALL содержать окно отмены `opens_at`/`closes_at` из той же cancellation policy, что использует Request Order Cancellation; для `cancelled` и `completed` окно MUST отсутствовать.

#### Scenario: Авторизованное чтение
**ID:** AC-126

- **WHEN** subject с правом `view` запрашивает `Order`
- **THEN** система возвращает состав, total, Delivery Address, Order State, отражённые факты и причину отрицательного итога, если она есть

#### Scenario: Чтение без права или несуществующего заказа
**ID:** AC-127

- **WHEN** subject без права `view` запрашивает существующий или несуществующий `Order`
- **THEN** система возвращает отказ доступа; при наличии права и отсутствии `Order` — ошибку «не найден»

#### Scenario: Окно отмены незавершённого заказа
**ID:** AC-504

- **WHEN** subject с правом `view` запрашивает `Order` в состоянии `placed` или `confirmed`
- **THEN** представление содержит `opens_at`, равный моменту Place Order, и `closes_at`, равный `opens_at` плюс настроенная длительность окна

#### Scenario: Завершённый заказ без окна
**ID:** AC-505

- **WHEN** subject с правом `view` запрашивает `Order` в состоянии `cancelled` или `completed`
- **THEN** представление не содержит окна отмены

## ADDED Requirements

### Requirement: Cancellation policy задаётся конфигурацией
**ID:** FR-462

Production cancellation policy SHALL открывать окно в момент Place Order и закрывать его через `cancellation.window`. Неположительная или неразбираемая длительность MUST останавливать процесс на старте, а тестовые адаптеры `AlwaysOpen` и `FixedWindow` MUST NOT использоваться в production composition root.

#### Scenario: Неверная длительность окна
**ID:** AC-506

- **WHEN** процесс `orders` стартует с `CANCELLATION__WINDOW=0s`
- **THEN** загрузка конфигурации завершается ошибкой, и процесс не начинает обслуживать запросы

### Requirement: Ошибка cancellation policy не ломает Get Order
**ID:** FR-463

Если cancellation policy возвращает ошибку, Get Order SHALL возвращать представление без окна отмены и MUST фиксировать ошибку в log и trace.

#### Scenario: Политика недоступна
**ID:** AC-507

- **WHEN** cancellation policy возвращает ошибку для `Order` в состоянии `placed`
- **THEN** Get Order возвращает представление заказа без окна отмены, а ошибка записана в log
