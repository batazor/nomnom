# Spec Delta

## ADDED Requirements

### Requirement: restaurants-consumer в тестовом окружении
**ID:** FR-499

Тестовое kind-окружение SHALL разворачивать `restaurants-consumer` отдельным release chart `restaurants` с тем же образом, без Service и миграций, после release `restaurants`, с Kafka `nomnom-infra` и MongoDB `restaurants`.

#### Scenario: Consumer готов
**ID:** AC-569

- **WHEN** `make kind-deploy` завершён
- **THEN** pod `restaurants-consumer` Ready, подключён к Kafka и MongoDB под существующими NetworkPolicy
