## ADDED Requirements

### Requirement: Kafka тестового окружения под Strimzi
**ID:** FR-720

Тестовое kind-окружение SHALL разворачивать Apache Kafka ресурсами Strimzi `Kafka` и `KafkaNodePool` в `nomnom-infra`: KRaft, один узел с ролями broker и controller, listener без TLS на 9092, bootstrap `kafka-kafka-bootstrap.nomnom-infra.svc.cluster.local:9092`. Strimzi Cluster Operator MUST ставиться платформой kind (`make kind-up`). Брокер MUST NOT создавать topics при первом обращении (`auto.create.topics.enable=false`).

#### Scenario: Брокер готов до charts приложений
**ID:** AC-982

- **WHEN** окружение kind разворачивается через Skaffold на сброшенном кластере
- **THEN** ресурс `kafka/kafka` в `Ready` до установки releases приложений, а consumers и Kafka Connect подключаются к bootstrap

#### Scenario: Topic без объявления не появляется
**ID:** AC-983

- **WHEN** клиент публикует в topic, для которого нет `KafkaTopic`
- **THEN** публикация завершается ошибкой, и topic в брокере не создаётся

### Requirement: Topics объявлены ресурсами KafkaTopic
**ID:** FR-721

Каждый topic тестового окружения SHALL быть ресурсом `KafkaTopic` в `nomnom-infra` (манифест `ops/kind/manifests/messaging/topics.yaml`): стримы Integration Events и Change Notifications, DLQ, служебные topics Kafka Connect и `_schemas` Schema Registry. Деплой MUST ждать `Ready` всех `KafkaTopic` до установки charts приложений.

#### Scenario: Перезапуск брокера восстанавливает topics
**ID:** AC-985

- **WHEN** pod брокера перезапускается и ephemeral storage теряется
- **THEN** Topic Operator создаёт заново каждый topic со своей конфигурацией, и Kafka Connect стартует со служебными topics `compact`

### Requirement: Конфигурация topics
**ID:** FR-723

Стримы Integration Events и Change Notifications SHALL иметь 3 partitions; DLQ — 1 partition, `retention.ms` 30 дней и `cleanup.policy=delete`; служебные topics Kafka Connect — `cleanup.policy=compact`, `nomnom-cdc-configs` — одну partition; `_schemas` — 1 partition и `cleanup.policy=compact`.

#### Scenario: Конфигурация DLQ
**ID:** AC-984

- **WHEN** окружение kind развёрнуто
- **THEN** `delivery.v1.delivery-events.dlq` имеет одну partition, `retention.ms=2592000000` и `cleanup.policy=delete`, а `KafkaTopic` в `Ready`

### Requirement: Kafka Connect ресурсом KafkaConnect
**ID:** FR-722

Тестовое kind-окружение SHALL разворачивать Kafka Connect ресурсом Strimzi `KafkaConnect` (`kafka-connect`, аннотация `strimzi.io/use-connector-resources: "true"`) с образом на базе Strimzi Kafka, в `/opt/kafka/plugins` которого лежат плагины Debezium PostgreSQL и MongoDB и SMT `SchemaFraming`. Пароли коннекторов MUST читаться `DirectoryConfigProvider` из Secret `cdc-credentials`; метрики MUST отдаваться `metricsConfig` с прежними именами.

#### Scenario: Connect готов с плагинами
**ID:** AC-986

- **WHEN** release `kafka-connect` установлен
- **THEN** `kafkaconnect/kafka-connect` в `Ready`, а его плагины включают `io.debezium.connector.postgresql.PostgresConnector`, `io.debezium.connector.mongodb.MongoDbConnector` и `nomnom.connect.SchemaFraming`

## MODIFIED Requirements

### Requirement: Schema Registry в тестовом окружении
**ID:** FR-716

Тестовое kind-окружение SHALL включать Confluent Schema Registry (Deployment и Service `schema-registry.nomnom-infra.svc.cluster.local:8081`, topic `_schemas`) и регистрировать схемы hook Job release `schema-registration`, от которого зависит release `kafka-connect`: коннекторы стартуют после регистрации. Job, завершившийся ошибкой, MUST проваливать release и деплой. Egress к Registry MUST быть открыт consumers `orders`, `restaurants` и `delivery`.

#### Scenario: Деплой регистрирует схемы до consumers
**ID:** AC-980

- **WHEN** окружение kind разворачивается через Skaffold на сброшенном кластере
- **THEN** Job `schema-registration` в `Complete` до установки `kafka-connect`, subjects всех Integration Events зарегистрированы в Confluent Schema Registry, коннекторы `RUNNING`, а consumers читают события без записей в DLQ
