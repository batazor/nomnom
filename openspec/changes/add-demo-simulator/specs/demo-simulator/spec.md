# Spec Delta

## ADDED Requirements

### Requirement: Переключение Simulator
**ID:** FR-604

`orders`, `restaurants` и `delivery` SHALL предоставлять RPC `GetSimulator` и `SetSimulatorEnabled(enabled)`, отвечающие текущим состоянием `enabled`. Оба RPC MUST требовать `platform#operate_simulator`; отказ — `PERMISSION_DENIED`. Состояние SHALL храниться в хранилище сервиса и переживать рестарт; новый сервис стартует с выключенным Simulator.

#### Scenario: Оператор включает Simulator
**ID:** AC-761

- **WHEN** operator вызывает `SetSimulatorEnabled(true)`
- **THEN** сервис отвечает `enabled = true`, а после рестарта `GetSimulator` отвечает `enabled = true`

#### Scenario: Не оператор
**ID:** AC-762

- **WHEN** покупатель или владелец ресторана вызывает `GetSimulator` или `SetSimulatorEnabled`
- **THEN** сервис отвечает `PERMISSION_DENIED`, состояние не меняется

### Requirement: Действия Simulator
**ID:** FR-605

Пока Simulator включён, сервис SHALL периодически находить сущности, ожидающие действия участника, и для каждой выполнять одно действие через настоящий use case после случайной задержки в `[SIMULATOR__MIN_DELAY, SIMULATOR__MAX_DELAY]`, отсчитанной от момента, когда Simulator увидел сущность. Отказ use case MUST записываться в лог и не останавливать Simulator. Выключенный Simulator MUST NOT выполнять действий.

#### Scenario: Кухня принимает заказ
**ID:** AC-763

- **GIVEN** Simulator `restaurants` включён
- **WHEN** приходит Restaurant Order в `RECEIVED`
- **THEN** не раньше `SIMULATOR__MIN_DELAY` и не позже `SIMULATOR__MAX_DELAY` плюс интервал опроса он принят или отклонён от имени владельца ресторана

#### Scenario: Выключенный Simulator
**ID:** AC-764

- **WHEN** Simulator выключен
- **THEN** заказы, офферы и доставки не меняются без участника

### Requirement: Исходы Simulator
**ID:** FR-608

Simulator SHALL выбирать исход каждого действия случайно с весами из таблицы «Исходы» в `design.md`; успешный исход MUST быть самым вероятным в каждом состоянии.

#### Scenario: Курьер отклоняет оффер
**ID:** AC-768

- **WHEN** у курьера pending offer, и случайное значение попадает в 15% reject
- **THEN** Simulator вызывает `RejectDeliveryOffer` от имени этого курьера

### Requirement: Конфиг Simulator
**ID:** FR-606

Сервисы SHALL читать `SIMULATOR__INTERVAL`, `SIMULATOR__MIN_DELAY`, `SIMULATOR__MAX_DELAY`, а `orders` — ещё `SIMULATOR__CUSTOMER_ID`. Пустое или невалидное значение и `MIN_DELAY > MAX_DELAY` MUST останавливать процесс на старте.

#### Scenario: Невалидный интервал задержек
**ID:** AC-765

- **WHEN** `SIMULATOR__MIN_DELAY=3m`, `SIMULATOR__MAX_DELAY=1m`
- **THEN** процесс завершается на старте с ошибкой конфига

### Requirement: Переключатель Simulator в UI
**ID:** FR-607

BFF SHALL предоставлять query `simulator { enabled }` и mutation `setSimulatorEnabled(enabled)`, вызывающие три сервиса от имени пользователя; `enabled` — `true`, только если включён во всех трёх. UI SHALL показывать в шапке переключатель «Эмулятор», если `simulator` ответил без ошибки, и не показывать его при `PermissionDenied` или `Unauthenticated`. Пока mutation выполняется, переключатель MUST быть в pending; состояние меняется только по ответу.

#### Scenario: Оператор включает эмулятор
**ID:** AC-766

- **WHEN** operator включает переключатель «Эмулятор»
- **THEN** BFF вызывает `SetSimulatorEnabled(true)` в трёх сервисах, и после ответа переключатель включён

#### Scenario: Покупатель
**ID:** AC-767

- **WHEN** `simulator` отвечает `PermissionDenied`
- **THEN** шапка не показывает переключатель
