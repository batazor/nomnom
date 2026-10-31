# Spec Delta

## ADDED Requirements

### Requirement: Раскрытие и забвение пользователя — отдельное полномочие
**ID:** FR-750

Схема SHALL определять на `platform:nomnom` relation `auditor` и permissions `reveal_subjects` и `forget_subjects`, выданные только ею. `operator` MUST NOT входить в эти permissions. Проверка этих permissions MUST использовать `fully_consistent`, чтобы отозванная связь действовала сразу.

#### Scenario: Grant аудитора
**ID:** AC-1045

- **WHEN** выполняется `make authz-validate`
- **THEN** `auditor` имеет `reveal_subjects` и `forget_subjects`, а `operator` без `auditor` и любой `service` не имеют

#### Scenario: Отозванный доступ
**ID:** AC-1046

- **GIVEN** связь `auditor` пользователя удалена
- **WHEN** пользователь сразу запрашивает раскрытие
- **THEN** запрос отклоняется `PERMISSION_DENIED`, данные не раскрываются
