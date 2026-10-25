# Spec Delta

## ADDED Requirements

### Requirement: Reconciliation охватывает все relations заказа
**ID:** FR-470

Reconciler SHALL сверять `order#customer`, `order#restaurant`, `order#courier` и `order#delivery` с колонками `customer_id`, `restaurant_id`, `courier_id`, `delivery_id` строки `orders` тем же алгоритмом: orphan или stale relationship удаляется после двух проходов с интервалом не меньше grace, missing восстанавливается `TOUCH` с обновлением `authorization_revision`. Заказ без курьера MUST NOT получать relationship `courier`/`delivery`.

#### Scenario: Business-сторона назначения
**ID:** AC-523

- **WHEN** store сохраняет назначение и reconciler читает subjects relation
- **THEN** заказ без назначения не имеет subject `courier`, после назначения subject — текущий курьер

#### Scenario: Stale курьер
**ID:** AC-524

- **WHEN** в графе остался прежний курьер, а relationships текущего курьера, delivery и ресторана потеряны
- **THEN** первый проход восстанавливает missing, а прежний курьер удаляется после grace
