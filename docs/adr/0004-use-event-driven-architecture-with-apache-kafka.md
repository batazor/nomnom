# 4. Использовать Event-Driven Architecture и Apache Kafka

Date: 2026-10-24

## Status

Accepted

Дополнено [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md)

Дополнено [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md)

## Context

Bounded contexts должны обмениваться результатами автономно и без жёсткой временной связанности. Для такого взаимодействия нужен общий подход к интеграционным событиям и единый транспорт.

## Decision

Использовать Event-Driven Architecture для асинхронного взаимодействия между bounded contexts через интеграционные события. Apache Kafka используется как транспорт этих событий.

Конкретные топики, контракты и схемы событий этим решением не определяются. Выбор Kafka-клиента относится к архитектурным решениям конкретного контекста.

## Consequences

Контексты смогут взаимодействовать асинхронно и развиваться с меньшей временной связанностью. При этом система должна учитывать eventual consistency, повторную доставку, порядок сообщений, наблюдаемость и совместимость контрактов. Kafka становится общим интеграционным транспортом, а клиентские библиотеки выбираются и документируются внутри использующих их контекстов.
