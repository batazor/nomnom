# restaurant-delivery-relationships Specification

## Purpose
Определяет запись relationships `restaurant#owner`, `restaurant#staff`, `courier#self` и `delivery#courier` их контекстами-владельцами и сверку этих relationships с business state.

## Requirements

### Requirement: restaurants записывает relationships владельца и персонала
**ID:** FR-443

Register Restaurant SHALL выполнять `TOUCH restaurant#owner` до сохранения ресторана. Assign Staff и Revoke Staff SHALL требовать `restaurant#manage` и выполнять `TOUCH` или `DELETE restaurant#staff` до сохранения состава персонала.

#### Scenario: Персонал получает доступ к кухне
**ID:** AC-476

- **WHEN** владелец назначает пользователя персоналом ресторана
- **THEN** пользователь получает `restaurant#operate_kitchen`, а после Revoke Staff теряет его

### Requirement: delivery записывает relationships курьера
**ID:** FR-444

Register Courier SHALL выполнять `TOUCH courier#self@user`. `delivery#courier` SHALL соответствовать `Delivery.courier_id`: `TOUCH` при Assignment, `DELETE` при Reassign Courier и Cancel Delivery, до сохранения.

#### Scenario: Курьер теряет доступ после переназначения
**ID:** AC-477

- **WHEN** назначенного курьера снимают через Reassign Courier
- **THEN** он больше не имеет `delivery#act_as_courier` на эту доставку

### Requirement: Relationships restaurants и delivery сверяются с business state
**ID:** FR-445

Каждый контекст SHALL периодически удалять orphan/stale relationships своих definitions после grace на двух проходах и восстанавливать missing relationships. Reconciler `restaurants` MUST восстанавливать missing `restaurant#owner` и `restaurant#staff` с revision, только если связь отсутствовала у того же пользователя на двух проходах не реже grace; кандидат, ставший present или исчезнувший из ресторана, MUST забываться.

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
