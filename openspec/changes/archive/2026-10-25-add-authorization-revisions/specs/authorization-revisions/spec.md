# Spec Delta

## ADDED Requirements

### Requirement: Проверка видит собственную запись relationship
**ID:** FR-447

`restaurants` и `delivery` SHALL сохранять ZedToken каждой записи relationship (`TOUCH` и `DELETE`) вместе с ресурсом и проверять права на этот ресурс с `at_least_as_fresh` по сохранённому токену.

#### Scenario: Персонал сразу после назначения
**ID:** AC-482

- **WHEN** владелец назначил пользователя персоналом и сразу проверяется `restaurant#operate_kitchen`
- **THEN** проверка разрешает доступ без ожидания репликации

#### Scenario: Отзыв действует сразу
**ID:** AC-483

- **WHEN** курьера сняли с доставки через Reassign Courier и сразу проверяется `delivery#act_as_courier`
- **THEN** проверка отказывает

### Requirement: Без revision используется minimize_latency
**ID:** FR-448

Для ресурса без сохранённой revision и для `platform:nomnom` проверка SHALL использовать `minimize_latency`; ошибка чтения revision MUST приводить к отказу authorization unavailable.

#### Scenario: Ресурс без revision
**ID:** AC-484

- **WHEN** проверяется ресурс, для которого revision не сохранена
- **THEN** запрос в SpiceDB использует `minimize_latency`
