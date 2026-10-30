## ADDED Requirements

### Requirement: Schema Registry в тестовом окружении
**ID:** FR-716

Тестовое kind-окружение SHALL включать Schema Registry Redpanda (`kafka.nomnom-infra.svc.cluster.local:8081`) и регистрировать схемы hook Job release `schema-registration`, от которого зависит release `kafka-connect`: коннекторы стартуют после регистрации. Job, завершившийся ошибкой, MUST проваливать release и деплой. Egress к Registry MUST быть открыт consumers `orders`, `restaurants` и `delivery`.

#### Scenario: Деплой регистрирует схемы до consumers
**ID:** AC-980

- **WHEN** окружение kind разворачивается через Skaffold на сброшенном кластере
- **THEN** Job `schema-registration` в `Complete` до установки `kafka-connect`, subjects всех Integration Events зарегистрированы, коннекторы `RUNNING`, а consumers читают события без записей в DLQ
