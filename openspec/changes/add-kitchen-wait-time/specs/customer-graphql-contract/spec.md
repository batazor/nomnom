# Spec Delta

## ADDED Requirements

### Requirement: Ожидание ресторана в customer-схеме
**ID:** FR-647

`Restaurant` SHALL иметь поля `waitTimeElevated: Boolean!` и `expectedWaitMinutes: Int!` из публичного представления `restaurants`. Поля SHALL отдаваться и в `restaurants`, и в `restaurant`. MSW-моки SHALL покрывать оба уровня.

#### Scenario: Перегруженная кухня
**ID:** AC-847

- **WHEN** у ресторана повышенное ожидание и текущее ожидание 42 минуты
- **THEN** `Restaurant.waitTimeElevated = true`, `expectedWaitMinutes = 42`
