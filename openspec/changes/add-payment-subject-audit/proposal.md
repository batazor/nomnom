# Proposal

## Why

`payments-admin payments history` раскрывает идентификатор пользователя, а `subjects forget` его уничтожает, но запись об этом нигде не остаётся. Записать автора в CLI нельзя надёжно: под не знает, кто вызвал `kubectl exec`, а значение `--operator` никто не проверяет.

Проверенная личность в проекте есть только у пользователя, пришедшего через BFF: только BFF подписывает user assertion (ADR 32). Поэтому раскрытие и забвение переходят на отдельные RPC с проверкой права в SpiceDB.

Решение и его границы описаны в [ADR 2 payments](../../../boundaries/payments/docs/adr/0002-event-sourcing-for-payment.md#аудит-раскрытия-и-забвения).

## What Changes

- **RPC `payments`.** `RevealPaymentRequesters` и `ForgetSubject`: только для пользователя с user assertion, с обязательной причиной.
- **Таблица `subject_key_audit`.** Только для добавления: кто, что, какой `key_id`, когда, зачем. Запись фиксируется до расшифровки, а при забвении — в одной транзакции с очисткой ключа. Идентификатор забываемого пользователя в неё не попадает.
- **SpiceDB.** `relation auditor` и права `reveal_subjects`, `forget_subjects` на `platform:nomnom`, с `fully_consistent` при проверке: отозванный доступ перестаёт работать сразу.
- **Gateway и доступ сервисов.** Отдельный `GRPCRoute` без автоматических повторов, `bff` разрешён в `payments`.
- **BFF.** Клиент `payments` и две GraphQL-мутации: раскрытие пишет в аудит, поэтому это не query.
- **UI.** Экран аудита: история платежа и забвение с подтверждением. Результат не кешируется и не сохраняется в браузере.
- **`payments-admin`.** Команды `payments history` и `subjects forget` удаляются, чтобы не оставлять обход. `subjects rewrap` остаётся и пишет в аудит.
- **Тесты.** Use case на BDD через `pytest-bdd` (новая dev-зависимость), сквозной сценарий в `ops/kind/e2e`.

Не меняются: поток событий, формат `SealedSubject`, загрузка `Payment`, Integration Events.

**BREAKING (для операторов):** раскрытие и забвение больше не доступны через `kubectl exec`.

Не входит:

- защита от того, у кого есть доступ к поду: у него есть KEK и база;
- общий процесс удаления пользователя между контекстами;
- просмотр самого журнала аудита через UI и API: только SQL;
- срок хранения журнала аудита.

## Capabilities

### New Capabilities

- `payment-subject-audit`: раскрытие автора и забвение пользователя с проверенной личностью, журнал аудита, GraphQL-мутации и экран.

### Modified Capabilities

- `spicedb-authorization`: роль `auditor` и её права.
- `payment-event-sourcing`: фиксирует уже реализованные запечатанный идентификатор и ротацию KEK, которых в спеке ещё нет.

## Impact

- **`authorization`:** `schema.zed`, `validation.yaml`; `ops/kind/authz/bootstrap-kind.yaml`.
- **`boundaries/payments`:**
  - `service.proto`, `payments.proto`, servicer, маппинг ошибок;
  - слайс `payment`: два use case с README, порты, права, адаптер SpiceDB, миграция, DI, имена use case в telemetry;
  - `payments-admin`, `pyproject.toml`, `tests/features`.
- **`ops/kind`:** `manifests/gateway/gateway.yaml`, `values/payments.yaml`, values BFF (audience `nomnom.payments`), `e2e/chart`, `e2e/gateway`, новый сценарий e2e.
- **`boundaries/bff`:** SDL, resolvers и presenter плагина, клиент, persisted operations, тесты.
- **`boundaries/ui`:** новый feature, маршрут, i18n, моки, тесты.
- **Документация:** ADR 2, README и `ops/README.md` `payments`, `docs/use-cases.md`, `docs/security.md`, `GLOSSARY.md`, `docs/ubiquitous-language.md`.
