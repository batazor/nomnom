# Proposal

## Why

Kafka тестового окружения — Deployment Redpanda с `emptyDir` и auto create topics. Topics, которые нельзя оставить auto create, объявляет Job `dead-letter-topics` скриптом `rpk`, а коннекторы Debezium регистрируют hook Jobs контекстов скриптом `curl`. Это императивный bash, порядок которого держится на before-hook Skaffold. Перезапуск Redpanda уже ломал Connect: работавший Connect успевал пересоздать свои служебные topics auto create брокера с неверными параметрами.

Strimzi описывает всё это ресурсами Kubernetes и приводит кластер к ним сам: брокер (`Kafka`, `KafkaNodePool`), topics (`KafkaTopic`), Kafka Connect (`KafkaConnect`) и коннекторы (`KafkaConnector`). Стенд при этом переходит на Apache Kafka — тот же брокер, что у testcontainers тестов контекстов и, вероятно, у production. Strimzi — проект CNCF под Apache 2.0.

## What Changes

- **Strimzi Cluster Operator** 1.3.0 ставится в платформу kind (`make kind-up`) Helm chart, CRD API `v1`.
- **Брокер — ресурсы `Kafka` и `KafkaNodePool`**: Apache Kafka 4.3.1 в KRaft, один узел с ролями broker и controller, ephemeral storage, `auto.create.topics.enable=false`. Deployment Redpanda удаляется. Bootstrap — Service `kafka-kafka-bootstrap.nomnom-infra:9092`.
- **Все topics — ресурсы `KafkaTopic`** Topic Operator (Entity Operator ресурса `Kafka`): стримы Integration Events и Change Notifications, DLQ, служебные topics Kafka Connect и `_schemas`. Job `dead-letter-topics` удаляется. Topic без ресурса создать нельзя: publish в него — ошибка.
- **Schema Registry — Confluent Schema Registry 8.4.0** отдельным Deployment в `nomnom-infra` (`schema-registry:8081`). Встроенный Registry Redpanda уходит вместе с брокером. SMT, consumers и `schema-registration` не меняются: меняется только URL.
- **Kafka Connect — ресурс `KafkaConnect`** с образом на базе `quay.io/strimzi/kafka:1.3.0-kafka-4.3.1`: плагины Debezium PostgreSQL и MongoDB и SMT `SchemaFraming` в `/opt/kafka/plugins`. Метрики — `metricsConfig` Strimzi с прежними правилами JMX exporter. Chart `ops/kind/kafka-connect/chart` рендерит этот ресурс вместо Deployment: образ, собранный Skaffold, доступен только releases.
- **Коннекторы — ресурсы `KafkaConnector`.** Конфигурация остаётся в chart контекста (`cdc.*`, ADR 37), но вместо hook Job, регистрирующего её через REST Connect, chart создаёт `KafkaConnector` в `nomnom-infra`. Пароли читает `DirectoryConfigProvider` из Secret `cdc-credentials`, смонтированного через `template` ресурса `KafkaConnect`.
- **Адреса и правила** меняются вслед за брокером: `KAFKA__BROKERS` и `SCHEMA_REGISTRY__URL` в values контекстов, NetworkPolicy consumers, проверки e2e, `console.yaml`, команды `dlq`.
- **testcontainers** тестов контекстов переходят на `apache/kafka:4.3.1` — версию брокера стенда; тесты Schema Registry — на Confluent Schema Registry вместо Redpanda.
- **Переход сразу:** стенд пересоздаётся (`make kind-down`, `make kind-up`, `make kind-deploy`), данных для переноса нет.

Не входит:

- аутентификация и ACL брокера (`KafkaUser`, TLS listeners): стенд остаётся без них, как сейчас;
- production: выбор окружения, где работает Strimzi, и provisioning его ресурсов;
- `KafkaConnect` build Strimzi (Buildah и registry для push): образ Connect по-прежнему собирает Skaffold.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `kubernetes-test-environment`: Kafka под Strimzi, topics как `KafkaTopic`, Kafka Connect как `KafkaConnect`, Confluent Schema Registry.
- `integration-event-publication`: развёртывание коннектора ресурсом `KafkaConnector` вместо hook Job.
- `integration-event-schemas`: источник схем — файлы buf-модулей, скомпилированные компилятором buf (уточнение формулировки, поведение то же).

## Impact

- **`ops/kind`:** Strimzi в Skaffold-модуле `platform`; ресурсы `Kafka`, `KafkaNodePool`, `KafkaTopic`, `KafkaConnect` и Deployment Schema Registry вместо `manifests/messaging/infrastructure.yaml` (Temporal остаётся), `dead-letter-topics.yaml`; chart `kafka-connect` с ресурсом `KafkaConnect`; образ Connect на базе Strimzi; before-hook Skaffold; NetworkPolicy; values контекстов; e2e (`infra-connectivity` и другие проверки, читающие Kafka); `console.yaml`; README и `versions.env`.
- **Helm charts `orders`, `payments`, `delivery`, `restaurants`:** `templates/cdc-connector.yaml` — `KafkaConnector` вместо ConfigMap и hook Job; `cdc.connectUrl` и `cdc.registrar` удаляются.
- **SMT `SchemaFraming`:** jar несёт зависимости, которых нет в `/opt/kafka/libs` образа Strimzi.
- **Тесты контекстов:** образы Kafka и Schema Registry в testcontainers.
- **Документация:** ADR 37 (Kafka Connect и коннекторы ресурсами Strimzi), ADR 25 (Registry), `docs/dlq.md`, `ops/kind/README.md`, README контекстов.
- **Grafana и Alloy:** scrape метрик Kafka Connect и брокера под labels pod'ов Strimzi.
