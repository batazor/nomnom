# Design

## Термины

- **Strimzi** — operator Apache Kafka для Kubernetes (CNCF, Apache 2.0). Cluster Operator приводит кластер к ресурсам `Kafka`, `KafkaNodePool`, `KafkaConnect` и `KafkaConnector`; Topic Operator в Entity Operator ресурса `Kafka` — к ресурсам `KafkaTopic`.
- **Стрим** — topic Integration Events или Change Notifications; **служебные topics** — topics хранения Kafka Connect (`nomnom-cdc-*`) и Schema Registry (`_schemas`).

## Версии

| Что | Версия | Почему |
| --- | --- | --- |
| Strimzi | 1.3.0 (2026-10-12) | последний стабильный; Kubernetes 1.32+, у kind 1.37; CRD API только `v1` |
| Apache Kafka | 4.3.1 | последняя версия, которую поддерживает Strimzi 1.3.0 (4.4.0 — нет); testcontainers переходят на неё же |
| Confluent Schema Registry | 8.4.0 | последний стабильный Confluent Platform; API, на который рассчитаны SMT и consumers |
| Debezium | 3.7.0.Final (плагины) | без изменений; совместимость с Connect runtime Kafka 4.3 проверяется на стенде |

Версии закрепляются в `ops/kind/versions.env` и манифестах; образы — по digest.

## Топология `nomnom-infra`

```text
Strimzi Cluster Operator (namespace strimzi, платформа)
  └─ Kafka kafka + KafkaNodePool dual         — pod kafka-dual-0: broker + controller (KRaft)
       ├─ Service kafka-kafka-bootstrap:9092   — адрес клиентов (KAFKA__BROKERS)
       └─ Entity Operator: Topic Operator      — KafkaTopic в nomnom-infra
  └─ KafkaConnect kafka-connect               — Debezium + SchemaFraming; KafkaConnector в nomnom-infra
Deployment schema-registry                    — Confluent Schema Registry, Service schema-registry:8081
Deployment temporal                           — без изменений
```

- `Kafka`: listener `plain` 9092 без TLS и аутентификации, как сейчас; `auto.create.topics.enable=false`; `offsets.topic.replication.factor`, `transaction.state.log.*` и `default.replication.factor` = 1; ephemeral storage (данные теряются при рестарте pod, как у Redpanda сейчас).
- Память задаётся явно в ресурсах (admission-политика ADR 17): брокер `-Xms256m -Xmx512m`, limit 1Gi; Entity Operator и Cluster Operator — limits из chart values; Schema Registry `-Xmx256m`, limit 512Mi; Kafka Connect — прежние 256m heap.
- Pod'ы Strimzi несут `app.kubernetes.io/name` (`kafka`, `kafka-connect`, `entity-operator`): admission-политика их пропускает, NetworkPolicy contexts выбирают брокер тем же label `app.kubernetes.io/name: kafka`.

## Topics

Все topics — `KafkaTopic` в одном манифесте `ops/kind/manifests/messaging/topics.yaml`. Broker auto create выключен, поэтому topic без ресурса не появится: publisher получит ошибку, а не topic с параметрами по умолчанию.

| Topics | Partitions | Конфигурация |
| --- | --- | --- |
| стримы Integration Events (`orders.v1.order-events`, `orders.v1.order-relationships`, `payments.v1.payment-events`, `delivery.v1.delivery-events`, `restaurants.v1.restaurant-order-events`, `restaurants.v1.restaurant-events`, `restaurants.v1.menu-events`) | 3 | по умолчанию брокера |
| Change Notifications (`orders.v1.order-changes`, `restaurants.v1.kitchen-changes`, `delivery.v1.courier-changes`, `delivery.v1.courier-pool-changes`) | 3 | по умолчанию брокера |
| DLQ (`<stream>.dlq` четырёх стримов consumers) | 1 | `retention.ms=2592000000`, `cleanup.policy=delete` |
| `nomnom-cdc-configs` | 1 | `cleanup.policy=compact` |
| `nomnom-cdc-offsets`, `nomnom-cdc-status` | 25, 5 | `cleanup.policy=compact` |
| `_schemas` | 1 | `cleanup.policy=compact` |

Topics лежат вместе, а не в charts контекстов: DLQ общие для групп разных контекстов, у них нет одного владельца. Распределение по владельцам — решение production.

Удаление `KafkaTopic` удаляет topic вместе с данными, а уменьшить число partitions нельзя: изменение partitions существующего topic — только увеличение, иначе Topic Operator отклоняет ресурс (`Ready=False`).

## Kafka Connect

Ресурс `KafkaConnect kafka-connect` рендерит chart `ops/kind/kafka-connect/chart` вместо прежнего Deployment: образ, собранный Skaffold, доступен только releases (`setValueTemplates`). Аннотация `strimzi.io/use-connector-resources: "true"` — коннекторы задаются только `KafkaConnector`, REST API для регистрации не используется.

- Образ: `quay.io/strimzi/kafka:1.3.0-kafka-4.3.1`, в `/opt/kafka/plugins` — каталоги Debezium PostgreSQL и MongoDB из архивов релиза 3.7.0.Final (checksum) и `schema-framing`. Сборку Strimzi (`spec.build`) не используем: ей нужен registry для push и Buildah.
- `group.id` и topics хранения — прежние (`nomnom-cdc`, `nomnom-cdc-configs`, `nomnom-cdc-offsets`, `nomnom-cdc-status`); topics объявлены `KafkaTopic`, Connect их не создаёт.
- Пароли: `config.providers=dir` (`DirectoryConfigProvider`, `allowed.paths=/etc/nomnom/cdc`), Secret `cdc-credentials` монтируется через `template.pod.volumes` и `template.connectContainer.volumeMounts`.
- Метрики: `metricsConfig` типа `jmxPrometheusExporter` с ConfigMap прежних правил `metrics.yaml`; имена метрик для Alloy и дашбордов не меняются.
- SMT `SchemaFraming`: в образе Debezium `protobuf-java` и Jackson лежали в `/kafka/libs`; в образе Strimzi их наличие и версии проверяются первой задачей. Недостающие зависимости кладутся в каталог плагина рядом с jar (`dependency:copy-dependencies`), а не в общий classpath.

## Коннекторы

`templates/cdc-connector.yaml` chart контекста рендерит `KafkaConnector <context>-outbox` в `nomnom-infra` с label `strimzi.io/cluster: kafka-connect`, `class`, `tasksMax: 1` и прежним `cdc.connector.config` поверх адреса базы. `autoRestart.enabled: true`: задачу в `FAILED` (запись без схемы, FR-718) Strimzi перезапускает с backoff — после регистрации схемы коннектор продолжает сам, раньше это делал hook Job при следующем деплое. `cdc.connectUrl` и `cdc.registrar` удаляются вместе с hook Job.

Ресурс создаётся в чужом namespace релиза: Strimzi читает `KafkaConnector` только в namespace своего `KafkaConnect`.

## Schema Registry

Deployment `schema-registry` (`confluentinc/cp-schema-registry:8.4.0` по digest): `SCHEMA_REGISTRY_KAFKASTORE_BOOTSTRAP_SERVERS=kafka-kafka-bootstrap:9092`, `SCHEMA_REGISTRY_KAFKASTORE_TOPIC=_schemas`, `SCHEMA_REGISTRY_LISTENERS=http://0.0.0.0:8081`. Endpoints, которые использует код, — API Confluent: `?format=serialized` (SMT), `/schemas/ids/{id}/versions` (consumers), `PUT /config`, регистрация и lookup (`schema-registration`). Различия формата ответов Redpanda и Confluent (например, ответ регистрации без `version`) уже учтены: `schema-registration` берёт версию lookup'ом.

## Порядок деплоя

1. `make kind-up`: Strimzi Cluster Operator (Helm chart `oci://quay.io/strimzi-helm/strimzi-kafka-operator`, `watchNamespaces: [nomnom-infra]`) вместе с остальной платформой.
2. Before-hook модуля `apps`: применяет `Kafka`, `KafkaNodePool`, `KafkaTopic`, Schema Registry и Temporal; ждёт `kafka/kafka` `Ready`, все `kafkatopic` `Ready`, Deployment `schema-registry` и `temporal` `Available`.
3. Release `schema-registration` (hook Job) — до `kafka-connect`.
4. Release `kafka-connect` — ресурс `KafkaConnect`; Skaffold after-hook ждёт `kafkaconnect/kafka-connect` `Ready`.
5. Releases контекстов создают `KafkaConnector`; after-hook ждёт `kafkaconnector --all` `Ready` в `nomnom-infra`.

## Тесты

- testcontainers контекстов: `apache/kafka:4.3.1`.
- Тесты Schema Registry (`schema-registration`, SMT, `orders`, `restaurants`, `delivery`) — Confluent Schema Registry 8.4.0 рядом с контейнером Kafka в общей сети testcontainers, вместо Redpanda.

## Риски

- **Память ноутбука.** Apache Kafka и Schema Registry на JVM, Cluster Operator и Entity Operator тяжелее одного Redpanda. Heap и limits заданы явно; итог меряется на стенде и записывается в README.
- **Совместимость Debezium 3.7 с Kafka Connect 4.3.** Проверка — коннекторы `RUNNING` и сквозной путь заказа в kind.
- **Удаление `KafkaTopic` удаляет данные.** Переименование или перенос ресурса между манифестами — это удаление topic; ревью манифеста topics обязательно.
- **Topic без ресурса.** Новый стрим без `KafkaTopic` — ошибка publish и `FAILED` коннектора; это намеренно, вместо topic с параметрами по умолчанию.
