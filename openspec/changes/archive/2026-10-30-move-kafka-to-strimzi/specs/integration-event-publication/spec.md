## MODIFIED Requirements

### Requirement: Развёртывание коннектора
**ID:** FR-538

Helm chart каждого контекста SHALL содержать конфигурацию коннектора (`cdc.*`) и при `cdc.enabled` создавать ресурс Strimzi `KafkaConnector` `<context>-outbox` в namespace `KafkaConnect` (`nomnom-infra`, label `strimzi.io/cluster: kafka-connect`) с `autoRestart.enabled: true`. Регистрации коннектора через REST Kafka Connect MUST NOT быть. Пароли коннектора MUST браться config provider Kafka Connect (`${dir:...}`), а не храниться в конфигурации. Тестовое kind-окружение SHALL разворачивать Kafka Connect с Debezium в `nomnom-infra` и включать `cdc` в основном release каждого контекста.

#### Scenario: Коннекторы в kind
**ID:** AC-624

- **WHEN** окружение kind разворачивается через Skaffold
- **THEN** `KafkaConnector` `orders-outbox`, `payments-outbox`, `delivery-outbox` и `restaurants-outbox` в `Ready`, коннекторы и их задачи в состоянии `RUNNING`, и процессов relay нет

#### Scenario: Задача восстанавливается без деплоя
**ID:** AC-987

- **WHEN** задача коннектора перешла в `FAILED` из-за записи без схемы, а затем схема зарегистрирована
- **THEN** Strimzi перезапускает задачу, и она публикует запись без повторного деплоя
