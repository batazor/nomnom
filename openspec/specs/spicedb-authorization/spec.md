# spicedb-authorization Specification

## Purpose
Определяет authoritative authorization bounded contexts через SpiceDB (ADR 18): общую schema и grants развёртывания, обязательную конфигурацию SpiceDB, fail-closed проверки и creation-time relationships `orders`.

## Requirements

### Requirement: Общая schema и grants развёртывания
**ID:** FR-434

Система SHALL хранить authorization schema в `authorization/schema.zed`, а grants `service` identities — в `authorization/bootstrap.yaml` с assertions, проверяемыми `zed validate`. Fulfillment-команды MUST быть permissions `platform:nomnom`, выданными relation `fulfillment`.

#### Scenario: Grant orders-worker
**ID:** AC-464

- **WHEN** выполняется `make authz-validate`
- **THEN** `service:orders-worker` имеет `manage_payments`, `coordinate_fulfillment` и `coordinate_delivery` на `platform:nomnom`, а другой service — нет

### Requirement: Права диспетчера доставки
**ID:** FR-726

Действия диспетчера доставки SHALL быть permission `platform:nomnom` `dispatch_delivery`, выданной relation `operator`. Offer доставки заказа выбранному курьеру MUST быть permission `offer_delivery`, выданной relations `operator` и `dispatcher`; `service` identities, включая `service:orders-worker`, MUST NOT получать ни одну из них.

#### Scenario: Grant диспетчера
**ID:** AC-695

- **WHEN** выполняется `make authz-validate`
- **THEN** operator платформы имеет `dispatch_delivery`, а курьер и `service:orders-worker` — нет

#### Scenario: Offer выбранному курьеру не у workflow
**ID:** AC-989

- **WHEN** выполняется `make authz-validate`
- **THEN** `service:orders-worker` не имеет `offer_delivery`, а schema выдаёт её только relations `operator` и `dispatcher`

### Requirement: Контекст проверяет permission в SpiceDB
**ID:** FR-435

При заданном `SPICEDB__ENDPOINT` authorization-порт каждого контекста SHALL выполнять `CheckPermission` по таблице соответствия design.md. Ошибка или timeout SpiceDB MUST приводить к отказу «authorization unavailable», а не к разрешению.

#### Scenario: Worker вызывает команду соседа
**ID:** AC-465

- **WHEN** `service:orders-worker` вызывает `AuthorizePayment`, `ReceiveOrderRequest` или `CreateDelivery`
- **THEN** проверка в SpiceDB разрешает вызов

#### Scenario: SpiceDB недоступен
**ID:** AC-466

- **WHEN** SpiceDB не отвечает в пределах `SPICEDB__TIMEOUT`
- **THEN** use case отклоняется как authorization unavailable

### Requirement: orders записывает customer relationship при оформлении
**ID:** FR-436

`placeorder` SHALL выполнять `TOUCH order:<id>#customer@user:<customer_id>` до commit, а последующие checks этого `Order` SHALL использовать ZedToken записи.

#### Scenario: Клиент видит свой заказ сразу после оформления
**ID:** AC-467

- **WHEN** customer оформил заказ и сразу запрашивает его
- **THEN** `order:<id>#view` разрешён для `user:<customer_id>`

### Requirement: Order видят ресторан и назначенный курьер
**ID:** FR-467

`definition order` SHALL содержать relations `customer: user`, `restaurant: restaurant`, `delivery: delivery` и `permission view = customer + restaurant->view_orders + delivery->act_as_courier`; relation `courier` MUST NOT существовать: доступ курьера к заказу определяет только `delivery`. `cancel` MUST оставаться только у customer.

#### Scenario: Пути прав заказа
**ID:** AC-517

- **WHEN** `make authz-validate` проверяет `validation.yaml`
- **THEN** owner и staff ресторана и курьер с `delivery#courier` доставки заказа имеют `view`, но не `cancel`, а посторонний пользователь и курьер без `delivery#courier` не имеют `view`

### Requirement: Placement пишет customer и restaurant одним запросом
**ID:** FR-468

Place Order SHALL записывать `order#customer` и `order#restaurant` одним `WriteRelationships` до local commit и сохранять ZedToken в `authorization_revision`.

#### Scenario: Creation-time relationships
**ID:** AC-516

- **WHEN** Place Order пишет relationships
- **THEN** SpiceDB получает один запрос с двумя `TOUCH`

### Requirement: Courier Assignment пишет relationships курьера
**ID:** FR-469

Record Courier Assignment SHALL в Inbox unit of work до commit записывать `order#delivery` из `CourierAssignedToDelivery` и сохранять `delivery_id`, `courier_id`, `courier_assigned_at`, Delivery `aggregate_version` события и `authorization_revision`.

#### Scenario: Назначение курьера
**ID:** AC-518

- **WHEN** consumer обрабатывает `CourierAssignedToDelivery`
- **THEN** `order#delivery` записан, назначение сохранено в `orders`, и курьер видит заказ через `delivery#courier`

#### Scenario: SpiceDB недоступен
**ID:** AC-521

- **WHEN** запись relationships завершается ошибкой
- **THEN** транзакция откатывается вместе с Inbox record, workflow не уведомляется, а redelivery повторяет событие

#### Scenario: Проверка против SpiceDB
**ID:** AC-522

- **WHEN** adapter пишет placement и назначения в SpiceDB со schema `authorization/schema.zed`
- **THEN** owner ресторана видит заказ, курьер видит его, пока у него есть `delivery#courier`, никто кроме customer не имеет `cancel`

### Requirement: Смена Delivery атомарна, переназначение в той же Delivery не пишет в SpiceDB
**ID:** FR-574

Смена Delivery MUST выполняться одним атомарным запросом `TOUCH` новой и `DELETE` прежней; переназначение в пределах той же Delivery MUST NOT писать в SpiceDB. Событие с версией Delivery ниже сохранённого назначения или снятия MUST NOT менять relationships (FR-564).

#### Scenario: Переназначение
**ID:** AC-519

- **WHEN** приходит назначение другого курьера той же Delivery
- **THEN** назначение в `orders` обновлено, а запросов к SpiceDB нет

#### Scenario: Событие не по порядку
**ID:** AC-520

- **WHEN** приходит назначение с версией Delivery ниже сохранённой
- **THEN** relationships и назначение не меняются

### Requirement: Снятие курьера отзывает relationships курьера
**ID:** FR-476

Record Courier Release SHALL в Inbox unit of work до commit удалять `order#delivery` по `DeliveryCancelled`, если событие относится к сохранённой Delivery и его версия Delivery не ниже сохранённой (FR-564). `CourierUnassignedFromDelivery` MUST только очищать `courier_id`: доступ курьера отзывает `delivery`. Снятие MUST сохраняться с версией события, в том числе без сохранённого назначения. Order и fulfillment workflow MUST NOT меняться.

#### Scenario: Снятие курьера без замены
**ID:** AC-534

- **WHEN** consumer обрабатывает `CourierUnassignedFromDelivery` назначенного курьера
- **THEN** `courier_id` очищен, версия сохранена, `order#delivery` остаётся, запросов к SpiceDB нет, workflow не уведомляется

#### Scenario: Отмена Delivery
**ID:** AC-535

- **WHEN** consumer обрабатывает `DeliveryCancelled` Delivery сохранённого назначения
- **THEN** `order#delivery` удалён, `courier_id` и `delivery_id` очищены, revision сохранена

#### Scenario: Событие не относится к сохранённому назначению
**ID:** AC-536

- **WHEN** событие называет другую Delivery или его версия Delivery ниже сохранённого назначения, или после снятия приходит назначение более старой версии
- **THEN** relationships и назначение не меняются, Inbox record фиксируется

#### Scenario: SpiceDB недоступен при снятии
**ID:** AC-537

- **WHEN** удаление `order#delivery` завершается ошибкой
- **THEN** транзакция откатывается вместе с Inbox record, а redelivery повторяет событие

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
- **THEN** снятие запоминается со своей версией, и назначение, обработанное позже, не меняет назначение и relationships
