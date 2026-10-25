# Spec Delta

## MODIFIED Requirements

### Requirement: Courier Assignment пишет relationships курьера
**ID:** FR-469

Record Courier Assignment SHALL в Inbox unit of work до commit записывать `order#courier` и `order#delivery` из `CourierAssignedToDelivery` и сохранять `delivery_id`, `courier_id`, `courier_assigned_at`, Delivery `aggregate_version` события и `authorization_revision`. Reassignment MUST выполняться одним атомарным запросом `TOUCH` нового и `DELETE` прежнего subject; событие с версией Delivery ниже сохранённого назначения или снятия MUST NOT менять relationships (FR-564).

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

- **WHEN** приходит назначение с версией Delivery ниже сохранённой
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

Record Courier Release SHALL в Inbox unit of work до commit удалять `order#courier` сохранённого курьера по `CourierUnassignedFromDelivery` и `order#courier` с `order#delivery` по `DeliveryCancelled`, если событие относится к сохранённой Delivery и его версия Delivery не ниже сохранённой (FR-564). Снятие MUST сохраняться с версией события, в том числе без сохранённого назначения. Order и fulfillment workflow MUST NOT меняться.

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

- **WHEN** событие называет другую Delivery или его версия Delivery ниже сохранённого назначения, или после снятия приходит назначение более старой версии
- **THEN** relationships и назначение не меняются, Inbox record фиксируется

#### Scenario: SpiceDB недоступен при снятии
**ID:** AC-537

- **WHEN** удаление relationships завершается ошибкой
- **THEN** транзакция откатывается вместе с Inbox record, а redelivery повторяет событие

## ADDED Requirements

### Requirement: Порядок courier events задаёт версия Delivery
**ID:** FR-564

`orders` SHALL упорядочивать courier events заказа по Delivery `aggregate_version` из envelope и хранить её в `courier_delivery_version`; `occurred_at` MUST NOT задавать порядок. Применённое снятие MUST освобождать сохранённого курьера, даже если называет другого. Снятие без сохранённого назначения MUST ничего не удалять и запоминаться со своей версией (`remembered_ahead`).

#### Scenario: Версия, а не время
**ID:** AC-669

- **WHEN** событие несёт более позднее `occurred_at`, но версию Delivery ниже сохранённой, или более раннее `occurred_at` и версию выше
- **THEN** первое пропускается, второе применяется

#### Scenario: Снятие раньше назначения
**ID:** AC-670

- **WHEN** снятие обработано до более старого `CourierAssignedToDelivery` того же заказа
- **THEN** снятие запоминается со своей версией, и назначение, обработанное позже, не выдаёт курьеру `view`
