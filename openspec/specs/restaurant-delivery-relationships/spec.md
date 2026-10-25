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

Каждый контекст SHALL периодически удалять orphan/stale relationships своих definitions после grace на двух проходах и восстанавливать missing relationships.

#### Scenario: Потерянная связь владельца
**ID:** AC-478

- **WHEN** ресторан существует, а `restaurant#owner` в SpiceDB отсутствует
- **THEN** после прохода reconciliation владелец снова имеет `restaurant#manage`
