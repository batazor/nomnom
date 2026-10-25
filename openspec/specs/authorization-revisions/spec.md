# authorization-revisions Specification

## Purpose
Определяет хранение ZedToken записей relationships вместе с ресурсами `restaurants` и `delivery` и проверки прав этих ресурсов `at_least_as_fresh`.

## Requirements

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

### Requirement: Revision вызывающего из metadata
**ID:** FR-449

`restaurants` SHALL читать ZedToken из gRPC metadata `authorization-revision` и проверять `platform:nomnom` и ресурсы без сохранённой revision с `at_least_as_fresh` по нему. Сохранённая revision ресурса MUST иметь приоритет; пустое значение или длиннее 1024 символов MUST игнорироваться.

#### Scenario: Оператор сразу после выдачи права
**ID:** AC-485

- **WHEN** оператору выдано `platform#operator`, и он вызывает `RegisterRestaurant` с ZedToken этой записи в `authorization-revision`
- **THEN** регистрация проходит без ожидания репликации
