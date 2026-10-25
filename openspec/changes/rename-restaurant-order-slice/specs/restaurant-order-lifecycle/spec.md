# Spec Delta

## ADDED Requirements

### Requirement: Слайс Restaurant Order назван по Ubiquitous Language
**ID:** FR-487

Код агрегата Restaurant Order и его use cases SHALL находиться в слайсе `restaurants/slices/restaurantorder`, а каталоги use cases Start Preparation и Mark Ready for Pickup SHALL называться `start-preparation` и `mark-ready-for-pickup`. Внешние имена RPC, событий, permissions и коллекций MUST NOT меняться.

#### Scenario: Старых имён в коде не осталось
**ID:** AC-552

- **WHEN** проверка ищет `kitchenorder`, `start-preparing-order` и `mark-order-ready` в `boundaries/restaurants` (кроме `node_modules`, `dist` и истории OpenSpec)
- **THEN** совпадений нет, а typecheck и тесты проходят
