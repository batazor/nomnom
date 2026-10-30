# Design

Решение и отклонённые варианты — в [ADR 2 payments](../../../boundaries/payments/docs/adr/0002-event-sourcing-for-payment.md). Здесь — как его реализовать.

## Схема

Миграция `00003_payment_events.sql` набора `payments`:

```sql
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM payments) THEN
        RAISE EXCEPTION 'payments must be empty: existing rows have no event stream';
    END IF;
END $$;

CREATE TABLE payment_events (
    payment_id     TEXT        NOT NULL,
    version        BIGINT      NOT NULL CHECK (version > 0),
    event_type     TEXT        NOT NULL,
    schema_version INT         NOT NULL CHECK (schema_version > 0),
    payload        JSONB       NOT NULL,
    -- W3C trace context of the write, '' without one, as in the Outbox.
    traceparent    TEXT        NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL,
    CONSTRAINT payment_events_pkey PRIMARY KEY (payment_id, version)
);

-- Row trigger: rejects UPDATE and DELETE. TRUNCATE stays available to tests.
CREATE TRIGGER payment_events_append_only ...;

CREATE TABLE payment_snapshots (
    payment_id     TEXT        PRIMARY KEY,
    version        BIGINT      NOT NULL CHECK (version > 0),
    schema_version INT         NOT NULL CHECK (schema_version > 0),
    state          JSONB       NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL
);

ALTER TABLE payment_operations DROP COLUMN version;
```

`payments.version` остаётся и равна версии потока после commit. Индексы `payments` и `payment_operations` не меняются. Индекс `payment_operations_one_pending` остаётся страховкой схемы: основную проверку теперь делает агрегат.

## События

| Событие | Поля | Статус | Публикуется |
| --- | --- | --- | --- |
| `PaymentCreated` | `payment_id`, `order_id`, `market_id`, `amount` | новое | нет |
| `PaymentOperationRequested` | `operation_id`, `payment_id`, `kind`, `idempotency_key`, `amount`, `requested_at` | новое | нет |
| `PaymentOperationSucceeded` | `operation_id`, `payment_id`, `kind`, `provider_reference?`, `completed_at` | новое | нет |
| `PaymentOperationDeclined` | + `completed_at` | меняется | нет |
| `PaymentAuthorized` | + `provider_reference` | меняется | да, без нового поля |
| `PaymentCaptured`, `PaymentAuthorizationCancelled`, `PaymentRefunded`, `PaymentFailed` | без изменений | — | да |

Обычный заказ даёт такой поток:

```text
1 PaymentCreated
2 PaymentOperationRequested(authorize)    UoW#1
3 PaymentOperationSucceeded(authorize)    UoW#2 ┐ один commit
4 PaymentAuthorized                             ┘
5 PaymentOperationRequested(capture)
6 PaymentOperationSucceeded(capture)
7 PaymentCaptured
```

Порядок «сначала событие операции» совпадает с текущим `complete_operation`. Отказ provider в Authorization: `PaymentOperationDeclined`, затем `PaymentFailed`. Ответ `accepted` событий не создаёт, как и неудачный вызов provider (`FR-407`, `FR-408`).

## Домен

```python
# domains/payment/decider.py
def decide(state: PaymentModel | None, command: PaymentCommand) -> list[PaymentDomainEvent]: ...
def evolve(state: PaymentModel | None, event: PaymentDomainEvent) -> PaymentModel: ...
```

- `PaymentModel` — неизменяемое состояние: поля `Payment` и операции. `PaymentState` уже занято листовым состоянием FSM, поэтому имя другое.
- `decide` проверяет правила через `transition` и `rules_of`, а `evolve` только применяет факт и не бросает бизнес-ошибок.
- `Payment` хранит модель и версию. Его команды (`request_operation`, `complete_operation`) вызывают `decide`, затем `evolve`, и накапливают новые события и transitions. Методы `authorize`, `capture`, `cancel_authorization`, `refund` и `fail` удалены: эти действия стали чистыми функциями правил `OPERATION_KINDS`, которые возвращают факты. Transition выводится из факта при его применении.
- `Payment.from_history(snapshot, version, events)` собирает состояние без накопления событий и transitions.
- `PaymentOperation` становится неизменяемым представлением операции внутри `PaymentModel` и теряет `version` и `pull_events`.

### Порты

```python
class PaymentRepository(Protocol):
    def get(self, payment_id) -> Payment | None: ...
    def get_by_order(self, order_id) -> Payment | None: ...
    def get_by_operation(self, operation_id) -> Payment | None: ...
    def get_by_idempotency_key(self, idempotency_key) -> Payment | None: ...
    def add(self, payment) -> int: ...
    def save(self, payment) -> int: ...
```

`PaymentOperationRepository` и `PendingOperationQuery` удаляются. `_reserve` загружает `Payment` по `payment_id` и ищет операцию с ключом. Если её нет, `get_by_idempotency_key` проверяет, не занят ли ключ другим `Payment`: это даёт `IdempotencyKeyConflictError`. `DuplicateIdempotencyKeyError` остаётся на случай гонки между разными `Payment`.

`CreatePaymentHandler` по-прежнему работает без UoW: `add` атомарен сам за счёт `conn.transaction()`.

## Запись

`save` и `add` в одном savepoint:

```text
INSERT payment_events (version = загруженная + 1, + 2, …)
  payment_events_pkey занят → ConcurrencyConflictError (UoW повторит, ADR 20)
UPSERT payments, payment_operations из состояния агрегата
  order_id / idempotency_key заняты → доменная ошибка; savepoint откатывает и события
если граница пересечена → UPSERT payment_snapshots
```

`recorded_at` — `now()`, как у Outbox. Версия возвращается и идёт в `AggregateRef`: у Outbox Records одного commit одна `aggregate_version`, как описано в `docs/domain-events.md`.

### Гонки

| Гонка | Результат |
| --- | --- |
| Два запроса с одним ключом | Один записывает `PaymentOperationRequested`, второй получает конфликт, UoW повторяет, второй находит операцию и продолжает с ней. Provider получает один ключ |
| Два запроса с разными ключами | Второй после повтора видит незавершённую операцию: `OperationInProgressError` |
| Результат provider и повторный callback | Второй после повтора видит завершённую операцию: no-op или `ProviderResultConflictError` |
| Create Payment для одного `Order` | `payment_id` разные, поэтому конфликт на `payments_order_id_key`: `DuplicatePaymentError`, поток второго не создаётся |
| Один ключ у двух разных `Payment` | `payment_operations_idempotency_key_key`: `DuplicateIdempotencyKeyError`, затем `IdempotencyKeyConflictError` при повторном резервировании |

Поиск ключа через проекцию может вернуть тот же `Payment`, если его состояние устарело: тогда `_reserve` продолжает, запись конфликтует на потоке, а UoW повторяет работу на свежем состоянии. Конфликт ключа — только когда им владеет другой `Payment`.

## Загрузка

```text
SELECT снапшот                                    get: payment_id из аргумента
SELECT события WHERE version > версия снапшота    get_by_*: payment_id из проекции
ORDER BY version
→ upcast → Payment.from_history
```

Нет ни снапшота, ни событий — `None`. Разрыв в версиях или хвост, не начинающийся сразу за снапшотом, — ошибка данных, а не попытка продолжить.

## Кодек

`infrastructure/repository/event_codec.py`:

- реестр `event_type ↔ dataclass`: имена — явные строки, совпадающие сейчас с именами классов;
- `Money` — объект со строковой суммой и валютой, время — ISO 8601 с часовым поясом, value objects — их значения;
- upcaster'ы — таблица `(event_type, schema_version) → функция на следующую версию`; первая версия всех событий — 1, поэтому таблица пока пуста;
- неизвестный тип или версия новее текущей — ошибка загрузки; событие не пропускается.

Снапшот кодируется там же с константой `SNAPSHOT_SCHEMA_VERSION`. Другая версия — снапшот не используется. Ошибка разбора при текущей версии — ошибка загрузки.

## Снапшоты

```python
def crosses_snapshot_boundary(old: int, new: int, every: int) -> bool:
    return new // every > old // every
```

С `every = 5` обычный поток из 7 событий записывает снапшот, когда commit доходит до версии 5 или проходит её. Запись снапшота — upsert с условием `WHERE EXCLUDED.version > payment_snapshots.version`.

`EVENT_STORE__SNAPSHOT_EVERY` — обязательный положительный integer, проверяется при загрузке конфига. Значения задаются явно: 5 в `.env.example` и values kind.

## Telemetry

Гистограмма `nomnom_payments_event_store_replayed_events` с атрибутом `nomnom.event_store.from_snapshot` (`true` или `false`), записью в `ops/semconv/model/event_store.yaml` и инвентарём в `ops/grafana/inventory`. `payment_id` в атрибуты не попадает (ADR 9). Метрика пишется и при чтении вне UoW, поэтому она не идёт через `CommitScope`.

## Тесты

- **Unit:** `decide` и `evolve` таблицей given/when/then на событиях, включая отказы; replay не накапливает события и transitions; граница снапшота, включая commit из нескольких событий.
- **Golden:** записанные потоки в `tests/fixtures/payment_streams/` читаются текущим кодом. Для каждого потока загрузка со снапшотом на каждой версии равна полному replay. Это ловит забытый подъём `SNAPSHOT_SCHEMA_VERSION`.
- **Integration (Testcontainers):** конфликт версий, триггер неизменяемости, откат событий при дубликате, удалённый снапшот, снапшот другой версии, проекция равна состоянию из потока, гонки из таблицы выше.
- **E2E (kind):** `order-lifecycle` без изменений: проверка `SELECT state FROM payments` подтверждает работу проекции.

Существующие тесты `payment-operations` проверяют неизменность поведения и меняются только там, где удалены порты.

## Риски

| Риск | Что с этим делаем |
| --- | --- |
| Ошибка в событии остаётся навсегда | Обязательные golden-потоки; исправление только новым событием или upcaster'ом |
| Забытый подъём версии снапшота даёт неверное состояние | Golden-тест сравнения снапшота с replay |
| Больше конфликтов на одном `Payment` | Записи на один `Payment` редки, а UoW уже повторяет конфликт; виден в `nomnom.uow.retry` |
| Расхождение проекции и потока | Обновляются в одном savepoint; integration-тест их сравнивает |
| `Temporal` повторяет Activity после сброса данных | В kind незавершённые заказы сбрасываются вместе с хранилищем |
