# Proposal

## Why

Ревью SOLID `payments`:

- Знание о каждом `OperationKind` разнесено по пяти местам: право в `operation_flow.py` (`_PERMISSION_OF`), trigger в `operation.py` (`PAYMENT_TRIGGER_OF`), особый случай отказа Authorization, цепочка `_apply_success`, где ветка `else` молча вызывает `payment.refund()`, и `_DECLINES` с цепочкой `isinstance` в метриках. Новый вид операции был бы обработан как возврат денег (OCP).
- `complete_operation` — доменный сервис двух агрегатов, но лежит в модуле сущности.
- `infrastructure/grpc/service.py` объединяет servicer, таблицу ошибок, lifecycle сервера и генератор идентификаторов (`UuidIdentifiers`, который DI берёт из gRPC-модуля).
- `PaymentOperationExecutor` сам создаёт `ProviderResultApplier` и принимает Outbox только чтобы передать его дальше; DI создаёт второй applier.
- `identity.py` смешивает чтение metadata, проверку JWT и политику делегатов; импортирует приватные имена.
- `OutboxStore` объединяет блокировку лидера, очередь публикации, очистку и метрики backlog.

## What Changes

- Одна доменная таблица `OperationKind → {permission, trigger, on_success, on_decline}`; неизвестный вид — ошибка, а не refund; метрики используют ту же таблицу.
- `complete_operation` → `domains/payment/completion.py`.
- `grpc/servicer.py`, `grpc/errors.py`, `grpc/worker.py`, `infrastructure/ids.py`.
- Applier внедряется в executor; `PaymentIdentifiers` делится на источники id платежа и операции.
- `ServiceTokenVerifier` без транспорта, `UserAssertionVerifier`, resolver читает metadata и соединяет их.
- `OutboxStore` делится на `OutboxLease`, `OutboxQueue`, `OutboxRetention`, `OutboxBacklog`.

Не входит: изменение поведения финансовых операций и контрактов.

## Capabilities

### Modified Capabilities

- `payment-operations`: явное поведение каждого вида операции.
- `source-module-structure`: модули payments по ответственности.

## Impact

`boundaries/payments` (domains, applications, infrastructure, di, тесты, README).
