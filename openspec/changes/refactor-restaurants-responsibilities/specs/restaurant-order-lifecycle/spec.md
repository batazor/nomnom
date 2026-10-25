# Spec Delta

## ADDED Requirements

### Requirement: Исход отмены решает агрегат Restaurant Order
**ID:** FR-547

Решение Cancel Restaurant Order (отменён, повтор, отказ с причиной по стадии) SHALL принимать агрегат `RestaurantOrder`; handler MUST только сохранять результат и публиковать события.

#### Scenario: Отмена готового заказа
**ID:** AC-637

- **WHEN** отменяется Restaurant Order в состоянии Ready
- **THEN** агрегат возвращает отказ `already_ready`, а внешний ответ совпадает с прежним
