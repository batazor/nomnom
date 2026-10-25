# Spec Delta

## MODIFIED Requirements

### Requirement: Reconciliation охватывает все relations заказа
**ID:** FR-470

Reconciler SHALL сверять `order#customer`, `order#restaurant`, `order#courier` и `order#delivery` с колонками `customer_id`, `restaurant_id`, `courier_id`, `delivery_id` строки `orders` тем же алгоритмом: orphan или stale relationship удаляется после двух проходов с интервалом не меньше grace, missing восстанавливается `TOUCH` с обновлением `authorization_revision` по правилу FR-566. Заказ без курьера MUST NOT получать relationship `courier`/`delivery`.

#### Scenario: Business-сторона назначения
**ID:** AC-523

- **WHEN** store сохраняет назначение и reconciler читает subjects relation
- **THEN** заказ без назначения не имеет subject `courier`, после назначения subject — текущий курьер

#### Scenario: Stale курьер
**ID:** AC-524

- **WHEN** в графе остался прежний курьер, а relationships текущего курьера, delivery и ресторана потеряны
- **THEN** первый проход восстанавливает missing, а прежний курьер удаляется после grace

## ADDED Requirements

### Requirement: Missing relationship восстанавливается только по зафиксированному subject
**ID:** FR-566

Reconciler `orders` SHALL восстанавливать missing relationship, только если subject, перечитанный под `FOR SHARE` lock строки `orders` после чтения графа, совпадает; `TOUCH` и revision MUST писаться в той же транзакции. Record Courier Assignment и Release MUST держать `FOR UPDATE` lock строки от чтения назначения до commit.

#### Scenario: Переназначение в полёте
**ID:** AC-667

- **WHEN** переназначение A→B записало в SpiceDB `TOUCH` B и `DELETE` A и ещё не закоммичено, а проход прочитал граф после записи и `courier_id = A` до commit
- **THEN** reconciler ждёт commit, видит B и не восстанавливает `order#courier@user:A`
