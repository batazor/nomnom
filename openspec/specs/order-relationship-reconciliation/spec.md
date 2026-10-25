# order-relationship-reconciliation Specification

## Purpose
Определяет сверку authorization graph `order#customer` в SpiceDB с business state `orders` в PostgreSQL: двухфазное удаление orphan и stale relationships после grace и восстановление missing relationships.

## Requirements

### Requirement: Orphan relationship удаляется после grace-периода
**ID:** FR-441

Reconciler SHALL удалять `order#customer` relationship, для которой нет `Order` с тем же customer в PostgreSQL, только если она оставалась такой на проходах, разделённых не менее чем grace-периодом. Relationship, ставшая валидной до удаления, MUST сохраняться.

#### Scenario: Связь неудавшегося оформления
**ID:** AC-473

- **WHEN** relationship `order:X#customer` существует, а `Order` X нет на двух проходах с интервалом не меньше grace
- **THEN** reconciler удаляет relationship

#### Scenario: Связь оформляемого заказа
**ID:** AC-474

- **WHEN** relationship найдена без `Order` на первом проходе, а до следующего прохода `Order` закоммичен
- **THEN** relationship не удаляется

### Requirement: Missing relationship восстанавливается
**ID:** FR-442

Для `Order` без relationship своего customer reconciler SHALL выполнить `TOUCH order#customer` и сохранить ZedToken в `authorization_revision`.

#### Scenario: SpiceDB потерял данные
**ID:** AC-475

- **WHEN** `Order` есть в PostgreSQL, а relationship `order#customer` отсутствует
- **THEN** после прохода reconciler relationship существует и customer имеет `view` на заказ

### Requirement: Reconciliation охватывает все relations заказа
**ID:** FR-470

Reconciler SHALL сверять `order#customer`, `order#restaurant` и `order#delivery` с колонками `customer_id`, `restaurant_id`, `delivery_id` строки `orders` тем же алгоритмом: orphan или stale relationship удаляется после двух проходов с интервалом не меньше grace, missing восстанавливается `TOUCH` с обновлением `authorization_revision` по правилу FR-566. Заказ без Delivery MUST NOT получать relationship `delivery`.

#### Scenario: Business-сторона назначения
**ID:** AC-523

- **WHEN** store сохраняет назначение и reconciler читает subjects relation
- **THEN** заказ без назначения не имеет subject `delivery`, после назначения subject — Delivery назначения

#### Scenario: Stale курьер
**ID:** AC-524

- **WHEN** в графе осталась прежняя Delivery, а relationships текущей Delivery и ресторана потеряны
- **THEN** первый проход восстанавливает missing, а прежняя Delivery удаляется после grace

### Requirement: Missing relationship восстанавливается только по зафиксированному subject
**ID:** FR-566

Reconciler `orders` SHALL восстанавливать missing relationship, только если subject, перечитанный под `FOR SHARE` lock строки `orders` после чтения графа, совпадает; `TOUCH` и revision MUST писаться в той же транзакции. Record Courier Assignment и Release MUST держать `FOR UPDATE` lock строки от чтения назначения до commit.

#### Scenario: Переназначение в полёте
**ID:** AC-667

- **WHEN** назначение новой Delivery B записало в SpiceDB `TOUCH` B и `DELETE` A и ещё не закоммичено, а проход прочитал граф после записи и `delivery_id = A` до commit
- **THEN** reconciler ждёт commit, видит B и не восстанавливает `order#delivery@delivery:A`
