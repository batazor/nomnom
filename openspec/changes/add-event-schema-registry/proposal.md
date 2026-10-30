# Proposal

## Why

Integration Events сейчас уходят в Kafka голыми байтами protobuf. Тип события есть только в header `event_type`, а то, что payload соответствует контракту, нигде не проверяется по пути доставки. `buf breaking` защищает совместимость `.proto` в репозитории, но брокер и consumers не знают, какие схемы опубликованы: событие с незарегистрированным или устаревшим типом обнаруживается только в handler'е consumer'а. Внешние инструменты (Redpanda Console, будущие sinks Kafka Connect, аналитика) не могут прочитать payload без копии `.proto`.

Schema Registry делает опубликованные схемы частью инфраструктуры: каждая запись стрима ссылается на зарегистрированную схему, Registry сам отклоняет несовместимую новую версию, а любой Confluent-совместимый клиент читает события без доступа к репозиторию.

Интеграция BSR с Confluent Schema Registry доступна только в Enterprise-плане Buf, а регистрация схем там — отдельная платная опция. Поэтому Registry — встроенный в Redpanda Schema Registry (Confluent-совместимый API). `buf` остаётся источником схем и первой проверкой совместимости.

## What Changes

- **Redpanda Schema Registry** включается в брокере. В kind это `:8081` у Deployment `kafka`, порт открыт в Service `kafka` и в NetworkPolicy consumers.
- **Регистрация схем из buf.** Новая утилита `schema-registration` (Go) собирает `buf build` модулей контекстов и регистрирует файлы Integration Events из явного списка, а также их зависимости. Subject = полное имя protobuf-сообщения (`RecordNameStrategy`), то есть совпадает с `event_type`. Глобальный режим совместимости — `FULL_TRANSITIVE`. Схемы регистрирует только этот шаг деплоя, producers и consumers схемы не регистрируют. В kind это Job после Kafka и до Kafka Connect и consumers.
- **Wire format Confluent.** Value записи стрима Integration Events — magic byte `0`, 4 байта id схемы, индексы сообщения и затем байты protobuf.
- **Свой SMT в Debezium.** Transform `SchemaFraming` в образе Kafka Connect дописывает префикс: id схемы он берёт по header `event_type` из subject Registry. Transform работает только на topics из своей конфигурации, то есть на стримах Integration Events коннектора. Записи Change Notifications (`*.v1.*-changes`, JSON для Cosmo Router) он не трогает. Запись стрима с `event_type` без subject переводит задачу коннектора в `FAILED`. Publishers (Go, TS, Rust, Python) не меняются: в outbox по-прежнему пишутся байты protobuf.
- **Consumers проверяют wire format.** `orders`, `restaurants` и `delivery` отрезают префикс, находят по id subjects схемы и сверяют их с `event_type`. Нет префикса, неизвестный id или subject не совпадает с `event_type` — это `deserialization_error`, запись уходит в DLQ. Конфиг `SCHEMA_REGISTRY__URL` обязателен: если он пуст или невалиден, процесс падает на старте.
- **Header `event_type` остаётся** ключом маршрутизации, Inbox и DLQ. Envelope в headers не меняется.
- **Redpanda Console** читает payload через Schema Registry.
- **Переход сразу, без двойного формата.** Окружение только для разработки, хранилища kind (`emptyDir`) сбрасываются: старых записей без префикса в стримах и DLQ нет.

Не входит:

- Schema Registry в production: выбор реализации (встроенный в Redpanda или отдельный Confluent-совместимый, если брокер — Apache Kafka) и шаг регистрации в pipeline релиза;
- проверка содержимого payload по схеме в SMT сверх соответствия типа;
- Registry для Change Notifications (JSON, их потребитель — Cosmo Router);
- расшифровка payload в `dlq list`.

## Capabilities

### New Capabilities

- `integration-event-schemas`: регистрация схем Integration Events из buf, wire format Confluent, SMT `SchemaFraming`, проверка формата consumers.

### Modified Capabilities

- `integration-event-publication`: value стрима Integration Events — wire format Confluent вместо payload без изменений; Change Notifications — без изменений.
- `kubernetes-test-environment`: Schema Registry в kind, Job регистрации схем до Connect и consumers.

## Impact

- **Новое:** `ops/schema-registration` (Go, Dockerfile с `buf build`), `ops/kafka-connect/schema-framing` (Java, SMT).
- **`ops/kind`:** аргументы и Service `kafka` (`:8081`), образ Kafka Connect с SMT, Job `schema-registration` в before-hook, NetworkPolicy consumers, `console.yaml`, README.
- **Helm charts `orders`, `restaurants`, `delivery`, `payments`:** `transforms` коннектора `cdc.*`; `SCHEMA_REGISTRY__URL` у consumer-процессов `orders`, `restaurants`, `delivery`.
- **`boundaries/orders`:** `GuardedUnmarshaler` и тесты с Registry из testcontainers.
- **`boundaries/restaurants`:** envelope и processor consumer'а.
- **`boundaries/delivery`:** envelope consumer'а `delivery.v1.delivery-events`.
- **Документация:** ADR 25 и ADR 28 (формат value), ADR 7 (публикация схем Integration Events), ADR 37 (SMT), открытый вопрос «размещение event-схем в Buf» в `docs/event-driven-architecture.md`, `docs/dlq.md`.
- **Kind сбрасывается:** записи без префикса в стримах несовместимы с новыми consumers.
