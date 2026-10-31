# Design

Решение и его границы — в [ADR 2 payments](../../../boundaries/payments/docs/adr/0002-event-sourcing-for-payment.md#аудит-раскрытия-и-забвения). Здесь — как его реализовать.

## Путь запроса

```text
оператор → Dex → UI → GraphQL (persisted operation) → BFF подписывает user assertion
  → internal gateway → payments: service token + assertion → Subject(user, sub, delegate=bff)
  → проверка «это пользователь» → SpiceDB fully_consistent → use case
```

Подписать assertion может только BFF (ADR 32), поэтому другого пути с проверенной личностью у человека нет. `e2e-driver` получает право делегата только в kind, как уже сделано для `delivery` и `restaurants`.

## Контракт gRPC

```protobuf
rpc RevealPaymentRequesters(RevealPaymentRequestersRequest) returns (RevealPaymentRequestersResponse);
rpc ForgetSubject(ForgetSubjectRequest) returns (ForgetSubjectResponse);

message RevealPaymentRequestersRequest { string payment_id = 1; string reason = 2; }
message RevealPaymentRequestersResponse {
  string payment_id = 1;
  PaymentState state = 2;
  repeated AuditedOperation operations = 3;
}
message AuditedOperation {
  string operation_id = 1;
  OperationKind kind = 2;
  OperationState state = 3;
  google.protobuf.Timestamp requested_at = 4;
  Requester requester = 5;
}
message Requester {
  RequesterStatus status = 1;   // KNOWN, FORGOTTEN, UNKNOWN
  RequesterKind kind = 2;
  string id = 3;                // пусто, если не KNOWN
  string delegate = 4;
}
message ForgetSubjectRequest { string subject_id = 1; string reason = 2; }
message ForgetSubjectResponse { bool forgotten = 1; }
```

Имена существующих enum-ов берутся из `payments.proto`; при реализации новые типы не дублируют уже объявленные.

| Условие | Код gRPC | `ErrorInfo.reason` |
| --- | --- | --- |
| Пустой `payment_id`, `subject_id` или `reason`; `reason` длиннее 200 символов | `INVALID_ARGUMENT` | `INVALID_PAYMENT_REQUEST` |
| Вызов сервиса без user assertion, нет права | `PERMISSION_DENIED` | существующий reason отказа |
| Проверка права не удалась | `UNAVAILABLE` | существующий reason |
| Неизвестный `Payment` | `NOT_FOUND` | `PAYMENT_NOT_FOUND` |

Неизвестный `subject_id` не ошибка: иначе ответ раскрывал бы, был ли такой пользователь в `payments`. Но `forgotten=false` тоже отличает его, поэтому оба ответа доступны только `auditor`.

## Журнал аудита

Миграция `00006_subject_key_audit.sql`:

```sql
CREATE TABLE subject_key_audit (
    id            TEXT        PRIMARY KEY,
    action        TEXT        NOT NULL CHECK (action IN ('reveal', 'forget', 'rewrap')),
    outcome       TEXT        NOT NULL CHECK (outcome IN ('applied', 'no_key')),
    actor_kind    TEXT        NOT NULL CHECK (actor_kind IN ('user', 'cli')),
    actor_id      TEXT,
    delegate      TEXT,
    key_id        TEXT,
    payment_id    TEXT,
    affected_keys INT,
    reason        TEXT        NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 200),
    trace_id      TEXT        NOT NULL,
    recorded_at   TIMESTAMPTZ NOT NULL,
    CONSTRAINT subject_key_audit_actor CHECK ((actor_kind = 'user') = (actor_id IS NOT NULL))
);
```

`UPDATE` и `DELETE` отклоняет триггер, как у `payment_events`. `actor_id` хранится открыто: это проверенный автор действия, и журнал существует ради него. Поэтому `subjects forget` не стирает записи, где этот пользователь был автором.

| Действие | Что пишем | Когда |
| --- | --- | --- |
| Раскрытие | Строка `reveal` на каждый открываемый `key_id` | Commit до расшифровки. Нет запечатанных авторов или все забыты — записи нет |
| Забвение | Строка `forget`: `applied` с `key_id` или `no_key` без него | В одной транзакции с очисткой ключа |
| `rewrap` | Одна строка: `actor_kind = cli`, `affected_keys` | После завершения пачек; `--reason` обязателен |

Отказ в праве в таблицу не пишется: журнал отражает доступ к данным, а не попытки. Отказ идёт в лог и `nomnom.authz.checks`.

## Use cases

Оба лежат в `slices/payment/applications/` с README по шаблону контекста и используют порты:

- `SubjectKeyAudit.record(entry)` — запись в активный UoW; реализация пишет в `subject_key_audit`.
- `SubjectKeys.reveal(sealed)` и `SubjectKeys.forget(subject_id) -> key_id | None`. Сейчас `forget` возвращает `bool`: `key_id` нужен для аудита.
- `PaymentAuditReader.history(payment_id)` — read-порт поверх `DatabasePaymentAuditReader`. Его view переезжают в application.

**Reveal Payment Requesters:**

```text
проверка входа → пользователь? → право reveal_subjects
→ чтение view → UoW { аудит на каждый key_id с открываемым автором }
→ расшифровка → результат
```

Если `reveal` падает после commit, запись аудита остаётся: это попытка, а данных вызывающий не получил.

**Forget Subject:**

```text
проверка входа → пользователь? → право forget_subjects
→ UoW { forget + запись аудита } → forgotten
```

Проверка «это пользователь» — в use case, до SpiceDB: `Subject.kind is SubjectKind.USER`, иначе `PermissionDeniedError`.

## SpiceDB

```text
relation auditor: user
permission reveal_subjects = auditor
permission forget_subjects = auditor
```

`operator` в них намеренно не входит: управлять сервисами и раскрывать людей — разные полномочия. В kind право получают `kind-operator` и его Dex-субъект.

`check_request` сейчас всегда использует `minimize_latency`. Для этих двух прав он использует `fully_consistent`, по таблице рядом с `PERMISSION_MAPPING`: неизвестное право — ошибка, а не значение по умолчанию.

## Gateway и identity

- `GRPCRoute payments-v1-subject-audit` с точными методами, без добавления в политику повторов чтений (ADR 15): раскрытие пишет в аудит.
- `ops/kind/values/payments.yaml`: `serviceAccounts` получает `nomnom-bff/bff=bff` и `nomnom-bff/e2e-driver=e2e-driver`, `userDelegates` — `e2e-driver` только в kind.
- BFF и `e2e-driver` получают projected token с audience `nomnom.payments`.
- NetworkPolicy менять не нужно: BFF уже ходит только через gateway. При реализации это проверяет `e2e/gateway`.

## BFF

```graphql
extend type Mutation {
  revealPaymentRequesters(paymentId: ID!, reason: String!): PaymentRequestersPayload!
  forgetSubject(subjectId: ID!, reason: String!): ForgetSubjectPayload!
}
```

Это мутации: раскрытие меняет журнал, и его нельзя кешировать или повторять как чтение. Плагин вызывает RPC с assertion текущего пользователя. Бизнес-отказы идут в payload, а отказ в праве — как `PERMISSION_DENIED`, по правилам customer-схемы.

**Утечка через логи.** `config.production.yaml` не включает логирование переменных GraphQL, но проверить надо поведение, а не конфиг: сценарий e2e передаёт уникальный идентификатор и ищет его в логах BFF, gateway и `payments`. Если значение нашлось, переменная маскируется средствами Router. Атрибуты трейсов проверяются там же, где это позволяет окружение kind, иначе это записывается как непроверенное.

## UI

Новый feature `payment-subject-audit`, маршрут `/audit/payments`, только для вошедшего пользователя:

- **Раскрытие:** `payment_id` и причина → таблица операций с авторами. Статусы `forgotten` и `unknown` показаны явно.
- **Забвение:** отдельная форма с причиной и подтверждением: оператор повторно вводит идентификатор.
- **Без кеша:** `fetchPolicy: "no-cache"`, результат живёт в памяти экрана и не попадает в `localStorage`. Персистентный кеш запросов на него не распространяется.
- **Нет права:** понятное сообщение из `PERMISSION_DENIED`. Ссылку видят все вошедшие: UI не знает о правах заранее, а граница всё равно в `payments`.

## `payments-admin`

Команды `payments history` и `subjects forget` удаляются. `subjects rewrap` получает обязательный `--reason` и пишет запись `rewrap`.

## Тесты

- **BDD:** `tests/features/*.feature` с тегами `@AC-…` на `pytest-bdd`, реальный PostgreSQL. Моки — только для SpiceDB и провайдера, инлайн в тесте.
- **Unit:** маппинг прав и уровень согласованности, кодирование ответа servicer.
- **Integration:** триггер журнала, атомарность забвения, порядок «аудит до расшифровки».
- **SpiceDB:** `make authz-validate`, реальная проверка в существующем `test_spicedb_integration`.
- **BFF:** тесты resolver и persisted operations.
- **UI:** тесты feature на моках, отсутствие результата в `localStorage`.
- **E2E (kind):** новый сценарий `subject-audit`: сервис и оператор без права получают отказ, `auditor` раскрывает и забывает, после чего автор `forgotten`, а журнал содержит записи и не содержит идентификатора. Плюс проверка логов на маркер.

## Риски

| Риск | Что с этим делаем |
| --- | --- |
| Идентификатор в логах промежуточных сервисов | Сценарий с маркером, маскировка при необходимости |
| Опечатка в `forget` стирает верный чужой идентификатор | Подтверждение повторным вводом; действие остаётся в журнале с автором |
| Причина содержит персональные данные | Предупреждение на экране и в ADR, лимит длины. Запись неизменяема |
| Доступ к поду обходит всё это | Записано как граница решения; ловит только audit log Kubernetes |
| `fully_consistent` дороже обычной проверки | Только два редких права |
| Secret в kind создан вручную | Настоящий `helm upgrade bootstrap` выполняется в рамках проверки этого change |
