# Spec Delta

## ADDED Requirements

### Requirement: OrderCompleted несёт момент завершения
**ID:** FR-544

Domain Event `OrderCompleted` SHALL содержать момент завершения из домена, а Integration Event `OrderCompleted` SHALL публиковать его как время события; время записи в Outbox MUST NOT подменять его.

#### Scenario: Повтор публикации
**ID:** AC-634

- **WHEN** Outbox публикует `OrderCompleted` повторно после сбоя
- **THEN** время события совпадает с моментом завершения, а не с моментом повторной записи
