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

1. Получает buf image (`buf build -o image.binpb`), собранный в стадии сборки Docker-образа из корня репозитория.
2. Регистрирует файлы Integration Events из явного списка `ops/schema-registration/event-files.yaml` (`nomnom/<context>/v1/events.proto`, `nomnom/orders/v1/relationships.proto`).
3. Сначала регистрирует импортированные файлы нашего репозитория (subject = путь файла, например `nomnom/payments/v1/payments.proto`), затем файл события со ссылками (`references`) на них. Well-known types (`google/protobuf/*.proto`) Registry знает сам. Если Redpanda их не знает, утилита регистрирует их из buf image тем же способом (проверяется в задаче 1.3).
4. Для каждого сообщения файла события регистрирует файл под subject = полное имя сообщения. Одинаковый текст схемы Registry хранит под одним id, поэтому все сообщения одного файла получают один id, а сообщение различают индексы wire format.
5. Перед регистрацией выставляет глобальный режим `FULL_TRANSITIVE` (`PUT /config`). Несовместимая версия — ответ `409`, утилита завершается ошибкой, деплой останавливается.

Повторный запуск идемпотентен: та же схема возвращает существующий id.

`FULL_TRANSITIVE` дублирует правила additive-first ADR 25 на стороне Registry: новая версия совместима со всеми прежними в обе стороны, поэтому publisher и consumer деплоятся в любом порядке. `buf breaking` (`FILE`) остаётся первой и более строгой проверкой в PR; Registry ловит то, что прошло мимо репозитория.

Schema Registry не принимает схемы ни от кого, кроме утилиты: SMT и consumers только читают.

## SMT `SchemaFraming`

Java-модуль `ops/kafka-connect/schema-framing`, jar кладётся в образ Kafka Connect (`/kafka/connect/schema-framing`). Transform стоит после `EventRouter`:

```yaml
transforms: outbox,schema
transforms.schema.type: nomnom.connect.SchemaFraming
transforms.schema.topics: delivery.v1.delivery-events
transforms.schema.registry.url: http://kafka.nomnom-infra.svc.cluster.local:8081
transforms.schema.cache.ttl.ms: "60000"
```

Для записи с topic из `topics`:

1. Читает header `event_type`. Нет header — `DataException`.
2. Берёт id последней версии subject `event_type` (`GET /subjects/{event_type}/versions/latest`) и индексы сообщения в файле схемы. Ответ кешируется на `cache.ttl.ms`, а новая версия, зарегистрированная при деплое, подхватывается после истечения TTL.
3. Проверяет, что value разбирается как это сообщение (`DynamicMessage.parseFrom`), и возвращает запись с value = префикс + исходные байты.

Subject нет, Registry недоступен или value не разбирается — `DataException` (`errors.tolerance=none`): задача коннектора переходит в `FAILED`, записи не пропускаются (FR-528). Запись с topic вне `topics` проходит без изменений: это Change Notifications. Неизвестных конфигураций нет: пустые `topics` или `registry.url` — ошибка `configure`, коннектор не стартует.

Почему id последней версии, а не версии, с которой собран publisher: SMT этого не знает, а publisher в outbox не пишет ничего, кроме байтов. `FULL_TRANSITIVE` гарантирует, что байты любой прежней версии читаются последней схемой, поэтому consumer, разбирающий по ней, получает те же поля.

Индексы сообщения и разбор `.proto` даёт `io.confluent:kafka-protobuf-provider` (`ProtobufSchema.toMessageIndexes`). Сериализатор Confluent целиком не используется: он работает с объектами Connect, а у нас value — байты.

## Consumers

Общий контракт для `orders` (Go), `restaurants` (TS) и `delivery` (Rust):

1. Value короче 6 байт или первый байт не `0x00` — `deserialization_error`.
2. Id схемы → subjects (`GET /schemas/ids/{id}/versions`). Id в Registry неизменяемы, поэтому соответствие кешируется без срока.
3. Subjects не содержат `event_type` записи — `deserialization_error`.
4. Индексы пропускаются, остаток разбирается сгенерированным типом контракта, как сейчас.

Registry недоступен — transient-ошибка: partition ждёт, запись в DLQ не уходит (ADR 28). `deserialization_error` получает те же метаданные, что и сейчас, и попадает в `<stream>.dlq` тем же deserialization guard. Redrive публикует value с префиксом без изменений.

Конфиг `SCHEMA_REGISTRY__URL` обязателен у `orders-consumer`, `restaurants-consumer` и процесса `delivery`, который читает `delivery.v1.delivery-events`. Пустой или невалидный URL — падение на старте.

## Kind

- Redpanda стартует с `--schema-registry-addr=0.0.0.0:8081`, у Service `kafka` появляется порт `schema-registry` `8081`.
- Before-hook helm-деплоера после Kafka и `dead-letter-topics` запускает Job `schema-registration` (образ `nomnom/schema-registration` собирает Skaffold) и ждёт `Complete`. Консьюмеры и коннекторы ставятся после него.
- NetworkPolicy `allow-infrastructure-egress` в `nomnom-orders`, `nomnom-restaurants` и `nomnom-delivery` открывает `kafka:8081`. Kafka Connect в `nomnom-infra` политиками не ограничен.
- `console.yaml`: `schemaRegistry.enabled: true` с тем же URL, payload в Console расшифровывается.
- Переход сразу: перед первым деплоем kind сбрасывается (`make kind-down`, `make kind-up`, `make kind-deploy`), как при прошлых несовместимых изменениях хранилищ.

## Риски

- **Registry становится зависимостью доставки.** Если Registry недоступен, коннекторы падают в `FAILED`, а consumers ждут. Это осознанно: без Registry запись нельзя ни оформить, ни проверить. Алерты коннекторов (`cdc-*`) уже есть.
- **Регистрация до деплоя publisher'а.** Новое событие или поле должно быть зарегистрировано до того, как publisher его запишет, иначе коннектор остановится. В kind порядок обеспечивает before-hook; для production это требование к pipeline.
- **TTL кеша SMT.** До истечения TTL SMT оформляет запись прежней последней версией. Из-за `FULL_TRANSITIVE` это безопасно.
- **Java-код в образе Connect.** Сборка образа получает стадию Gradle, версия Kafka Connect API должна совпадать с Debezium 3.7.
