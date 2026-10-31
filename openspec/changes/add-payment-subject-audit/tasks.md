# Tasks

## 1. SpiceDB

- [ ] 1.1 `relation auditor`, `reveal_subjects`, `forget_subjects` в `authorization/schema.zed`; assertions в `validation.yaml`: `auditor` имеет оба права, `operator` без `auditor` и сервис не имеют (`FR-750`, `AC-1045`).
- [ ] 1.2 Связь `auditor` для `kind-operator` и его Dex-субъекта в `ops/kind/authz/bootstrap-kind.yaml`.

## 2. payments

- [ ] 2.1 `RevealPaymentRequesters`, `ForgetSubject` и сообщения в proto, `make generate`, проверка `buf breaking` (`FR-751`).
- [ ] 2.2 Миграция `00006_subject_key_audit.sql`, адаптер и порт журнала, триггер неизменяемости (`FR-754`).
- [ ] 2.3 `PaymentPermission`, маппинг и `fully_consistent` для двух прав в адаптере SpiceDB (`FR-750`, `AC-1046`).
- [ ] 2.4 `forget` возвращает `key_id`; read-порт `PaymentAuditReader`, view в application.
- [ ] 2.5 Use case Reveal Payment Requesters с README (`FR-751`, `FR-752`, `FR-755`).
- [ ] 2.6 Use case Forget Subject с README (`FR-751`, `FR-753`, `FR-755`).
- [ ] 2.7 Servicer, маппинг ошибок, DI, имена use case в telemetry.
- [ ] 2.8 `payments-admin`: удалить `payments history` и `subjects forget`, `subjects rewrap --reason` с записью в журнал (`FR-756`).
- [ ] 2.9 `pytest-bdd` в dev-зависимостях, `tests/features` с `@AC-…`; integration-тесты атомарности и порядка.

## 3. Gateway и identity

- [ ] 3.1 `GRPCRoute payments-v1-subject-audit` без повторов, проверка в `e2e/gateway`.
- [ ] 3.2 `serviceAccounts` и `userDelegates` в `ops/kind/values/payments.yaml`, audience `nomnom.payments` у BFF и `e2e-driver`.

## 4. BFF

- [ ] 4.1 Клиент `payments`, мутации в SDL, resolvers, presenter, маппинг ошибок (`FR-757`).
- [ ] 4.2 Persisted operations, композиция, тесты плагина.
- [ ] 4.3 Проверка, что переменные мутаций не попадают в логи Router; маскировка, если попадают (`FR-759`).

## 5. UI

- [ ] 5.1 Feature `payment-subject-audit`, маршрут, операции GraphQL, i18n (`FR-758`).
- [ ] 5.2 Подтверждение забвения, предупреждение о причине, `no-cache`, тесты и моки (`AC-1062`, `AC-1063`).

## 6. Документация

- [ ] 6.1 ADR 2: отметить реализацию; `README` и `ops/README.md` `payments`.
- [ ] 6.2 `docs/use-cases.md`, `docs/security.md`, `GLOSSARY.md`, `docs/ubiquitous-language.md`.

## 7. Проверка

- [ ] 7.1 Сценарий `ops/kind/e2e/subject-audit` проходит, включая поиск маркера в логах (`AC-1064`).
- [ ] 7.2 `helm upgrade bootstrap` подхватывает созданный вручную Secret `payments-subject-keys` и сохраняет его значения.
- [ ] 7.3 `order-lifecycle` и `projections check` в kind проходят.
