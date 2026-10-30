# syntax=docker/dockerfile:1.7
#
# Kafka Connect с Debezium (ADR 37) и Prometheus JMX exporter. Образ Debezium включает
# exporter переменной ENABLE_JMX_EXPORTER, но jar в него не входит: агент и правила
# (metrics.yaml) кладёт этот образ. Метрики — :9404/metrics, их скрейпит Alloy.
# SMT SchemaFraming (FR-714): value стримов Integration Events — wire format Schema Registry.
# Тесты модуля (Redpanda из testcontainers) идут mvn package вне сборки образа.
FROM maven:3.10.0-eclipse-temurin-25@sha256:9d329e21adce7c0cc53f2fd21e73c127a247c73287b8503196136ec5cff52328 AS schema-framing
WORKDIR /src
COPY schema-framing/pom.xml ./
RUN --mount=type=cache,target=/root/.m2 mvn -q -B dependency:go-offline
COPY schema-framing/src ./src
RUN --mount=type=cache,target=/root/.m2 mvn -q -B -DskipTests package

FROM quay.io/debezium/connect:3.7.0.Final@sha256:19e5f6f86255473ee7f813280fe5ce3d0d987b83dfce590b77c31a7564533e13

# prometheus/jmx_exporter 1.7.0 (2026-10-06), sha256 из .sha256 релиза.
ADD --chmod=644 --checksum=sha256:0ad843261c567d1924a017d542e14c40f2afff523d940c25ee000742fde4b3a9 \
    https://github.com/prometheus/jmx_exporter/releases/download/1.7.0/jmx_prometheus_javaagent-1.7.0.jar \
    /kafka/libs/jmx_prometheus_javaagent-1.7.0.jar
COPY --chmod=644 metrics.yaml /kafka/config/metrics.yaml
# Плагин в своём каталоге plugin.path, владелец — пользователь kafka процесса Connect (без
# --chmod: он задал бы каталогу режим файла). Connect находит плагин по манифесту ServiceLoader
# (CONNECT_PLUGIN_DISCOVERY=service_load).
COPY --from=schema-framing --chown=kafka:kafka /src/target/schema-framing-1.0.0.jar /kafka/connect/schema-framing/schema-framing-1.0.0.jar

ENV ENABLE_JMX_EXPORTER=true
EXPOSE 9404
