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

### Requirement: Relationships restaurants и delivery сверяются с business state
**ID:** FR-445

Каждый контекст SHALL периодически удалять orphan/stale relationships своих definitions после grace на двух проходах и восстанавливать missing relationships.

#### Scenario: Потерянная связь владельца
**ID:** AC-478

- **WHEN** ресторан существует, а `restaurant#owner` в SpiceDB отсутствует на двух проходах с интервалом не меньше grace
- **THEN** после второго прохода владелец снова имеет `restaurant#manage`

### Requirement: Reconciler restaurants восстанавливает связь только при неизменной версии ресторана
**ID:** FR-572

Reconciler `restaurants` MUST восстанавливать missing `restaurant#owner` и `restaurant#staff` с revision, только если связь отсутствовала у того же пользователя на двух проходах не реже grace и оба прохода видели одну версию документа ресторана; другая версия MUST начинать grace заново. Собственная запись revision reconciler'а MAY переносить кандидатов на записанную версию. Кандидат, ставший present или исчезнувший из ресторана, MUST забываться.

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

### Requirement: Missing relationship delivery восстанавливается после grace
**ID:** FR-563

Reconciler `delivery` SHALL восстанавливать missing `courier#self` и `delivery#courier` с сохранением revision, только если relationship отсутствовала при том же subject и той же версии строки Courier/Delivery на двух проходах с интервалом не меньше grace (writers делают `DELETE` до commit без row lock); другая версия MUST начинать grace заново. Кандидат, ставший present или потерявший subject, MUST забываться.

#### Scenario: Снятие курьера в полёте
**ID:** AC-668

- **WHEN** Reassign Courier удалил `delivery#courier` курьера до commit, а проход прочитал граф после `DELETE` и `courier_id` до commit
- **THEN** relationship не восстанавливается, а после commit снятия кандидат забывается без записи

#### Scenario: Потерянная связь курьера
**ID:** AC-672

- **WHEN** доставка назначена курьеру, а `delivery#courier` отсутствует на двух проходах с интервалом не меньше grace
- **THEN** reconciler восстанавливает relationship и сохраняет её revision

#### Scenario: Повторное назначение и снятие между проходами
**ID:** AC-760

- **WHEN** первый проход видит снятие курьера в полёте, затем снятие закоммичено, курьер снова назначен и второй Reassign Courier удалил `delivery#courier` до commit, а второй проход через grace видит курьера назначенным без связи
- **THEN** relationship не восстанавливается, версия доставки другая и grace начинается заново; после commit второго снятия кандидат забывается

### Requirement: Связь курьера с доставкой следует активному назначению
**ID:** FR-573

Ожидаемый subject `delivery#courier` MUST строиться из активного назначения (FR-444): связь закрытой доставки MUST удаляться как stale и MUST NOT восстанавливаться.

#### Scenario: Закрытая доставка
**ID:** AC-693

- **WHEN** доставка delivered или failed, `courier_id` задан, а `delivery#courier` остался в SpiceDB или отсутствует
- **THEN** оставшаяся связь удаляется после grace, а отсутствующая не восстанавливается
