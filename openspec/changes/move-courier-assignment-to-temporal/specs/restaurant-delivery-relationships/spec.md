## MODIFIED Requirements

### Requirement: delivery записывает relationships курьера
**ID:** FR-444

Register Courier SHALL выполнять `TOUCH courier#self@user`. `delivery#courier` SHALL соответствовать активному назначению: `courier_id` задан и `Delivery` assigned или picked up. Assignment MUST выполнять `TOUCH`, а Reassign Courier, Cancel Delivery, Confirm Delivery и Report Delivery Failure — `DELETE` до сохранения с записью revision. `Delivery.courier_id` закрытой доставки MUST сохраняться как факт и MUST NOT давать доступ.

#### Scenario: Курьер теряет доступ после переназначения
**ID:** AC-477

- **WHEN** назначенного курьера снимают через Reassign Courier
- **THEN** он больше не имеет `delivery#act_as_courier` на эту доставку

#### Scenario: Курьер теряет доступ после доставки
**ID:** AC-690

- **WHEN** назначенный курьер подтверждает Delivery Completion
- **THEN** он больше не имеет `delivery#act_as_courier` и `order#view`, а `Delivery.courier_id` остаётся его id

#### Scenario: Курьер теряет доступ после неудачи
**ID:** AC-691

- **WHEN** о неудаче доставки сообщает курьер или диспетчер
- **THEN** курьер больше не имеет `delivery#act_as_courier` и `order#view`

#### Scenario: SpiceDB недоступен при закрытии доставки
**ID:** AC-692

- **WHEN** `DELETE delivery#courier` при Confirm Delivery завершается ошибкой
- **THEN** Unit of Work откатывается: `Delivery` остаётся picked up, курьер не освобождён и Outbox пуст
