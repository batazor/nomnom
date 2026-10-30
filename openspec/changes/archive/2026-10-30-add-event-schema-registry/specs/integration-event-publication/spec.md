## MODIFIED Requirements

### Requirement: Event Envelope совпадает с orders
**ID:** FR-530

Коннектор SHALL передавать Event Envelope в Kafka headers строками (`SimpleHeaderConverter`): `event_id`, `_watermill_message_uuid` (= `event_id`), `event_type`, `occurred_at` (RFC 3339 UTC), `aggregate_type`, `aggregate_id`, `aggregate_version` (десятичная строка), `traceparent` (пустой без trace context). Value стрима Integration Events MUST быть `payload` в wire format Schema Registry (transform `SchemaFraming`, FR-714). Value Change Notification streams (`*.v1.*-changes`) MUST быть `payload` без изменений (`ByteArrayConverter`).

#### Scenario: Headers опубликованного события
**ID:** AC-620

- **WHEN** коннектор публикует запись с `occurred_at = 2026-10-25T10:15:30.120000Z` без trace context
- **THEN** сообщение несёт все headers Envelope, `occurred_at` — RFC 3339 UTC, `traceparent` пустой, а Go consumer `orders` читает `event_id` как UUID сообщения Watermill

#### Scenario: Value Integration Event и Change Notification
**ID:** AC-981

- **WHEN** транзакция `orders` записывает Integration Event в `orders.v1.order-events` и Order Change Notification в `orders.v1.order-changes`
- **THEN** value первого — wire format с id subject его `event_type`, value второго — JSON без префикса
