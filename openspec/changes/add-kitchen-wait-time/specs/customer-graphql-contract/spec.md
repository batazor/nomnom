# Spec Delta

## ADDED Requirements

### Requirement: Ожидание ресторана в customer-схеме
**ID:** FR-647

`Restaurant` SHALL иметь поле `waitTime: RestaurantWaitTime` с `elevated: Boolean!` и `expectedWaitMinutes: Int!` из публичного представления `restaurants`. Запросы `restaurants` и `restaurant` SHALL заполнять его всегда; в остальных местах схемы оно MUST быть `null`, как `menu`, а не нулевыми значениями. MSW-моки SHALL покрывать оба уровня.

#### Scenario: Перегруженная кухня
**ID:** AC-847

- **WHEN** у ресторана повышенное ожидание и текущее ожидание 42 минуты
- **THEN** `Restaurant.waitTime` равно `{ elevated: true, expectedWaitMinutes: 42 }`
