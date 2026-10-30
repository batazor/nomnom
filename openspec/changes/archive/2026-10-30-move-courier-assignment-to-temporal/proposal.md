# Proposal

## Why

Назначение курьера в `delivery` сейчас идёт двумя фоновыми проходами по PostgreSQL: `OfferExpiryWorker` раз в 15 s закрывает просроченные offers, `DispatchWorker` раз в 2 s предлагает ожидающие доставки следующим курьерам. Offer живёт 60 s, а истекает с опозданием до 15 s. Чаще проходить дорого: каждый проход сканирует таблицы, даже когда ничего не происходит.

По сути это один процесс на одну доставку: доставка ждёт курьера, ей по очереди предлагают курьеров, кто-то соглашается. Durable workflow с таймером на каждый offer и пробуждением по событиям даёт точный срок истечения без polling. Кроме того, новые сроки на уровне доставки (SLA на Pickup, напоминания курьеру) становятся просто таймерами того же workflow.

Temporal Rust SDK вышел в GA 4 сентября 2026 (сейчас `temporalio-sdk` 1.1.0, MSRV 1.92) и совместим с нашими `tokio`, `tonic` 0.14 и `prost` 0.14.

Параллельно `orders` держит своё ожидание курьера: через `FULFILLMENT__ASSIGNMENT_TIMEOUT` (15m) вызывает `ReplanDelivery` до `MaxReplans` раз, затем ждёт `OperatorDecision`. Продуктово это не нужно: курьер рано или поздно найдётся, а оператор доставку сам не выполнит. Когда поиском целиком владеет `delivery`, эта логика только дублирует его.

## What Changes

- **Workflow назначения в `delivery`.** Workflow `delivery:<delivery_id>` на Temporal Rust SDK запускается из Create Delivery после commit и живёт до Pickup или отмены доставки. Цикл: Find Courier и Offer Delivery → ожидание ответа курьера или таймер до `expiresAt` → Expire → следующий курьер. Принятый offer — ожидание Pickup; Reassign Courier возвращает доставку в поиск.
- **Без кандидата — ожидание пробуждения.** Курьер, ставший кандидатом (Set Courier Availability, сохранённая позиция available курьера, освобождение после Confirm Delivery, Report Delivery Failure, Reassign Courier, Cancel Delivery), будит самую старую ожидающую доставку своего Market, которой его ещё не предлагали. Таймеров перепроверки и polling нет.
- **Signals после commit.** Use cases, меняющие ход назначения, после commit передают workflow signal. Ошибка signal — `UNAVAILABLE`, повтор вызова передаёт signal заново, даже если состояние уже не меняется.
- **Удаляется:** `OfferExpiryWorker`, `DispatchWorker`, конфиг `OFFERS__EXPIRY_INTERVAL`, `OFFERS__EXPIRY_BATCH`, `OFFERS__DISPATCH_INTERVAL`, `OFFERS__DISPATCH_BATCH`, query-порты `ExpiredDeliveryOffers` и `WaitingDeliveries`, use case Replan Delivery, gRPC `ReplanDelivery` и право `ReplanDelivery`.
- **Новая deployable role `delivery-worker`** с обязательным конфигом `TEMPORAL__*` (у процесса `delivery` тоже): пустое или невалидное значение — падение на старте. Таймауты Activities и continue-as-new — в коде workflow, как в `orders`.
- **OpenTelemetry не меняется.** sdk-rust тянет `opentelemetry` 0.32 только с feature `otel`/`opentelemetry`; SDK подключён без default features и с `vendored-protox`, поэтому `delivery` остаётся на 0.33 и сборке не нужен `protoc`.
- **`orders` ждёт назначения без срока.** Из модели fulfillment удаляются `AssignmentTimeout`, `MaxReplans`, `OperatorDecisionTimeout`, шаг `ReplanDelivery`, stage `waiting_operator`, signal `OperatorDecision` и причина `operator_decision`. После Create Delivery workflow просто ждёт `CourierAssignedToDelivery`.

Не входит:

- новые бизнес-таймеры после назначения (SLA на Pickup и вручение);
- перенос в workflow уже ожидающих доставок: окружение только для разработки, хранилище kind сбрасывается;
- изменение алгоритма Find Courier и срока жизни offer.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `delivery-assignment`: workflow назначения, истечение offer по таймеру, пробуждение при освобождении курьера, signals после commit, удаление Dispatch Deliveries.
- `order-fulfillment-orchestration`: ожидание назначения курьера без срока, без replan и решения оператора.
- `fulfillment-command-api`: без `ReplanDelivery`.
- `source-module-structure`: без Replan Delivery в требованиях к прикладным сервисам `delivery`.
- `restaurant-delivery-relationships`: без Replan Delivery среди сценариев, удаляющих `delivery#courier`.

## Impact

- **`boundaries/delivery`:**
  - Temporal-адаптер `infrastructure/temporal`: workflow, Activities, client для start и signals;
  - бинарник `delivery-worker`, его composition root и конфиг `TEMPORAL__*`;
  - use cases Create Delivery, Accept, Reject, Reassign, Cancel Delivery, Confirm Pickup, Confirm Delivery, Report Delivery Failure, Set Courier Availability, Update Courier Location — start или signal после commit;
  - use case Advance Assignment вместо Dispatch Deliveries, Expire Delivery Offer для одного offer вместо пачек;
  - query-порт выбора доставки для пробуждения;
  - удаление фоновых workers, Replan Delivery, `ReplanDelivery` из proto и SpiceDB mapping;
  - Helm chart, kind, README, новый локальный ADR 2.
- **`boundaries/orders`:** модель и Temporal-адаптер fulfillment без replan и эскалации, конфиг `FULFILLMENT__ASSIGNMENT_TIMEOUT`, `FULFILLMENT__MAX_REPLANS`, `FULFILLMENT__OPERATOR_DECISION_TIMEOUT` удаляется, клиент `ReplanDelivery`, README и ADR 5.
- **`boundaries/bff`:** перегенерированные stubs `nomnom.delivery.v1` без `ReplanDelivery`.
- **`authorization/schema.zed`:** комментарий о действиях `delivery` без replan.
- **Workflow `orders` в работе** несовместимы с новым кодом; окружение только для разработки, поэтому kind сбрасывается без patch.
