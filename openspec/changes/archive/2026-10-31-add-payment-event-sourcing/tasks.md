# Tasks

## 1. Домен

- [x] 1.1 Полный набор событий: `PaymentCreated`, `PaymentOperationRequested`, `PaymentOperationSucceeded`, `provider_reference` в `PaymentAuthorized`, `completed_at` в `PaymentOperationDeclined` (`FR-741`).
- [x] 1.2 `PaymentModel`, `decide`, `evolve` поверх FSM и `OPERATION_KINDS`; unit-тесты given/when/then (`FR-740`, `FR-741`).
- [x] 1.3 `PaymentOperation` внутри агрегата, `Payment` как оболочка и `Payment.from_history` без накопления событий и transitions (`FR-740`, `AC-1019`).
- [x] 1.4 `crosses_snapshot_boundary`, unit-тесты с commit из нескольких событий (`FR-746`).

## 2. Хранилище

- [x] 2.1 Миграция `00003_payment_events.sql`: проверка пустой `payments`, таблицы, триггер неизменяемости, удаление `payment_operations.version` (`FR-742`, `FR-749`).
- [x] 2.2 `event_codec.py`: реестр типов, кодирование `Money` и времени, upcaster'ы, снапшот с `SNAPSHOT_SCHEMA_VERSION` (`FR-745`, `FR-747`).
- [x] 2.3 Event store и новый `PaymentRepository`: запись событий, проекций и снапшота в savepoint, загрузка снапшота и хвоста, `get_by_operation`, `get_by_idempotency_key` (`FR-743`, `FR-744`, `FR-746`, `FR-747`).
- [x] 2.4 Конфиг `EVENT_STORE__SNAPSHOT_EVERY`: падение на старте при пустом или невалидном значении, `.env.example`, values (`FR-746`, `AC-1037`).

## 3. Application

- [x] 3.1 `operation_flow` и `provider_results` на новом `PaymentRepository`, удаление `PaymentOperationRepository`, `PendingOperationQuery` и их адаптеров, DI (`FR-743`).
- [x] 3.2 `AggregateRef` с версией потока; проверка, что новые события не попадают в Outbox, а контракт `PaymentAuthorized` не содержит `provider_reference` (`FR-748`).
- [x] 3.3 Метрика `nomnom_payments_event_store_replayed_events`, запись в `ops/grafana/inventory`.

## 4. Тесты

- [x] 4.1 Golden-потоки в `tests/fixtures/payment_streams/`: чтение текущим кодом и сравнение снапшота на каждой версии с полным replay (`FR-745`, `FR-747`).
- [x] 4.2 Integration на Testcontainers: конфликт версий, неизменяемость, откат при дубликате, снапшоты удалённый и другой версии, проекция равна потоку, гонки из `design.md` (`FR-742`–`FR-747`).
- [x] 4.3 Существующие тесты `payments` обновлены только там, где удалены порты; поведение `payment-operations` не меняется.

## 5. Документация

- [x] 5.1 ADR 1 `payments`: конкурентность, инварианты, новые таблицы, ссылка на ADR 2.
- [x] 5.2 ADR 21, `docs/domain-events.md`, `docs/unit-of-work.md`, `docs/fsm.md`: для `Payment` Domain Events ещё и сохраняются в потоке, версия равна версии потока.
- [x] 5.3 README и `GLOSSARY.md` `payments`, `docs/ubiquitous-language.md`: Payment Event Stream, Payment Snapshot, Payment Operation как часть `Payment`.

## 6. Проверка

- [x] 6.1 Хранилище `payments` в kind пусто на момент выкатки, миграция применена, `order-lifecycle` проходит.
- [x] 6.2 В kind у завершённого заказа есть поток и снапшот. Метрику загрузки со снапшотом проверяет integration-тест `AC-1038`; в kind она не проверялась.
