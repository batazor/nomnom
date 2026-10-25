# Spec Delta

## ADDED Requirements

### Requirement: Регистрация ресторана и управление персоналом доступны по gRPC
**ID:** FR-446

`restaurants` SHALL публиковать `RegisterRestaurant`, `AssignStaff` и `RevokeStaff`. Процесс SHALL собирать их с SpiceDB-проверками и SpiceDB-записью relationships, поэтому успешный вызов MUST оставлять соответствующую `restaurant#owner` или `restaurant#staff` relationship.

#### Scenario: Оператор регистрирует ресторан
**ID:** AC-479

- **WHEN** оператор с `platform#register_restaurant` вызывает `RegisterRestaurant` с `owner_id`
- **THEN** ответ содержит ресторан, а владелец получает `restaurant#manage`

#### Scenario: Владелец назначает и снимает персонал
**ID:** AC-480

- **WHEN** владелец вызывает `AssignStaff`, а затем `RevokeStaff` для пользователя
- **THEN** после `AssignStaff` пользователь имеет `restaurant#operate_kitchen`, после `RevokeStaff` — нет; повторный вызов возвращает `changed = false`

#### Scenario: Без права manage
**ID:** AC-481

- **WHEN** пользователь без `restaurant#manage` вызывает `AssignStaff`
- **THEN** вызов отклоняется `PERMISSION_DENIED`, персонал не меняется
