# Spec Delta

## MODIFIED Requirements

### Requirement: Relationships restaurants и delivery сверяются с business state
**ID:** FR-445

Каждый контекст SHALL периодически удалять orphan/stale relationships своих definitions после grace на двух проходах и восстанавливать missing relationships. Reconciler `restaurants` MUST восстанавливать missing `restaurant#owner` и `restaurant#staff` с revision, только если связь отсутствовала у того же пользователя на двух проходах не реже grace и оба прохода видели одну версию документа ресторана; другая версия MUST начинать grace заново. Собственная запись revision reconciler'а MAY переносить кандидатов на записанную версию. Кандидат, ставший present или исчезнувший из ресторана, MUST забываться.

#### Scenario: Потерянная связь владельца
**ID:** AC-478

- **WHEN** ресторан существует, а `restaurant#owner` в SpiceDB отсутствует на двух проходах с интервалом не меньше grace
- **THEN** после второго прохода владелец снова имеет `restaurant#manage`

#### Scenario: Снятие персонала в полёте
**ID:** AC-700

- **WHEN** Revoke Staff удалил `restaurant#staff` до сохранения ресторана, а проход прочитал граф после `DELETE` и ресторан до сохранения
- **THEN** relationship не восстанавливается, а после сохранения revoke кандидат забывается без записи

#### Scenario: Потерянная связь персонала
**ID:** AC-701

- **WHEN** пользователь входит в персонал ресторана, а `restaurant#staff` отсутствует на двух проходах с интервалом не меньше grace
- **THEN** reconciler восстанавливает relationship и сохраняет её revision

#### Scenario: Повторное назначение и снятие между проходами
**ID:** AC-710

- **WHEN** первый проход видит снятие в полёте, затем revoke сохранён, пользователь снова назначен и второй Revoke Staff удалил связь до сохранения, а второй проход через grace видит пользователя в персонале без связи
- **THEN** relationship не восстанавливается, версия ресторана другая и grace начинается заново; после сохранения второго revoke кандидат забывается
