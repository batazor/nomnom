# Spec Delta

## ADDED Requirements

### Requirement: Get Menu доступен по gRPC
**ID:** FR-493

`restaurants` SHALL публиковать RPC `GetMenu(restaurant_id)`, возвращающий Menu Item с названием, описанием, ценой и доступностью по правилам FR-216, без authorization. Отсутствующее меню MUST возвращаться `NOT_FOUND`.

#### Scenario: Меню по gRPC
**ID:** AC-561

- **WHEN** вызывающий запрашивает `GetMenu` ресторана с опубликованным меню
- **THEN** ответ содержит все Menu Item с текущими ценами и доступностью
