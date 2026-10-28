# Spec Delta

## ADDED Requirements

### Requirement: Расписание ресторана в customer-схеме
**ID:** FR-700

Схема SHALL содержать `Restaurant.timeZone`, `Restaurant.openingHours` (дни недели с интервалами), `Restaurant.acceptingOrders` с учётом расписания, `Restaurant.nextOpeningAt` и mutation `setOpeningHours(restaurantId, openingHours)` для владельца. Ошибка пересечения интервалов SHALL возвращаться как `INVALID_ARGUMENT` с днём и интервалом.

#### Scenario: Ресторан до открытия
**ID:** AC-949

- **WHEN** покупатель запрашивает ресторан до начала интервала расписания
- **THEN** `acceptingOrders = false`, `nextOpeningAt` — начало интервала

#### Scenario: Пересечение интервалов
**ID:** AC-950

- **WHEN** владелец сохраняет расписание с пересекающимися интервалами
- **THEN** mutation возвращает `INVALID_ARGUMENT` с днём и интервалом
