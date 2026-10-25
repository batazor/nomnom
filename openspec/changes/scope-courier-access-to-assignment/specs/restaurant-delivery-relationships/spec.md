# Spec Delta

## MODIFIED Requirements

### Requirement: delivery записывает relationships курьера
**ID:** FR-444

Register Courier SHALL выполнять `TOUCH courier#self@user`. `delivery#courier` SHALL соответствовать активному назначению: `courier_id` задан и `Delivery` assigned или picked up. Assignment MUST выполнять `TOUCH`, а Reassign Courier, Replan Delivery, Cancel Delivery, Confirm Delivery и Report Delivery Failure — `DELETE` до сохранения с записью revision. `Delivery.courier_id` закрытой доставки MUST сохраняться как факт и MUST NOT давать доступ.

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

### Requirement: Missing relationship delivery восстанавливается после grace
**ID:** FR-563

Reconciler `delivery` SHALL восстанавливать missing `courier#self` и `delivery#courier` с сохранением revision, только если relationship отсутствовала при том же subject на двух проходах с интервалом не меньше grace (writers делают `DELETE` до commit без row lock). Ожидаемый subject `delivery#courier` MUST строиться из активного назначения (FR-444): связь закрытой доставки MUST удаляться как stale и MUST NOT восстанавливаться. Кандидат, ставший present или потерявший subject, MUST забываться.

#### Scenario: Снятие курьера в полёте
**ID:** AC-668

- **WHEN** Reassign Courier удалил `delivery#courier` курьера до commit, а проход прочитал граф после `DELETE` и `courier_id` до commit
- **THEN** relationship не восстанавливается, а после commit снятия кандидат забывается без записи

#### Scenario: Потерянная связь курьера
**ID:** AC-672

- **WHEN** доставка назначена курьеру, а `delivery#courier` отсутствует на двух проходах с интервалом не меньше grace
- **THEN** reconciler восстанавливает relationship и сохраняет её revision

#### Scenario: Закрытая доставка
**ID:** AC-693

- **WHEN** доставка delivered или failed, `courier_id` задан, а `delivery#courier` остался в SpiceDB или отсутствует
- **THEN** оставшаяся связь удаляется после grace, а отсутствующая не восстанавливается
