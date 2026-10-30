# Design

## Термины

- **Schema Registry** — Confluent-совместимый REST API схем, встроенный в Redpanda (`:8081`).
- **Subject** — имя, под которым зарегистрированы версии схемы. Для Integration Event это полное имя protobuf-сообщения (`nomnom.delivery.v1.DeliveryPickedUp`), то есть то же, что header `event_type`.
- **Wire format** — формат value Confluent: `0x00`, id схемы (4 байта big-endian), индексы сообщения в файле (varint zigzag; `0x00` — первое сообщение файла), затем байты protobuf.
- **Integration Event stream** — topic, value которого — Integration Event контракта: `orders.v1.order-events`, `orders.v1.order-relationships`, `payments.v1.payment-events`, `delivery.v1.delivery-events`, `restaurants.v1.restaurant-order-events`, `restaurants.v1.restaurant-events`, `restaurants.v1.menu-events`. Change Notification streams (`*.v1.*-changes`) к ним не относятся.

## Выбор Registry

| Вариант | Решение |
| --- | --- |
| BSR, Confluent-совместимый API | Отклонён: доступен только в Enterprise-плане Buf, регистрация схем оплачивается отдельно. |
| Redpanda Schema Registry | Выбран: встроен в брокер, бесплатный, тот же API Confluent. Переход на другой Confluent-совместимый Registry не меняет ни SMT, ни consumers: меняется только URL. |

## Регистрация схем

Источник схем — buf-модули контекстов ([ADR 7](../../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md)). Утилита `ops/schema-registration`:

1. Компилирует файлы Integration Events из явного списка `ops/schema-registration/event-files.yaml` (`nomnom/<context>/v1/events.proto`, `nomnom/orders/v1/relationships.proto`) компилятором buf (`github.com/bufbuild/protocompile`) с import paths модулей из того же списка. Регистрируется исходный текст `.proto`: Registry принимает схему Protobuf текстом. Исходники модулей кладёт в образ его Dockerfile (контекст — корень репозитория).
2. Сначала регистрирует импортированные файлы нашего репозитория (subject = путь файла, например `nomnom/payments/v1/payments.proto`), затем файл события со ссылками (`references`) на них. Версию ссылки даёт lookup той же схемы (`POST /subjects/{subject}`): ответ регистрации API Confluent содержит только id. Well-known types (`google/protobuf/*.proto`) Redpanda знает сам — проверено на v26.2.6.
3. Для каждого сообщения файла события регистрирует файл под subject = полное имя сообщения. Одинаковый текст схемы Registry хранит под одним id, поэтому все сообщения одного файла получают один id, а сообщение различают индексы wire format.
4. Перед регистрацией выставляет глобальный режим `FULL_TRANSITIVE` (`PUT /config`). Несовместимая версия — ответ `409`, утилита завершается ошибкой, деплой останавливается.

Повторный запуск идемпотентен: та же схема возвращает существующий id.

`FULL_TRANSITIVE` дублирует правила additive-first ADR 25 на стороне Registry: новая версия совместима со всеми прежними в обе стороны, поэтому publisher и consumer деплоятся в любом порядке. `buf breaking` (`FILE`) остаётся первой и более строгой проверкой в PR: Registry Redpanda отклоняет смену типа поля и удаление сообщения, но пропускает удаление поля и перестановку номеров полей (проверено на v26.2.6).

Schema Registry не принимает схемы ни от кого, кроме утилиты: SMT и consumers только читают.

## SMT `SchemaFraming`

Java-модуль `ops/kind/kafka-connect/schema-framing` (Maven) рядом с Dockerfile образа Kafka Connect: стадия сборки образа собирает jar и кладёт его в `/kafka/connect/schema-framing`; Connect находит плагин по манифесту ServiceLoader (`CONNECT_PLUGIN_DISCOVERY=service_load`). Kafka Connect API, `protobuf-java` и Jackson — `provided`: их версии из `/kafka/libs` Debezium 3.7. Transform стоит после `EventRouter`:

```yaml
transforms: outbox,schema
transforms.schema.type: nomnom.connect.SchemaFraming
transforms.schema.topics: delivery.v1.delivery-events
transforms.schema.registry.url: http://kafka.nomnom-infra.svc.cluster.local:8081
transforms.schema.cache.ttl.ms: "60000"
```

Для записи с topic из `topics`:

1. Читает header `event_type`. Нет header — `DataException`.
2. Берёт id последней версии subject `event_type` и её `FileDescriptorProto` (`GET /subjects/{event_type}/versions/latest?format=serialized`, ссылки — так же), индекс сообщения — его позиция среди сообщений верхнего уровня файла. Ответ кешируется на `cache.ttl.ms`, а новая версия, зарегистрированная при деплое, подхватывается после истечения TTL.
3. Проверяет, что value разбирается как это сообщение (`DynamicMessage.parseFrom`), и возвращает запись с value = префикс + исходные байты.

Subject нет, Registry недоступен или value не разбирается — `DataException` (`errors.tolerance=none`): задача коннектора переходит в `FAILED`, записи не пропускаются (FR-528). Запись с topic вне `topics` проходит без изменений: это Change Notifications. Неизвестных конфигураций нет: пустые `topics` или `registry.url` — ошибка `configure`, коннектор не стартует.

Почему id последней версии, а не версии, с которой собран publisher: SMT этого не знает, а publisher в outbox не пишет ничего, кроме байтов. `FULL_TRANSITIVE` гарантирует, что байты любой прежней версии читаются последней схемой, поэтому consumer, разбирающий по ней, получает те же поля.

Разбор `.proto` не нужен: Registry отдаёт схему сериализованным `FileDescriptorProto`, дескриптор собирает `protobuf-java`, well-known types берутся из него же. Библиотеки Confluent не используются: их сериализатор работает с объектами Connect, а у нас value — байты.

## Consumers

Общий контракт для `orders` (Go), `restaurants` (TS) и `delivery` (Rust):

1. Value короче 6 байт или первый байт не `0x00` — `deserialization_error`.
2. Id схемы → subjects (`GET /schemas/ids/{id}/versions`). Id в Registry неизменяемы, поэтому соответствие кешируется без срока.
3. Subjects не содержат `event_type` записи — `deserialization_error`.
4. Индексы пропускаются, остаток разбирается сгенерированным типом контракта, как сейчас.

Registry недоступен — transient-ошибка: partition ждёт, запись в DLQ не уходит (ADR 28). В `orders` проверка subject — middleware сразу после deserialization guard, недоступный Registry — nack до Retry; `restaurants` проверяет записи тех `event_type`, которые обрабатывает (прочие подтверждаются без разбора, как и раньше), и так же записи группы обновления кеша; `delivery` повторяет запрос к Registry с backoff, как transient-ошибку handler'а. `deserialization_error` получает те же метаданные, что и сейчас, и попадает в `<stream>.dlq` тем же deserialization guard. Redrive публикует value с префиксом без изменений.

Конфиг `SCHEMA_REGISTRY__URL` обязателен у `orders-consumer`, `restaurants-consumer` и процесса `delivery`, который читает `delivery.v1.delivery-events`. Пустой или невалидный URL — падение на старте. У `orders` и `restaurants` таймаут запроса — `SCHEMA_REGISTRY__TIMEOUT` (`5s`), у `delivery` — константа клиента.

## Kind

- Redpanda стартует с `--schema-registry-addr=0.0.0.0:8081`, у Service `kafka` появляется порт `schema-registry` `8081`.
- Release `schema-registration` (chart `ops/kind/schema-registration`, образ `nomnom/schema-registration` собирает Skaffold) запускает hook Job и ждёт его завершения; release `kafka-connect` зависит от него (`dependsOn`), коннекторы регистрируются ещё позже. Before-hook не подходит: образ, собранный Skaffold, доступен только releases (`setValueTemplates`).
- NetworkPolicy `allow-infrastructure-egress` в `nomnom-orders`, `nomnom-restaurants` и `nomnom-delivery` открывает `kafka:8081`. Kafka Connect в `nomnom-infra` политиками не ограничен.
- `console.yaml`: `schemaRegistry.enabled: true` с тем же URL, payload в Console расшифровывается.
- Переход сразу: перед первым деплоем kind сбрасывается (`make kind-down`, `make kind-up`, `make kind-deploy`), как при прошлых несовместимых изменениях хранилищ.

## Риски

- **Registry становится зависимостью доставки.** Если Registry недоступен, коннекторы падают в `FAILED`, а consumers ждут. Это осознанно: без Registry запись нельзя ни оформить, ни проверить. Алерты коннекторов (`cdc-*`) уже есть.
- **Регистрация до деплоя publisher'а.** Новое событие или поле должно быть зарегистрировано до того, как publisher его запишет, иначе коннектор остановится. В kind порядок обеспечивает `dependsOn` release `kafka-connect`; для production это требование к pipeline.
- **TTL кеша SMT.** До истечения TTL SMT оформляет запись прежней последней версией. Из-за `FULL_TRANSITIVE` это безопасно.
- **Java-код в образе Connect.** Сборка образа получает стадию Maven, версии Kafka Connect API и `protobuf-java` должны совпадать с Debezium 3.7.
