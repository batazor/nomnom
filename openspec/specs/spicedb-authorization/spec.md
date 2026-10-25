# spicedb-authorization Specification

## Purpose
Определяет authoritative authorization bounded contexts через SpiceDB (ADR 18): общую schema и grants развёртывания, обязательную конфигурацию SpiceDB, fail-closed проверки и creation-time relationships `orders`.

## Requirements

### Requirement: Общая schema и grants развёртывания
**ID:** FR-434

Система SHALL хранить authorization schema в `authorization/schema.zed`, а grants `service` identities — в `authorization/bootstrap.yaml` с assertions, проверяемыми `zed validate`. Fulfillment-команды MUST быть permissions `platform:nomnom`, выданными relation `fulfillment`.

#### Scenario: Grant orders-worker
**ID:** AC-464

- **WHEN** выполняется `make authz-validate`
- **THEN** `service:orders-worker` имеет `manage_payments`, `coordinate_fulfillment` и `coordinate_delivery` на `platform:nomnom`, а другой service — нет

### Requirement: Контекст проверяет permission в SpiceDB
**ID:** FR-435

При заданном `SPICEDB__ENDPOINT` authorization-порт каждого контекста SHALL выполнять `CheckPermission` по таблице соответствия design.md. Ошибка или timeout SpiceDB MUST приводить к отказу «authorization unavailable», а не к разрешению.

#### Scenario: Worker вызывает команду соседа
**ID:** AC-465

- **WHEN** `service:orders-worker` вызывает `AuthorizePayment`, `ReceiveOrderRequest` или `CreateDelivery`
- **THEN** проверка в SpiceDB разрешает вызов

#### Scenario: SpiceDB недоступен
**ID:** AC-466

- **WHEN** SpiceDB не отвечает в пределах `SPICEDB__TIMEOUT`
- **THEN** use case отклоняется как authorization unavailable

### Requirement: orders записывает customer relationship при оформлении
**ID:** FR-436

`placeorder` SHALL выполнять `TOUCH order:<id>#customer@user:<customer_id>` до commit, а последующие checks этого `Order` SHALL использовать ZedToken записи.

#### Scenario: Клиент видит свой заказ сразу после оформления
**ID:** AC-467

- **WHEN** customer оформил заказ и сразу запрашивает его
- **THEN** `order:<id>#view` разрешён для `user:<customer_id>`
