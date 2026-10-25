# Tasks

## 1. Доменная модель

- [x] 1.1 Добавить в `domains/payment/` модуль `events.py` с событиями `PaymentAuthorized`, `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded`, `PaymentFailed`, `PaymentOperationDeclined`; записывать события в методах `Payment`, добавить `Payment.ensure_can(trigger)` и `fail(reason)`; обновить `tests/test_payment_domain.py` и проверить `make test`.
- [x] 1.2 Добавить `PaymentOperation` с табличной FSM `pending → succeeded | declined`, доменную функцию `complete_operation` по таблице design.md (решение 3) и контракты `PaymentRepository`/`PaymentOperationRepository` с ошибками уникальности; покрыть FSM полнотой таблицы и каждую строку `complete_operation` unit-тестами в `tests/test_payment_operation.py`.

## 2. Порты и test adapters

- [x] 2.1 Добавить `pkg/application` (`UnitOfWork`, `Clock`, `Subject`, `AggregateRef`, generic errors) и `slices/payment/applications/{ports,errors}.py` с портами permissions, provider gateway, Outbox, provider message inbox и identifiers; проверить `make typecheck`.
- [x] 2.2 Добавить `tests/payment_fakes.py`: in-memory store с snapshot/rollback UoW, repositories с optimistic concurrency и уникальностью, Outbox, provider message inbox, fake provider с дедупликацией по idempotency key, fake permissions, фиксированный clock и последовательные ids; покрыть rollback и уникальность тестами `tests/test_payment_fakes.py`.

## 3. Commands

- [x] 3.1 Реализовать `create_payment` (FR-400, FR-411) и тесты AC-400…AC-403, AC-426 в `tests/test_create_payment.py`.
- [x] 3.2 Реализовать общий `operation_flow` и `authorize_payment` (FR-401…FR-403, FR-407, FR-408, FR-414) с тестами AC-404…AC-409, AC-411, AC-418, AC-419, AC-426, AC-427, AC-432 в `tests/test_authorize_payment.py`.
- [x] 3.3 Реализовать `capture_payment`, `cancel_authorization`, `refund_payment` (FR-404…FR-406) с тестами AC-407, AC-410, AC-412…AC-417 в соответствующих `tests/test_<usecase>.py`.
- [x] 3.4 Реализовать `apply_provider_callback` и `apply_provider_operation_result` (FR-409, FR-410) с тестами AC-420…AC-425 в соответствующих `tests/test_<usecase>.py`.

## 4. Queries

- [x] 4.1 Реализовать `get_payment_status` и `list_payment_operations` (FR-412, FR-413) с тестами AC-428…AC-431, включая отсутствие `provider_reference` в результате.

## 5. Документация и проверка

- [x] 5.1 Обновить статус реализации, порты и ошибки в `boundaries/payments/README.md`, README затронутых use cases и термин Payment Operation в `boundaries/payments/GLOSSARY.md`; проверить, что общие документы вне `boundaries/payments` не изменены.
- [x] 5.2 Выполнить `make check` в `boundaries/payments` и `openspec validate implement-payments-application-layer --strict`; оба должны завершиться успешно.

## Workflow follow-up

- Внести предложенные правки общих документов (design.md — «Follow-up для общих документов») отдельным согласованным изменением.
- Архивировать change после ревью.
