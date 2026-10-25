# Spec Delta

## ADDED Requirements

### Requirement: Order видят ресторан и назначенный курьер
**ID:** FR-467

`definition order` SHALL содержать relations `restaurant: restaurant`, `courier: user`, `delivery: delivery` и `permission view = customer + restaurant->view_orders + courier + delivery->act_as_courier`; `cancel` MUST оставаться только у customer.

#### Scenario: Пути прав заказа
**ID:** AC-517

- **WHEN** `make authz-validate` проверяет `validation.yaml`
- **THEN** owner и staff ресторана и назначенный курьер имеют `view`, но не `cancel`, а посторонний пользователь не имеет `view`

### Requirement: Placement пишет customer и restaurant одним запросом
**ID:** FR-468

Place Order SHALL записывать `order#customer` и `order#restaurant` одним `WriteRelationships` до local commit и сохранять ZedToken в `authorization_revision`.

#### Scenario: Creation-time relationships
**ID:** AC-516

- **WHEN** Place Order пишет relationships
- **THEN** SpiceDB получает один запрос с двумя `TOUCH`

### Requirement: Courier Assignment пишет relationships курьера
**ID:** FR-469

Record Courier Assignment SHALL в Inbox unit of work до commit записывать `order#courier` и `order#delivery` из `CourierAssignedToDelivery` и сохранять `delivery_id`, `courier_id`, `courier_assigned_at` и `authorization_revision`. Reassignment MUST выполняться одним атомарным запросом `TOUCH` нового и `DELETE` прежнего subject; событие старше сохранённого назначения MUST NOT менять relationships.

#### Scenario: Назначение курьера
**ID:** AC-518

- **WHEN** consumer обрабатывает `CourierAssignedToDelivery`
- **THEN** курьер получает `view` на заказ, а назначение сохранено в `orders`

#### Scenario: Переназначение
**ID:** AC-519

- **WHEN** приходит назначение другого курьера для заказа с уже записанным фактом
- **THEN** новый курьер получает `view`, прежний теряет его, а следующая проверка читает не старее сохранённой revision

#### Scenario: Событие не по порядку
**ID:** AC-520

- **WHEN** приходит назначение старше сохранённого
- **THEN** relationships и назначение не меняются

#### Scenario: SpiceDB недоступен
**ID:** AC-521

- **WHEN** запись relationships завершается ошибкой
- **THEN** транзакция откатывается вместе с Inbox record, workflow не уведомляется, а redelivery повторяет событие

#### Scenario: Проверка против SpiceDB
**ID:** AC-522

- **WHEN** adapter пишет placement и назначения в SpiceDB со schema `authorization/schema.zed`
- **THEN** owner ресторана видит заказ, курьер видит его до переназначения, никто кроме customer не имеет `cancel`

### Requirement: Снятие курьера отзывает relationships курьера
**ID:** FR-476

Record Courier Release SHALL в Inbox unit of work до commit удалять `order#courier` по `CourierUnassignedFromDelivery` и `order#courier` с `order#delivery` по `DeliveryCancelled`, если сохранённое назначение относится к той же Delivery и курьеру и не новее события. Order и fulfillment workflow MUST NOT меняться.

#### Scenario: Снятие курьера без замены
**ID:** AC-534

- **WHEN** consumer обрабатывает `CourierUnassignedFromDelivery` назначенного курьера
- **THEN** курьер теряет `view`, `order#delivery` остаётся, `courier_id` очищен, revision сохранена, workflow не уведомляется

#### Scenario: Отмена Delivery
**ID:** AC-535

- **WHEN** consumer обрабатывает `DeliveryCancelled` Delivery сохранённого назначения
- **THEN** `order#courier` и `order#delivery` удалены, `courier_id` и `delivery_id` очищены

#### Scenario: Событие не относится к сохранённому назначению
**ID:** AC-536

- **WHEN** событие называет другую Delivery или другого курьера либо старше сохранённого назначения, или после снятия приходит более старое назначение
- **THEN** relationships и назначение не меняются, Inbox record фиксируется

#### Scenario: SpiceDB недоступен при снятии
**ID:** AC-537

- **WHEN** удаление relationships завершается ошибкой
- **THEN** транзакция откатывается вместе с Inbox record, а redelivery повторяет событие
