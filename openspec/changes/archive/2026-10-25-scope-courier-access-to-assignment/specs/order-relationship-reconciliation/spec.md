# Spec Delta

## MODIFIED Requirements

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

## REMOVED Requirements

### Requirement: Снятый курьер отсутствует для reconciliation
**ID:** FR-477

**Reason**: relation `order#courier` удалена из schema; доступ курьера определяет `delivery#courier`, который сверяет reconciler `delivery` (FR-563).

**Migration**: оставшиеся `order#courier` удаляются перед записью новой schema (design, раздел 5).
