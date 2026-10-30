# syntax=docker/dockerfile:1.7
#
# Образ Kafka Connect ресурса KafkaConnect kafka-connect (FR-722, ADR 37): Strimzi Kafka с
# плагинами в /opt/kafka/plugins. Метрики отдаёт metricsConfig Strimzi (JMX exporter :9404 по
# правилам chart/files/metrics.yaml), свой агент образу не нужен.
#
# SMT SchemaFraming (FR-714): value стримов Integration Events — wire format Schema Registry.
# connect-api, protobuf-java и Jackson — provided: те же версии лежат в /opt/kafka/libs Strimzi
# Kafka 4.3.1. Тесты модуля (testcontainers) идут mvn package вне сборки образа.
FROM maven:3.10.0-eclipse-temurin-25@sha256:9d329e21adce7c0cc53f2fd21e73c127a247c73287b8503196136ec5cff52328 AS schema-framing
WORKDIR /src
COPY schema-framing/pom.xml ./
RUN --mount=type=cache,target=/root/.m2 mvn -q -B dependency:go-offline
COPY schema-framing/src ./src
RUN --mount=type=cache,target=/root/.m2 mvn -q -B -DskipTests package

# Плагины Debezium 3.7.0.Final — каталоги из закреплённого образа Debezium Connect: каждый
# самодостаточен (EventRouter — в debezium-connect-plugins, MongoEventRouter — в jar коннектора).
FROM quay.io/debezium/connect:3.7.0.Final@sha256:19e5f6f86255473ee7f813280fe5ce3d0d987b83dfce590b77c31a7564533e13 AS debezium

FROM quay.io/strimzi/kafka:1.3.0-kafka-4.3.1@sha256:1f189b1f0f32684302d8b018b1d21b411e2469a9c465fbdc18fa3599cb5bbdad

# Владелец — пользователь Connect образа Strimzi (1001, группа 0). Без --chmod: он задал бы
# каталогам режим файла. Connect находит плагины по манифестам ServiceLoader
# (plugin.discovery=service_load ресурса KafkaConnect).
COPY --from=debezium --chown=1001:0 /kafka/connect/debezium-connector-postgres /opt/kafka/plugins/debezium-connector-postgres
COPY --from=debezium --chown=1001:0 /kafka/connect/debezium-connector-mongodb /opt/kafka/plugins/debezium-connector-mongodb
COPY --from=schema-framing --chown=1001:0 /src/target/schema-framing-1.0.0.jar /opt/kafka/plugins/schema-framing/schema-framing-1.0.0.jar
