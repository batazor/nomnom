## ADDED Requirements

### Requirement: Схемы Integration Events регистрируются из buf
**ID:** FR-712

Схемы Integration Events SHALL регистрироваться в Schema Registry только шагом деплоя `schema-registration` из buf image модулей контекстов: файлы из явного списка `ops/schema-registration/event-files.yaml` вместе с импортированными файлами репозитория. Producers, SMT и consumers MUST NOT регистрировать схемы.

#### Scenario: Повторная регистрация
**ID:** AC-972

- **WHEN** `schema-registration` запускается повторно без изменений `.proto`
- **THEN** новые версии subjects не появляются, утилита завершается успешно

### Requirement: Subject события — его event_type
**ID:** FR-717

Subject Integration Event SHALL быть полным именем protobuf-сообщения, равным его `event_type`. Импортированный файл репозитория MUST регистрироваться под subject = путь файла и указываться в `references` схемы события.

#### Scenario: Регистрация событий delivery
**ID:** AC-971

- **WHEN** `schema-registration` запускается на пустом Registry
- **THEN** subjects `nomnom.delivery.v1.CourierAssignedToDelivery` … `nomnom.delivery.v1.DeliveryCancelled` существуют, у всех один id схемы файла `nomnom/delivery/v1/events.proto`

### Requirement: Registry проверяет совместимость FULL_TRANSITIVE
**ID:** FR-713

`schema-registration` SHALL выставлять глобальный режим совместимости Registry `FULL_TRANSITIVE` до регистрации. Несовместимая новая версия MUST завершать шаг ошибкой и останавливать деплой. `buf breaking` MUST оставаться проверкой PR.

#### Scenario: Удалённое поле
**ID:** AC-973

- **WHEN** из зарегистрированного сообщения удалено поле и `schema-registration` пытается зарегистрировать новую версию
- **THEN** Registry отвечает `409`, утилита завершается с ненулевым кодом, новая версия не зарегистрирована

### Requirement: SMT оформляет value Integration Event в wire format
**ID:** FR-714

Коннектор Outbox SHALL после `EventRouter` применять transform `SchemaFraming` к записям topics из `transforms.schema.topics`: value MUST становиться `0x00`, id последней версии subject `event_type` (4 байта big-endian), индексы сообщения и исходные байты protobuf. Записи прочих topics MUST проходить без изменений.

#### Scenario: Событие стрима получает префикс
**ID:** AC-974

- **WHEN** Confirm Pickup записывает `nomnom.delivery.v1.DeliveryPickedUp` в Outbox
- **THEN** value записи `delivery.v1.delivery-events` начинается с `0x00` и id subject `nomnom.delivery.v1.DeliveryPickedUp`, за которыми следуют индексы и байты protobuf из Outbox

#### Scenario: Change Notification без изменений
**ID:** AC-976

- **WHEN** `orders` записывает Order Change Notification в `orders.v1.order-changes`
- **THEN** value в Kafka — JSON `{"__typename":"Order","id":…}` без префикса

### Requirement: SMT не пропускает запись без схемы
**ID:** FR-718

Запись topic из `transforms.schema.topics` без `event_type`, с `event_type` без subject, с value, которое не разбирается этим сообщением, или при недоступном Registry MUST переводить задачу коннектора в `FAILED` без пропуска записи. Пустые `topics` или `registry.url` MUST не давать коннектору стартовать.

#### Scenario: Незарегистрированный тип останавливает коннектор
**ID:** AC-975

- **WHEN** Outbox содержит запись стрима Integration Events с `event_type` без subject в Registry
- **THEN** задача коннектора в `FAILED`, ни эта, ни последующие записи не опубликованы до регистрации схемы и restart задачи

### Requirement: Consumers проверяют wire format и subject
**ID:** FR-715

Consumers `orders`, `restaurants` и `delivery` SHALL перед разбором payload проверять wire format и то, что subjects id схемы (`GET /schemas/ids/{id}/versions`) содержат `event_type` записи. Нет префикса, неизвестный id или несовпадение subject MUST давать `deserialization_error` и отправку в `<stream>.dlq`.

#### Scenario: Запись без префикса
**ID:** AC-977

- **WHEN** в стрим попадает value из голых байтов protobuf
- **THEN** consumer отправляет запись в `<stream>.dlq` с `deserialization_error` и продолжает читать partition

#### Scenario: Subject не совпадает с event_type
**ID:** AC-978

- **WHEN** value оформлено id схемы subject `nomnom.delivery.v1.DeliveryCompleted`, а header `event_type` — `nomnom.delivery.v1.DeliveryPickedUp`
- **THEN** consumer отправляет запись в `<stream>.dlq` с `deserialization_error`, handler не вызывается

### Requirement: Registry — обязательная зависимость consumers
**ID:** FR-719

Конфиг `SCHEMA_REGISTRY__URL` consumers SHALL быть обязательным: пустой или невалидный — падение на старте. Недоступный Registry MUST быть transient-ошибкой: partition ждёт, запись не уходит в DLQ.

#### Scenario: Registry недоступен
**ID:** AC-979

- **WHEN** consumer получает запись с id схемы, которого нет в кеше, а Registry не отвечает
- **THEN** запись не подтверждается и не уходит в DLQ; после восстановления Registry она обрабатывается
