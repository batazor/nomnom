# 37. Доставка Outbox через CDC на Debezium вместо polling relay

Date: 2026-10-25

## Status

Accepted

Заменяет раздел «Polling relay, не CDC» и выбор relay-библиотек в
[31. Outbox Storage, Polling Relay and Client Libraries](0031-outbox-storage-polling-relay-and-clients.md),
а также Watermill Forwarder `orders` из [13. Transactional Outbox](0013-transactional-outbox-for-integration-event-publishing.md).
Хранилища и Transactional Outbox как паттерн (ADR 8, 13, 31) не меняются.

## Context

Outbox каждого контекста публиковал отдельный процесс, который опрашивал свою таблицу или
коллекцию: `orders-forwarder` (Watermill Forwarder), `restaurants-relay`, `delivery-relay` и
`payments-relay`. Опрос даёт задержку не меньше своего интервала, постоянную нагрузку на базу
без событий, четыре реализации на четырёх языках (lease, head-of-line, retries, метрики) и
четыре лишних процесса. С ADR 36 Outbox несёт и Change Notifications для GraphQL subscriptions,
поэтому задержка relay стала видна пользователю.

ADR 31 отказался от CDC как от лишней инфраструктуры. Это решение пересматривается: CDC читает
журнал базы (PostgreSQL WAL, MongoDB change streams) и отдаёт запись сразу после commit, без
опроса и без кода публикации в сервисах.

Проверено на стенде Redpanda 26.2 + Debezium Connect 3.7.0.Final + PostgreSQL 18 + MongoDB 8
(2026-10-25):

- Outbox Event Router отдаёт сообщение с ключом `aggregate_id`, payload — сырыми байтами
  (`ByteArrayConverter`, Protobuf не меняется), заголовки — строками
  (`SimpleHeaderConverter`), одно поле можно положить в несколько заголовков;
- `INSERT` и `DELETE` строки Outbox в одной транзакции всё равно публикуют событие;
- `MongoEventRouter` передаёт `Binary` как есть, BSON `Date` — как RFC 3339 с миллисекундами;
  документ без поля, названного в `fields.additional.placement`, останавливает коннектор.

Debezium Server не подходит: заголовки он пишет только в JSON (`"\"value\""`), а у Server нет
хранения offset в Kafka для `restaurants` без SQL-базы.

## Decision

### Kafka Connect + Debezium

Один Kafka Connect кластер (`quay.io/debezium/connect`, 3.7.0.Final, образ `ops/kind/kafka-connect` с JMX exporter) в `nomnom-infra`. Его
config, offsets и status хранятся в топиках брокера, поэтому сам Connect stateless. На каждый
контекст свой коннектор:

| Контекст | Коннектор | Источник |
| --- | --- | --- |
| `orders` | `orders-outbox` | PostgreSQL, таблица `orders_outbox`, publication `orders_outbox` |
| `payments` | `payments-outbox` | PostgreSQL, таблица `payments_outbox`, publication `payments_outbox` |
| `delivery` | `delivery-outbox` | PostgreSQL, таблица `delivery_outbox`, publication `delivery_outbox` |
| `restaurants` | `restaurants-outbox` | MongoDB, коллекция `outbox`, change streams |

Коннектор принадлежит контексту: конфигурацию держит его Helm chart (`cdc.*`), а hook Job
release контекста регистрирует её через REST Connect (`PUT /connectors/<name>/config`).
Пароли Connect читает из Secret `cdc-credentials` в `nomnom-infra` через
`DirectoryConfigProvider` (`${dir:/etc/nomnom/cdc:<key>}`); в конфигурации коннектора их нет.

### Контракт записи Outbox

Сообщение в Kafka собирается только из записи Outbox. Поэтому каждая запись содержит:

| Поле PostgreSQL | Поле MongoDB | Куда | Формат |
| --- | --- | --- | --- |
| `event_id` | `eventId` | заголовки `event_id` и `_watermill_message_uuid` | UUID строкой |
| `event_type` | `eventType` | заголовок `event_type` | полное имя Protobuf или тип Change Notification |
| `aggregate_type` | `aggregateType` | заголовок `aggregate_type` | строка |
| `aggregate_id` | `aggregateId` | ключ Kafka и заголовок `aggregate_id` | строка |
| `aggregate_version` | `aggregateVersion` | заголовок `aggregate_version` | целое |
| `occurred_at` | `occurredAt` | заголовок `occurred_at` | `timestamptz` / BSON `Date`, в заголовке RFC 3339 UTC |
| `topic` | `topic` | топик Kafka (`route.by.field`) | строка |
| `payload` | `payload` | value | `bytea` / BSON `Binary`: Protobuf или JSON Change Notification |
| `traceparent` | `traceparent` | заголовок `traceparent` | W3C trace context или `''`, не `NULL` и не отсутствует |

Остальные поля таблицы Debezium не читает. Envelope совпадает с прежним (ADR 25, ADR 28):
консьюмеры не меняются.

- PostgreSQL: publication создаёт миграция контекста —
  `CREATE PUBLICATION <ctx>_outbox FOR TABLE <ctx>_outbox WITH (publish = 'insert')`. Удаления
  не попадают в поток. Слот логической репликации `<ctx>_outbox` создаёт коннектор;
  `wal_level = logical`.
- MongoDB: коннектор пропускает `update` и `delete` (`skipped.operations = u,d,t`).
- Хранение: запись нужна только до чтения журнала, а журнал держит её независимо от таблицы.
  PostgreSQL-контексты удаляют записи старше часа (`created_at`) в транзакции очередной записи
  Outbox; у MongoDB — TTL-индекс по `createdAt` (1 час). Фонового процесса нет.
- `published_at`, `attempts`, lease и прочие поля relay удаляются.

### Без relay

Процессы `orders-forwarder`, `restaurants-relay`, `delivery-relay` и `payments-relay` и их
код, конфигурация, метрики и charts удаляются. Сервис пишет запись Outbox в транзакции
изменения и больше ничего не делает для публикации.

## Consequences

- Задержка публикации — время чтения журнала (миллисекунды), а не интервал опроса. Базы не
  опрашиваются.
- Порядок по агрегату сохраняется: журнал упорядочен по commit, ключ Kafka — `aggregate_id`.
  Доставка at-least-once: после рестарта Connect возможны повторы с последнего offset;
  консьюмеры идемпотентны (Inbox, ADR 11).
- Запись, нарушающая контракт (например, документ MongoDB без поля заголовка), останавливает
  коннектор (head-of-line, как у relay). Видно в `GET /connectors/<name>/status`; после
  исправления — `POST /connectors/<name>/restart?includeTasks=true`.
- Слот репликации держит WAL, пока коннектор не прочитал его. Остановленный надолго коннектор
  растит WAL: alerts Platform / Overview `replication-slot-inactive` и `replication-slot-lag`
  (postgres_exporter `replication_slot`, разница `current_wal_lsn − confirmed_flush_lsn`).
- Connect — новая инфраструктура (JVM, около 512 MiB) вместо четырёх процессов relay.
- В production пользователю CDC нужны `REPLICATION` и `SELECT` на таблице Outbox. В kind
  коннектор работает под владельцем базы.
- Метрики relay (`nomnom.outbox.*`) исчезают. Их место занимают метрики Connect и Debezium:
  образ `ops/kind/kafka-connect` добавляет Prometheus JMX exporter (`:9404`, правила
  `metrics.yaml`), Alloy скрейпит его как `job="infra/kafka-connect"`. Состояние задач
  (`kafka_connect_connector_task_status`), поток (`debezium_metrics_TotalNumberOfEventsSeen`),
  отставание (`debezium_metrics_MilliSecondsBehindSource`) и очередь — row CDC в Platform /
  Overview и row Kafka сервисов; alerts `cdc-task-failed`, `cdc-task-not-running`, `cdc-lag`.
