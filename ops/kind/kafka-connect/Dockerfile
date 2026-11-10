# syntax=docker/dockerfile:1.7
#
# Образ Kafka Connect ресурса KafkaConnect kafka-connect (FR-722, ADR 37): Strimzi Kafka с
# плагинами в /opt/kafka/plugins. Метрики отдаёт metricsConfig Strimzi (JMX exporter :9404 по
# правилам chart/files/metrics.yaml), свой агент образу не нужен.
#
# SMT SchemaFraming (FR-714): value стримов Integration Events — wire format Schema Registry.
# connect-api, protobuf-java и Jackson — provided: те же версии лежат в /opt/kafka/libs Strimzi
# Kafka 4.3.2. Тесты модуля (testcontainers) идут mvn package вне сборки образа.
FROM maven:3.10.0-eclipse-temurin-25@sha256:9d329e21adce7c0cc53f2fd21e73c127a247c73287b8503196136ec5cff52328 AS schema-framing
WORKDIR /src
COPY schema-framing/pom.xml ./
RUN --mount=type=cache,target=/root/.m2 mvn -q -B dependency:go-offline
COPY schema-framing/src ./src
RUN --mount=type=cache,target=/root/.m2 mvn -q -B -DskipTests package

# Плагины Debezium 3.7.1.Final — каталоги из закреплённого образа Debezium Connect: каждый
# самодостаточен (EventRouter — в debezium-connect-plugins, MongoEventRouter — в jar коннектора).
FROM quay.io/debezium/connect:3.7.1.Final@sha256:9bf1239e4cc46199848d24489517432c98ab0071eb2b1fc3408a8767dbd65386 AS debezium

FROM quay.io/strimzi/kafka:1.3.1-kafka-4.3.2@sha256:9cea505e434a53cea40941d2c6e9aa5896792fe9c9380feb1d7adea3867a3a0b

# Владелец — пользователь Connect образа Strimzi (1001, группа 0). Без --chmod: он задал бы
# каталогам режим файла. Connect находит плагины по манифестам ServiceLoader
# (plugin.discovery=service_load ресурса KafkaConnect).
COPY --from=debezium --chown=1001:0 /kafka/connect/debezium-connector-postgres /opt/kafka/plugins/debezium-connector-postgres
COPY --from=debezium --chown=1001:0 /kafka/connect/debezium-connector-mongodb /opt/kafka/plugins/debezium-connector-mongodb
COPY --from=schema-framing --chown=1001:0 /src/target/schema-framing-1.0.0.jar /opt/kafka/plugins/schema-framing/schema-framing-1.0.0.jar
