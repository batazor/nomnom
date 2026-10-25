# Spec Delta

## MODIFIED Requirements

### Requirement: Authoritative authorization сценариев выполнения
**ID:** FR-314

Сценарии выполнения доставки SHALL проверять право subject действовать от имени указанного курьера (`courier#act`), а для действий диспетчера — `platform:nomnom#dispatch_delivery`, до доменной операции. Отказ authorization MUST NOT менять `Delivery`, `Courier` и Outbox.

#### Scenario: Отказ authorization при Pickup
**ID:** AC-335

- **WHEN** subject без права действовать от имени назначенного курьера подтверждает Pickup
- **THEN** сценарий отклоняется как permission denied, `Delivery` остаётся assigned и Outbox пуст

#### Scenario: Право диспетчера
**ID:** AC-694

- **WHEN** operator платформы сообщает о неудаче доставки, на которую не назначен
- **THEN** проверка `dispatch_delivery` разрешает действие, а пользователь без relation `operator` получает permission denied
