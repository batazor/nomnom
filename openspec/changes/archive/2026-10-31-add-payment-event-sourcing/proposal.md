# Proposal

## Why

Проект учебный, и Event Sourcing в нём ещё не пробовали. `Payment` подходит для первого опыта: поток короткий, журнал фактов о деньгах полезен сам по себе, а чистая FSM уже есть. Сейчас `payments` и `payment_operations` хранят только текущие строки, поэтому историю `Payment` восстановить нельзя.

Решение и отклонённые варианты описаны в [ADR 2 payments](../../../../boundaries/payments/docs/adr/0002-event-sourcing-for-payment.md).

## What Changes

- **Поток событий — источник истины.** Новая таблица `payment_events`, неизменяемая и с первичным ключом `(payment_id, version)`, который обеспечивает optimistic concurrency.
- **Один агрегат.** `PaymentOperation` становится сущностью внутри `Payment`, один `Payment` — один поток. `PaymentOperationRepository` и `PendingOperationQuery` удаляются, `complete_operation` переходит в агрегат.
- **Decider.** Чистые `decide` и `evolve` поверх FSM. `Payment` остаётся тонкой оболочкой, поэтому `pull_events` и `pull_transitions` работают как раньше.
- **Полные события.** Добавляются `PaymentCreated`, `PaymentOperationRequested` и `PaymentOperationSucceeded`. `PaymentAuthorized` получает `provider_reference`, `PaymentOperationDeclined` — `completed_at`.
- **Снапшоты.** Новая таблица `payment_snapshots` с последним снапшотом `Payment`, запись в той же транзакции по границе обязательного `EVENT_STORE__SNAPSHOT_EVERY`, загрузка снапшота и хвоста потока.
- **Версионирование.** `schema_version` у событий и снапшотов, upcaster'ы; неизвестный тип или версия события — ошибка.
- **Синхронные проекции.** `payments` и `payment_operations` обновляются в той же транзакции; их запросы и уникальные индексы сохраняются.
- **Метрика.** Гистограмма числа применённых при загрузке событий показывает, работают ли снапшоты.

Не меняются:

- gRPC API, Integration Events, топик, Outbox и Debezium;
- наблюдаемое поведение use cases `payment-operations`, кроме атомарной записи событий.

**BREAKING (только для данных):** перенос существующих `Payment` не делается. Миграция завершается ошибкой, если `payments` не пуста, поэтому хранилище kind сбрасывается перед выкаткой.

Не входит:

- команда пересборки проекций;
- запись subject в событиях для аудита;
- Dynamic Consistency Boundary и Event Sourcing в других контекстах.

## Capabilities

### New Capabilities

- `payment-event-sourcing`: поток событий `Payment`, конкурентная запись, проекции, версионирование и снапшоты.

### Modified Capabilities

- `payment-operations`: атомарная фиксация включает события и снапшот (`FR-414`).

## Impact

- **`boundaries/payments`:**
  - `domains/payment`: `events`, `decider`, `payment`, `operation`, `completion`, `operation_kinds`, `repository`;
  - `applications`: `operation_flow`, `provider_results`, порты;
  - `infrastructure/repository`: event store, кодек событий и снапшотов, проекции, поиск операции, миграция;
  - конфиг `EVENT_STORE__SNAPSHOT_EVERY`, `.env.example`, values;
  - telemetry и тесты.
- **`ops/grafana/inventory`:** новая метрика.
- **`ops/kind`:** сброс хранилища `payments` при выкатке, новая переменная в окружении.
- **Документация:** ADR 1 и README `payments`, `GLOSSARY.md`, ADR 21, `docs/domain-events.md`, `docs/unit-of-work.md`, `docs/fsm.md`.
