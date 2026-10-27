# Spec Delta

## ADDED Requirements

### Requirement: Update Restaurant доступен по gRPC
**ID:** FR-649

`restaurants` SHALL публиковать `UpdateRestaurant` с `restaurant_id`, `name` и явной операцией над доплатой: задать `premium_fee` или убрать её (`clear_premium_fee`). Одновременно заданные `premium_fee` и `clear_premium_fee` MUST отвечать `INVALID_ARGUMENT`. Вызов SHALL требовать `restaurant#manage`.

#### Scenario: Владелец задаёт доплату
**ID:** AC-852

- **WHEN** владелец вызывает `UpdateRestaurant` с `premium_fee`
- **THEN** ответ содержит ресторан с доплатой и новую версию

#### Scenario: Без права manage
**ID:** AC-853

- **WHEN** пользователь без `restaurant#manage` вызывает `UpdateRestaurant`
- **THEN** вызов отклоняется `PERMISSION_DENIED`, ресторан не меняется
