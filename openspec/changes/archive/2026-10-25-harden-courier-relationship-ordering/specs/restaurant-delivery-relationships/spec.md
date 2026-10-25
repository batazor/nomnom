# Spec Delta

## ADDED Requirements

### Requirement: Missing relationship delivery восстанавливается после grace
**ID:** FR-563

Reconciler `delivery` SHALL восстанавливать missing `courier#self` и `delivery#courier` с сохранением revision, только если relationship отсутствовала при том же subject на двух проходах с интервалом не меньше grace (writers делают `DELETE` до commit без row lock). Кандидат, ставший present или потерявший subject, MUST забываться.

#### Scenario: Снятие курьера в полёте
**ID:** AC-668

- **WHEN** Reassign Courier удалил `delivery#courier` курьера до commit, а проход прочитал граф после `DELETE` и `courier_id` до commit
- **THEN** relationship не восстанавливается, а после commit снятия кандидат забывается без записи

#### Scenario: Потерянная связь курьера
**ID:** AC-672

- **WHEN** доставка назначена курьеру, а `delivery#courier` отсутствует на двух проходах с интервалом не меньше grace
- **THEN** reconciler восстанавливает relationship и сохраняет её revision
