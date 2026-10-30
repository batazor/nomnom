# Tasks

## 1. Schema Registry и регистрация схем

- [ ] 1.1 Redpanda с `--schema-registry-addr=0.0.0.0:8081`, порт `schema-registry` у Service `kafka`; egress к `kafka:8081` в NetworkPolicy `nomnom-orders`, `nomnom-restaurants`, `nomnom-delivery` (`FR-716`).
- [ ] 1.2 Утилита `ops/schema-registration` (Go): buf image, список `event-files.yaml`, регистрация зависимостей с `references`, subject = полное имя сообщения, глобальный `FULL_TRANSITIVE`, ошибка на `409`; Dockerfile со стадией `buf build`; тесты на Redpanda из testcontainers (`FR-712`, `FR-713`, `FR-717`, `AC-971`, `AC-972`, `AC-973`).
- [ ] 1.3 Проверить, знает ли Redpanda Schema Registry well-known types; если нет — регистрировать их из buf image (design).
- [ ] 1.4 Job `schema-registration` в before-hook после `dead-letter-topics`, artifact `nomnom/schema-registration` в Skaffold (`FR-716`, `AC-980`).

## 2. SMT `SchemaFraming`

- [ ] 2.1 Модуль `ops/kafka-connect/schema-framing` (Java, Gradle): config `topics`, `registry.url`, `cache.ttl.ms`; префикс wire format с индексами сообщения; `DataException` без header, без subject, при недоступном Registry и неразбираемом value; записи вне `topics` без изменений; unit-тесты и тест на Redpanda из testcontainers (`FR-714`, `FR-718`, `AC-974`, `AC-975`, `AC-976`).
- [ ] 2.2 Стадия сборки jar в `ops/kind/kafka-connect/Dockerfile`, jar в `/kafka/connect/schema-framing`.
- [ ] 2.3 `transforms: outbox,schema` в `cdc.*` charts `orders`, `payments`, `delivery`, `restaurants`: Integration Event streams контекста в `transforms.schema.topics`, Change Notification streams вне списка (`FR-530`, `AC-620`, `AC-981`).

## 3. Consumers

- [ ] 3.1 `orders`: `GuardedUnmarshaler` проверяет wire format и subject по `event_type`, кеш id → subjects, transient при недоступном Registry; конфиг `SCHEMA_REGISTRY__URL` с падением на старте; тесты с Redpanda из testcontainers (`FR-715`, `FR-719`, `AC-977`, `AC-978`, `AC-979`).
- [ ] 3.2 `restaurants`: то же в envelope и processor consumer'а `restaurants-consumer` (`FR-715`, `FR-719`, `AC-977`, `AC-978`, `AC-979`).
- [ ] 3.3 `delivery`: то же в envelope consumer'а `delivery.v1.delivery-events` (`FR-715`, `FR-719`, `AC-977`, `AC-978`, `AC-979`).
- [ ] 3.4 Fixtures тестов, которые публикуют события в Kafka напрямую, пишут wire format.
- [ ] 3.5 `SCHEMA_REGISTRY__URL` в Helm charts и `ops/kind/values` consumers.

## 4. Kind и документация

- [ ] 4.1 `console.yaml`: `schemaRegistry.enabled` с URL Registry.
- [ ] 4.2 ADR 25 и ADR 28 — формат value; ADR 7 — публикация схем Integration Events из buf в Registry; ADR 37 — SMT; закрыть вопрос «размещение event-схем в Buf» в `docs/event-driven-architecture.md`; `docs/dlq.md`; README `ops/kind` и контекстов.
- [ ] 4.3 Сбросить kind, `make kind-deploy` и `make kind-test`: коннекторы `RUNNING`, события доходят до consumers, DLQ пуст, Console показывает расшифрованный payload.
