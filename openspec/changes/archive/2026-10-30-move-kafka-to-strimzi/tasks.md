# Tasks

## 1. Проверки до переноса

- [x] 1.1 Состав `/opt/kafka/libs` образа `quay.io/strimzi/kafka:1.3.0-kafka-4.3.1`: `connect-api`, `protobuf-java`, Jackson и их версии; от этого зависят `provided` и каталог плагина SMT (design).
- [x] 1.2 Confluent Schema Registry 8.4.0 против Kafka 4.3.1 в testcontainers: регистрация с `references`, lookup, `PUT /config`, `?format=serialized`, `/schemas/ids/{id}/versions` — ответы, на которые рассчитаны `schema-registration`, SMT и consumers.

## 2. Платформа и брокер

- [x] 2.1 Strimzi Cluster Operator 1.3.0 в Skaffold-модуле `platform` (Helm chart, `watchNamespaces: [nomnom-infra]`, limits памяти); версия в `versions.env` и README (`FR-720`).
- [x] 2.2 `Kafka kafka` и `KafkaNodePool dual`: KRaft, один узел, listener `plain` 9092, `auto.create.topics.enable=false`, replication 1, ephemeral storage, heap и limits; Entity Operator с Topic Operator (`FR-720`, `AC-982`, `AC-983`).
- [x] 2.3 Удалить Redpanda из `manifests/messaging/infrastructure.yaml` (Temporal остаётся) и Job `dead-letter-topics`.

## 3. Topics и Schema Registry

- [x] 3.1 `manifests/messaging/topics.yaml`: `KafkaTopic` стримов, Change Notifications, DLQ, служебных topics Connect и `_schemas` с partitions и конфигурацией из design (`FR-721`, `FR-723`, `AC-984`, `AC-985`).
- [x] 3.2 Deployment и Service `schema-registry` (Confluent Schema Registry 8.4.0 по digest, heap и limits, label `app.kubernetes.io/name`) (`FR-716`).
- [x] 3.3 Before-hook Skaffold: применить брокер, topics, Registry и Temporal; ждать `kafka/kafka` и всех `kafkatopic` `Ready`, Deployments `Available` (`AC-982`).

## 4. Kafka Connect и коннекторы

- [x] 4.1 Образ Connect на базе Strimzi Kafka: плагины Debezium PostgreSQL и MongoDB 3.7.0.Final из закреплённого образа Debezium, SMT `SchemaFraming` (зависимости `provided`, задача 1.1).
- [x] 4.2 Chart `ops/kind/kafka-connect/chart`: `KafkaConnect kafka-connect` вместо Deployment — bootstrap, `group.id` и topics хранения, `DirectoryConfigProvider` и Secret `cdc-credentials` через `template`, `metricsConfig` с правилами `metrics.yaml`, ресурсы; after-hook ждёт `Ready` (`FR-722`, `AC-986`).
- [x] 4.3 `templates/cdc-connector.yaml` charts `orders`, `payments`, `delivery`, `restaurants`: `KafkaConnector` в `nomnom-infra` с `autoRestart`; удалить hook Job, `cdc.connectUrl`, `cdc.registrar`; after-hook ждёт `kafkaconnector --all` `Ready` (`FR-538`, `AC-624`, `AC-987`).

## 5. Адреса, политики и проверки

- [x] 5.1 `KAFKA__BROKERS` (`kafka-kafka-bootstrap…:9092`) и `SCHEMA_REGISTRY__URL` (`schema-registry…:8081`) в `ops/kind/values`, `transforms.schema.registry.url` и `schemaRegistryUrl` коннекторов, `schema-registration`.
- [x] 5.2 NetworkPolicy consumers: egress к брокеру Strimzi и `schema-registry:8081`; labels pod'ов Strimzi.
- [x] 5.3 e2e (`infra-connectivity` и проверки, читающие Kafka или Connect), `console.yaml`, команды `dlq`, scrape Alloy для брокера и Connect.

## 6. Тесты контекстов

- [x] 6.1 testcontainers: `apache/kafka:4.3.1` в `orders`, `restaurants`, `delivery`.
- [x] 6.2 Тесты Schema Registry (`ops/schema-registration`, SMT, `orders`, `restaurants`, `delivery`) — Confluent Schema Registry рядом с Kafka вместо Redpanda.

## 7. Документация и проверка

- [x] 7.1 ADR 37 (Kafka Connect и коннекторы ресурсами Strimzi), ADR 25 (Confluent Schema Registry вместо Redpanda), `docs/dlq.md`, `ops/kind/README.md`, README контекстов; замеры памяти стенда в README.
- [x] 7.2 Пересоздать kind (`make kind-down`, `make kind-up`, `make kind-deploy`), `make kind-test`: брокер, topics и коннекторы `Ready`, путь заказа проходит, DLQ пусты; перезапуск pod брокера восстанавливает topics и Connect (`AC-985`).
