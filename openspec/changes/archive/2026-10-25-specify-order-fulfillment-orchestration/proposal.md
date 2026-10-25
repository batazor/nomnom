# Proposal

## Why

Принятый ADR о Temporal фиксирует только выбор технологии, но не определяет границу её применения, идентичность workflow, безопасный старт после создания заказа и протокол входящих событий. Без единого контракта реализация может смешать состояние `Order` и workflow, использовать Temporal для CRUD или допустить повторные финансовые и компенсационные эффекты.

## What Changes

- Определить **Order Fulfillment Workflow** как один durable workflow на один логический `Order` с детерминированным Workflow ID, namespace на окружение и task queue `orders`.
- Зафиксировать основной путь старта через собственное событие `OrderPlaced` (`Outbox → Kafka → Inbox → SignalWithStart`) и допустимую альтернативу синхронного старта после commit с retry.
- Описать координацию Authorization/Capture/Refund, Order Acceptance и Preparation, Courier Assignment, Pickup и Delivery Completion без переноса доменных правил из `payments`, `restaurants` и `delivery` в `orders`.
- Определить протокол `Kafka → Inbox → application handler → Signal/Update`, два уровня дедупликации, правила неожиданного и позднего события и отличие Signal, Update и Query.
- Зафиксировать business cancellation, orchestration-based Saga, технические retries, бизнес-таймеры, activity idempotency и разделение Kafka и Temporal.
- Явно перечислить процессы, для которых Temporal не используется: CQRS queries, локальные транзакции, event delivery, authorization, хранение агрегатов и универсальные фоновые задания.
- Обновить документацию `orders` и добавить новый локальный ADR, не изменяя существующие ADR.
- Исходный код Workflow и Activities, инфраструктура Temporal и точные продуктовые таймауты остаются вне объёма изменения.

## Capabilities

### New Capabilities

- `order-fulfillment-orchestration`: контракт durable orchestration общего жизненного цикла `Order`, его интеграции с bounded contexts, идемпотентности, ожиданий, компенсаций и границ неприменения Temporal.

### Modified Capabilities

Нет.

## Impact

- Документация: `boundaries/orders/README.md`, новый ADR в `boundaries/orders/docs/adr/`, OpenSpec change и delta spec.
- Будущие процессы `orders`: API, Kafka consumer и Temporal worker должны следовать единой схеме старта, сигналов, retries и shutdown.
- Контракты взаимодействия с `payments`, `restaurants` и `delivery` уточняются на уровне orchestration, но их доменная ответственность и source of truth не меняются.
- Код, protobuf-контракты, Kafka topics, схемы хранения, Helm charts и runtime-конфигурация на этом шаге не изменяются.
